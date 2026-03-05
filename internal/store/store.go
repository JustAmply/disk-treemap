package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
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

type DiffQueryOptions struct {
	Limit   int
	Query   string
	Kind    string
	MinSize int64
	Sort    string
}

type DiffItem struct {
	Path            string  `json:"path"`
	Name            string  `json:"name"`
	Kind            string  `json:"type"`
	BeforeExists    bool    `json:"before_exists"`
	AfterExists     bool    `json:"after_exists"`
	BeforeBytes     int64   `json:"before_bytes"`
	AfterBytes      int64   `json:"after_bytes"`
	DeltaBytes      int64   `json:"delta_bytes"`
	DeltaPercent    float64 `json:"delta_percent"`
	VisualSizeBytes int64   `json:"visual_size_bytes"`
	ChangeClass     string  `json:"change_class"`
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
	var run ScanRun
	var startedAt, finishedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, started_at, finished_at, status, error, root_path, total_bytes, total_nodes, warning_count
		FROM scan_runs ORDER BY id DESC LIMIT 1
	`).Scan(&run.ID, &startedAt, &finishedAt, &run.Status, &run.Error, &run.RootPath, &run.TotalBytes, &run.TotalNodes, &run.WarningCount)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest scan run: %w", err)
	}
	attachScanTimestamps(&run, startedAt, finishedAt)
	return &run, nil
}

func (s *Store) ListScanRuns(ctx context.Context, limit int, status string) ([]ScanRun, error) {
	if limit <= 0 {
		limit = 50
	}

	baseQuery := `
		SELECT id, started_at, finished_at, status, error, root_path, total_bytes, total_nodes, warning_count
		FROM scan_runs
	`

	args := make([]any, 0, 2)
	if status != "" {
		baseQuery += ` WHERE status=?`
		args = append(args, status)
	}
	baseQuery += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("list scan runs: %w", err)
	}
	defer rows.Close()

	runs := make([]ScanRun, 0, limit)
	for rows.Next() {
		var run ScanRun
		var startedAt, finishedAt sql.NullString
		if err := rows.Scan(&run.ID, &startedAt, &finishedAt, &run.Status, &run.Error, &run.RootPath, &run.TotalBytes, &run.TotalNodes, &run.WarningCount); err != nil {
			return nil, fmt.Errorf("scan scan_runs row: %w", err)
		}
		attachScanTimestamps(&run, startedAt, finishedAt)
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scan runs: %w", err)
	}

	return runs, nil
}

func (s *Store) DeleteScanRun(ctx context.Context, scanID int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM scan_runs WHERE id=?`, scanID)
	if err != nil {
		return false, fmt.Errorf("delete scan run: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete scan run rows affected: %w", err)
	}
	return affected > 0, nil
}

