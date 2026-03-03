package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/justamply/disk-treemap/internal/config"
	"github.com/justamply/disk-treemap/internal/pathutil"
	"github.com/justamply/disk-treemap/internal/scan"
	"github.com/justamply/disk-treemap/internal/store"
)

var ErrScanRunning = errors.New("scan already running")

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

	scanCtx := context.Background()
	cancel := func() {}
	if s.cfg.ScanTimeout > 0 {
		scanCtx, cancel = context.WithTimeout(context.Background(), s.cfg.ScanTimeout)
	}
	defer cancel()

	writer, err := s.store.BeginNodeWriter(scanCtx, scanID)
	if err != nil {
		s.finishFailure(scanID, fmt.Errorf("open node writer: %w", err), 0, 0, 0)
		return
	}

	scanner := s.makeScanner(s.cfg.AnalyzeRoot, s.cfg.ScanMaxConcurrency)
	result, scanErr := scanner.Scan(scanCtx, func(node scan.NodeRecord) error {
		if err := writer.InsertNode(scanCtx, scanID, store.Node{
			Path:       node.Path,
			ParentPath: node.ParentPath,
			Name:       node.Name,
			Kind:       node.Kind,
			SizeBytes:  node.SizeBytes,
			MtimeUnix:  node.MtimeUnix,
		}); err != nil {
			return err
		}
		s.recordProgress(scanID, node)
		return nil
	})
	if scanErr != nil {
		_ = writer.Rollback()
		s.finishFailure(scanID, scanErr, 0, 0, result.WarningCount)
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
}

func (s *Service) recordProgress(scanID int64, node scan.NodeRecord) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running || s.runningScanID != scanID || s.progress.ScanID != scanID {
		return
	}

	s.progress.CurrentPath = node.Path
	s.progress.ScannedNodes++
	s.progress.UpdatedAt = now

	switch node.Kind {
	case "file":
		s.progress.ScannedFiles++
		s.progress.ScannedBytes += node.SizeBytes
	case "dir":
		s.progress.ScannedDirs++
	}
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

func (s *Service) GetChildren(ctx context.Context, scanID int64, requestedPath string, limit int) (ChildrenResponse, error) {
	path, err := pathutil.NormalizeWithinRoot(s.cfg.AnalyzeRoot, requestedPath)
	if err != nil {
		return ChildrenResponse{}, err
	}
	if limit <= 0 || limit > s.cfg.MaxChildrenPerQuery {
		limit = s.cfg.MaxChildrenPerQuery
	}

	node, err := s.store.GetNode(ctx, scanID, path)
	if err != nil {
		return ChildrenResponse{}, err
	}

	children, err := s.store.ListChildren(ctx, scanID, path, limit)
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

func (s *Service) GetLargest(ctx context.Context, scanID int64, requestedPath string, limit int) (LargestResponse, error) {
	path, err := pathutil.NormalizeWithinRoot(s.cfg.AnalyzeRoot, requestedPath)
	if err != nil {
		return LargestResponse{}, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	items, err := s.store.ListLargestInPath(ctx, scanID, path, limit)
	if err != nil {
		return LargestResponse{}, err
	}

	return LargestResponse{
		ScanID: scanID,
		Path:   path,
		Items:  items,
	}, nil
}

func (s *Service) Config() config.Config {
	return s.cfg
}
