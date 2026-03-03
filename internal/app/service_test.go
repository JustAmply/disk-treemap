package app

import (
	"context"
	"errors"
	"path/filepath"
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

func TestServiceAllowsOnlyOneRunningScan(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()

	cfg := config.Config{
		AnalyzeRoot:          root,
		DataDir:              dataDir,
		ScanMaxConcurrency:   2,
		MaxChildrenPerQuery:  100,
	}

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

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, getErr := st.GetScanRun(context.Background(), id)
		if getErr == nil && (run.Status == "completed" || run.Status == "failed") {
			if run.Status != "completed" {
				t.Fatalf("expected completed, got %s (%s)", run.Status, run.Error)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("scan did not complete in time")
}

func TestGetScanRunIncludesLiveProgress(t *testing.T) {
	root := t.TempDir()
	dataDir := t.TempDir()
	filePath := filepath.Join(root, "example.bin")

	cfg := config.Config{
		AnalyzeRoot:          root,
		DataDir:              dataDir,
		ScanMaxConcurrency:   2,
		MaxChildrenPerQuery:  100,
	}

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

	run, err := svc.GetScanRun(context.Background(), scanID)
	if err != nil {
		t.Fatalf("get scan run: %v", err)
	}
	if run.Progress == nil {
		t.Fatalf("expected live progress")
	}
	if run.Progress.ScannedNodes != 1 {
		t.Fatalf("expected scanned nodes=1, got %d", run.Progress.ScannedNodes)
	}
	if run.Progress.ScannedFiles != 1 {
		t.Fatalf("expected scanned files=1, got %d", run.Progress.ScannedFiles)
	}
	if run.Progress.ScannedDirs != 0 {
		t.Fatalf("expected scanned dirs=0, got %d", run.Progress.ScannedDirs)
	}
	if run.Progress.ScannedBytes != 42 {
		t.Fatalf("expected scanned bytes=42, got %d", run.Progress.ScannedBytes)
	}
	if run.Progress.CurrentPath != filePath {
		t.Fatalf("expected current path %q, got %q", filePath, run.Progress.CurrentPath)
	}

	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, getErr := svc.GetScanRun(context.Background(), scanID)
		if getErr == nil && run.Status == "completed" {
			if run.Progress != nil {
				t.Fatalf("expected no live progress after completion")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("scan did not complete in time")
}
