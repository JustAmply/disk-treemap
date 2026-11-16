package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type ScanWarning struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Message   string `json:"message"`
}
type ScanDiagnostics struct {
	Warnings        []ScanWarning `json:"warnings"`
	ExcludedCount   int64         `json:"excluded_count"`
	ExcludePatterns []string      `json:"exclude_patterns"`
}

func (s *Store) initNodeMetadata(ctx context.Context) error {
	columns := []struct{ name, definition string }{
		{"allocated_bytes", "INTEGER"},
		{"incomplete", "INTEGER NOT NULL DEFAULT 0"},
		{"excluded", "INTEGER NOT NULL DEFAULT 0"},
		{"hardlink", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, column := range columns {
		exists, err := s.tableHasColumn(ctx, "nodes", column.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := s.db.ExecContext(ctx, "ALTER TABLE nodes ADD COLUMN "+column.name+" "+column.definition); err != nil {
				return fmt.Errorf("add node metadata: %w", err)
			}
		}
	}
	return nil
}
func (s *Store) initScanDiagnostics(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS scan_diagnostics (scan_id INTEGER PRIMARY KEY REFERENCES scan_runs(id) ON DELETE CASCADE, details TEXT NOT NULL)`)
	return err
}
func (s *Store) SaveScanDiagnostics(ctx context.Context, id int64, details ScanDiagnostics) error {
	if details.Warnings == nil {
		details.Warnings = []ScanWarning{}
	}
	if details.ExcludePatterns == nil {
		details.ExcludePatterns = []string{}
	}
	data, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO scan_diagnostics(scan_id,details) VALUES(?,?) ON CONFLICT(scan_id) DO UPDATE SET details=excluded.details`, id, string(data))
	return err
}
func (s *Store) GetScanDiagnostics(ctx context.Context, id int64) (ScanDiagnostics, error) {
	details := ScanDiagnostics{Warnings: []ScanWarning{}, ExcludePatterns: []string{}}
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT details FROM scan_diagnostics WHERE scan_id=?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.GetScanRun(ctx, id)
		return details, err
	}
	if err != nil {
		return details, err
	}
	err = json.Unmarshal([]byte(data), &details)
	return details, err
}
