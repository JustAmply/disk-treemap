package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	sqliteMaxBindParameters = 32766
	nodeInsertColumnCount   = 7
	maxNodeRowsPerInsert    = sqliteMaxBindParameters / nodeInsertColumnCount
)

type Store struct {
	db *sql.DB
}

type ScanRun struct {
	ID           int64         `json:"id"`
	StartedAt    *time.Time    `json:"started_at,omitempty"`
	FinishedAt   *time.Time    `json:"finished_at,omitempty"`
	Status       string        `json:"status"`
	Error        string        `json:"error,omitempty"`
	RootPath     string        `json:"root_path"`
	TotalBytes   int64         `json:"total_bytes"`
	TotalNodes   int64         `json:"total_nodes"`
	WarningCount int64         `json:"warning_count"`
	Progress     *ScanProgress `json:"progress,omitempty"`
}

type ScanProgress struct {
	CurrentPath  string     `json:"current_path"`
	ScannedNodes int64      `json:"scanned_nodes"`
	ScannedFiles int64      `json:"scanned_files"`
	ScannedDirs  int64      `json:"scanned_dirs"`
	ScannedBytes int64      `json:"scanned_bytes"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
}

type Node struct {
	Path       string `json:"path"`
	ParentPath string `json:"parent_path"`
	Name       string `json:"name"`
	Kind       string `json:"type"`
	SizeBytes  int64  `json:"size_bytes"`
	MtimeUnix  int64  `json:"mtime_unix"`
}

type NodeQueryOptions struct {
	Limit   int
	Query   string
	Kind    string
	MinSize int64
	Sort    string
}

type ChildAggregate struct {
	Count      int64
	TotalBytes int64
}

type NodeWriter struct {
	tx *sql.Tx
}

func Open(dbPath string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Init(ctx context.Context) error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS scan_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at TEXT,
			finished_at TEXT,
			status TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT '',
			root_path TEXT NOT NULL,
			total_bytes INTEGER NOT NULL DEFAULT 0,
			total_nodes INTEGER NOT NULL DEFAULT 0,
			warning_count INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_id INTEGER NOT NULL,
			path TEXT NOT NULL,
			parent_path TEXT NOT NULL,
			name TEXT NOT NULL,
			kind TEXT NOT NULL CHECK(kind IN ('file','dir')),
			size_bytes INTEGER NOT NULL,
			mtime_unix INTEGER NOT NULL,
			FOREIGN KEY (scan_id) REFERENCES scan_runs(id) ON DELETE CASCADE
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_scan_path ON nodes(scan_id, path);`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_scan_parent ON nodes(scan_id, parent_path);`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_scan_size ON nodes(scan_id, size_bytes DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_scan_parent_kind_name ON nodes(scan_id, parent_path, kind, name);`,
		`CREATE INDEX IF NOT EXISTS idx_scan_runs_status_id ON scan_runs(status, id DESC);`,
	}

	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("init schema: %w", err)
		}
	}
	return nil
}

func (s *Store) CreateScanRun(ctx context.Context, rootPath string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO scan_runs(status, root_path) VALUES('queued', ?)`, rootPath)
	if err != nil {
		return 0, fmt.Errorf("insert scan run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("scan id: %w", err)
	}
	return id, nil
}

func (s *Store) MarkScanRunning(ctx context.Context, scanID int64, startedAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE scan_runs SET status='running', started_at=? WHERE id=?`, startedAt.UTC().Format(time.RFC3339Nano), scanID)
	if err != nil {
		return fmt.Errorf("mark running: %w", err)
	}
	return nil
}

