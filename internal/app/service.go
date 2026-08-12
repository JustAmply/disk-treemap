package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/justamply/disk-treemap/internal/config"
	"github.com/justamply/disk-treemap/internal/scan"
	"github.com/justamply/disk-treemap/internal/scancontrol"
	"github.com/justamply/disk-treemap/internal/store"
)

const minProgressInterval = 10 * time.Millisecond

var (
	ErrScanRunning  = errors.New("scan already running")
	ErrInvalidInput = errors.New("invalid input")
)

type scannerFactory func(root string, maxConcurrency int) scan.Engine

type batchSizeController struct {
	current int64
	min     int64
	max     int64
}

func newBatchSizeController(initial, minValue, maxValue int) *batchSizeController {
	if minValue < 1 {
		minValue = 1
	}
	if maxValue < minValue {
		maxValue = minValue
	}
	return &batchSizeController{
		current: int64(clampInt(initial, minValue, maxValue)),
		min:     int64(minValue),
		max:     int64(maxValue),
	}
}

func (b *batchSizeController) Get() int {
	return int(atomic.LoadInt64(&b.current))
}

func (b *batchSizeController) Set(size int) int {
	if size < int(b.min) {
		size = int(b.min)
	}
	if size > int(b.max) {
		size = int(b.max)
	}
	atomic.StoreInt64(&b.current, int64(size))
	return size
}

type Service struct {
	cfg         config.Config
	store       *store.Store
	runs        *scanRunLifecycle
	folders     *folderView
	makeScanner scannerFactory
}

func NewService(cfg config.Config, st *store.Store) *Service {
	return &Service{
		cfg:         cfg,
		store:       st,
		runs:        newScanRunLifecycle(st),
		folders:     newFolderView(cfg.AnalyzeRoot, cfg.MaxChildrenPerQuery, st),
		makeScanner: func(root string, maxConcurrency int) scan.Engine { return scan.New(root, maxConcurrency) },
	}
}

func (s *Service) SetScannerFactoryForTests(factory scannerFactory) {
	if factory == nil {
		return
	}
	s.makeScanner = factory
}

func (s *Service) StartScan(ctx context.Context) (int64, error) {
	scanID, err := s.runs.queue(ctx, s.cfg.AnalyzeRoot)
	if err != nil {
		return 0, err
	}

	limits := scancontrol.New(s.cfg.ScanProfile).Limits()
	log.Printf(
		"scan #%d queued: root=%q profile=%s adaptive=%t concurrency=%d..%d batch_size=%d..%d",
		scanID,
		s.cfg.AnalyzeRoot,
		s.cfg.ScanProfile,
		limits.Adaptive,
		limits.Min.Concurrency,
		limits.Max.Concurrency,
		limits.Min.BatchSize,
		limits.Max.BatchSize,
	)
	go s.runScan(scanID)
	return scanID, nil
}

