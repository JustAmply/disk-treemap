package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
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

func TestAggregateChildrenWithOptions(t *testing.T) {
	st := newTestStore(t)
	scanID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 75},
		{Path: "/scanroot/logs", ParentPath: "/scanroot", Name: "logs", Kind: "dir", SizeBytes: 50},
		{Path: "/scanroot/a.bin", ParentPath: "/scanroot", Name: "a.bin", Kind: "file", SizeBytes: 20},
		{Path: "/scanroot/b.log", ParentPath: "/scanroot", Name: "b.log", Kind: "file", SizeBytes: 5},
	})

	agg, err := st.AggregateChildrenWithOptions(context.Background(), scanID, "/scanroot", NodeQueryOptions{
		Query:   ".",
		Kind:    "file",
		MinSize: 10,
	})
	if err != nil {
		t.Fatalf("aggregate children: %v", err)
	}

	if agg.Count != 1 {
		t.Fatalf("expected 1 matching child, got %d", agg.Count)
	}
	if agg.TotalBytes != 20 {
		t.Fatalf("expected 20 matching bytes, got %d", agg.TotalBytes)
	}
}

func TestGetNodeResolvesDeepCompactPath(t *testing.T) {
	st := newTestStore(t)
	root := "/scanroot"
	dirPath := filepath.Join(root, "a", "b")
	filePath := filepath.Join(dirPath, "payload.bin")

	scanID := insertCompletedScan(t, st, root, []Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 25},
		{Path: filepath.Join(root, "a"), ParentPath: root, Name: "a", Kind: "dir", SizeBytes: 25},
		{Path: dirPath, ParentPath: filepath.Join(root, "a"), Name: "b", Kind: "dir", SizeBytes: 25},
		{Path: filePath, ParentPath: dirPath, Name: "payload.bin", Kind: "file", SizeBytes: 25},
	})

	node, err := st.GetNode(context.Background(), scanID, filePath)
	if err != nil {
		t.Fatalf("get deep node: %v", err)
	}
	if node.Path != filePath || node.ParentPath != dirPath || node.Name != "payload.bin" || node.Kind != "file" {
		t.Fatalf("unexpected deep node: %+v", node)
	}
}

func TestListLargestInPathFindsDeepDescendants(t *testing.T) {
	st := newTestStore(t)
	root := "/scanroot"
	dirPath := filepath.Join(root, "media", "nested")
	largeFile := filepath.Join(dirPath, "large.bin")
	smallFile := filepath.Join(root, "small.bin")

	scanID := insertCompletedScan(t, st, root, []Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 110},
		{Path: filepath.Join(root, "media"), ParentPath: root, Name: "media", Kind: "dir", SizeBytes: 100},
		{Path: dirPath, ParentPath: filepath.Join(root, "media"), Name: "nested", Kind: "dir", SizeBytes: 100},
		{Path: largeFile, ParentPath: dirPath, Name: "large.bin", Kind: "file", SizeBytes: 100},
		{Path: smallFile, ParentPath: root, Name: "small.bin", Kind: "file", SizeBytes: 10},
	})

	items, err := st.ListLargestInPathWithOptions(context.Background(), scanID, filepath.Join(root, "media"), NodeQueryOptions{
		Limit: 10,
		Kind:  "file",
	})
	if err != nil {
		t.Fatalf("list largest deep descendants: %v", err)
	}
	if len(items) != 1 || items[0].Path != largeFile || items[0].ParentPath != dirPath {
		t.Fatalf("unexpected largest descendants: %+v", items)
	}
}

func TestListLargestInPathReturnsEmptyForMissingBasePath(t *testing.T) {
	st := newTestStore(t)
	root := "/scanroot"

	scanID := insertCompletedScan(t, st, root, []Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 10},
		{Path: filepath.Join(root, "file.bin"), ParentPath: root, Name: "file.bin", Kind: "file", SizeBytes: 10},
	})

	items, err := st.ListLargestInPath(context.Background(), scanID, filepath.Join(root, "missing"), 10)
	if err != nil {
		t.Fatalf("list largest missing base: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items for missing base path, got %+v", items)
	}
}

