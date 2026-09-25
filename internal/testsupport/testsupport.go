// Package testsupport provides the fixtures shared by the app and api test
// suites. It must not import those packages: their white-box tests import
// this one, and a reverse dependency would be an import cycle.
package testsupport

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/justamply/disk-treemap/internal/config"
	"github.com/justamply/disk-treemap/internal/scancontrol"
	"github.com/justamply/disk-treemap/internal/store"
)

// TestConfig is the shared baseline config for app and api tests: fixed
// scan profile, fast progress interval.
func TestConfig(root, dataDir string) config.Config {
	return config.Config{
		AnalyzeRoot:          root,
		DataDir:              dataDir,
		ScanProfile:          scancontrol.ProfileFixed,
		ScanProgressInterval: 25 * time.Millisecond,
		MaxChildrenPerQuery:  100,
	}
}

// OpenStore opens and initializes a scan database inside dataDir and closes
// it when the test finishes.
func OpenStore(t *testing.T, dataDir string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(dataDir, "scan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return st
}

// RegisterServiceCleanup joins an asynchronous service before the store
// cleanup registered by OpenStore runs.
func RegisterServiceCleanup(t *testing.T, shutdown func(context.Context) error) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			t.Errorf("shutdown service: %v", err)
		}
	})
}

// CompletedScan queues, runs, publishes, and finishes a completed scan
// containing nodes, returning its scan id.
func CompletedScan(t *testing.T, st *store.Store, root string, nodes []store.Node) int64 {
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
	if err := st.FinishRun(context.Background(), scanID, store.ScanOutcome{
		Status:     store.ScanCompleted,
		FinishedAt: time.Now().UTC(),
		TotalNodes: int64(len(nodes)),
	}); err != nil {
		t.Fatalf("complete scan: %v", err)
	}
	return scanID
}

// WaitForTerminalScan polls getRun until the scan reports a terminal
// status (completed or failed).
func WaitForTerminalScan(t *testing.T, scanID int64, getRun func() (store.ScanRun, error)) store.ScanRun {
	t.Helper()
	return WaitForTerminalScanWithin(t, scanID, getRun, 5*time.Second)
}

// WaitForTerminalScanWithin uses a caller-supplied deadline for slower scan fixtures.
func WaitForTerminalScanWithin(t *testing.T, scanID int64, getRun func() (store.ScanRun, error), timeout time.Duration) store.ScanRun {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastRun store.ScanRun
	var lastErr error
	for time.Now().Before(deadline) {
		run, err := getRun()
		lastRun = run
		lastErr = err
		if err == nil && (run.Status == store.ScanCompleted || run.Status == store.ScanFailed) {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("scan %d did not reach a terminal status in time; last status=%q, last error=%v", scanID, lastRun.Status, lastErr)
	return store.ScanRun{}
}