func (s *Service) runScan(scanID int64) {
	ctx := context.Background()
	if err := s.runs.start(ctx, scanID); err != nil {
		s.runs.fail(scanID, fmt.Errorf("set running status: %w", err), 0, 0, 0)
		return
	}
	log.Printf("scan #%d running", scanID)

	scanCtxBase, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()

	scanCtx := scanCtxBase
	cancel := cancelBase
	if s.cfg.ScanTimeout > 0 {
		timeoutCtx, timeoutCancel := context.WithTimeout(scanCtxBase, s.cfg.ScanTimeout)
		scanCtx = timeoutCtx
		cancel = func() {
			timeoutCancel()
			cancelBase()
		}
	}
	defer cancel()

	writer, err := s.store.BeginSnapshot(scanCtx, scanID)
	if err != nil {
		s.runs.fail(scanID, fmt.Errorf("open node writer: %w", err), 0, 0, 0)
		return
	}

	controller := scancontrol.New(s.cfg.ScanProfile)
	limits := controller.Limits()
	batchSize := limits.Initial.BatchSize
	batchController := newBatchSizeController(batchSize, limits.Min.BatchSize, limits.Max.BatchSize)
	progressInterval := s.scanProgressInterval()
	queueSize := maxInt(batchSize*8, limits.Max.BatchSize*2)
	if queueSize < 1 {
		queueSize = 8
	}

	nodeCh := make(chan store.Node, queueSize)
	writerErrCh := make(chan error, 1)

	s.runs.recordWriteBatchSize(scanID, batchController.Get())
	go s.runNodeWriter(scanCtx, scanID, writer, nodeCh, writerErrCh, batchController, progressInterval)

	scanner := s.makeScanner(s.cfg.AnalyzeRoot, limits.Max.Concurrency)
	autotuneCtx, cancelAutotune := context.WithCancel(scanCtx)
	defer cancelAutotune()
	if tuner, ok := scanner.(scan.AdjustableConcurrency); ok {
		applied := tuner.SetConcurrencyLimit(limits.Initial.Concurrency)
		if limits.Adaptive {
			log.Printf("scan #%d adaptive control enabled: profile=%s initial_concurrency=%d max_concurrency=%d", scanID, s.cfg.ScanProfile, applied, limits.Max.Concurrency)
			target := &scanTuningTarget{runs: s.runs, scanID: scanID, tuner: tuner, batchController: batchController}
			go controller.Run(autotuneCtx, target, func(event scancontrol.Event) { logScanControlEvent(scanID, event) })
		} else {
			log.Printf("scan #%d fixed control: concurrency=%d batch_size=%d", scanID, applied, batchController.Get())
		}
	}
	result, scanErr := scanner.Scan(scanCtx, func(node scan.NodeRecord) error {
		storedNode := store.Node{
			Path:       node.Path,
			ParentPath: node.ParentPath,
			Name:       node.Name,
			Kind:       node.Kind,
			SizeBytes:  node.SizeBytes,
			MtimeUnix:  node.MtimeUnix,
		}

		select {
		case <-scanCtx.Done():
			return scanCtx.Err()
		case nodeCh <- storedNode:
			s.runs.recordNodeEnqueued(scanID, len(nodeCh), cap(nodeCh))
			return nil
		}
	})

	close(nodeCh)
	writerErr := <-writerErrCh
	cancelAutotune()

	if scanErr == nil && writerErr == nil && isUnreadableScanResult(result) {
		scanErr = fmt.Errorf("scan found no readable files under %q (warnings: %d); check mount and permissions", s.cfg.AnalyzeRoot, result.WarningCount)
	}

	if scanErr != nil {
		_ = writer.Discard()
		log.Printf("scan #%d failed: %v (nodes=%d bytes=%d warnings=%d)", scanID, scanErr, result.TotalNodes, result.TotalBytes, result.WarningCount)
		s.runs.fail(scanID, scanErr, 0, 0, result.WarningCount)
		return
	}
	if writerErr != nil {
		_ = writer.Discard()
		log.Printf("scan #%d failed: %v (nodes=%d bytes=%d warnings=%d)", scanID, writerErr, result.TotalNodes, result.TotalBytes, result.WarningCount)
		s.runs.fail(scanID, fmt.Errorf("write nodes: %w", writerErr), 0, 0, result.WarningCount)
		return
	}

	commitStarted := time.Now()
	if err := writer.Publish(); err != nil {
		s.runs.fail(scanID, fmt.Errorf("commit nodes: %w", err), result.TotalBytes, result.TotalNodes, result.WarningCount)
		return
	}
	log.Printf("scan #%d committed staged nodes: nodes=%d bytes=%d warnings=%d duration=%s", scanID, result.TotalNodes, result.TotalBytes, result.WarningCount, time.Since(commitStarted))

	if err := s.runs.complete(scanID, result); err != nil {
		s.runs.fail(scanID, fmt.Errorf("complete scan record: %w", err), result.TotalBytes, result.TotalNodes, result.WarningCount)
		return
	}
	log.Printf("scan #%d completed: nodes=%d bytes=%d warnings=%d", scanID, result.TotalNodes, result.TotalBytes, result.WarningCount)
}

func (s *Service) runNodeWriter(
	ctx context.Context,
	scanID int64,
	writer *store.SnapshotWriter,
	nodeCh <-chan store.Node,
	writerErrCh chan<- error,
	batchController *batchSizeController,
	progressInterval time.Duration,
) {
	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()

	batch := make([]store.Node, 0, batchController.Get())
	flush := func(now time.Time) error {
		if len(batch) == 0 {
			return nil
		}
		started := time.Now()
		if err := writer.Write(ctx, batch); err != nil {
			return err
		}
		s.runs.recordWriterFlush(scanID, time.Since(started))
		s.runs.recordProgressBatch(scanID, batch, now)
		batch = batch[:0]
		return nil
	}

	for {
		select {
		case node, ok := <-nodeCh:
			s.runs.recordWriterQueue(scanID, len(nodeCh), cap(nodeCh))
			if !ok {
				writerErrCh <- flush(time.Now().UTC())
				return
			}
			batch = append(batch, node)
			currentBatchSize := batchController.Get()
			s.runs.recordWriteBatchSize(scanID, currentBatchSize)
			if len(batch) >= currentBatchSize {
				if err := flush(time.Now().UTC()); err != nil {
					writerErrCh <- err
					return
				}
			}
		case tickAt := <-ticker.C:
			if err := flush(tickAt.UTC()); err != nil {
				writerErrCh <- err
				return
			}
		case <-ctx.Done():
			writerErrCh <- ctx.Err()
			return
		}
	}
}

