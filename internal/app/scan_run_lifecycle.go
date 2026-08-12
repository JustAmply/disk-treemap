package app

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/justamply/disk-treemap/internal/scan"
	"github.com/justamply/disk-treemap/internal/store"
)

const storageOptimizeTimeout = 30 * time.Second

type RecoveryReport struct {
	InterruptedRuns int
	PrunedRuns      int
}

type liveProgress struct {
	ScanID       int64
	CurrentPath  string
	ScannedNodes int64
	ScannedFiles int64
	ScannedDirs  int64
	ScannedBytes int64
	UpdatedAt    time.Time
}

type scanRuntimeMetrics struct {
	ScanID            int64
	EnqueuedNodes     int64
	WriterQueueDepth  int
	WriterQueueCap    int
	WriteBatchSize    int
	LastFlushDuration time.Duration
	LastFlushAt       time.Time
	FlushCount        int64
}

type scanRuntimeSnapshot struct {
	Active   bool
	Progress liveProgress
	Metrics  scanRuntimeMetrics
}

type scanRunLifecycle struct {
	store *store.Store

	mu            sync.Mutex
	runningScanID int64
	progress      liveProgress
	metrics       scanRuntimeMetrics
}

func newScanRunLifecycle(st *store.Store) *scanRunLifecycle {
	return &scanRunLifecycle{store: st}
}

func (l *scanRunLifecycle) recover(ctx context.Context) (RecoveryReport, error) {
	interrupted, err := l.store.FailInterruptedScans(ctx, time.Now().UTC())
	if err != nil {
		return RecoveryReport{}, fmt.Errorf("fail interrupted scan runs: %w", err)
	}

	pruned, err := l.store.PruneOperationalScans(ctx)
	if err != nil {
		return RecoveryReport{}, fmt.Errorf("apply scan run retention: %w", err)
	}
	if len(pruned) > 0 {
		l.optimizeStorage(0)
	}

	return RecoveryReport{
		InterruptedRuns: len(interrupted),
		PrunedRuns:      len(pruned),
	}, nil
}

func (l *scanRunLifecycle) queue(ctx context.Context, rootPath string) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.runningScanID != 0 {
		return 0, ErrScanRunning
	}

	scanID, err := l.store.QueueRun(ctx, rootPath)
	if err != nil {
		return 0, err
	}

	l.runningScanID = scanID
	l.progress = liveProgress{
		ScanID:      scanID,
		CurrentPath: rootPath,
		UpdatedAt:   time.Now().UTC(),
	}
	l.metrics = scanRuntimeMetrics{ScanID: scanID}
	return scanID, nil
}

func (l *scanRunLifecycle) start(ctx context.Context, scanID int64) error {
	return l.store.StartRun(ctx, scanID, time.Now().UTC())
}

func (l *scanRunLifecycle) complete(scanID int64, result scan.Result) error {
	err := l.store.FinishRun(context.Background(), scanID, store.ScanOutcome{
		Status:       store.ScanCompleted,
		FinishedAt:   time.Now().UTC(),
		TotalBytes:   result.TotalBytes,
		TotalNodes:   result.TotalNodes,
		WarningCount: result.WarningCount,
	})
	if err != nil {
		return err
	}

	l.clear(scanID)
	l.prune(scanID)
	l.optimizeStorage(scanID)
	return nil
}

func (l *scanRunLifecycle) fail(scanID int64, scanErr error, totalBytes, totalNodes, warnings int64) {
	err := l.store.FinishRun(context.Background(), scanID, store.ScanOutcome{
		Status:       store.ScanFailed,
		FinishedAt:   time.Now().UTC(),
		TotalBytes:   totalBytes,
		TotalNodes:   totalNodes,
		WarningCount: warnings,
		Error:        scanErr.Error(),
	})
	if err != nil {
		log.Printf("scan #%d record failure warning: %v", scanID, err)
	}
	l.clear(scanID)
	l.prune(scanID)
}

func (l *scanRunLifecycle) get(ctx context.Context, scanID int64) (store.ScanRun, error) {
	run, err := l.store.GetScanRun(ctx, scanID)
	if err != nil {
		return store.ScanRun{}, err
	}
	if progress := l.snapshotProgress(scanID); progress != nil {
		run.Progress = progress
	}
	return run, nil
}

