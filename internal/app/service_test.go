package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justamply/disk-treemap/internal/config"
	"github.com/justamply/disk-treemap/internal/scan"
	"github.com/justamply/disk-treemap/internal/scancontrol"
	"github.com/justamply/disk-treemap/internal/store"
)

type blockingScanner struct {
	root    string
	start   chan struct{}
	release chan struct{}
}

func (b *blockingScanner) Scan(ctx context.Context, cb scan.NodeCallback) (scan.Result, error) {
	close(b.start)
	select {
	case <-b.release:
	case <-ctx.Done():
		return scan.Result{}, ctx.Err()
	}

	if err := cb(scan.NodeRecord{
		Path:       b.root,
		ParentPath: "",
		Name:       filepath.Base(b.root),
		Kind:       "dir",
		SizeBytes:  1,
		MtimeUnix:  time.Now().Unix(),
	}); err != nil {
		return scan.Result{}, err
	}

	return scan.Result{TotalBytes: 1, TotalNodes: 1}, nil
}

type shutdownBlockingScanner struct {
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (s *shutdownBlockingScanner) Scan(ctx context.Context, _ scan.NodeCallback) (scan.Result, error) {
	close(s.started)
	<-ctx.Done()
	close(s.canceled)
	<-s.release
	return scan.Result{}, ctx.Err()
}

type progressScanner struct {
	root      string
	firstPath string
	reached   chan struct{}
	release   chan struct{}
}

func (p *progressScanner) Scan(ctx context.Context, cb scan.NodeCallback) (scan.Result, error) {
	if err := cb(scan.NodeRecord{
		Path:       p.firstPath,
		ParentPath: p.root,
		Name:       filepath.Base(p.firstPath),
		Kind:       "file",
		SizeBytes:  42,
		MtimeUnix:  time.Now().Unix(),
	}); err != nil {
		return scan.Result{}, err
	}
	close(p.reached)

	select {
	case <-p.release:
	case <-ctx.Done():
		return scan.Result{}, ctx.Err()
	}

	if err := cb(scan.NodeRecord{
		Path:       p.root,
		ParentPath: "",
		Name:       filepath.Base(p.root),
		Kind:       "dir",
		SizeBytes:  42,
		MtimeUnix:  time.Now().Unix(),
	}); err != nil {
		return scan.Result{}, err
	}

	return scan.Result{TotalBytes: 42, TotalNodes: 2}, nil
}

type staticResultScanner struct {
	result scan.Result
}

func (s *staticResultScanner) Scan(ctx context.Context, cb scan.NodeCallback) (scan.Result, error) {
	return s.result, nil
}

type duplicateNodeScanner struct {
	root string
}

func (d *duplicateNodeScanner) Scan(ctx context.Context, cb scan.NodeCallback) (scan.Result, error) {
	node := scan.NodeRecord{
		Path:       filepath.Join(d.root, "dup.bin"),
		ParentPath: d.root,
		Name:       "dup.bin",
		Kind:       "file",
		SizeBytes:  1,
		MtimeUnix:  time.Now().Unix(),
	}
	if err := cb(node); err != nil {
		return scan.Result{}, err
	}
	if err := cb(node); err != nil {
		return scan.Result{}, err
	}
	if err := cb(scan.NodeRecord{
		Path:       d.root,
		ParentPath: "",
		Name:       filepath.Base(d.root),
		Kind:       "dir",
		SizeBytes:  2,
		MtimeUnix:  time.Now().Unix(),
	}); err != nil {
		return scan.Result{}, err
	}
	return scan.Result{TotalBytes: 2, TotalNodes: 3}, nil
}

type singleNodeScanner struct {
	root string
}

func (s *singleNodeScanner) Scan(ctx context.Context, cb scan.NodeCallback) (scan.Result, error) {
	if err := cb(scan.NodeRecord{
		Path:       s.root,
		ParentPath: "",
		Name:       filepath.Base(s.root),
		Kind:       "dir",
		SizeBytes:  1,
		MtimeUnix:  time.Now().Unix(),
	}); err != nil {
		return scan.Result{}, err
	}
	return scan.Result{TotalBytes: 1, TotalNodes: 1}, nil
}

type failingScanner struct {
	err error
}

func (f *failingScanner) Scan(ctx context.Context, cb scan.NodeCallback) (scan.Result, error) {
	return scan.Result{}, f.err
}

type midStreamWriterFailureScanner struct {
	root       string
	totalNodes int
	invalidAt  int
}

func (m *midStreamWriterFailureScanner) Scan(ctx context.Context, cb scan.NodeCallback) (scan.Result, error) {
	for i := 0; i < m.totalNodes; i++ {
		kind := "file"
		if i == m.invalidAt {
			// The snapshot writer rejects unknown kinds, failing the write
			// while the scanner is still producing nodes.
			kind = "symlink"
		}
		node := scan.NodeRecord{
			Path:       filepath.Join(m.root, fmt.Sprintf("node-%06d.bin", i)),
			ParentPath: m.root,
			Name:       fmt.Sprintf("node-%06d.bin", i),
			Kind:       kind,
			SizeBytes:  1,
			MtimeUnix:  1,
		}
		if err := cb(node); err != nil {
			return scan.Result{}, err
		}
	}
	return scan.Result{TotalNodes: int64(m.totalNodes), TotalBytes: int64(m.totalNodes)}, nil
}

func TestServiceAllowsOnlyOneRunningScan(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	svc := NewService(cfg, st)
	start := make(chan struct{})
	release := make(chan struct{})
	svc.SetScannerFactoryForTests(func(root string, _ int) scan.Engine {
		return &blockingScanner{root: root, start: start, release: release}
	})

	id, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatalf("start first scan: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected scan id")
	}

	<-start

	_, err = svc.StartScan(context.Background())
	if !errors.Is(err, ErrScanRunning) {
		t.Fatalf("expected ErrScanRunning, got %v", err)
	}

	close(release)

	run := waitForScanStatus(t, st, id)
	if run.Status != "completed" {
		t.Fatalf("expected completed, got %s (%s)", run.Status, run.Error)
	}
}

