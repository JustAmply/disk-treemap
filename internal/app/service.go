package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/justamply/disk-treemap/internal/config"
	"github.com/justamply/disk-treemap/internal/pathutil"
	"github.com/justamply/disk-treemap/internal/scan"
	"github.com/justamply/disk-treemap/internal/store"
)

const minProgressInterval = 10 * time.Millisecond

var (
	ErrScanRunning  = errors.New("scan already running")
	ErrInvalidInput = errors.New("invalid input")
)

type scannerFactory func(root string, maxConcurrency int) scan.Engine

type liveProgress struct {
	ScanID       int64
	CurrentPath  string
	ScannedNodes int64
	ScannedFiles int64
	ScannedDirs  int64
	ScannedBytes int64
	UpdatedAt    time.Time
}

type Service struct {
	cfg           config.Config
	store         *store.Store
	makeScanner   scannerFactory
	mu            sync.Mutex
	running       bool
	runningScanID int64
	progress      liveProgress
}

type NodeQueryOptions struct {
	Limit   int
	Query   string
	Kind    string
	MinSize int64
	Sort    string
}

type ChildrenResponse struct {
	ScanID     int64        `json:"scan_id"`
	Path       string       `json:"path"`
	TotalBytes int64        `json:"total_bytes"`
	Children   []store.Node `json:"children"`
}

type LargestResponse struct {
	ScanID int64        `json:"scan_id"`
	Path   string       `json:"path"`
	Items  []store.Node `json:"items"`
}

type DiffResponse struct {
	TargetScanID int64            `json:"target_scan_id"`
	BaseScanID   int64            `json:"base_scan_id"`
	Path         string           `json:"path"`
	Items        []store.DiffItem `json:"items"`
}

func NewService(cfg config.Config, st *store.Store) *Service {
	return &Service{
		cfg:         cfg,
		store:       st,
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
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return 0, ErrScanRunning
	}

	scanID, err := s.store.CreateScanRun(ctx, s.cfg.AnalyzeRoot)
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	s.running = true
	s.runningScanID = scanID
	s.progress = liveProgress{
		ScanID:      scanID,
		CurrentPath: s.cfg.AnalyzeRoot,
		UpdatedAt:   time.Now().UTC(),
	}
	s.mu.Unlock()

	go s.runScan(scanID)
	return scanID, nil
}

func (s *Service) runScan(scanID int64) {
	ctx := context.Background()
	if err := s.store.MarkScanRunning(ctx, scanID, time.Now().UTC()); err != nil {
		s.finishFailure(scanID, fmt.Errorf("set running status: %w", err), 0, 0, 0)
		return
	}

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

	writer, err := s.store.BeginNodeWriter(scanCtx, scanID)
	if err != nil {
		s.finishFailure(scanID, fmt.Errorf("open node writer: %w", err), 0, 0, 0)
		return
	}

	batchSize := s.scanWriteBatchSize()
	progressInterval := s.scanProgressInterval()
	queueSize := batchSize * 8
	if queueSize < 1 {
		queueSize = 8
	}

	nodeCh := make(chan store.Node, queueSize)
	writerErrCh := make(chan error, 1)

	go s.runNodeWriter(scanCtx, scanID, writer, nodeCh, writerErrCh, batchSize, progressInterval)

	scanner := s.makeScanner(s.cfg.AnalyzeRoot, s.cfg.ScanMaxConcurrency)
	result, scanErr := scanner.Scan(scanCtx, func(node scan.NodeRecord) error {
		dbNode := store.Node{
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
		case nodeCh <- dbNode:
			return nil
		}
	})

	close(nodeCh)
	writerErr := <-writerErrCh

	if scanErr == nil && writerErr == nil && isUnreadableScanResult(result) {
		scanErr = fmt.Errorf("scan found no readable files under %q (warnings: %d); check mount and permissions", s.cfg.AnalyzeRoot, result.WarningCount)
	}

	if scanErr != nil {
		_ = writer.Rollback()
		log.Printf("scan #%d failed: %v (nodes=%d bytes=%d warnings=%d)", scanID, scanErr, result.TotalNodes, result.TotalBytes, result.WarningCount)
		s.finishFailure(scanID, scanErr, 0, 0, result.WarningCount)
		return
	}
	if writerErr != nil {
		_ = writer.Rollback()
		log.Printf("scan #%d failed: %v (nodes=%d bytes=%d warnings=%d)", scanID, writerErr, result.TotalNodes, result.TotalBytes, result.WarningCount)
		s.finishFailure(scanID, fmt.Errorf("write nodes: %w", writerErr), 0, 0, result.WarningCount)
		return
	}

	if err := writer.Commit(); err != nil {
		s.finishFailure(scanID, fmt.Errorf("commit nodes: %w", err), result.TotalBytes, result.TotalNodes, result.WarningCount)
		return
	}

	if err := s.store.CompleteScan(ctx, scanID, "completed", time.Now().UTC(), result.TotalBytes, result.TotalNodes, result.WarningCount, ""); err != nil {
		s.finishFailure(scanID, fmt.Errorf("complete scan record: %w", err), result.TotalBytes, result.TotalNodes, result.WarningCount)
		return
	}

	s.clearRunning(scanID)
	if deleted, err := s.store.PruneCompletedFailedScans(context.Background(), s.cfg.ScanHistoryMaxRuns); err != nil {
		log.Printf("scan #%d prune warning: %v", scanID, err)
	} else if len(deleted) > 0 {
		log.Printf("scan #%d pruned %d old scan run(s)", scanID, len(deleted))
	}

	log.Printf("scan #%d completed: nodes=%d bytes=%d warnings=%d", scanID, result.TotalNodes, result.TotalBytes, result.WarningCount)
}