type scanTuningTarget struct {
	runs            *scanRunLifecycle
	scanID          int64
	tuner           scan.AdjustableConcurrency
	batchController *batchSizeController
}

func (t *scanTuningTarget) Snapshot() scancontrol.Snapshot {
	runtime := t.runs.runtimeSnapshot(t.scanID)
	if !runtime.Active {
		return scancontrol.Snapshot{}
	}

	stats := t.tuner.ConcurrencyStats()
	var occupancy float64
	if runtime.Metrics.WriterQueueCap > 0 {
		occupancy = float64(runtime.Metrics.WriterQueueDepth) / float64(runtime.Metrics.WriterQueueCap)
	}

	return scancontrol.Snapshot{
		Active: true,
		Settings: scancontrol.Settings{
			Concurrency: stats.Limit,
			BatchSize:   t.batchController.Get(),
		},
		EnqueuedNodes:     runtime.Metrics.EnqueuedNodes,
		WrittenNodes:      runtime.Progress.ScannedNodes,
		QueueOccupancy:    occupancy,
		LastFlushDuration: runtime.Metrics.LastFlushDuration,
		LastFlushAt:       runtime.Metrics.LastFlushAt,
		FlushCount:        runtime.Metrics.FlushCount,
		InUse:             stats.InUse,
	}
}

func (t *scanTuningTarget) Apply(settings scancontrol.Settings) scancontrol.Settings {
	settings.Concurrency = t.tuner.SetConcurrencyLimit(settings.Concurrency)
	settings.BatchSize = t.batchController.Set(settings.BatchSize)
	t.runs.recordWriteBatchSize(t.scanID, settings.BatchSize)
	return settings
}

func logScanControlEvent(scanID int64, event scancontrol.Event) {
	log.Printf(
		"scan #%d adaptive control %s: reason=%s concurrency=%d->%d batch_size=%d->%d written_per_sec=%.1f enqueued_per_sec=%.1f queue=%.0f%% queue_delta=%+.0f%% flush=%s last_flush_age=%s in_use=%d no_progress_samples=%d",
		scanID,
		event.Kind,
		event.Reason,
		event.Previous.Concurrency,
		event.Current.Concurrency,
		event.Previous.BatchSize,
		event.Current.BatchSize,
		event.WrittenPerSecond,
		event.EnqueuedPerSecond,
		event.Snapshot.QueueOccupancy*100,
		event.QueueDelta*100,
		event.Snapshot.LastFlushDuration,
		flushAge(time.Now(), event.Snapshot.LastFlushAt),
		event.Snapshot.InUse,
		event.NoProgressSamples,
	)
}

func flushAge(now time.Time, flushedAt time.Time) time.Duration {
	if flushedAt.IsZero() {
		return 0
	}
	age := now.Sub(flushedAt)
	if age < 0 {
		return 0
	}
	return age.Round(time.Second)
}

func clampInt(v, minValue, maxValue int) int {
	if v < minValue {
		return minValue
	}
	if v > maxValue {
		return maxValue
	}
	return v
}

func (s *Service) scanProgressInterval() time.Duration {
	if s.cfg.ScanProgressInterval < minProgressInterval {
		return minProgressInterval
	}
	return s.cfg.ScanProgressInterval
}

func (s *Service) GetScanRun(ctx context.Context, scanID int64) (store.ScanRun, error) {
	return s.runs.get(ctx, scanID)
}

func (s *Service) GetCurrentScanRun(ctx context.Context) (*store.ScanRun, error) {
	return s.runs.current(ctx)
}

func (s *Service) GetLatestCompletedScanRun(ctx context.Context) (*store.ScanRun, error) {
	return s.runs.latestSnapshot(ctx)
}

func (s *Service) Recover(ctx context.Context) (RecoveryReport, error) {
	return s.runs.recover(ctx)
}

func (s *Service) GetChildren(ctx context.Context, scanID int64, request FolderViewRequest) (ChildrenResponse, error) {
	return s.folders.children(ctx, scanID, request)
}

func (s *Service) GetLargest(ctx context.Context, scanID int64, request FolderViewRequest) (LargestResponse, error) {
	return s.folders.largest(ctx, scanID, request)
}

func (s *Service) GetFolderView(ctx context.Context, scanID int64, request FolderViewRequest) (FolderViewResponse, error) {
	return s.folders.read(ctx, scanID, request)
}

func (s *Service) Config() config.Config {
	return s.cfg
}

func isUnreadableScanResult(result scan.Result) bool {
	if result.WarningCount == 0 || result.TotalBytes > 0 {
		return false
	}
	// If every discovered non-root node is a warning-only node, the scan is effectively unreadable.
	return result.TotalNodes <= result.WarningCount+1
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
