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

const (
	minProgressInterval         = 10 * time.Millisecond
	storageOptimizeTimeout      = 30 * time.Second
	exploreExpandedDirLimit     = 12
	exploreBranchLimit          = 14
	exploreTreemapNodeBudget    = 240
	exploreSyntheticNodeMinByte = 1
)

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

type scannedNodeWrite struct {
	Stored   store.StoredNode
	Progress store.Node
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

type ExploreSummary struct {
	Name              string `json:"name"`
	TotalBytes        int64  `json:"total_bytes"`
	VisibleBytes      int64  `json:"visible_bytes"`
	MatchingItemCount int64  `json:"matching_item_count"`
	ReturnedItemCount int    `json:"returned_item_count"`
	VisibleDirCount   int    `json:"visible_dir_count"`
	VisibleFileCount  int    `json:"visible_file_count"`
	HiddenItemCount   int64  `json:"hidden_item_count"`
	HasActiveFilters  bool   `json:"has_active_filters"`
	IsResultTruncated bool   `json:"is_result_truncated"`
}

type ExploreTreemapNode struct {
	Name            string               `json:"name"`
	Path            string               `json:"path,omitempty"`
	Type            string               `json:"type"`
	SizeBytes       int64                `json:"size_bytes"`
	Clickable       bool                 `json:"clickable"`
	Synthetic       bool                 `json:"synthetic,omitempty"`
	HiddenItemCount int64                `json:"hidden_item_count,omitempty"`
	Children        []ExploreTreemapNode `json:"children,omitempty"`
}

type ExploreResponse struct {
	ScanID  int64              `json:"scan_id"`
	Path    string             `json:"path"`
	Summary ExploreSummary     `json:"summary"`
	Items   []store.Node       `json:"items"`
	Treemap ExploreTreemapNode `json:"treemap"`
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

	if _, err := s.store.PruneOperationalScans(context.Background()); err != nil {
		log.Printf("scan #%d prune warning: %v", scanID, err)
	}

	log.Printf("scan #%d queued: root=%q concurrency=%d batch_size=%d", scanID, s.cfg.AnalyzeRoot, s.cfg.ScanMaxConcurrency, s.scanWriteBatchSize())
	go s.runScan(scanID)
	return scanID, nil
}

func (s *Service) runScan(scanID int64) {
	ctx := context.Background()
	if err := s.store.MarkScanRunning(ctx, scanID, time.Now().UTC()); err != nil {
		s.finishFailure(scanID, fmt.Errorf("set running status: %w", err), 0, 0, 0)
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

	nodeCh := make(chan scannedNodeWrite, queueSize)
	writerErrCh := make(chan error, 1)

	go s.runNodeWriter(scanCtx, scanID, writer, nodeCh, writerErrCh, batchSize, progressInterval)

	scanner := s.makeScanner(s.cfg.AnalyzeRoot, s.cfg.ScanMaxConcurrency)
	var fallbackMu sync.Mutex
	var fallbackNextID int64
	fallbackPathIDs := map[string]int64{}
	fallbackIDForPath := func(path string) int64 {
		if id, ok := fallbackPathIDs[path]; ok {
			return id
		}
		fallbackNextID++
		id := fallbackNextID
		fallbackPathIDs[path] = id
		return id
	}
	toStoredNode := func(node scan.NodeRecord) store.StoredNode {
		nodeID := node.NodeID
		parentID := node.ParentID
		if nodeID == 0 {
			fallbackMu.Lock()
			nodeID = fallbackIDForPath(node.Path)
			if parentID == nil && node.ParentPath != "" {
				parent := fallbackIDForPath(node.ParentPath)
				parentID = &parent
			}
			fallbackMu.Unlock()
		}
		return store.StoredNode{
			NodeID:    nodeID,
			ParentID:  parentID,
			Name:      node.Name,
			Kind:      node.Kind,
			SizeBytes: node.SizeBytes,
			MtimeUnix: node.MtimeUnix,
		}
	}
	result, scanErr := scanner.Scan(scanCtx, func(node scan.NodeRecord) error {
		progressNode := store.Node{
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
		case nodeCh <- scannedNodeWrite{Stored: toStoredNode(node), Progress: progressNode}:
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
	log.Printf("scan #%d committed node batch: nodes=%d bytes=%d warnings=%d", scanID, result.TotalNodes, result.TotalBytes, result.WarningCount)

	if err := s.store.CompleteScan(ctx, scanID, "completed", time.Now().UTC(), result.TotalBytes, result.TotalNodes, result.WarningCount, ""); err != nil {
		s.finishFailure(scanID, fmt.Errorf("complete scan record: %w", err), result.TotalBytes, result.TotalNodes, result.WarningCount)
		return
	}

	s.clearRunning(scanID)
	s.pruneOperationalScans(scanID)
	optimizeCtx, optimizeCancel := context.WithTimeout(context.Background(), storageOptimizeTimeout)
	defer optimizeCancel()
	if err := s.store.OptimizeStorage(optimizeCtx, false); err != nil {
		log.Printf("scan #%d storage optimize warning: %v", scanID, err)
	}
	log.Printf("scan #%d completed: nodes=%d bytes=%d warnings=%d", scanID, result.TotalNodes, result.TotalBytes, result.WarningCount)
}

func (s *Service) runNodeWriter(
	ctx context.Context,
	scanID int64,
	writer *store.NodeWriter,
	nodeCh <-chan scannedNodeWrite,
	writerErrCh chan<- error,
	batchSize int,
	progressInterval time.Duration,
) {
	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()

	batch := make([]scannedNodeWrite, 0, batchSize)
	flush := func(now time.Time) error {
		if len(batch) == 0 {
			return nil
		}
		storedBatch := make([]store.StoredNode, 0, len(batch))
		for _, node := range batch {
			storedBatch = append(storedBatch, node.Stored)
		}
		if err := writer.InsertStoredNodesBatch(ctx, scanID, storedBatch); err != nil {
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

func (s *Service) recordProgressBatch(scanID int64, batch []scannedNodeWrite, updatedAt time.Time) {
	var nodes int64
	var files int64
	var dirs int64
	var bytes int64

	for _, item := range batch {
		node := item.Progress
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

	s.progress.CurrentPath = batch[len(batch)-1].Progress.Path
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
	s.pruneOperationalScans(scanID)
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

func (s *Service) pruneOperationalScans(scanID int64) {
	deleted, err := s.store.PruneOperationalScans(context.Background())
	if err != nil {
		log.Printf("scan #%d prune warning: %v", scanID, err)
		return
	}
	if len(deleted) > 0 {
		log.Printf("scan #%d pruned %d old scan run(s)", scanID, len(deleted))
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

func (s *Service) GetCurrentScanRun(ctx context.Context) (*store.ScanRun, error) {
	run, err := s.store.GetLatestScanRun(ctx)
	if err != nil || run == nil {
		return run, err
	}
	if progress := s.snapshotProgress(run.ID); progress != nil {
		run.Progress = progress
	}
	return run, nil
}

func (s *Service) GetLatestCompletedScanRun(ctx context.Context) (*store.ScanRun, error) {
	return s.store.GetLatestCompletedScanRun(ctx)
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

func (s *Service) GetExplore(ctx context.Context, scanID int64, requestedPath string, opts NodeQueryOptions) (ExploreResponse, error) {
	path, err := pathutil.NormalizeWithinRoot(s.cfg.AnalyzeRoot, requestedPath)
	if err != nil {
		return ExploreResponse{}, err
	}

	normalized, err := normalizeNodeQueryOptions(opts, s.cfg.MaxChildrenPerQuery, "size_desc")
	if err != nil {
		return ExploreResponse{}, err
	}

	currentNode, err := s.store.GetNode(ctx, scanID, path)
	if err != nil {
		return ExploreResponse{}, err
	}

	items, err := s.store.ListChildrenWithOptions(ctx, scanID, path, store.NodeQueryOptions{
		Limit:   normalized.Limit,
		Query:   normalized.Query,
		Kind:    normalized.Kind,
		MinSize: normalized.MinSize,
		Sort:    normalized.Sort,
	})
	if err != nil {
		return ExploreResponse{}, err
	}

	aggregate, err := s.store.AggregateChildrenWithOptions(ctx, scanID, path, store.NodeQueryOptions{
		Query:   normalized.Query,
		Kind:    normalized.Kind,
		MinSize: normalized.MinSize,
	})
	if err != nil {
		return ExploreResponse{}, err
	}

	summary := ExploreSummary{
		Name:              currentNode.Name,
		TotalBytes:        currentNode.SizeBytes,
		VisibleBytes:      aggregate.TotalBytes,
		MatchingItemCount: aggregate.Count,
		ReturnedItemCount: len(items),
		HiddenItemCount:   maxInt64(aggregate.Count-int64(len(items)), 0),
		HasActiveFilters:  normalized.Query != "" || normalized.Kind != "" || normalized.MinSize > 0,
	}
	summary.IsResultTruncated = summary.HiddenItemCount > 0
	for _, item := range items {
		switch item.Kind {
		case "dir":
			summary.VisibleDirCount++
		case "file":
			summary.VisibleFileCount++
		}
	}

	treemap, err := s.buildExploreTreemap(ctx, scanID, currentNode, items, aggregate, summary.HasActiveFilters)
	if err != nil {
		return ExploreResponse{}, err
	}

	return ExploreResponse{
		ScanID:  scanID,
		Path:    path,
		Summary: summary,
		Items:   items,
		Treemap: treemap,
	}, nil
}

func (s *Service) Config() config.Config {
	return s.cfg
}

func (s *Service) buildExploreTreemap(
	ctx context.Context,
	scanID int64,
	rootNode store.Node,
	items []store.Node,
	aggregate store.ChildAggregate,
	hasFilters bool,
) (ExploreTreemapNode, error) {
	root := toTreemapNode(rootNode)
	root.Clickable = false
	root.Children = make([]ExploreTreemapNode, 0, len(items)+1)

	var renderedBytes int64
	for _, item := range items {
		root.Children = append(root.Children, toTreemapNode(item))
		renderedBytes += item.SizeBytes
	}

	hiddenRootItems := aggregate.Count - int64(len(items))
	hiddenRootBytes := aggregate.TotalBytes - renderedBytes
	if hiddenRootBytes < 0 {
		hiddenRootBytes = 0
	}
	if hiddenRootItems > 0 {
		root.Children = append(root.Children, buildSyntheticTreemapNode(hiddenRootItems, hiddenRootBytes))
	}

	if hasFilters || len(root.Children) == 0 {
		return root, nil
	}

	remainingBudget := exploreTreemapNodeBudget - len(root.Children)
	if remainingBudget <= 1 {
		return root, nil
	}

	expandedDirs := 0
	for idx := range root.Children {
		child := &root.Children[idx]
		if child.Type != "dir" || child.Synthetic {
			continue
		}
		if expandedDirs >= exploreExpandedDirLimit || remainingBudget <= 1 {
			break
		}

		aggregate, err := s.store.AggregateChildrenWithOptions(ctx, scanID, child.Path, store.NodeQueryOptions{})
		if err != nil {
			return ExploreTreemapNode{}, err
		}
		if aggregate.Count == 0 {
			continue
		}

		branchLimit := minInt(exploreBranchLimit, remainingBudget)
		if branchLimit < 1 {
			break
		}

		grandchildren, err := s.store.ListChildrenWithOptions(ctx, scanID, child.Path, store.NodeQueryOptions{
			Limit: branchLimit,
			Sort:  "size_desc",
		})
		if err != nil {
			return ExploreTreemapNode{}, err
		}
		if len(grandchildren) == 0 {
			continue
		}

		child.Children = make([]ExploreTreemapNode, 0, len(grandchildren)+1)
		var renderedGrandchildBytes int64
		for _, grandchild := range grandchildren {
			child.Children = append(child.Children, toTreemapNode(grandchild))
			renderedGrandchildBytes += grandchild.SizeBytes
		}

		hiddenGrandchildren := aggregate.Count - int64(len(grandchildren))
		hiddenGrandchildBytes := child.SizeBytes - renderedGrandchildBytes
		if hiddenGrandchildBytes < 0 {
			hiddenGrandchildBytes = 0
		}
		if hiddenGrandchildren > 0 {
			child.Children = append(child.Children, buildSyntheticTreemapNode(hiddenGrandchildren, hiddenGrandchildBytes))
		}

		expandedDirs++
		remainingBudget -= len(grandchildren)
	}

	return root, nil
}

func toTreemapNode(node store.Node) ExploreTreemapNode {
	return ExploreTreemapNode{
		Name:      node.Name,
		Path:      node.Path,
		Type:      node.Kind,
		SizeBytes: node.SizeBytes,
		Clickable: node.Kind == "dir",
	}
}

func buildSyntheticTreemapNode(hiddenCount, hiddenBytes int64) ExploreTreemapNode {
	if hiddenBytes < exploreSyntheticNodeMinByte {
		hiddenBytes = exploreSyntheticNodeMinByte
	}

	label := "remaining item"
	if hiddenCount != 1 {
		label = "remaining items"
	}

	return ExploreTreemapNode{
		Name:            fmt.Sprintf("%d %s", hiddenCount, label),
		Type:            "group",
		SizeBytes:       hiddenBytes,
		Clickable:       false,
		Synthetic:       true,
		HiddenItemCount: hiddenCount,
	}
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

func isUnreadableScanResult(result scan.Result) bool {
	if result.WarningCount == 0 || result.TotalBytes > 0 {
		return false
	}
	// If every discovered non-root node is a warning-only node, the scan is effectively unreadable.
	return result.TotalNodes <= result.WarningCount+1
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