func TestServiceShutdownCancelsActiveScanAndWaitsForPersistence(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	svc := NewService(testConfig(root, dataDir), st)
	scanner := &shutdownBlockingScanner{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
	svc.SetScannerFactoryForTests(func(string, int) scan.Engine { return scanner })

	scanID, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}
	select {
	case <-scanner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("scanner did not start")
	}

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- svc.Shutdown(context.Background()) }()
	select {
	case <-scanner.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel scanner")
	}
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before scan completion: %v", err)
	default:
	}

	close(scanner.release)
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not wait for scan completion")
	}

	run, err := st.GetScanRun(context.Background(), scanID)
	if err != nil {
		t.Fatalf("get scan run after shutdown: %v", err)
	}
	if run.Status != store.ScanFailed {
		t.Fatalf("expected failed run after shutdown, got %s (%s)", run.Status, run.Error)
	}
	if !strings.Contains(run.Error, context.Canceled.Error()) {
		t.Fatalf("expected cancellation error after shutdown, got %q", run.Error)
	}
}

func TestServiceShutdownImmediatelyAfterStartScanWaitsForRegistration(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	svc := NewService(testConfig(root, dataDir), st)
	start := make(chan struct{})
	svc.SetScannerFactoryForTests(func(root string, _ int) scan.Engine {
		return &blockingScanner{root: root, start: start, release: make(chan struct{})}
	})

	scanID, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	run, err := st.GetScanRun(context.Background(), scanID)
	if err != nil {
		t.Fatalf("get scan run after immediate shutdown: %v", err)
	}
	if run.Status != store.ScanFailed {
		t.Fatalf("expected failed run after immediate shutdown, got %s (%s)", run.Status, run.Error)
	}
}

func TestServiceRejectsStartScanAfterShutdownBegins(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	svc := NewService(testConfig(root, dataDir), st)
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown without active scan: %v", err)
	}

	if _, err := svc.StartScan(context.Background()); !errors.Is(err, ErrServiceShuttingDown) {
		t.Fatalf("expected ErrServiceShuttingDown, got %v", err)
	}
}