func (s *Store) CompleteScan(ctx context.Context, scanID int64, status string, finishedAt time.Time, totalBytes, totalNodes, warningCount int64, errorMessage string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE scan_runs
		SET status=?, finished_at=?, total_bytes=?, total_nodes=?, warning_count=?, error=?
		WHERE id=?
	`, status, finishedAt.UTC().Format(time.RFC3339Nano), totalBytes, totalNodes, warningCount, errorMessage, scanID)
	if err != nil {
		return fmt.Errorf("complete scan: %w", err)
	}
	return nil
}

func (s *Store) BeginNodeWriter(ctx context.Context, scanID int64) (*NodeWriter, error) {
	_ = scanID
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	return &NodeWriter{tx: tx}, nil
}

func (w *NodeWriter) InsertNode(ctx context.Context, scanID int64, node Node) error {
	return w.InsertNodesBatch(ctx, scanID, []Node{node})
}

func (w *NodeWriter) InsertNodesBatch(ctx context.Context, scanID int64, nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}

	for start := 0; start < len(nodes); start += maxNodeRowsPerInsert {
		end := start + maxNodeRowsPerInsert
		if end > len(nodes) {
			end = len(nodes)
		}
		if err := w.insertNodeChunk(ctx, scanID, nodes[start:end]); err != nil {
			return err
		}
	}

	return nil
}

func (w *NodeWriter) insertNodeChunk(ctx context.Context, scanID int64, nodes []Node) error {
	var query strings.Builder
	query.WriteString(`INSERT INTO nodes(scan_id, path, parent_path, name, kind, size_bytes, mtime_unix) VALUES `)

	args := make([]any, 0, len(nodes)*nodeInsertColumnCount)
	for i, node := range nodes {
		if i > 0 {
			query.WriteString(",")
		}
		query.WriteString("(?, ?, ?, ?, ?, ?, ?)")
		args = append(args, scanID, node.Path, node.ParentPath, node.Name, node.Kind, node.SizeBytes, node.MtimeUnix)
	}

	if _, err := w.tx.ExecContext(ctx, query.String(), args...); err != nil {
		return fmt.Errorf("insert node batch (%d): %w", len(nodes), err)
	}

	return nil
}

func (w *NodeWriter) Commit() error {
	if w == nil {
		return nil
	}
	return w.tx.Commit()
}

func (w *NodeWriter) Rollback() error {
	if w == nil {
		return nil
	}
	return w.tx.Rollback()
}

func (s *Store) GetScanRun(ctx context.Context, scanID int64) (ScanRun, error) {
	var run ScanRun
	var startedAt, finishedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, started_at, finished_at, status, error, root_path, total_bytes, total_nodes, warning_count
		FROM scan_runs WHERE id=?
	`, scanID).Scan(&run.ID, &startedAt, &finishedAt, &run.Status, &run.Error, &run.RootPath, &run.TotalBytes, &run.TotalNodes, &run.WarningCount)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScanRun{}, ErrNotFound
		}
		return ScanRun{}, fmt.Errorf("get scan run: %w", err)
	}
	attachScanTimestamps(&run, startedAt, finishedAt)
	return run, nil
}

func (s *Store) GetLatestScanRun(ctx context.Context) (*ScanRun, error) {
	return s.getLatestScanRun(ctx, "")
}

func (s *Store) GetLatestCompletedScanRun(ctx context.Context) (*ScanRun, error) {
	return s.getLatestScanRun(ctx, "completed")
}

func (s *Store) getLatestScanRun(ctx context.Context, status string) (*ScanRun, error) {
	query := `
		SELECT id, started_at, finished_at, status, error, root_path, total_bytes, total_nodes, warning_count
		FROM scan_runs
	`
	args := make([]any, 0, 1)
	if status != "" {
		query += ` WHERE status=?`
		args = append(args, status)
	}
	query += ` ORDER BY id DESC LIMIT 1`

	var run ScanRun
	var startedAt, finishedAt sql.NullString
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&run.ID,
		&startedAt,
		&finishedAt,
		&run.Status,
		&run.Error,
		&run.RootPath,
		&run.TotalBytes,
		&run.TotalNodes,
		&run.WarningCount,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest scan run: %w", err)
	}
	attachScanTimestamps(&run, startedAt, finishedAt)
	return &run, nil
}

