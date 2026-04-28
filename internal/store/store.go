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

	compactKindFile = 1
	compactKindDir  = 2
)

type Store struct {
	db        *sql.DB
	hasLegacy bool
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

type StoredNode struct {
	NodeID    int64
	ParentID  *int64
	Name      string
	Kind      string
	SizeBytes int64
	MtimeUnix int64
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
	tx         *sql.Tx
	nextNodeID int64
	pathIDs    map[string]int64
}

type compactNodeRow struct {
	NodeID    int64
	ParentID  sql.NullInt64
	Name      string
	KindCode  int
	SizeBytes int64
	MtimeUnix int64
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
	if _, err := s.db.ExecContext(ctx, `PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		return fmt.Errorf("enable incremental auto vacuum: %w", err)
	}

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
		`CREATE INDEX IF NOT EXISTS idx_scan_runs_status_id ON scan_runs(status, id DESC);`,
	}

	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("init scan schema: %w", err)
		}
	}

	if err := s.migrateLegacyNodes(ctx); err != nil {
		return err
	}

	nodeSchema := []string{
		`CREATE TABLE IF NOT EXISTS nodes (
			scan_id INTEGER NOT NULL,
			node_id INTEGER NOT NULL,
			parent_id INTEGER,
			name TEXT NOT NULL,
			kind INTEGER NOT NULL CHECK(kind IN (1,2)),
			size_bytes INTEGER NOT NULL,
			mtime_unix INTEGER NOT NULL,
			PRIMARY KEY (scan_id, node_id),
			FOREIGN KEY (scan_id) REFERENCES scan_runs(id) ON DELETE CASCADE,
			FOREIGN KEY (scan_id, parent_id) REFERENCES nodes(scan_id, node_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED
		) WITHOUT ROWID;`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_v2_scan_parent_size ON nodes(scan_id, parent_id, size_bytes DESC, name);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_v2_scan_parent_name ON nodes(scan_id, parent_id, name);`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_v2_scan_size ON nodes(scan_id, size_bytes DESC, name);`,
	}

	for _, stmt := range nodeSchema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("init compact node schema: %w", err)
		}
	}
	return nil
}

func (s *Store) migrateLegacyNodes(ctx context.Context) error {
	nodesExists, err := s.tableExists(ctx, "nodes")
	if err != nil {
		return err
	}
	legacyExists, err := s.tableExists(ctx, "legacy_nodes")
	if err != nil {
		return err
	}
	s.hasLegacy = legacyExists

	if !nodesExists {
		return nil
	}

	hasNodeID, err := s.tableHasColumn(ctx, "nodes", "node_id")
	if err != nil {
		return err
	}
	if hasNodeID {
		return nil
	}

	if legacyExists {
		return errors.New("cannot migrate legacy nodes: both nodes and legacy_nodes tables exist")
	}

	if _, err := s.db.ExecContext(ctx, `ALTER TABLE nodes RENAME TO legacy_nodes`); err != nil {
		return fmt.Errorf("rename legacy nodes table: %w", err)
	}
	s.hasLegacy = true
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
	return &NodeWriter{
		tx:         tx,
		nextNodeID: 1,
		pathIDs:    map[string]int64{},
	}, nil
}

func (w *NodeWriter) InsertNode(ctx context.Context, scanID int64, node Node) error {
	return w.InsertNodesBatch(ctx, scanID, []Node{node})
}

func (w *NodeWriter) InsertNodesBatch(ctx context.Context, scanID int64, nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}

	stored := make([]StoredNode, 0, len(nodes))
	for _, node := range nodes {
		nodeID, ok := w.pathIDs[node.Path]
		if !ok {
			nodeID = w.nextNodeID
			w.nextNodeID++
			w.pathIDs[node.Path] = nodeID
		}

		var parentID *int64
		if node.ParentPath != "" {
			if id, ok := w.pathIDs[node.ParentPath]; ok {
				parent := id
				parentID = &parent
			}
		}

		stored = append(stored, StoredNode{
			NodeID:    nodeID,
			ParentID:  parentID,
			Name:      node.Name,
			Kind:      node.Kind,
			SizeBytes: node.SizeBytes,
			MtimeUnix: node.MtimeUnix,
		})
	}

	return w.InsertStoredNodesBatch(ctx, scanID, stored)
}

func (w *NodeWriter) InsertStoredNodesBatch(ctx context.Context, scanID int64, nodes []StoredNode) error {
	if len(nodes) == 0 {
		return nil
	}

	for start := 0; start < len(nodes); start += maxNodeRowsPerInsert {
		end := start + maxNodeRowsPerInsert
		if end > len(nodes) {
			end = len(nodes)
		}
		if err := w.insertStoredNodeChunk(ctx, scanID, nodes[start:end]); err != nil {
			return err
		}
	}

	return nil
}

func (w *NodeWriter) insertStoredNodeChunk(ctx context.Context, scanID int64, nodes []StoredNode) error {
	var query strings.Builder
	query.WriteString(`INSERT INTO nodes(scan_id, node_id, parent_id, name, kind, size_bytes, mtime_unix) VALUES `)

	args := make([]any, 0, len(nodes)*nodeInsertColumnCount)
	for i, node := range nodes {
		if i > 0 {
			query.WriteString(",")
		}
		kindCode, err := compactKindCode(node.Kind)
		if err != nil {
			return err
		}
		query.WriteString("(?, ?, ?, ?, ?, ?, ?)")
		args = append(args, scanID, node.NodeID, nullableInt64(node.ParentID), node.Name, kindCode, node.SizeBytes, node.MtimeUnix)
	}

	if _, err := w.tx.ExecContext(ctx, query.String(), args...); err != nil {
		return fmt.Errorf("insert compact node batch (%d): %w", len(nodes), err)
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

func (s *Store) OptimizeStorage(ctx context.Context, forceVacuum bool) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA optimize`); err != nil {
		return fmt.Errorf("optimize sqlite planner stats: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("truncate wal checkpoint: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA incremental_vacuum`); err != nil {
		return fmt.Errorf("incremental vacuum: %w", err)
	}
	if forceVacuum {
		if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
			return fmt.Errorf("vacuum sqlite database: %w", err)
		}
		if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			return fmt.Errorf("post-vacuum wal checkpoint: %w", err)
		}
	}
	return nil
}

func (s *Store) GetNode(ctx context.Context, scanID int64, path string) (Node, error) {
	rootPath, nodeID, err := s.resolveNodeID(ctx, scanID, path)
	if err != nil {
		if s.hasLegacy {
			return s.getLegacyNode(ctx, scanID, path)
		}
		return Node{}, err
	}

	row, err := s.getCompactNodeRow(ctx, scanID, nodeID)
	if err != nil {
		return Node{}, err
	}

	cleanPath := filepath.Clean(path)
	return compactRowToNode(cleanPath, compactParentPath(rootPath, cleanPath), row), nil
}

func (s *Store) ListChildren(ctx context.Context, scanID int64, parentPath string, limit int) ([]Node, error) {
	return s.ListChildrenWithOptions(ctx, scanID, parentPath, NodeQueryOptions{Limit: limit, Sort: "size_desc"})
}

func (s *Store) AggregateChildrenWithOptions(ctx context.Context, scanID int64, parentPath string, opts NodeQueryOptions) (ChildAggregate, error) {
	_, parentID, err := s.resolveNodeID(ctx, scanID, parentPath)
	if err != nil {
		if s.hasLegacy {
			return s.aggregateLegacyChildrenWithOptions(ctx, scanID, parentPath, opts)
		}
		return ChildAggregate{}, err
	}

	query := strings.Builder{}
	query.WriteString(`
		SELECT COUNT(*), COALESCE(SUM(size_bytes), 0)
		FROM nodes
		WHERE scan_id=? AND parent_id=?
	`)

	args := []any{scanID, parentID}
	appendCompactNodeFilters(&query, &args, opts)

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

	_, parentID, err := s.resolveNodeID(ctx, scanID, parentPath)
	if err != nil {
		if s.hasLegacy {
			return s.listLegacyChildrenWithOptions(ctx, scanID, parentPath, opts)
		}
		return nil, err
	}

	sortClause := normalizeSort(opts.Sort)
	query := strings.Builder{}
	query.WriteString(`
		SELECT node_id, parent_id, name, kind, size_bytes, mtime_unix
		FROM nodes
		WHERE scan_id=? AND parent_id=?
	`)

	args := []any{scanID, parentID}
	appendCompactNodeFilters(&query, &args, opts)

	query.WriteString(" ORDER BY ")
	query.WriteString(sortClause)
	query.WriteString(" LIMIT ?")
	args = append(args, opts.Limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list children: %w", err)
	}
	defer rows.Close()

	cleanParentPath := filepath.Clean(parentPath)
	items := make([]Node, 0)
	for rows.Next() {
		row, err := scanCompactNodeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan child row: %w", err)
		}
		childPath := filepath.Join(cleanParentPath, row.Name)
		items = append(items, compactRowToNode(childPath, cleanParentPath, row))
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

	rootPath, baseID, err := s.resolveNodeID(ctx, scanID, basePath)
	if err != nil {
		if s.hasLegacy {
			return s.listLegacyLargestInPathWithOptions(ctx, scanID, basePath, opts)
		}
		if errors.Is(err, ErrNotFound) {
			return []Node{}, nil
		}
		return nil, err
	}

	baseRelPrefix, err := descendantRelPrefix(rootPath, basePath)
	if err != nil {
		return nil, err
	}

	sortClause := normalizeSort(opts.Sort)
	query := strings.Builder{}
	query.WriteString(`
		WITH RECURSIVE descendants(node_id, parent_id, name, kind, size_bytes, mtime_unix, rel_path) AS (
			SELECT node_id, parent_id, name, kind, size_bytes, mtime_unix, ? || name
			FROM nodes
			WHERE scan_id=? AND parent_id=?
			UNION ALL
			SELECT n.node_id, n.parent_id, n.name, n.kind, n.size_bytes, n.mtime_unix, d.rel_path || ? || n.name
			FROM nodes n
			JOIN descendants d ON n.scan_id=? AND n.parent_id=d.node_id
		)
		SELECT node_id, parent_id, name, kind, size_bytes, mtime_unix, rel_path
		FROM descendants
		WHERE 1=1
	`)

	separator := string(filepath.Separator)
	args := []any{baseRelPrefix, scanID, baseID, separator, scanID}
	appendCompactNodeFilters(&query, &args, opts)

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
		row, relPath, err := scanCompactNodeRowWithRelPath(rows)
		if err != nil {
			return nil, fmt.Errorf("scan largest row: %w", err)
		}
		path := filepath.Join(rootPath, relPath)
		items = append(items, compactRowToNode(path, compactParentPath(rootPath, path), row))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate largest rows: %w", err)
	}
	return items, nil
}

func (s *Store) resolveNodeID(ctx context.Context, scanID int64, requestedPath string) (string, int64, error) {
	rootPath, err := s.scanRoot(ctx, scanID)
	if err != nil {
		return "", 0, err
	}

	cleanRoot := filepath.Clean(rootPath)
	cleanRequested := filepath.Clean(requestedPath)
	rel, err := filepath.Rel(cleanRoot, cleanRequested)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", 0, ErrNotFound
	}

	rootID, err := s.rootNodeID(ctx, scanID)
	if err != nil {
		return "", 0, err
	}
	if rel == "." {
		return cleanRoot, rootID, nil
	}

	currentID := rootID
	for _, name := range strings.Split(rel, string(filepath.Separator)) {
		if name == "" || name == "." {
			continue
		}
		var nextID int64
		err := s.db.QueryRowContext(ctx, `
			SELECT node_id FROM nodes
			WHERE scan_id=? AND parent_id=? AND name=?
		`, scanID, currentID, name).Scan(&nextID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", 0, ErrNotFound
			}
			return "", 0, fmt.Errorf("resolve node path: %w", err)
		}
		currentID = nextID
	}

	return cleanRoot, currentID, nil
}

func (s *Store) scanRoot(ctx context.Context, scanID int64) (string, error) {
	var root string
	err := s.db.QueryRowContext(ctx, `SELECT root_path FROM scan_runs WHERE id=?`, scanID).Scan(&root)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("get scan root: %w", err)
	}
	return filepath.Clean(root), nil
}

func (s *Store) rootNodeID(ctx context.Context, scanID int64) (int64, error) {
	var nodeID int64
	err := s.db.QueryRowContext(ctx, `
		SELECT node_id FROM nodes
		WHERE scan_id=? AND parent_id IS NULL
		LIMIT 1
	`, scanID).Scan(&nodeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("get root node: %w", err)
	}
	return nodeID, nil
}

func (s *Store) getCompactNodeRow(ctx context.Context, scanID, nodeID int64) (compactNodeRow, error) {
	var row compactNodeRow
	err := s.db.QueryRowContext(ctx, `
		SELECT node_id, parent_id, name, kind, size_bytes, mtime_unix
		FROM nodes WHERE scan_id=? AND node_id=?
	`, scanID, nodeID).Scan(&row.NodeID, &row.ParentID, &row.Name, &row.KindCode, &row.SizeBytes, &row.MtimeUnix)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return compactNodeRow{}, ErrNotFound
		}
		return compactNodeRow{}, fmt.Errorf("get compact node: %w", err)
	}
	return row, nil
}

func scanCompactNodeRow(rows *sql.Rows) (compactNodeRow, error) {
	var row compactNodeRow
	err := rows.Scan(&row.NodeID, &row.ParentID, &row.Name, &row.KindCode, &row.SizeBytes, &row.MtimeUnix)
	return row, err
}

func scanCompactNodeRowWithRelPath(rows *sql.Rows) (compactNodeRow, string, error) {
	var row compactNodeRow
	var relPath string
	err := rows.Scan(&row.NodeID, &row.ParentID, &row.Name, &row.KindCode, &row.SizeBytes, &row.MtimeUnix, &relPath)
	return row, relPath, err
}

func compactRowToNode(path, parentPath string, row compactNodeRow) Node {
	return Node{
		Path:       filepath.Clean(path),
		ParentPath: parentPath,
		Name:       row.Name,
		Kind:       compactKindName(row.KindCode),
		SizeBytes:  row.SizeBytes,
		MtimeUnix:  row.MtimeUnix,
	}
}

func compactParentPath(rootPath, path string) string {
	cleanRoot := filepath.Clean(rootPath)
	cleanPath := filepath.Clean(path)
	if cleanPath == cleanRoot {
		return ""
	}
	return filepath.Dir(cleanPath)
}

func descendantRelPrefix(rootPath, basePath string) (string, error) {
	cleanRoot := filepath.Clean(rootPath)
	cleanBase := filepath.Clean(basePath)
	if cleanRoot == cleanBase {
		return "", nil
	}
	rel, err := filepath.Rel(cleanRoot, cleanBase)
	if err != nil {
		return "", fmt.Errorf("relative descendant prefix: %w", err)
	}
	return rel + string(filepath.Separator), nil
}

func nullableInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func compactKindCode(kind string) (int, error) {
	switch kind {
	case "file":
		return compactKindFile, nil
	case "dir":
		return compactKindDir, nil
	default:
		return 0, fmt.Errorf("unsupported node kind %q", kind)
	}
}

func compactKindName(kindCode int) string {
	switch kindCode {
	case compactKindFile:
		return "file"
	case compactKindDir:
		return "dir"
	default:
		return ""
	}
}

func appendCompactNodeFilters(query *strings.Builder, args *[]any, opts NodeQueryOptions) {
	if opts.Kind == "file" || opts.Kind == "dir" {
		kindCode, _ := compactKindCode(opts.Kind)
		query.WriteString(" AND kind=?")
		*args = append(*args, kindCode)
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

func appendLegacyNodeFilters(query *strings.Builder, args *[]any, opts NodeQueryOptions) {
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

func (s *Store) getLegacyNode(ctx context.Context, scanID int64, path string) (Node, error) {
	var n Node
	err := s.db.QueryRowContext(ctx, `
		SELECT path, parent_path, name, kind, size_bytes, mtime_unix
		FROM legacy_nodes WHERE scan_id=? AND path=?
	`, scanID, path).Scan(&n.Path, &n.ParentPath, &n.Name, &n.Kind, &n.SizeBytes, &n.MtimeUnix)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Node{}, ErrNotFound
		}
		return Node{}, fmt.Errorf("get legacy node: %w", err)
	}
	return n, nil
}

func (s *Store) aggregateLegacyChildrenWithOptions(ctx context.Context, scanID int64, parentPath string, opts NodeQueryOptions) (ChildAggregate, error) {
	query := strings.Builder{}
	query.WriteString(`
		SELECT COUNT(*), COALESCE(SUM(size_bytes), 0)
		FROM legacy_nodes
		WHERE scan_id=? AND parent_path=?
	`)

	args := []any{scanID, parentPath}
	appendLegacyNodeFilters(&query, &args, opts)

	var agg ChildAggregate
	if err := s.db.QueryRowContext(ctx, query.String(), args...).Scan(&agg.Count, &agg.TotalBytes); err != nil {
		return ChildAggregate{}, fmt.Errorf("aggregate legacy children: %w", err)
	}
	return agg, nil
}

func (s *Store) listLegacyChildrenWithOptions(ctx context.Context, scanID int64, parentPath string, opts NodeQueryOptions) ([]Node, error) {
	sortClause := normalizeSort(opts.Sort)
	query := strings.Builder{}
	query.WriteString(`
		SELECT path, parent_path, name, kind, size_bytes, mtime_unix
		FROM legacy_nodes
		WHERE scan_id=? AND parent_path=?
	`)

	args := []any{scanID, parentPath}
	appendLegacyNodeFilters(&query, &args, opts)

	query.WriteString(" ORDER BY ")
	query.WriteString(sortClause)
	query.WriteString(" LIMIT ?")
	args = append(args, opts.Limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list legacy children: %w", err)
	}
	defer rows.Close()

	items := make([]Node, 0)
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.Path, &n.ParentPath, &n.Name, &n.Kind, &n.SizeBytes, &n.MtimeUnix); err != nil {
			return nil, fmt.Errorf("scan legacy child row: %w", err)
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy children: %w", err)
	}
	return items, nil
}

func (s *Store) listLegacyLargestInPathWithOptions(ctx context.Context, scanID int64, basePath string, opts NodeQueryOptions) ([]Node, error) {
	prefixes := descendantPrefixes(basePath)

	sortClause := normalizeSort(opts.Sort)
	query := strings.Builder{}
	query.WriteString(`
		SELECT path, parent_path, name, kind, size_bytes, mtime_unix
		FROM legacy_nodes
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
	appendLegacyNodeFilters(&query, &args, opts)

	query.WriteString(" ORDER BY ")
	query.WriteString(sortClause)
	query.WriteString(" LIMIT ?")
	args = append(args, opts.Limit)

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list legacy largest: %w", err)
	}
	defer rows.Close()

	items := make([]Node, 0)
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.Path, &n.ParentPath, &n.Name, &n.Kind, &n.SizeBytes, &n.MtimeUnix); err != nil {
			return nil, fmt.Errorf("scan legacy largest row: %w", err)
		}
		items = append(items, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate legacy largest rows: %w", err)
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

func (s *Store) tableExists(ctx context.Context, name string) (bool, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&count); err != nil {
		return false, fmt.Errorf("check table %s: %w", name, err)
	}
	return count > 0, nil
}

func (s *Store) tableHasColumn(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("table info %s: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, fmt.Errorf("scan table info %s: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate table info %s: %w", table, err)
	}
	return false, nil
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