func TestServiceShutdownReturnsContextErrorOnTimeout(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	svc := NewService(testConfig(root, dataDir), st)
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	svc.SetScannerFactoryForTests(func(root string, _ int) scan.Engine {
		return &shutdownBlockingScanner{started: started, canceled: canceled, release: release}
	})

	scanID, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("scanner did not start")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- svc.Shutdown(shutdownCtx)
	}()
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel scanner")
	}
	if err := <-shutdownDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected shutdown deadline error, got %v", err)
	}

	close(release)
	if err := svc.Shutdown(context.Background()); err != nil {
		t.Fatalf("finish shutdown: %v", err)
	}
	run, err := st.GetScanRun(context.Background(), scanID)
	if err != nil {
		t.Fatalf("get scan run after shutdown: %v", err)
	}
	if run.Status != store.ScanFailed {
		t.Fatalf("expected failed run after shutdown, got %s (%s)", run.Status, run.Error)
	}
}

func TestGetScanRunIncludesLiveProgress(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	filePath := filepath.Join(root, "example.bin")

	cfg := testConfig(root, dataDir)
	cfg.ScanProgressInterval = 10 * time.Millisecond

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	svc := NewService(cfg, st)
	reached := make(chan struct{})
	release := make(chan struct{})
	svc.SetScannerFactoryForTests(func(root string, _ int) scan.Engine {
		return &progressScanner{root: root, firstPath: filePath, reached: reached, release: release}
	})

	scanID, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}

	select {
	case <-reached:
	case <-time.After(2 * time.Second):
		t.Fatalf("scan did not reach first progress point")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, getErr := svc.GetScanRun(context.Background(), scanID)
		if getErr != nil {
			t.Fatalf("get scan run: %v", getErr)
		}
		if run.Progress != nil && run.Progress.ScannedNodes >= 1 {
			if run.Progress.ScannedFiles != 1 {
				t.Fatalf("expected scanned files=1, got %d", run.Progress.ScannedFiles)
			}
			if run.Progress.ScannedBytes != 42 {
				t.Fatalf("expected scanned bytes=42, got %d", run.Progress.ScannedBytes)
			}
			if run.Progress.CurrentPath != filePath {
				t.Fatalf("expected current path %q, got %q", filePath, run.Progress.CurrentPath)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(release)

	run := waitForScanStatusViaService(t, svc, scanID)
	if run.Status != "completed" {
		t.Fatalf("expected completed, got %s (%s)", run.Status, run.Error)
	}
	if run.Progress != nil {
		t.Fatalf("expected no live progress after completion")
	}
}

func TestServiceFailsUnreadableScanResult(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	svc := NewService(cfg, st)
	svc.SetScannerFactoryForTests(func(root string, _ int) scan.Engine {
		return &staticResultScanner{result: scan.Result{TotalBytes: 0, TotalNodes: 4, WarningCount: 3}}
	})

	scanID, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}

	run := waitForScanStatus(t, st, scanID)
	if run.Status != "failed" {
		t.Fatalf("expected failed, got %s", run.Status)
	}
	if run.WarningCount != 3 {
		t.Fatalf("expected warning count 3, got %d", run.WarningCount)
	}
	if !strings.Contains(run.Error, "no readable files") {
		t.Fatalf("expected unreadable scan error, got %q", run.Error)
	}
}

func TestServiceFailsWhenWriterReturnsError(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	cfg.ScanProgressInterval = 10 * time.Millisecond

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	svc := NewService(cfg, st)
	svc.SetScannerFactoryForTests(func(root string, _ int) scan.Engine {
		return &duplicateNodeScanner{root: root}
	})

	scanID, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}

	run := waitForScanStatus(t, st, scanID)
	if run.Status != "failed" {
		t.Fatalf("expected failed, got %s", run.Status)
	}
	if !strings.Contains(run.Error, "commit nodes") {
		t.Fatalf("expected commit node error, got %q", run.Error)
	}
}

