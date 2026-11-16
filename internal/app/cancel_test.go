package app

import (
	"context"
	"errors"
	"github.com/justamply/disk-treemap/internal/scan"
	"github.com/justamply/disk-treemap/internal/store"
	"github.com/justamply/disk-treemap/internal/testsupport"
	"testing"
)

func TestCancelScanPreservesPreviousSnapshot(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	st := testsupport.OpenStore(t, data)
	previous := testsupport.CompletedScan(t, st, root, []store.Node{{Path: root, Name: "root", Kind: "dir"}})
	svc := NewService(testsupport.TestConfig(root, data), st)
	testsupport.RegisterServiceCleanup(t, svc.Shutdown)
	started := make(chan struct{})
	svc.SetScannerFactoryForTests(func(root string, _ int) scan.Engine {
		return &blockingScanner{root: root, start: started, release: make(chan struct{})}
	})
	id, err := svc.StartScan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := svc.CancelScan(id + 1); !errors.Is(err, ErrScanNotActive) {
		t.Fatalf("wrong scan: %v", err)
	}
	if err := svc.CancelScan(id); err != nil {
		t.Fatal(err)
	}
	run := testsupport.WaitForTerminalScan(t, id, func() (store.ScanRun, error) { return st.GetScanRun(context.Background(), id) })
	if run.Status != store.ScanCancelled {
		t.Fatalf("got %s", run.Status)
	}
	latest, err := st.GetLatestCompletedScanRun(context.Background())
	if err != nil || latest == nil || latest.ID != previous {
		t.Fatalf("previous snapshot lost: %v %v", latest, err)
	}
	if _, err := st.GetNode(context.Background(), id, root); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("partial snapshot published: %v", err)
	}
}
