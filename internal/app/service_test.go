package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/justamply/disk-treemap/internal/config"
	"github.com/justamply/disk-treemap/internal/scan"
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

func TestGetScanRunIncludesLiveProgress(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	filePath := filepath.Join(root, "example.bin")

	cfg := testConfig(root, dataDir)
	cfg.ScanWriteBatchSize = 1
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
	cfg.ScanWriteBatchSize = 16
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
	if !strings.Contains(run.Error, "write nodes") {
		t.Fatalf("expected write node error, got %q", run.Error)
	}
}

func testConfig(root, dataDir string) config.Config {
	return config.Config{
		AnalyzeRoot:          root,
		DataDir:              dataDir,
		ScanMaxConcurrency:   2,
		ScanWriteBatchSize:   8,
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