func TestServiceFailsWhenWriterFailsMidStream(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	svc := NewService(cfg, st)
	svc.SetScannerFactoryForTests(func(root string, _ int) scan.Engine {
		// totalNodes exceeds the node channel capacity (16384 for the fixed
		// profile) plus everything the writer consumes before the invalid
		// node kills it, so the producer provably blocks on a full channel
		// once the writer goroutine is gone.
		return &midStreamWriterFailureScanner{root: root, totalNodes: 30000, invalidAt: 3000}
	})

	scanID, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}

	run := waitForScanStatus(t, st, scanID)
	if run.Status != "failed" {
		t.Fatalf("expected failed, got %s", run.Status)
	}
	if !strings.Contains(run.Error, "write nodes") {
		t.Fatalf("expected write nodes error, got %q", run.Error)
	}
	if !strings.Contains(run.Error, "symlink") {
		t.Fatalf("expected unsupported kind in error, got %q", run.Error)
	}
}

func TestScanTimeoutFailsRun(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	cfg.ScanTimeout = 100 * time.Millisecond

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	svc := NewService(cfg, st)
	start := make(chan struct{})
	release := make(chan struct{})
	svc.SetScannerFactoryForTests(func(root string, _ int) scan.Engine {
		return &blockingScanner{root: root, start: start, release: release}
	})

	scanID, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}

	<-start

	run := waitForScanStatus(t, st, scanID)
	if run.Status != "failed" {
		t.Fatalf("expected failed, got %s", run.Status)
	}
	if !strings.Contains(run.Error, "context deadline exceeded") {
		t.Fatalf("expected deadline exceeded error, got %q", run.Error)
	}
}

func TestServicePrunesToNewestCompletedScan(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	for i := 0; i < 3; i++ {
		createCompletedScanWithNodesForServiceTest(t, st, root, []store.Node{
			{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: int64(i + 1), MtimeUnix: 1},
		})
	}

	svc := NewService(cfg, st)
	svc.SetScannerFactoryForTests(func(root string, _ int) scan.Engine {
		return &singleNodeScanner{root: root}
	})

	scanID, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}

	run := waitForScanStatus(t, st, scanID)
	if run.Status != "completed" {
		t.Fatalf("expected completed, got %s", run.Status)
	}

	current, err := svc.GetCurrentScanRun(context.Background())
	if err != nil {
		t.Fatalf("get current scan: %v", err)
	}
	latestCompleted, err := svc.GetLatestCompletedScanRun(context.Background())
	if err != nil {
		t.Fatalf("get latest completed scan: %v", err)
	}
	if current == nil || latestCompleted == nil || current.ID != latestCompleted.ID {
		t.Fatalf("expected current and latest completed to match, got current=%+v latestCompleted=%+v", current, latestCompleted)
	}

	if deleted, err := st.PruneOperationalScans(context.Background()); err != nil {
		t.Fatalf("prune operational scans: %v", err)
	} else if len(deleted) != 0 {
		t.Fatalf("expected store to already be pruned, deleted=%v", deleted)
	}
}

func TestServiceKeepsLastCompletedWhenNewerScanFails(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	completedID := createCompletedScanWithNodesForServiceTest(t, st, root, []store.Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 10, MtimeUnix: 1},
	})

	svc := NewService(cfg, st)
	svc.SetScannerFactoryForTests(func(root string, _ int) scan.Engine {
		return &failingScanner{err: fmt.Errorf("boom")}
	})

	failedID, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatalf("start scan: %v", err)
	}

	run := waitForScanStatus(t, st, failedID)
	if run.Status != "failed" {
		t.Fatalf("expected failed, got %s", run.Status)
	}

	latestCompleted, err := svc.GetLatestCompletedScanRun(context.Background())
	if err != nil {
		t.Fatalf("get latest completed scan: %v", err)
	}
	if latestCompleted == nil || latestCompleted.ID != completedID {
		t.Fatalf("expected completed scan %d to be preserved, got %+v", completedID, latestCompleted)
	}

	current, err := svc.GetCurrentScanRun(context.Background())
	if err != nil {
		t.Fatalf("get current scan: %v", err)
	}
	if current == nil || current.ID != failedID || current.Status != "failed" {
		t.Fatalf("expected failed scan to remain current, got %+v", current)
	}
}