func (s *Store) PruneOperationalScans(ctx context.Context) ([]int64, error) {
	var latestID sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM scan_runs ORDER BY id DESC LIMIT 1`).Scan(&latestID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest scan id: %w", err)
	}

	var latestCompletedID sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM scan_runs WHERE status='completed' ORDER BY id DESC LIMIT 1`).Scan(&latestCompletedID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get latest completed scan id: %w", err)
	}

	keepIDs := map[int64]struct{}{}
	if latestID.Valid {
		keepIDs[latestID.Int64] = struct{}{}
	}
	if latestCompletedID.Valid {
		keepIDs[latestCompletedID.Int64] = struct{}{}
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id FROM scan_runs ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("select scan runs to prune: %w", err)
	}
	defer rows.Close()

	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan prune id: %w", err)
		}
		if _, keep := keepIDs[id]; !keep {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prune ids: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin prune tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for start := 0; start < len(ids); start += sqliteMaxBindParameters {
		end := start + sqliteMaxBindParameters
		if end > len(ids) {
			end = len(ids)
		}

		chunk := ids[start:end]
		query := `DELETE FROM scan_runs WHERE id IN (` + strings.Join(makePlaceholders(len(chunk)), ",") + `)`
		args := make([]any, 0, len(chunk))
		for _, id := range chunk {
			args = append(args, id)
		}

		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return nil, fmt.Errorf("delete pruned scan runs: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit prune tx: %w", err)
	}

	return ids, nil
}

func (s *Store) GetNode(ctx context.Context, scanID int64, path string) (Node, error) {
	var n Node
	err := s.db.QueryRowContext(ctx, `
		SELECT path, parent_path, name, kind, size_bytes, mtime_unix
		FROM nodes WHERE scan_id=? AND path=?
	`, scanID, path).Scan(&n.Path, &n.ParentPath, &n.Name, &n.Kind, &n.SizeBytes, &n.MtimeUnix)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Node{}, ErrNotFound
		}
		return Node{}, fmt.Errorf("get node: %w", err)
	}
	return n, nil
}

func (s *Store) ListChildren(ctx context.Context, scanID int64, parentPath string, limit int) ([]Node, error) {
	return s.ListChildrenWithOptions(ctx, scanID, parentPath, NodeQueryOptions{Limit: limit, Sort: "size_desc"})
}

func (s *Store) AggregateChildrenWithOptions(ctx context.Context, scanID int64, parentPath string, opts NodeQueryOptions) (ChildAggregate, error) {
	query := strings.Builder{}
	query.WriteString(`
		SELECT COUNT(*), COALESCE(SUM(size_bytes), 0)
		FROM nodes
		WHERE scan_id=? AND parent_path=?
	`)

	args := []any{scanID, parentPath}
	appendNodeFilters(&query, &args, opts)

	var agg ChildAggregate
	if err := s.db.QueryRowContext(ctx, query.String(), args...).Scan(&agg.Count, &agg.TotalBytes); err != nil {
		return ChildAggregate{}, fmt.Errorf("aggregate children: %w", err)
	}

	return agg, nil
}

func (s *Store) ListChildrenWithOptions(ctx context.Context, scanID int64, parentPath string, opts NodeQueryOptions) ([]Node, error) {
	if opts.Limit <= 0 {
		opts.Limit = 500
	}

	sortClause := normalizeSort(opts.Sort)
	query := strings.Builder{}
	query.WriteString(`
		SELECT path, parent_path, name, kind, size_bytes, mtime_unix
		FROM nodes
		WHERE scan_id=? AND parent_path=?
	`)

	args := []any{scanID, parentPath}
	appendNodeFilters(&query, &args, opts)

	query.WriteString(" ORDER BY ")
	query.WriteString(sortClause)
	query.WriteString(" LIMIT ?")
	args = append(args, opts.Limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list children: %w", err)
	}
	defer rows.Close()

	items := make([]Node, 0)
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.Path, &n.ParentPath, &n.Name, &n.Kind, &n.SizeBytes, &n.MtimeUnix); err != nil {
			return nil, fmt.Errorf("scan child row: %w", err)
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate children: %w", err)
	}
	return items, nil
}