func (l *scanRunLifecycle) current(ctx context.Context) (*store.ScanRun, error) {
	run, err := l.store.GetLatestScanRun(ctx)
	if err != nil || run == nil {
		return run, err
	}
	if progress := l.snapshotProgress(run.ID); progress != nil {
		run.Progress = progress
	}
	return run, nil
}

func (l *scanRunLifecycle) latestSnapshot(ctx context.Context) (*store.ScanRun, error) {
	return l.store.GetLatestCompletedScanRun(ctx)
}

func (l *scanRunLifecycle) recordProgressBatch(scanID int64, batch []store.Node, updatedAt time.Time) {
	var nodes int64
	var files int64
	var dirs int64
	var bytes int64

	for _, node := range batch {
		nodes++
		switch node.Kind {
		case "file":
			files++
			bytes += node.SizeBytes
		case "dir":
			dirs++
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.isActiveLocked(scanID) {
		return
	}

	l.progress.CurrentPath = batch[len(batch)-1].Path
	l.progress.ScannedNodes += nodes
	l.progress.ScannedFiles += files
	l.progress.ScannedDirs += dirs
	l.progress.ScannedBytes += bytes
	l.progress.UpdatedAt = updatedAt
}

func (l *scanRunLifecycle) recordWriterQueue(scanID int64, depth, capacity int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.isActiveLocked(scanID) {
		return
	}
	l.metrics.WriterQueueDepth = depth
	l.metrics.WriterQueueCap = capacity
}

func (l *scanRunLifecycle) recordNodeEnqueued(scanID int64, depth, capacity int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.isActiveLocked(scanID) {
		return
	}
	l.metrics.EnqueuedNodes++
	l.metrics.WriterQueueDepth = depth
	l.metrics.WriterQueueCap = capacity
}

func (l *scanRunLifecycle) recordWriterFlush(scanID int64, duration time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.isActiveLocked(scanID) {
		return
	}
	l.metrics.LastFlushDuration = duration
	l.metrics.LastFlushAt = time.Now().UTC()
	l.metrics.FlushCount++
}

func (l *scanRunLifecycle) recordWriteBatchSize(scanID int64, size int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.isActiveLocked(scanID) {
		return
	}
	l.metrics.WriteBatchSize = size
}

func (l *scanRunLifecycle) runtimeSnapshot(scanID int64) scanRuntimeSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.isActiveLocked(scanID) {
		return scanRuntimeSnapshot{}
	}
	return scanRuntimeSnapshot{
		Active:   true,
		Progress: l.progress,
		Metrics:  l.metrics,
	}
}

func (l *scanRunLifecycle) snapshotProgress(scanID int64) *store.ScanProgress {
	snapshot := l.runtimeSnapshot(scanID)
	if !snapshot.Active {
		return nil
	}

	updated := snapshot.Progress.UpdatedAt
	return &store.ScanProgress{
		CurrentPath:  snapshot.Progress.CurrentPath,
		ScannedNodes: snapshot.Progress.ScannedNodes,
		ScannedFiles: snapshot.Progress.ScannedFiles,
		ScannedDirs:  snapshot.Progress.ScannedDirs,
		ScannedBytes: snapshot.Progress.ScannedBytes,
		UpdatedAt:    &updated,
	}
}

func (l *scanRunLifecycle) clear(scanID int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.runningScanID != scanID {
		return
	}
	l.runningScanID = 0
	l.progress = liveProgress{}
	l.metrics = scanRuntimeMetrics{}
}

func (l *scanRunLifecycle) isActiveLocked(scanID int64) bool {
	return l.runningScanID == scanID && l.progress.ScanID == scanID && l.metrics.ScanID == scanID
}

func (l *scanRunLifecycle) prune(scanID int64) {
	deleted, err := l.store.PruneOperationalScans(context.Background())
	if err != nil {
		log.Printf("scan #%d prune warning: %v", scanID, err)
		return
	}
	if len(deleted) > 0 {
		log.Printf("scan #%d pruned %d old scan run(s)", scanID, len(deleted))
	}
}

func (l *scanRunLifecycle) optimizeStorage(scanID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), storageOptimizeTimeout)
	defer cancel()
	if err := l.store.OptimizeStorage(ctx, false); err != nil {
		if scanID == 0 {
			log.Printf("storage optimize warning: %v", err)
			return
		}
		log.Printf("scan #%d storage optimize warning: %v", scanID, err)
	}
}
