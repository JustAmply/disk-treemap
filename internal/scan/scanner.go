package scan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type NodeRecord struct {
	NodeID     int64
	ParentID   *int64
	Path       string
	ParentPath string
	Name       string
	Kind       string
	SizeBytes  int64
	MtimeUnix  int64
}

type Result struct {
	TotalBytes   int64
	TotalNodes   int64
	WarningCount int64
}

type NodeCallback func(NodeRecord) error

type Engine interface {
	Scan(ctx context.Context, cb NodeCallback) (Result, error)
}

type Scanner struct {
	root        string
	fileLimiter *concurrencyLimiter
	dirLimiter  *concurrencyLimiter
}

func New(root string, maxConcurrency int) *Scanner {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	s := &Scanner{root: filepath.Clean(root)}
	if maxConcurrency > 1 {
		s.fileLimiter = newConcurrencyLimiter(maxConcurrency, maxConcurrency)
		s.dirLimiter = newConcurrencyLimiter(maxConcurrency-1, maxConcurrency-1)
	}
	return s
}

type ConcurrencyStats struct {
	Limit int
	InUse int
	Max   int
}

type AdjustableConcurrency interface {
	SetConcurrencyLimit(limit int) int
	ConcurrencyStats() ConcurrencyStats
}

type concurrencyLimiter struct {
	mu    sync.Mutex
	limit int
	inUse int
	max   int
}

func newConcurrencyLimiter(limit, max int) *concurrencyLimiter {
	if max < 0 {
		max = 0
	}
	if limit < 0 {
		limit = 0
	}
	if limit > max {
		limit = max
	}
	return &concurrencyLimiter{limit: limit, max: max}
}

func (l *concurrencyLimiter) Acquire(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		l.mu.Lock()
		if l.inUse < l.limit {
			l.inUse++
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (l *concurrencyLimiter) TryAcquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inUse >= l.limit {
		return false
	}
	l.inUse++
	return true
}

func (l *concurrencyLimiter) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inUse > 0 {
		l.inUse--
	}
}

func (l *concurrencyLimiter) SetLimit(limit int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit < 0 {
		limit = 0
	}
	if limit > l.max {
		limit = l.max
	}
	l.limit = limit
	return l.limit
}

func (l *concurrencyLimiter) Stats() ConcurrencyStats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return ConcurrencyStats{Limit: l.limit, InUse: l.inUse, Max: l.max}
}

func (s *Scanner) SetConcurrencyLimit(limit int) int {
	if limit < 1 {
		limit = 1
	}
	if s.fileLimiter == nil {
		return 1
	}
	applied := s.fileLimiter.SetLimit(limit)
	if s.dirLimiter != nil {
		s.dirLimiter.SetLimit(applied - 1)
	}
	return applied
}

func (s *Scanner) ConcurrencyStats() ConcurrencyStats {
	if s.fileLimiter == nil {
		return ConcurrencyStats{Limit: 1, Max: 1}
	}
	fileStats := s.fileLimiter.Stats()
	if s.dirLimiter != nil {
		dirStats := s.dirLimiter.Stats()
		fileStats.InUse += dirStats.InUse
	}
	return fileStats
}

func (s *Scanner) Scan(ctx context.Context, cb NodeCallback) (Result, error) {
	if cb == nil {
		return Result{}, errors.New("node callback is required")
	}

	var nextNodeID int64
	allocNodeID := func() int64 {
		return atomic.AddInt64(&nextNodeID, 1)
	}

	totalBytes, totalNodes, warnings, err := s.scanNode(ctx, s.root, "", 0, false, nil, nil, s.dirLimiter, s.fileLimiter, allocNodeID, cb)
	if err != nil {
		return Result{}, err
	}

	return Result{
		TotalBytes:   totalBytes,
		TotalNodes:   totalNodes,
		WarningCount: warnings,
	}, nil
}

