package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListChildrenSortedBySizeThenName(t *testing.T) {
	st := newTestStore(t)

	scanID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 25},
		{Path: "/scanroot/a", ParentPath: "/scanroot", Name: "a", Kind: "dir", SizeBytes: 10},
		{Path: "/scanroot/b", ParentPath: "/scanroot", Name: "b", Kind: "dir", SizeBytes: 10},
		{Path: "/scanroot/c", ParentPath: "/scanroot", Name: "c", Kind: "file", SizeBytes: 5},
	})

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

func TestListChildrenWithFilters(t *testing.T) {
	st := newTestStore(t)
	scanID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 50},
		{Path: "/scanroot/logs", ParentPath: "/scanroot", Name: "logs", Kind: "dir", SizeBytes: 40},
		{Path: "/scanroot/tmp.bin", ParentPath: "/scanroot", Name: "tmp.bin", Kind: "file", SizeBytes: 20},
		{Path: "/scanroot/trace.log", ParentPath: "/scanroot", Name: "trace.log", Kind: "file", SizeBytes: 5},
	})

	children, err := st.ListChildrenWithOptions(context.Background(), scanID, "/scanroot", NodeQueryOptions{
		Limit:   10,
		Kind:    "file",
		Query:   ".log",
		MinSize: 6,
	})
	if err != nil {
		t.Fatalf("list children with filters: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("expected no files after min size filter, got %+v", children)
	}

	children, err = st.ListChildrenWithOptions(context.Background(), scanID, "/scanroot", NodeQueryOptions{
		Limit:   10,
		Kind:    "file",
		Query:   ".log",
		MinSize: 1,
	})
	if err != nil {
		t.Fatalf("list children with filters: %v", err)
	}
	if len(children) != 1 || children[0].Name != "trace.log" {
		t.Fatalf("unexpected filtered children: %+v", children)
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

func TestPruneCompletedFailedScansKeepsNewestN(t *testing.T) {
	st := newTestStore(t)

	for i := 0; i < 6; i++ {
		insertCompletedScan(t, st, "/scanroot", []Node{{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: int64(i)}})
	}

	deleted, err := st.PruneCompletedFailedScans(context.Background(), 2)
	if err != nil {
		t.Fatalf("prune scans: %v", err)
	}
	if len(deleted) != 4 {
		t.Fatalf("expected 4 deleted scans, got %d", len(deleted))
	}

	runs, err := st.ListScanRuns(context.Background(), 10, "completed")
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 completed runs after prune, got %d", len(runs))
	}
}

func TestDeleteScanRunCascadesNodes(t *testing.T) {
	st := newTestStore(t)
	scanID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 10},
		{Path: "/scanroot/a", ParentPath: "/scanroot", Name: "a", Kind: "file", SizeBytes: 5},
	})

	deleted, err := st.DeleteScanRun(context.Background(), scanID)
	if err != nil {
		t.Fatalf("delete scan run: %v", err)
	}
	if !deleted {
		t.Fatalf("expected deleted=true")
	}

	var count int
	if err := st.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM nodes WHERE scan_id=?`, scanID).Scan(&count); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 nodes after delete, got %d", count)
	}
}

func TestListDirectoryDiffIncludesGrowthShrinkNewRemoved(t *testing.T) {
	st := newTestStore(t)

	baseID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 300},
		{Path: "/scanroot/a", ParentPath: "/scanroot", Name: "a", Kind: "dir", SizeBytes: 100},
		{Path: "/scanroot/b", ParentPath: "/scanroot", Name: "b", Kind: "dir", SizeBytes: 200},
	})
	targetID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 350},
		{Path: "/scanroot/a", ParentPath: "/scanroot", Name: "a", Kind: "dir", SizeBytes: 120},
		{Path: "/scanroot/c", ParentPath: "/scanroot", Name: "c", Kind: "dir", SizeBytes: 230},
	})

	diffs, err := st.ListDirectoryDiff(context.Background(), targetID, baseID, "/scanroot", 10)
	if err != nil {
		t.Fatalf("list directory diff: %v", err)
	}
	if len(diffs) != 3 {
		t.Fatalf("expected 3 diff rows, got %d", len(diffs))
	}

	seen := map[string]string{}
	for _, d := range diffs {
		seen[d.Name] = d.ChangeClass
	}
	if seen["a"] != "grew" {
		t.Fatalf("expected a to grow, got %q", seen["a"])
	}
	if seen["b"] != "removed" {
		t.Fatalf("expected b to be removed, got %q", seen["b"])
	}
	if seen["c"] != "new" {
		t.Fatalf("expected c to be new, got %q", seen["c"])
	}
}

func TestListLargestInPathWithOptionsSupportsSort(t *testing.T) {
	st := newTestStore(t)
	scanID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 100},
		{Path: "/scanroot/zeta", ParentPath: "/scanroot", Name: "zeta", Kind: "file", SizeBytes: 10},
		{Path: "/scanroot/alpha", ParentPath: "/scanroot", Name: "alpha", Kind: "file", SizeBytes: 9},
	})

	items, err := st.ListLargestInPathWithOptions(context.Background(), scanID, "/scanroot", NodeQueryOptions{
		Limit: 10,
		Sort:  "name_asc",
		Kind:  "file",
	})
	if err != nil {
		t.Fatalf("list largest with options: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Name != "alpha" || items[1].Name != "zeta" {
		t.Fatalf("unexpected sort order: %+v", items)
	}
}

func BenchmarkListDirectoryDiff(b *testing.B) {
	st := newTestStoreForBenchmark(b)

	baseNodes := []Node{{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 0}}
	targetNodes := []Node{{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 0}}
	for i := 0; i < 500; i++ {
		name := fmt.Sprintf("dir-%d", i)
		baseNodes = append(baseNodes, Node{Path: "/scanroot/" + name, ParentPath: "/scanroot", Name: name, Kind: "dir", SizeBytes: int64(i)})
		targetNodes = append(targetNodes, Node{Path: "/scanroot/" + name, ParentPath: "/scanroot", Name: name, Kind: "dir", SizeBytes: int64(i + 25)})
	}

	baseID := insertCompletedScanBenchmark(b, st, "/scanroot", baseNodes)
	targetID := insertCompletedScanBenchmark(b, st, "/scanroot", targetNodes)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.ListDirectoryDiff(context.Background(), targetID, baseID, "/scanroot", 200); err != nil {
			b.Fatalf("list diff: %v", err)
		}
	}
}

func insertCompletedScan(t *testing.T, st *Store, root string, nodes []Node) int64 {
	t.Helper()
	scanID, err := st.CreateScanRun(context.Background(), root)
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
	if err := writer.InsertNodesBatch(context.Background(), scanID, nodes); err != nil {
		_ = writer.Rollback()
		t.Fatalf("insert nodes: %v", err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatalf("commit nodes: %v", err)
	}
	var total int64
	for _, n := range nodes {
		total += n.SizeBytes
	}
	if err := st.CompleteScan(context.Background(), scanID, "completed", time.Now().UTC(), total, int64(len(nodes)), 0, ""); err != nil {
		t.Fatalf("complete scan: %v", err)
	}
	return scanID
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

func newTestStoreForBenchmark(b *testing.B) *Store {
	b.Helper()

	dataDir := b.TempDir()
	st, err := Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	b.Cleanup(func() {
		_ = st.Close()
	})
	if err := st.Init(context.Background()); err != nil {
		b.Fatalf("init store: %v", err)
	}
	return st
}

func insertCompletedScanBenchmark(b *testing.B, st *Store, root string, nodes []Node) int64 {
	b.Helper()
	scanID, err := st.CreateScanRun(context.Background(), root)
	if err != nil {
		b.Fatalf("create scan: %v", err)
	}
	if err := st.MarkScanRunning(context.Background(), scanID, time.Now().UTC()); err != nil {
		b.Fatalf("mark running: %v", err)
	}
	writer, err := st.BeginNodeWriter(context.Background(), scanID)
	if err != nil {
		b.Fatalf("begin writer: %v", err)
	}
	if err := writer.InsertNodesBatch(context.Background(), scanID, nodes); err != nil {
		_ = writer.Rollback()
		b.Fatalf("insert nodes: %v", err)
	}
	if err := writer.Commit(); err != nil {
		b.Fatalf("commit nodes: %v", err)
	}

	if err := st.CompleteScan(context.Background(), scanID, "completed", time.Now().UTC(), 0, int64(len(nodes)), 0, ""); err != nil {
		b.Fatalf("complete scan: %v", err)
	}
	return scanID
}

func TestNormalizeSortDefaultsToSizeDesc(t *testing.T) {
	if got := normalizeSort("invalid"); !strings.Contains(got, "size_bytes DESC") {
		t.Fatalf("expected fallback sort, got %q", got)
	}
}

func TestListDiffChildrenIncludesFilesDirsAndOmitsUnchanged(t *testing.T) {
	st := newTestStore(t)

	baseID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 220},
		{Path: "/scanroot/docs", ParentPath: "/scanroot", Name: "docs", Kind: "dir", SizeBytes: 100},
		{Path: "/scanroot/stable", ParentPath: "/scanroot", Name: "stable", Kind: "dir", SizeBytes: 10},
		{Path: "/scanroot/old.log", ParentPath: "/scanroot", Name: "old.log", Kind: "file", SizeBytes: 40},
		{Path: "/scanroot/report.txt", ParentPath: "/scanroot", Name: "report.txt", Kind: "file", SizeBytes: 20},
	})
	targetID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 365},
		{Path: "/scanroot/docs", ParentPath: "/scanroot", Name: "docs", Kind: "dir", SizeBytes: 160},
		{Path: "/scanroot/stable", ParentPath: "/scanroot", Name: "stable", Kind: "dir", SizeBytes: 10},
		{Path: "/scanroot/new.txt", ParentPath: "/scanroot", Name: "new.txt", Kind: "file", SizeBytes: 95},
		{Path: "/scanroot/report.txt", ParentPath: "/scanroot", Name: "report.txt", Kind: "file", SizeBytes: 25},
	})

	items, err := st.ListDiffChildren(context.Background(), targetID, baseID, "/scanroot", DiffQueryOptions{Limit: 10, Sort: "delta_desc"})
	if err != nil {
		t.Fatalf("list diff children: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("expected 4 diff rows, got %d", len(items))
	}

	seen := map[string]DiffItem{}
	for _, item := range items {
		seen[item.Name+"|"+item.Kind] = item
	}

	docs := seen["docs|dir"]
	if docs.ChangeClass != "grew" || docs.DeltaBytes != 60 || docs.VisualSizeBytes != 160 {
		t.Fatalf("unexpected docs diff: %+v", docs)
	}
	oldLog := seen["old.log|file"]
	if oldLog.ChangeClass != "removed" || oldLog.BeforeExists != true || oldLog.AfterExists != false {
		t.Fatalf("unexpected removed file diff: %+v", oldLog)
	}
	newTxt := seen["new.txt|file"]
	if newTxt.ChangeClass != "new" || newTxt.VisualSizeBytes != 95 {
		t.Fatalf("unexpected new file diff: %+v", newTxt)
	}
	if _, ok := seen["stable|dir"]; ok {
		t.Fatalf("unchanged item should be omitted: %+v", seen["stable|dir"])
	}
}

func TestListDiffChildrenSupportsFiltersAndSizeSort(t *testing.T) {
	st := newTestStore(t)

	baseID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 150},
		{Path: "/scanroot/a.txt", ParentPath: "/scanroot", Name: "a.txt", Kind: "file", SizeBytes: 5},
		{Path: "/scanroot/logs", ParentPath: "/scanroot", Name: "logs", Kind: "dir", SizeBytes: 30},
	})
	targetID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 280},
		{Path: "/scanroot/a.txt", ParentPath: "/scanroot", Name: "a.txt", Kind: "file", SizeBytes: 80},
		{Path: "/scanroot/archive.txt", ParentPath: "/scanroot", Name: "archive.txt", Kind: "file", SizeBytes: 60},
		{Path: "/scanroot/logs", ParentPath: "/scanroot", Name: "logs", Kind: "dir", SizeBytes: 90},
	})

	items, err := st.ListDiffChildren(context.Background(), targetID, baseID, "/scanroot", DiffQueryOptions{
		Limit:   10,
		Kind:    "file",
		Query:   ".txt",
		MinSize: 50,
		Sort:    "size_desc",
	})
	if err != nil {
		t.Fatalf("list filtered diff children: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 filtered diff rows, got %d", len(items))
	}
	if items[0].Name != "a.txt" || items[1].Name != "archive.txt" {
		t.Fatalf("unexpected filtered order: %+v", items)
	}
}