func (s *Store) ListLargestInPath(ctx context.Context, scanID int64, basePath string, limit int) ([]Node, error) {
	return s.ListLargestInPathWithOptions(ctx, scanID, basePath, NodeQueryOptions{Limit: limit, Sort: "size_desc"})
}

func (s *Store) ListLargestInPathWithOptions(ctx context.Context, scanID int64, basePath string, opts NodeQueryOptions) ([]Node, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}

	prefixes := descendantPrefixes(basePath)

	sortClause := normalizeSort(opts.Sort)
	query := strings.Builder{}
	query.WriteString(`
		SELECT path, parent_path, name, kind, size_bytes, mtime_unix
		FROM nodes
		WHERE scan_id=?
		  AND path<>?
		  AND (`)
	for i := range prefixes {
		if i > 0 {
			query.WriteString(" OR ")
		}
		query.WriteString("instr(path, ?) = 1")
	}
	query.WriteString(")")

	args := make([]any, 0, 2+len(prefixes)+4)
	args = append(args, scanID, basePath)
	for _, prefix := range prefixes {
		args = append(args, prefix)
	}
	appendNodeFilters(&query, &args, opts)

	query.WriteString(" ORDER BY ")
	query.WriteString(sortClause)
	query.WriteString(" LIMIT ?")
	args = append(args, opts.Limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list largest: %w", err)
	}
	defer rows.Close()

	items := make([]Node, 0)
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.Path, &n.ParentPath, &n.Name, &n.Kind, &n.SizeBytes, &n.MtimeUnix); err != nil {
			return nil, fmt.Errorf("scan largest row: %w", err)
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate largest rows: %w", err)
	}
	return items, nil
}

func descendantPrefixes(basePath string) []string {
	prefixes := make([]string, 0, 2)
	seen := map[string]struct{}{}

	add := func(prefix string) {
		if prefix == "" {
			return
		}
		if _, ok := seen[prefix]; ok {
			return
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}

	if basePath == "/" || basePath == `\` {
		add(basePath)
		return prefixes
	}

	cleanForward := strings.TrimRight(basePath, "/") + "/"
	cleanBackward := strings.TrimRight(basePath, `\`) + `\`
	add(cleanForward)
	add(cleanBackward)
	return prefixes
}

func appendNodeFilters(query *strings.Builder, args *[]any, opts NodeQueryOptions) {
	if opts.Kind == "file" || opts.Kind == "dir" {
		query.WriteString(" AND kind=?")
		*args = append(*args, opts.Kind)
	}
	if opts.Query != "" {
		query.WriteString(" AND lower(name) LIKE ?")
		*args = append(*args, "%"+strings.ToLower(opts.Query)+"%")
	}
	if opts.MinSize > 0 {
		query.WriteString(" AND size_bytes>=?")
		*args = append(*args, opts.MinSize)
	}
}

func normalizeSort(sort string) string {
	switch sort {
	case "size_asc":
		return "size_bytes ASC, name ASC"
	case "name_asc":
		return "name ASC"
	case "name_desc":
		return "name DESC"
	case "size_desc", "":
		return "size_bytes DESC, name ASC"
	default:
		return "size_bytes DESC, name ASC"
	}
}

func attachScanTimestamps(run *ScanRun, startedAt, finishedAt sql.NullString) {
	if startedAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, startedAt.String)
		if err == nil {
			run.StartedAt = &t
		}
	}
	if finishedAt.Valid {
		t, err := time.Parse(time.RFC3339Nano, finishedAt.String)
		if err == nil {
			run.FinishedAt = &t
		}
	}
}

func makePlaceholders(n int) []string {
	items := make([]string, n)
	for i := 0; i < n; i++ {
		items[i] = "?"
	}
	return items
}

var ErrNotFound = errors.New("not found")
