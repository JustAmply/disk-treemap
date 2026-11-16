package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNodeMetadataAndDiagnosticsRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	root := filepath.Clean("/scanroot")
	id, err := st.QueueRun(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	bytes := int64(4096)
	writer, err := st.BeginSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []Node{{Path: root, Name: "scanroot", Kind: "dir", AllocatedBytes: &bytes, Incomplete: true}, {Path: filepath.Join(root, "file"), ParentPath: root, Name: "file", Kind: "file", AllocatedBytes: &bytes, Hardlink: true}, {Path: filepath.Join(root, "cache"), ParentPath: root, Name: "cache", Kind: "dir", Excluded: true}}
	if err = writer.Write(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	if err = writer.Publish(); err != nil {
		t.Fatal(err)
	}
	node, err := st.GetNode(ctx, id, root)
	if err != nil {
		t.Fatal(err)
	}
	if node.AllocatedBytes == nil || *node.AllocatedBytes != bytes || !node.Incomplete {
		t.Fatalf("lost root metadata: %+v", node)
	}
	children, err := st.ListChildrenWithOptions(ctx, id, root, NodeQueryOptions{Sort: "name_asc"})
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 || !children[0].Excluded || !children[1].Hardlink {
		t.Fatalf("lost child metadata: %+v", children)
	}
	largest, err := st.ListLargestInPathWithOptions(ctx, id, root, NodeQueryOptions{Kind: "file"})
	if err != nil {
		t.Fatal(err)
	}
	if len(largest) != 1 || largest[0].AllocatedBytes == nil || !largest[0].Hardlink {
		t.Fatalf("lost recursive metadata: %+v", largest)
	}
	details := ScanDiagnostics{Warnings: []ScanWarning{{Path: root, Operation: "read_directory", Message: "permission denied"}}, ExcludedCount: 1, ExcludePatterns: []string{"cache"}}
	if err = st.SaveScanDiagnostics(ctx, id, details); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.GetScanDiagnostics(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Warnings) != 1 || loaded.Warnings[0].Message != "permission denied" || loaded.ExcludedCount != 1 {
		t.Fatalf("lost diagnostics: %+v", loaded)
	}
	if _, err = st.db.ExecContext(ctx, "DELETE FROM scan_runs WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = st.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM scan_diagnostics").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("diagnostics were not pruned with scan")
	}
}
func TestMetadataMigrationPreservesExistingCompactSnapshot(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.db.ExecContext(ctx, `DROP TABLE nodes; CREATE TABLE nodes (scan_id INTEGER NOT NULL,node_id INTEGER NOT NULL,parent_id INTEGER,name TEXT NOT NULL,kind INTEGER NOT NULL,size_bytes INTEGER NOT NULL,mtime_unix INTEGER NOT NULL,PRIMARY KEY(scan_id,node_id)) WITHOUT ROWID; INSERT INTO scan_runs(id,status,root_path) VALUES(1,'completed','/scanroot'); INSERT INTO nodes VALUES(1,1,NULL,'scanroot',2,123,0);`); err != nil {
		t.Fatal(err)
	}
	if err := st.Init(ctx); err != nil {
		t.Fatal(err)
	}
	node, err := st.GetNode(ctx, 1, "/scanroot")
	if err != nil {
		t.Fatal(err)
	}
	if node.SizeBytes != 123 || node.AllocatedBytes != nil || node.Incomplete {
		t.Fatalf("migration changed existing data: %+v", node)
	}
}