func TestSnapshotWriterPublishesMultipleNodes(t *testing.T) {
	st := newTestStore(t)

	scanID, err := st.QueueRun(context.Background(), "/scanroot")
	if err != nil {
		t.Fatalf("create scan: %v", err)
	}

	writer, err := st.BeginSnapshot(context.Background(), scanID)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}

	nodes := []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 30},
		{Path: "/scanroot/a.bin", ParentPath: "/scanroot", Name: "a.bin", Kind: "file", SizeBytes: 10},
		{Path: "/scanroot/b.bin", ParentPath: "/scanroot", Name: "b.bin", Kind: "file", SizeBytes: 20},
	}

	if err := writer.Write(context.Background(), nodes); err != nil {
		_ = writer.Discard()
		t.Fatalf("insert nodes batch: %v", err)
	}
	if err := writer.Publish(); err != nil {
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

func TestRunTransitionsRejectInvalidOrder(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	scanID, err := st.QueueRun(ctx, "/scanroot")
	if err != nil {
		t.Fatalf("queue run: %v", err)
	}

	completed := ScanOutcome{Status: ScanCompleted, FinishedAt: time.Now().UTC()}
	if err := st.FinishRun(ctx, scanID, completed); !errors.Is(err, ErrInvalidScanTransition) {
		t.Fatalf("expected queued-to-completed transition error, got %v", err)
	}
	if err := st.StartRun(ctx, scanID, time.Now().UTC()); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := st.StartRun(ctx, scanID, time.Now().UTC()); !errors.Is(err, ErrInvalidScanTransition) {
		t.Fatalf("expected duplicate start transition error, got %v", err)
	}
	if err := st.FinishRun(ctx, scanID, completed); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	if err := st.FinishRun(ctx, scanID, ScanOutcome{Status: ScanFailed, FinishedAt: time.Now().UTC()}); !errors.Is(err, ErrInvalidScanTransition) {
		t.Fatalf("expected completed-to-failed transition error, got %v", err)
	}
	if err := st.FinishRun(ctx, scanID, ScanOutcome{Status: ScanRunning, FinishedAt: time.Now().UTC()}); err == nil {
		t.Fatalf("expected non-terminal outcome error")
	}

	run, err := st.GetScanRun(ctx, scanID)
	if err != nil {
		t.Fatalf("get completed run: %v", err)
	}
	if run.Status != ScanCompleted {
		t.Fatalf("expected completed run to remain completed, got %s", run.Status)
	}
}

func TestSnapshotWriterSupportsParentInLaterBatch(t *testing.T) {
	st := newTestStore(t)

	scanID, err := st.QueueRun(context.Background(), "/scanroot")
	if err != nil {
		t.Fatalf("create scan: %v", err)
	}

	writer, err := st.BeginSnapshot(context.Background(), scanID)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}

	filePath := filepath.Join("/scanroot", "dir", "file.bin")
	dirPath := filepath.Join("/scanroot", "dir")
	for _, batch := range [][]Node{
		{{Path: filePath, ParentPath: dirPath, Name: "file.bin", Kind: "file", SizeBytes: 10}},
		{{Path: dirPath, ParentPath: "/scanroot", Name: "dir", Kind: "dir", SizeBytes: 10}},
		{{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 10}},
	} {
		if err := writer.Write(context.Background(), batch); err != nil {
			_ = writer.Discard()
			t.Fatalf("write out-of-order batch: %v", err)
		}
	}
	if err := writer.Publish(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	file, err := st.GetNode(context.Background(), scanID, filePath)
	if err != nil {
		t.Fatalf("get out-of-order file: %v", err)
	}
	if file.ParentPath != dirPath || file.Name != "file.bin" {
		t.Fatalf("unexpected file node: %+v", file)
	}
}

func TestSnapshotWriterRejectsMissingParentOnPublish(t *testing.T) {
	st := newTestStore(t)

	scanID, err := st.QueueRun(context.Background(), "/scanroot")
	if err != nil {
		t.Fatalf("create scan: %v", err)
	}

	writer, err := st.BeginSnapshot(context.Background(), scanID)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	defer writer.Discard()

	err = writer.Write(context.Background(), []Node{
		{Path: filepath.Join("/scanroot", "orphan.bin"), ParentPath: "/scanroot", Name: "orphan.bin", Kind: "file", SizeBytes: 10},
	})
	if err != nil {
		t.Fatalf("write orphan node: %v", err)
	}
	err = writer.Publish()
	if err == nil || !strings.Contains(err.Error(), "parent path") {
		t.Fatalf("expected missing parent error, got %v", err)
	}
}

func TestSnapshotWriterChunksBeyondSQLiteParameterLimit(t *testing.T) {
	st := newTestStore(t)

	scanID, err := st.QueueRun(context.Background(), "/scanroot")
	if err != nil {
		t.Fatalf("create scan: %v", err)
	}

	writer, err := st.BeginSnapshot(context.Background(), scanID)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}

	total := maxNodeRowsPerInsert + 10
	nodes := []Node{{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 0}}
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

	if err := writer.Write(context.Background(), nodes); err != nil {
		_ = writer.Discard()
		t.Fatalf("insert nodes batch: %v", err)
	}
	if err := writer.Publish(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var count int
	if err := st.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM nodes WHERE scan_id=?`, scanID).Scan(&count); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if count != len(nodes) {
		t.Fatalf("expected %d nodes, got %d", len(nodes), count)
	}
}

func TestCompactSchemaIsSmallerForDeepPaths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scanroot")
	nodes := syntheticDeepPathNodes(root, 1200)

	legacyDBPath := filepath.Join(t.TempDir(), "legacy.db")
	insertLegacyFixture(t, legacyDBPath, root, nodes)
	legacySize := fileSize(t, legacyDBPath)

	compactDBPath := filepath.Join(t.TempDir(), "compact.db")
	st, err := Open(compactDBPath)
	if err != nil {
		t.Fatalf("open compact store: %v", err)
	}
	if err := st.Init(context.Background()); err != nil {
		_ = st.Close()
		t.Fatalf("init compact store: %v", err)
	}
	insertCompletedScan(t, st, root, nodes)
	if err := st.OptimizeStorage(context.Background(), true); err != nil {
		_ = st.Close()
		t.Fatalf("optimize compact store: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close compact store: %v", err)
	}
	compactSize := fileSize(t, compactDBPath)
	t.Logf("synthetic deep fixture DB size: legacy=%d compact=%d reduction=%.1f%%", legacySize, compactSize, 100*(1-float64(compactSize)/float64(legacySize)))

	if compactSize >= legacySize*6/10 {
		t.Fatalf("expected compact DB to be at least 40%% smaller, legacy=%d compact=%d", legacySize, compactSize)
	}
}

func TestInitReplacesLegacyDatabaseWithEmptyCompactStore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scanroot")
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	insertLegacyFixture(t, dbPath, root, []Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 10},
		{Path: filepath.Join(root, "old.bin"), ParentPath: root, Name: "old.bin", Kind: "file", SizeBytes: 10},
	})

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("replace legacy store: %v", err)
	}

	current, err := st.GetLatestScanRun(context.Background())
	if err != nil {
		t.Fatalf("get latest scan after replacement: %v", err)
	}
	if current != nil {
		t.Fatalf("expected legacy run to be removed, got %+v", current)
	}
	if exists, err := st.tableExists(context.Background(), "legacy_nodes"); err != nil || exists {
		t.Fatalf("expected legacy table to be removed, exists=%t err=%v", exists, err)
	}
}

func TestInitDiscardsOnlyRunsBackedByLegacyNodes(t *testing.T) {
	st := newTestStore(t)
	root := filepath.Join(t.TempDir(), "scanroot")
	compactID := insertCompletedScan(t, st, root, []Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 20},
		{Path: filepath.Join(root, "current.bin"), ParentPath: root, Name: "current.bin", Kind: "file", SizeBytes: 20},
	})

	legacyID, err := st.QueueRun(context.Background(), root)
	if err != nil {
		t.Fatalf("create legacy run metadata: %v", err)
	}
	if err := st.StartRun(context.Background(), legacyID, time.Now().UTC()); err != nil {
		t.Fatalf("start legacy run metadata: %v", err)
	}
	if err := st.FinishRun(context.Background(), legacyID, ScanOutcome{
		Status:     ScanCompleted,
		FinishedAt: time.Now().UTC(),
		TotalBytes: 10,
		TotalNodes: 1,
	}); err != nil {
		t.Fatalf("complete legacy run metadata: %v", err)
	}
	if _, err := st.db.ExecContext(context.Background(), `
		CREATE TABLE legacy_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_id INTEGER NOT NULL,
			path TEXT NOT NULL,
			parent_path TEXT NOT NULL,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			mtime_unix INTEGER NOT NULL,
			FOREIGN KEY (scan_id) REFERENCES scan_runs(id) ON DELETE CASCADE
		)
	`); err != nil {
		t.Fatalf("create coexisting legacy table: %v", err)
	}
	if _, err := st.db.ExecContext(context.Background(), `
		INSERT INTO legacy_nodes(scan_id, path, parent_path, name, kind, size_bytes, mtime_unix)
		VALUES(?, ?, '', ?, 'dir', 10, 1)
	`, legacyID, root, filepath.Base(root)); err != nil {
		t.Fatalf("insert coexisting legacy node: %v", err)
	}

	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("discard coexisting legacy run: %v", err)
	}
	if _, err := st.GetScanRun(context.Background(), legacyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected legacy run %d to be removed, got %v", legacyID, err)
	}
	if _, err := st.GetNode(context.Background(), compactID, root); err != nil {
		t.Fatalf("expected compact snapshot %d to remain readable: %v", compactID, err)
	}
}

func TestPruneOperationalScansKeepsLatestAndLatestCompleted(t *testing.T) {
	st := newTestStore(t)

	olderCompleted := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 10},
	})
	latestCompleted := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 20},
	})
	failedID, err := st.QueueRun(context.Background(), "/scanroot")
	if err != nil {
		t.Fatalf("create failed run: %v", err)
	}
	if err := st.StartRun(context.Background(), failedID, time.Now().UTC()); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if err := st.FinishRun(context.Background(), failedID, ScanOutcome{
		Status:       ScanFailed,
		FinishedAt:   time.Now().UTC(),
		WarningCount: 1,
		Error:        "boom",
	}); err != nil {
		t.Fatalf("complete failed run: %v", err)
	}

	deleted, err := st.PruneOperationalScans(context.Background())
	if err != nil {
		t.Fatalf("prune scans: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != olderCompleted {
		t.Fatalf("unexpected deleted scans: %v", deleted)
	}

	current, err := st.GetLatestScanRun(context.Background())
	if err != nil {
		t.Fatalf("get latest scan: %v", err)
	}
	if current == nil || current.ID != failedID {
		t.Fatalf("expected failed run to remain current, got %+v", current)
	}

	completed, err := st.GetLatestCompletedScanRun(context.Background())
	if err != nil {
		t.Fatalf("get latest completed scan: %v", err)
	}
	if completed == nil || completed.ID != latestCompleted {
		t.Fatalf("expected latest completed run to remain, got %+v", completed)
	}
}

func TestFailInterruptedScansMarksQueuedAndRunningAsFailed(t *testing.T) {
	st := newTestStore(t)

	completedID := insertCompletedScan(t, st, "/scanroot", []Node{
		{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 10},
	})

	queuedID, err := st.QueueRun(context.Background(), "/scanroot")
	if err != nil {
		t.Fatalf("create queued run: %v", err)
	}
	runningID, err := st.QueueRun(context.Background(), "/scanroot")
	if err != nil {
		t.Fatalf("create running run: %v", err)
	}
	if err := st.StartRun(context.Background(), runningID, time.Now().UTC()); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	finishedAt := time.Now().UTC()
	interrupted, err := st.FailInterruptedScans(context.Background(), finishedAt)
	if err != nil {
		t.Fatalf("fail interrupted scans: %v", err)
	}
	if len(interrupted) != 2 || interrupted[0] != queuedID || interrupted[1] != runningID {
		t.Fatalf("unexpected interrupted ids: %v", interrupted)
	}

	for _, scanID := range []int64{queuedID, runningID} {
		run, err := st.GetScanRun(context.Background(), scanID)
		if err != nil {
			t.Fatalf("get scan %d: %v", scanID, err)
		}
		if run.Status != "failed" {
			t.Fatalf("expected scan %d failed, got %s", scanID, run.Status)
		}
		if run.FinishedAt == nil {
			t.Fatalf("expected scan %d to have a finish time", scanID)
		}
		if !strings.Contains(run.Error, "interrupted") {
			t.Fatalf("expected interrupted error for scan %d, got %q", scanID, run.Error)
		}
	}

	completed, err := st.GetLatestCompletedScanRun(context.Background())
	if err != nil {
		t.Fatalf("get latest completed scan: %v", err)
	}
	if completed == nil || completed.ID != completedID {
		t.Fatalf("expected completed scan %d to remain latest completed, got %+v", completedID, completed)
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

func BenchmarkListLargestInPath(b *testing.B) {
	st := newTestStoreForBenchmark(b)

	nodes := []Node{{Path: "/scanroot", ParentPath: "", Name: "scanroot", Kind: "dir", SizeBytes: 0}}
	for i := 0; i < 500; i++ {
		nodes = append(nodes, Node{
			Path:       fmt.Sprintf("/scanroot/file-%d.bin", i),
			ParentPath: "/scanroot",
			Name:       fmt.Sprintf("file-%d.bin", i),
			Kind:       "file",
			SizeBytes:  int64(i + 1),
		})
	}

	scanID := insertCompletedScanBenchmark(b, st, "/scanroot", nodes)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.ListLargestInPath(context.Background(), scanID, "/scanroot", 200); err != nil {
			b.Fatalf("list largest: %v", err)
		}
	}
}

func insertCompletedScan(t *testing.T, st *Store, root string, nodes []Node) int64 {
	t.Helper()
	scanID, err := st.QueueRun(context.Background(), root)
	if err != nil {
		t.Fatalf("create scan: %v", err)
	}
	if err := st.StartRun(context.Background(), scanID, time.Now().UTC()); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	writer, err := st.BeginSnapshot(context.Background(), scanID)
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	if err := writer.Write(context.Background(), nodes); err != nil {
		_ = writer.Discard()
		t.Fatalf("insert nodes: %v", err)
	}
	if err := writer.Publish(); err != nil {
		t.Fatalf("commit nodes: %v", err)
	}
	var total int64
	for _, n := range nodes {
		total += n.SizeBytes
	}
	if err := st.FinishRun(context.Background(), scanID, ScanOutcome{
		Status:     ScanCompleted,
		FinishedAt: time.Now().UTC(),
		TotalBytes: total,
		TotalNodes: int64(len(nodes)),
	}); err != nil {
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
	scanID, err := st.QueueRun(context.Background(), root)
	if err != nil {
		b.Fatalf("create scan: %v", err)
	}
	if err := st.StartRun(context.Background(), scanID, time.Now().UTC()); err != nil {
		b.Fatalf("mark running: %v", err)
	}
	writer, err := st.BeginSnapshot(context.Background(), scanID)
	if err != nil {
		b.Fatalf("begin writer: %v", err)
	}
	if err := writer.Write(context.Background(), nodes); err != nil {
		_ = writer.Discard()
		b.Fatalf("insert nodes: %v", err)
	}
	if err := writer.Publish(); err != nil {
		b.Fatalf("commit nodes: %v", err)
	}

	if err := st.FinishRun(context.Background(), scanID, ScanOutcome{
		Status:     ScanCompleted,
		FinishedAt: time.Now().UTC(),
		TotalNodes: int64(len(nodes)),
	}); err != nil {
		b.Fatalf("complete scan: %v", err)
	}
	return scanID
}

func TestNormalizeSortDefaultsToSizeDesc(t *testing.T) {
	if got := normalizeSort("invalid"); !strings.Contains(got, "size_bytes DESC") {
		t.Fatalf("expected fallback sort, got %q", got)
	}
}

func syntheticDeepPathNodes(root string, fileCount int) []Node {
	nodes := []Node{{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: int64(fileCount)}}
	parent := root
	for i := 0; i < 24; i++ {
		name := fmt.Sprintf("segment-%02d-with-a-long-repeated-directory-name", i)
		path := filepath.Join(parent, name)
		nodes = append(nodes, Node{
			Path:       path,
			ParentPath: parent,
			Name:       name,
			Kind:       "dir",
			SizeBytes:  int64(fileCount),
			MtimeUnix:  1,
		})
		parent = path
	}
	for i := 0; i < fileCount; i++ {
		name := fmt.Sprintf("file-%04d.bin", i)
		nodes = append(nodes, Node{
			Path:       filepath.Join(parent, name),
			ParentPath: parent,
			Name:       name,
			Kind:       "file",
			SizeBytes:  1,
			MtimeUnix:  1,
		})
	}
	return nodes
}

func insertLegacyFixture(t *testing.T, dbPath, root string, nodes []Node) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s", filepath.ToSlash(dbPath)))
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE scan_runs (
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
		`CREATE TABLE nodes (
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
		`CREATE UNIQUE INDEX idx_nodes_scan_path ON nodes(scan_id, path);`,
		`CREATE INDEX idx_nodes_scan_parent ON nodes(scan_id, parent_path);`,
		`CREATE INDEX idx_nodes_scan_size ON nodes(scan_id, size_bytes DESC);`,
		`CREATE INDEX idx_nodes_scan_parent_kind_name ON nodes(scan_id, parent_path, kind, name);`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create legacy fixture schema: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO scan_runs(id, status, root_path, total_nodes) VALUES(1, 'completed', ?, ?)`, root, len(nodes)); err != nil {
		t.Fatalf("insert legacy scan: %v", err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin legacy fixture tx: %v", err)
	}
	stmt, err := tx.Prepare(`INSERT INTO nodes(scan_id, path, parent_path, name, kind, size_bytes, mtime_unix) VALUES(1, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("prepare legacy insert: %v", err)
	}
	for _, node := range nodes {
		if _, err := stmt.Exec(node.Path, node.ParentPath, node.Name, node.Kind, node.SizeBytes, node.MtimeUnix); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			t.Fatalf("insert legacy node: %v", err)
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		t.Fatalf("close legacy insert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit legacy fixture: %v", err)
	}
	if _, err := db.Exec(`VACUUM`); err != nil {
		t.Fatalf("vacuum legacy fixture: %v", err)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}
