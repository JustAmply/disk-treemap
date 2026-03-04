package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
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

type NodeWriter struct {
	tx   *sql.Tx
	stmt *sql.Stmt
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO nodes(scan_id, path, parent_path, name, kind, size_bytes, mtime_unix)
		VALUES(?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("prepare insert node: %w", err)
	}

	return &NodeWriter{tx: tx, stmt: stmt}, nil
}

func (w *NodeWriter) InsertNode(ctx context.Context, scanID int64, node Node) error {
	if _, err := w.stmt.ExecContext(ctx, scanID, node.Path, node.ParentPath, node.Name, node.Kind, node.SizeBytes, node.MtimeUnix); err != nil {
		return fmt.Errorf("insert node %q: %w", node.Path, err)
	}
	return nil
}

func (w *NodeWriter) Commit() error {
	if w == nil {
		return nil
	}
	if w.stmt != nil {
		_ = w.stmt.Close()
	}
	return w.tx.Commit()
}

func (w *NodeWriter) Rollback() error {
	if w == nil {
		return nil
	}
	if w.stmt != nil {
		_ = w.stmt.Close()
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
	return &run, nil
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT path, parent_path, name, kind, size_bytes, mtime_unix
		FROM nodes
		WHERE scan_id=? AND parent_path=?
		ORDER BY size_bytes DESC, name ASC
		LIMIT ?
	`, scanID, parentPath, limit)
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
	prefix := basePath + string(filepath.Separator)
	if basePath == string(filepath.Separator) {
		prefix = basePath
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT path, parent_path, name, kind, size_bytes, mtime_unix
		FROM nodes
		WHERE scan_id=?
		  AND path<>?
		  AND instr(path, ?) = 1
		ORDER BY size_bytes DESC, name ASC
		LIMIT ?
	`, scanID, basePath, prefix, limit)
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

var ErrNotFound = errors.New("not found")
