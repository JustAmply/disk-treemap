package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

func (s *Store) SetSnapshotRetention(count int) { s.snapshotRetention = max(1, count) }

func (s *Store) ListSnapshots(ctx context.Context) ([]ScanRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM scan_runs WHERE status='completed' ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	runs := make([]ScanRun, 0, len(ids))
	for _, id := range ids {
		run, err := s.GetScanRun(ctx, id)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

// VisitNodes streams matching nodes without applying the interactive page limit.
func (s *Store) VisitNodes(ctx context.Context, scanID int64, path string, recursive bool, opts NodeQueryOptions, visit func(Node) error) error {
	root, id, err := s.resolveNodeID(ctx, scanID, path)
	if err != nil {
		return err
	}
	prefix, err := descendantRelPrefix(root, path)
	if err != nil {
		return err
	}
	q := strings.Builder{}
	q.WriteString(`WITH RECURSIVE descendants(node_id,parent_id,name,kind,size_bytes,mtime_unix,allocated_bytes,incomplete,excluded,hardlink,rel_path) AS (
 SELECT node_id,parent_id,name,kind,size_bytes,mtime_unix,allocated_bytes,incomplete,excluded,hardlink,?||name FROM nodes WHERE scan_id=? AND parent_id=?`)
	args := []any{prefix, scanID, id}
	if recursive {
		q.WriteString(` UNION ALL SELECT n.node_id,n.parent_id,n.name,n.kind,n.size_bytes,n.mtime_unix,n.allocated_bytes,n.incomplete,n.excluded,n.hardlink,d.rel_path||?||n.name FROM nodes n JOIN descendants d ON n.parent_id=d.node_id WHERE n.scan_id=?`)
		args = append(args, string(filepath.Separator), scanID)
	}
	q.WriteString(`) SELECT node_id,parent_id,name,kind,size_bytes,mtime_unix,allocated_bytes,incomplete,excluded,hardlink,rel_path FROM descendants WHERE 1=1`)
	appendCompactNodeFilters(&q, &args, opts)
	q.WriteString(" ORDER BY " + normalizeSort(opts.Sort))
	rows, err := s.db.QueryContext(ctx, q.String(), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		row, rel, err := scanCompactNodeRowWithRelPath(rows)
		if err != nil {
			return err
		}
		p := filepath.Join(root, rel)
		if err := visit(compactRowToNode(p, compactParentPath(root, p), row)); err != nil {
			return err
		}
	}
	return rows.Err()
}

type SnapshotChange struct {
	Path        string `json:"path"`
	Kind        string `json:"type"`
	Change      string `json:"change"`
	BeforeBytes int64  `json:"before_bytes"`
	AfterBytes  int64  `json:"after_bytes"`
	DeltaBytes  int64  `json:"delta_bytes"`
}

func (s *Store) CompareSnapshots(ctx context.Context, baseID, scanID int64, path string, limit, offset int) ([]SnapshotChange, error) {
	// Relative paths are rebuilt from each snapshot because node IDs are local to a snapshot.
	q := `WITH RECURSIVE tree(scan_id,node_id,name,kind,size_bytes,mtime_unix,path) AS (
 SELECT n.scan_id,n.node_id,n.name,n.kind,n.size_bytes,n.mtime_unix,r.root_path FROM nodes n JOIN scan_runs r ON r.id=n.scan_id WHERE n.parent_id IS NULL AND n.scan_id IN (?,?)
 UNION ALL SELECT n.scan_id,n.node_id,n.name,n.kind,n.size_bytes,n.mtime_unix,rtrim(t.path,?)||?||n.name FROM nodes n JOIN tree t ON n.scan_id=t.scan_id AND n.parent_id=t.node_id
 ), before AS (SELECT * FROM tree WHERE scan_id=?), after AS (SELECT * FROM tree WHERE scan_id=?), changes AS (
 SELECT coalesce(a.path,b.path) path,coalesce(a.kind,b.kind) kind,
 CASE WHEN b.path IS NULL THEN 'added' WHEN a.path IS NULL THEN 'removed' ELSE 'modified' END change,
 coalesce(b.size_bytes,0) before_bytes,coalesce(a.size_bytes,0) after_bytes
 FROM before b FULL OUTER JOIN after a ON a.path=b.path
 WHERE b.path IS NULL OR a.path IS NULL OR a.size_bytes<>b.size_bytes OR a.kind<>b.kind OR a.mtime_unix<>b.mtime_unix
 ) SELECT path,kind,change,before_bytes,after_bytes,after_bytes-before_bytes FROM changes
 WHERE path=? OR substr(path,1,length(?))=? ORDER BY abs(after_bytes-before_bytes) DESC,path ASC LIMIT ? OFFSET ?`
	prefix := strings.TrimRight(path, string(filepath.Separator)) + string(filepath.Separator)
	rows, err := s.db.QueryContext(ctx, q, baseID, scanID, string(filepath.Separator), string(filepath.Separator), baseID, scanID, path, prefix, prefix, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("compare snapshots: %w", err)
	}
	defer rows.Close()
	items := make([]SnapshotChange, 0)
	for rows.Next() {
		var c SnapshotChange
		var kind int
		if err := rows.Scan(&c.Path, &kind, &c.Change, &c.BeforeBytes, &c.AfterBytes, &c.DeltaBytes); err != nil {
			return nil, err
		}
		c.Kind = compactKindName(kind)
		items = append(items, c)
	}
	return items, rows.Err()
}