func TestServiceRecoverFailsInterruptedRunsAndAppliesRetention(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	completedID := createCompletedScanWithNodesForServiceTest(t, st, root, []store.Node{
		{Path: root, Name: filepath.Base(root), Kind: "dir", SizeBytes: 1},
	})
	queuedID, err := st.QueueRun(context.Background(), root)
	if err != nil {
		t.Fatalf("queue interrupted run: %v", err)
	}
	runningID, err := st.QueueRun(context.Background(), root)
	if err != nil {
		t.Fatalf("queue running run: %v", err)
	}
	if err := st.StartRun(context.Background(), runningID, time.Now().UTC()); err != nil {
		t.Fatalf("start running run: %v", err)
	}

	svc := NewService(testConfig(root, dataDir), st)
	report, err := svc.Recover(context.Background())
	if err != nil {
		t.Fatalf("recover service: %v", err)
	}
	if report.InterruptedRuns != 2 || report.PrunedRuns != 1 {
		t.Fatalf("unexpected recovery report: %+v", report)
	}

	if _, err := st.GetScanRun(context.Background(), queuedID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected older interrupted run to be pruned, got %v", err)
	}
	running, err := st.GetScanRun(context.Background(), runningID)
	if err != nil {
		t.Fatalf("get latest interrupted run: %v", err)
	}
	if running.Status != store.ScanFailed || !strings.Contains(running.Error, "interrupted") {
		t.Fatalf("expected latest interrupted run to be failed, got %+v", running)
	}
	completed, err := st.GetScanRun(context.Background(), completedID)
	if err != nil {
		t.Fatalf("get retained snapshot: %v", err)
	}
	if completed.Status != store.ScanCompleted {
		t.Fatalf("expected completed snapshot to remain, got %s", completed.Status)
	}
}

func TestGetFolderViewReturnsBoundedTreeAndHiddenBucket(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := testConfig(root, dataDir)
	cfg.MaxChildrenPerQuery = 2

	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}

	dirPath := filepath.Join(root, "docs")
	scanID := createCompletedScanWithNodesForServiceTest(t, st, root, []store.Node{
		{Path: root, ParentPath: "", Name: filepath.Base(root), Kind: "dir", SizeBytes: 101, MtimeUnix: 1},
		{Path: filepath.Join(root, "video.mkv"), ParentPath: root, Name: "video.mkv", Kind: "file", SizeBytes: 60, MtimeUnix: 1},
		{Path: dirPath, ParentPath: root, Name: "docs", Kind: "dir", SizeBytes: 40, MtimeUnix: 1},
		{Path: filepath.Join(root, "tiny.txt"), ParentPath: root, Name: "tiny.txt", Kind: "file", SizeBytes: 1, MtimeUnix: 1},
		{Path: filepath.Join(dirPath, "chapter-1.md"), ParentPath: dirPath, Name: "chapter-1.md", Kind: "file", SizeBytes: 25, MtimeUnix: 1},
		{Path: filepath.Join(dirPath, "chapter-2.md"), ParentPath: dirPath, Name: "chapter-2.md", Kind: "file", SizeBytes: 15, MtimeUnix: 1},
	})

	svc := NewService(cfg, st)

	view, err := svc.GetFolderView(context.Background(), scanID, FolderViewRequest{
		Path:  root,
		Limit: 999,
		Sort:  "size_desc",
	})
	if err != nil {
		t.Fatalf("get explore: %v", err)
	}

	if len(view.Items) != 2 {
		t.Fatalf("expected 2 visible items, got %d", len(view.Items))
	}
	if view.Summary.HiddenItemCount != 1 {
		t.Fatalf("expected 1 hidden item, got %d", view.Summary.HiddenItemCount)
	}
	if !view.Summary.IsResultTruncated {
		t.Fatalf("expected truncated summary")
	}
	if len(view.Treemap.Children) != 3 {
		t.Fatalf("expected 3 root treemap children, got %d", len(view.Treemap.Children))
	}

	var docs *FolderViewTreemapNode
	var hidden *FolderViewTreemapNode
	for i := range view.Treemap.Children {
		node := &view.Treemap.Children[i]
		if node.Path == dirPath {
			docs = node
		}
		if node.Synthetic {
			hidden = node
		}
	}

	if docs == nil {
		t.Fatalf("expected docs node in treemap")
	}
	if len(docs.Children) != 2 {
		t.Fatalf("expected docs to expand into 2 children, got %d", len(docs.Children))
	}
	if hidden == nil || hidden.HiddenItemCount != 1 {
		t.Fatalf("expected hidden bucket in root treemap, got %+v", hidden)
	}
}