func (s *Service) runNodeWriter(
	ctx context.Context,
	scanID int64,
	writer *store.NodeWriter,
	nodeCh <-chan store.Node,
	writerErrCh chan<- error,
	batchSize int,
	progressInterval time.Duration,
) {
	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()

	batch := make([]store.Node, 0, batchSize)
	flush := func(now time.Time) error {
		if len(batch) == 0 {
			return nil
		}
		if err := writer.InsertNodesBatch(ctx, scanID, batch); err != nil {
			return err
		}
		s.recordProgressBatch(scanID, batch, now)
		batch = batch[:0]
		return nil
	}

	for {
		select {
		case node, ok := <-nodeCh:
			if !ok {
				writerErrCh <- flush(time.Now().UTC())
				return
			}
			batch = append(batch, node)
			if len(batch) >= batchSize {
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

func (s *Service) recordProgressBatch(scanID int64, batch []store.Node, updatedAt time.Time) {
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

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.runningScanID != scanID || s.progress.ScanID != scanID {
		return
	}

	s.progress.CurrentPath = batch[len(batch)-1].Path
	s.progress.ScannedNodes += nodes
	s.progress.ScannedFiles += files
	s.progress.ScannedDirs += dirs
	s.progress.ScannedBytes += bytes
	s.progress.UpdatedAt = updatedAt
}

func (s *Service) scanWriteBatchSize() int {
	if s.cfg.ScanWriteBatchSize < 1 {
		return 1
	}
	return s.cfg.ScanWriteBatchSize
}

func (s *Service) scanProgressInterval() time.Duration {
	if s.cfg.ScanProgressInterval < minProgressInterval {
		return minProgressInterval
	}
	return s.cfg.ScanProgressInterval
}

func (s *Service) finishFailure(scanID int64, scanErr error, totalBytes, totalNodes, warnings int64) {
	_ = s.store.CompleteScan(context.Background(), scanID, "failed", time.Now().UTC(), totalBytes, totalNodes, warnings, scanErr.Error())
	s.clearRunning(scanID)
}

func (s *Service) clearRunning(scanID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running && s.runningScanID == scanID {
		s.running = false
		s.runningScanID = 0
		s.progress = liveProgress{}
	}
}

func (s *Service) GetScanRun(ctx context.Context, scanID int64) (store.ScanRun, error) {
	run, err := s.store.GetScanRun(ctx, scanID)
	if err != nil {
		return store.ScanRun{}, err
	}
	if progress := s.snapshotProgress(scanID); progress != nil {
		run.Progress = progress
	}
	return run, nil
}

func (s *Service) GetLatestScanRun(ctx context.Context) (*store.ScanRun, error) {
	run, err := s.store.GetLatestScanRun(ctx)
	if err != nil || run == nil {
		return run, err
	}
	if progress := s.snapshotProgress(run.ID); progress != nil {
		run.Progress = progress
	}
	return run, nil
}

func (s *Service) ListScans(ctx context.Context, limit int, status string) ([]store.ScanRun, error) {
	if status != "" && !isValidScanStatus(status) {
		return nil, fmt.Errorf("%w: unsupported scan status %q", ErrInvalidInput, status)
	}

	runs, err := s.store.ListScanRuns(ctx, limit, status)
	if err != nil {
		return nil, err
	}
	for i := range runs {
		if progress := s.snapshotProgress(runs[i].ID); progress != nil {
			runs[i].Progress = progress
		}
	}
	return runs, nil
}

func (s *Service) DeleteScan(ctx context.Context, scanID int64) error {
	s.mu.Lock()
	isRunning := s.running && s.runningScanID == scanID
	s.mu.Unlock()
	if isRunning {
		return ErrScanRunning
	}

	run, err := s.store.GetScanRun(ctx, scanID)
	if err != nil {
		return err
	}
	if run.Status == "running" || run.Status == "queued" {
		return ErrScanRunning
	}

	deleted, err := s.store.DeleteScanRun(ctx, scanID)
	if err != nil {
		return err
	}
	if !deleted {
		return store.ErrNotFound
	}
	return nil
}

func (s *Service) snapshotProgress(scanID int64) *store.ScanProgress {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.runningScanID != scanID || s.progress.ScanID != scanID {
		return nil
	}

	updated := s.progress.UpdatedAt
	return &store.ScanProgress{
		CurrentPath:  s.progress.CurrentPath,
		ScannedNodes: s.progress.ScannedNodes,
		ScannedFiles: s.progress.ScannedFiles,
		ScannedDirs:  s.progress.ScannedDirs,
		ScannedBytes: s.progress.ScannedBytes,
		UpdatedAt:    &updated,
	}
}

func (s *Service) GetChildren(ctx context.Context, scanID int64, requestedPath string, opts NodeQueryOptions) (ChildrenResponse, error) {
	path, err := pathutil.NormalizeWithinRoot(s.cfg.AnalyzeRoot, requestedPath)
	if err != nil {
		return ChildrenResponse{}, err
	}

	normalized, err := normalizeNodeQueryOptions(opts, s.cfg.MaxChildrenPerQuery, "size_desc")
	if err != nil {
		return ChildrenResponse{}, err
	}

	node, err := s.store.GetNode(ctx, scanID, path)
	if err != nil {
		return ChildrenResponse{}, err
	}

	children, err := s.store.ListChildrenWithOptions(ctx, scanID, path, store.NodeQueryOptions{
		Limit:   normalized.Limit,
		Query:   normalized.Query,
		Kind:    normalized.Kind,
		MinSize: normalized.MinSize,
		Sort:    normalized.Sort,
	})
	if err != nil {
		return ChildrenResponse{}, err
	}

	return ChildrenResponse{
		ScanID:     scanID,
		Path:       path,
		TotalBytes: node.SizeBytes,
		Children:   children,
	}, nil
}

func (s *Service) GetLargest(ctx context.Context, scanID int64, requestedPath string, opts NodeQueryOptions) (LargestResponse, error) {
	path, err := pathutil.NormalizeWithinRoot(s.cfg.AnalyzeRoot, requestedPath)
	if err != nil {
		return LargestResponse{}, err
	}

	normalized, err := normalizeNodeQueryOptions(opts, 1000, "size_desc")
	if err != nil {
		return LargestResponse{}, err
	}

	items, err := s.store.ListLargestInPathWithOptions(ctx, scanID, path, store.NodeQueryOptions{
		Limit:   normalized.Limit,
		Query:   normalized.Query,
		Kind:    normalized.Kind,
		MinSize: normalized.MinSize,
		Sort:    normalized.Sort,
	})
	if err != nil {
		return LargestResponse{}, err
	}

	return LargestResponse{
		ScanID: scanID,
		Path:   path,
		Items:  items,
	}, nil
}

func (s *Service) GetDirectoryDiff(ctx context.Context, targetScanID, baseScanID int64, requestedPath string, limit int) (DiffResponse, error) {
	path, err := pathutil.NormalizeWithinRoot(s.cfg.AnalyzeRoot, requestedPath)
	if err != nil {
		return DiffResponse{}, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	if _, err := s.store.GetScanRun(ctx, targetScanID); err != nil {
		return DiffResponse{}, err
	}
	if _, err := s.store.GetScanRun(ctx, baseScanID); err != nil {
		return DiffResponse{}, err
	}

	items, err := s.store.ListDirectoryDiff(ctx, targetScanID, baseScanID, path, limit)
	if err != nil {
		return DiffResponse{}, err
	}

	return DiffResponse{
		TargetScanID: targetScanID,
		BaseScanID:   baseScanID,
		Path:         path,
		Items:        items,
	}, nil
}

func (s *Service) Config() config.Config {
	return s.cfg
}

func normalizeNodeQueryOptions(opts NodeQueryOptions, maxLimit int, defaultSort string) (NodeQueryOptions, error) {
	if opts.Limit <= 0 {
		opts.Limit = maxLimit
	}
	if opts.Limit > maxLimit {
		opts.Limit = maxLimit
	}

	switch opts.Kind {
	case "", "file", "dir":
	default:
		return NodeQueryOptions{}, fmt.Errorf("%w: unsupported type filter %q", ErrInvalidInput, opts.Kind)
	}

	switch opts.Sort {
	case "", "size_desc", "size_asc", "name_asc", "name_desc":
	default:
		return NodeQueryOptions{}, fmt.Errorf("%w: unsupported sort %q", ErrInvalidInput, opts.Sort)
	}

	if opts.Sort == "" {
		opts.Sort = defaultSort
	}
	if opts.MinSize < 0 {
		return NodeQueryOptions{}, fmt.Errorf("%w: min_size must be >= 0", ErrInvalidInput)
	}
	return opts, nil
}

func isValidScanStatus(status string) bool {
	switch status {
	case "queued", "running", "completed", "failed":
		return true
	default:
		return false
	}
}

func isUnreadableScanResult(result scan.Result) bool {
	if result.WarningCount == 0 || result.TotalBytes > 0 {
		return false
	}
	// If every discovered non-root node is a warning-only node, the scan is effectively unreadable.
	return result.TotalNodes <= result.WarningCount+1
}
