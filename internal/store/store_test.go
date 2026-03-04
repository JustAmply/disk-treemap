package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestListChildrenSortedBySizeThenName(t *testing.T) {
	st := newTestStore(t)

	scanID, err := st.CreateScanRun(context.Background(), "/scanroot")
	if err != nil {
		t.Fatalf("create scan: %v", err)
	}
	if err := st.MarkScanRunning(context.Background(), scanID, time.Now().UTC()); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	writer, err := st.BeginNodeWriter(context.Background(), scanID)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}

	nodes := []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 25},
		{Path: "/scanroot/a", ParentPath: "/scanroot", Name: "a", Kind: "dir", SizeBytes: 10},
		{Path: "/scanroot/b", ParentPath: "/scanroot", Name: "b", Kind: "dir", SizeBytes: 10},
		{Path: "/scanroot/c", ParentPath: "/scanroot", Name: "c", Kind: "file", SizeBytes: 5},
	}
	for _, n := range nodes {
		if err := writer.InsertNode(context.Background(), scanID, n); err != nil {
			_ = writer.Rollback()
			t.Fatalf("insert node: %v", err)
		}
	}
	if err := writer.Commit(); err != nil {
		t.Fatalf("commit nodes: %v", err)
	}

	children, err := st.ListChildren(context.Background(), scanID, "/scanroot", 10)
	if err != nil {
		t.Fatalf("list children: %v", err)
	}

	if len(children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(children))
	}
	if children[0].Name != "a" || children[1].Name != "b" || children[2].Name != "c" {
		t.Fatalf("unexpected order: %+v", children)
	}
}

func TestInsertNodesBatchInsertsMultipleRows(t *testing.T) {
	st := newTestStore(t)

	scanID, err := st.CreateScanRun(context.Background(), "/scanroot")
	if err != nil {
		t.Fatalf("create scan: %v", err)
	}

	writer, err := st.BeginNodeWriter(context.Background(), scanID)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}

	nodes := []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 30},
		{Path: "/scanroot/a.bin", ParentPath: "/scanroot", Name: "a.bin", Kind: "file", SizeBytes: 10},
		{Path: "/scanroot/b.bin", ParentPath: "/scanroot", Name: "b.bin", Kind: "file", SizeBytes: 20},
	}

	if err := writer.InsertNodesBatch(context.Background(), scanID, nodes); err != nil {
		_ = writer.Rollback()
		t.Fatalf("insert nodes batch: %v", err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	children, err := st.ListChildren(context.Background(), scanID, "/scanroot", 10)
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(children))
	}
}

func TestInsertNodesBatchChunksWhenExceedingSQLiteParameterLimit(t *testing.T) {
	st := newTestStore(t)

	scanID, err := st.CreateScanRun(context.Background(), "/scanroot")
	if err != nil {
		t.Fatalf("create scan: %v", err)
	}

	writer, err := st.BeginNodeWriter(context.Background(), scanID)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}

	total := maxNodeRowsPerInsert + 10
	nodes := make([]Node, 0, total)
	for i := 0; i < total; i++ {
		nodes = append(nodes, Node{
			Path:       fmt.Sprintf("/scanroot/file-%d.bin", i),
			ParentPath: "/scanroot",
			Name:       fmt.Sprintf("file-%d.bin", i),
			Kind:       "file",
			SizeBytes:  int64(i + 1),
			MtimeUnix:  1000 + int64(i),
		})
	}

	if err := writer.InsertNodesBatch(context.Background(), scanID, nodes); err != nil {
		_ = writer.Rollback()
		t.Fatalf("insert nodes batch: %v", err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var count int
	if err := st.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM nodes WHERE scan_id=?`, scanID).Scan(&count); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if count != total {
		t.Fatalf("expected %d nodes, got %d", total, count)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()

	dataDir := t.TempDir()
	st, err := Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return st
}