func (s *Scanner) scanNode(
	ctx context.Context,
	path, parentPath string,
	parentID int64,
	hasParent bool,
	entry fs.DirEntry,
	knownInfo fs.FileInfo,
	dirLimiter *concurrencyLimiter,
	fileLimiter *concurrencyLimiter,
	allocNodeID func() int64,
	emit NodeCallback,
) (int64, int64, int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, 0, err
	}

	var info fs.FileInfo
	var err error
	if knownInfo != nil {
		info = knownInfo
	} else if entry != nil {
		if entry.Type()&os.ModeSymlink != 0 {
			return 0, 0, 0, nil
		}
		info, err = entry.Info()
	} else {
		info, err = os.Lstat(path)
	}
	if err != nil {
		if isNonFatal(err) {
			log.Printf("scan warning: lstat %q: %v", path, err)
			return 0, 0, 1, nil
		}
		return 0, 0, 0, fmt.Errorf("lstat %q: %w", path, err)
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return 0, 0, 0, nil
	}

	nodeID := allocNodeID()
	var nodeParentID *int64
	if hasParent {
		parent := parentID
		nodeParentID = &parent
	}

	node := NodeRecord{
		NodeID:     nodeID,
		ParentID:   nodeParentID,
		Path:       path,
		ParentPath: parentPath,
		Name:       filepath.Base(path),
		MtimeUnix:  info.ModTime().Unix(),
	}

	if !info.IsDir() {
		node.Kind = "file"
		node.SizeBytes = info.Size()
		if err := emit(node); err != nil {
			return 0, 0, 0, err
		}
		return node.SizeBytes, 1, 0, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		if isNonFatal(err) {
			log.Printf("scan warning: read dir %q: %v", path, err)
			node.Kind = "dir"
			node.SizeBytes = 0
			if emitErr := emit(node); emitErr != nil {
				return 0, 0, 0, emitErr
			}
			return 0, 1, 1, nil
		}
		return 0, 0, 0, fmt.Errorf("read dir %q: %w", path, err)
	}

	var totalSize int64
	var totalNodes int64
	var warningCount int64
	var firstErr error
	var mu sync.Mutex
	var wg sync.WaitGroup

	apply := func(sz, n, warnings int64, applyErr error) {
		mu.Lock()
		defer mu.Unlock()
		if applyErr != nil {
			if firstErr == nil {
				firstErr = applyErr
			}
			return
		}
		totalSize += sz
		totalNodes += n
		warningCount += warnings
	}

	for _, entry := range entries {
		childPath := filepath.Join(path, entry.Name())
		childEntry := entry

		if childEntry.Type()&os.ModeSymlink != 0 {
			continue
		}

		if fileLimiter != nil && !childEntry.IsDir() {
			if err := fileLimiter.Acquire(ctx); err != nil {
				apply(0, 0, 0, ctx.Err())
				continue
			}

			wg.Add(1)
			go func(p string, e fs.DirEntry) {
				defer wg.Done()
				defer fileLimiter.Release()

				info, infoErr := e.Info()
				if infoErr != nil {
					if isNonFatal(infoErr) {
						log.Printf("scan warning: lstat %q: %v", p, infoErr)
						apply(0, 0, 1, nil)
						return
					}
					apply(0, 0, 0, fmt.Errorf("lstat %q: %w", p, infoErr))
					return
				}

				if info.Mode()&os.ModeSymlink != 0 {
					return
				}

				if info.IsDir() {
					sz, n, w, childErr := s.scanNode(ctx, p, path, nodeID, true, e, info, dirLimiter, fileLimiter, allocNodeID, emit)
					apply(sz, n, w, childErr)
					return
				}

				sz, n, w, childErr := s.scanNode(ctx, p, path, nodeID, true, e, info, dirLimiter, fileLimiter, allocNodeID, emit)
				apply(sz, n, w, childErr)
			}(childPath, childEntry)
			continue
		}

		spawned := false
		if dirLimiter != nil {
			if dirLimiter.TryAcquire() {
				spawned = true
			}
		}

		if spawned {
			wg.Add(1)
			go func(p string, e fs.DirEntry) {
				defer wg.Done()
				defer dirLimiter.Release()
				sz, n, w, childErr := s.scanNode(ctx, p, path, nodeID, true, e, nil, dirLimiter, fileLimiter, allocNodeID, emit)
				apply(sz, n, w, childErr)
			}(childPath, childEntry)
			continue
		}

		sz, n, w, childErr := s.scanNode(ctx, childPath, path, nodeID, true, childEntry, nil, dirLimiter, fileLimiter, allocNodeID, emit)
		apply(sz, n, w, childErr)
	}

	wg.Wait()
	if firstErr != nil {
		return 0, 0, 0, firstErr
	}

	node.Kind = "dir"
	node.SizeBytes = totalSize
	if err := emit(node); err != nil {
		return 0, 0, 0, err
	}

	return totalSize, totalNodes + 1, warningCount, nil
}

func isNonFatal(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist)
}