func TestFolderViewTreemapEnforcesServerNodeBudget(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	items := make([]store.Node, 260)
	for i := range items {
		items[i] = store.Node{
			Path:      filepath.Join(root, fmt.Sprintf("file-%03d.bin", i)),
			Name:      fmt.Sprintf("file-%03d.bin", i),
			Kind:      "file",
			SizeBytes: 1,
		}
	}

	view := &folderView{}
	tree, err := view.buildTreemap(context.Background(), 1, store.Node{
		Path:      root,
		Name:      filepath.Base(root),
		Kind:      "dir",
		SizeBytes: int64(len(items)),
	}, items, store.ChildAggregate{Count: int64(len(items)), TotalBytes: int64(len(items))}, false)
	if err != nil {
		t.Fatalf("build bounded treemap: %v", err)
	}
	if len(tree.Children) != folderViewDescendantBudget {
		t.Fatalf("expected %d treemap children, got %d", folderViewDescendantBudget, len(tree.Children))
	}
	hidden := tree.Children[len(tree.Children)-1]
	if !hidden.Synthetic || hidden.HiddenItemCount != 21 {
		t.Fatalf("expected 21 grouped items at budget boundary, got %+v", hidden)
	}
}

func TestFolderViewNormalizesLimitsAndRejectsInvalidFilters(t *testing.T) {
	root := t.TempDir()
	view := newFolderView(root, 10, nil)

	_, err := view.normalize(FolderViewRequest{Path: root, Kind: "wat"}, 10, 10)
	if err == nil {
		t.Fatalf("expected error for invalid type")
	}

	_, err = view.normalize(FolderViewRequest{Path: root, Sort: "wat"}, 10, 10)
	if err == nil {
		t.Fatalf("expected error for invalid sort")
	}

	normalized, err := view.normalize(FolderViewRequest{Path: root, Limit: 999}, 2, 2)
	if err != nil {
		t.Fatalf("normalize bounded request: %v", err)
	}
	if normalized.Limit != 2 || normalized.Sort != "size_desc" {
		t.Fatalf("unexpected normalized request: %+v", normalized)
	}
}

func testConfig(root, dataDir string) config.Config {
	return config.Config{
		AnalyzeRoot:          root,
		DataDir:              dataDir,
		ScanProfile:          scancontrol.ProfileFixed,
		ScanProgressInterval: 25 * time.Millisecond,
		MaxChildrenPerQuery:  100,
	}
}

func waitForScanStatus(t *testing.T, st *store.Store, scanID int64) store.ScanRun {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, err := st.GetScanRun(context.Background(), scanID)
		if err == nil && (run.Status == "completed" || run.Status == "failed") {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("scan did not complete in time")
	return store.ScanRun{}
}

func waitForScanStatusViaService(t *testing.T, svc *Service, scanID int64) store.ScanRun {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, err := svc.GetScanRun(context.Background(), scanID)
		if err == nil && (run.Status == "completed" || run.Status == "failed") {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("scan did not complete in time")
	return store.ScanRun{}
}

func createCompletedScanWithNodesForServiceTest(t *testing.T, st *store.Store, root string, nodes []store.Node) int64 {
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
		t.Fatalf("commit writer: %v", err)
	}
	if err := st.FinishRun(context.Background(), scanID, store.ScanOutcome{
		Status:     store.ScanCompleted,
		FinishedAt: time.Now().UTC(),
		TotalNodes: int64(len(nodes)),
	}); err != nil {
		t.Fatalf("complete scan: %v", err)
	}
	return scanID
}