func (s *Store) PruneCompletedFailedScans(ctx context.Context, keepMax int) ([]int64, error) {
	if keepMax < 1 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM scan_runs
		WHERE status IN ('completed','failed')
		ORDER BY id DESC
		LIMIT -1 OFFSET ?
	`, keepMax)
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
		ids = append(ids, id)
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

func (s *Store) ListDirectoryDiff(ctx context.Context, targetScanID, baseScanID int64, parentPath string, limit int) ([]DiffItem, error) {
	return s.ListDiffChildren(ctx, targetScanID, baseScanID, parentPath, DiffQueryOptions{Limit: limit, Sort: "delta_desc"})
}

func (s *Store) ListDiffChildren(ctx context.Context, targetScanID, baseScanID int64, parentPath string, opts DiffQueryOptions) ([]DiffItem, error) {
	if opts.Limit <= 0 {
		opts.Limit = 100
	}

	query := strings.Builder{}
	query.WriteString(`
		WITH base_items AS (
			SELECT path, name, kind, size_bytes
			FROM nodes
			WHERE scan_id=? AND parent_path=?
		), target_items AS (
			SELECT path, name, kind, size_bytes
			FROM nodes
			WHERE scan_id=? AND parent_path=?
		), joined AS (
			SELECT
				COALESCE(t.path, b.path) AS path,
				COALESCE(t.name, b.name) AS name,
				COALESCE(t.kind, b.kind) AS kind,
				b.path AS before_path,
				t.path AS after_path,
				b.size_bytes AS before_bytes,
				t.size_bytes AS after_bytes
			FROM base_items b
			LEFT JOIN target_items t
				ON t.name = b.name
				AND t.kind = b.kind
			UNION ALL
			SELECT
				t.path AS path,
				t.name AS name,
				t.kind AS kind,
				NULL AS before_path,
				t.path AS after_path,
				NULL AS before_bytes,
				t.size_bytes AS after_bytes
			FROM target_items t
			LEFT JOIN base_items b
				ON b.name = t.name
				AND b.kind = t.kind
			WHERE b.path IS NULL
		)
		SELECT
			path,
			name,
			kind,
			before_path,
			after_path,
			before_bytes,
			after_bytes
		FROM joined
		WHERE (
			before_path IS NULL
			OR after_path IS NULL
			OR COALESCE(before_bytes, 0) <> COALESCE(after_bytes, 0)
		)
	`)

	args := []any{baseScanID, parentPath, targetScanID, parentPath}
	appendDiffFilters(&query, &args, opts)

	query.WriteString(" ORDER BY ")
	query.WriteString(normalizeDiffSort(opts.Sort))
	query.WriteString(" LIMIT ?")
	args = append(args, opts.Limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list diff children: %w", err)
	}
	defer rows.Close()

	items := make([]DiffItem, 0)
	for rows.Next() {
		var (
			path        string
			name        string
			kind        string
			beforePath  sql.NullString
			afterPath   sql.NullString
			beforeBytes sql.NullInt64
			afterBytes  sql.NullInt64
		)
		if err := rows.Scan(&path, &name, &kind, &beforePath, &afterPath, &beforeBytes, &afterBytes); err != nil {
			return nil, fmt.Errorf("scan diff row: %w", err)
		}
		items = append(items, buildDiffItem(path, name, kind, beforePath.Valid, afterPath.Valid, beforeBytes.Int64, afterBytes.Int64))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate diff rows: %w", err)
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

func appendDiffFilters(query *strings.Builder, args *[]any, opts DiffQueryOptions) {
	if opts.Kind == "file" || opts.Kind == "dir" {
		query.WriteString(" AND kind=?")
		*args = append(*args, opts.Kind)
	}
	if opts.Query != "" {
		query.WriteString(" AND lower(name) LIKE ?")
		*args = append(*args, "%"+strings.ToLower(opts.Query)+"%")
	}
	if opts.MinSize > 0 {
		query.WriteString(" AND MAX(COALESCE(before_bytes, 0), COALESCE(after_bytes, 0))>=?")
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

func normalizeDiffSort(sort string) string {
	switch sort {
	case "delta_asc":
		return "ABS(COALESCE(after_bytes, 0) - COALESCE(before_bytes, 0)) ASC, name ASC"
	case "size_desc":
		return "MAX(COALESCE(before_bytes, 0), COALESCE(after_bytes, 0)) DESC, name ASC"
	case "size_asc":
		return "MAX(COALESCE(before_bytes, 0), COALESCE(after_bytes, 0)) ASC, name ASC"
	case "name_asc":
		return "name ASC, kind ASC"
	case "name_desc":
		return "name DESC, kind DESC"
	case "delta_desc", "":
		return "ABS(COALESCE(after_bytes, 0) - COALESCE(before_bytes, 0)) DESC, name ASC"
	default:
		return "ABS(COALESCE(after_bytes, 0) - COALESCE(before_bytes, 0)) DESC, name ASC"
	}
}

func buildDiffItem(path, name, kind string, beforeExists, afterExists bool, beforeBytes, afterBytes int64) DiffItem {
	item := DiffItem{
		Path:            path,
		Name:            name,
		Kind:            kind,
		BeforeExists:    beforeExists,
		AfterExists:     afterExists,
		BeforeBytes:     beforeBytes,
		AfterBytes:      afterBytes,
		DeltaBytes:      afterBytes - beforeBytes,
		VisualSizeBytes: maxInt64(beforeBytes, afterBytes),
	}

	switch {
	case !beforeExists && afterExists:
		item.ChangeClass = "new"
	case beforeExists && !afterExists:
		item.ChangeClass = "removed"
	case item.DeltaBytes > 0:
		item.ChangeClass = "grew"
	case item.DeltaBytes < 0:
		item.ChangeClass = "shrunk"
	default:
		item.ChangeClass = "unchanged"
	}

	switch {
	case !beforeExists && afterExists:
		item.DeltaPercent = 100
	case beforeBytes == 0 && afterBytes == 0:
		item.DeltaPercent = 0
	case beforeBytes == 0:
		item.DeltaPercent = 100
	default:
		item.DeltaPercent = (float64(item.DeltaBytes) * 100.0) / float64(beforeBytes)
	}

	if math.IsNaN(item.DeltaPercent) || math.IsInf(item.DeltaPercent, 0) {
		item.DeltaPercent = 0
	}

	return item
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
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
