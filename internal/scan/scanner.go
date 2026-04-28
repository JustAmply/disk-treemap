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
	root           string
	maxConcurrency int
}

func New(root string, maxConcurrency int) *Scanner {
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	return &Scanner{root: filepath.Clean(root), maxConcurrency: maxConcurrency}
}

func (s *Scanner) Scan(ctx context.Context, cb NodeCallback) (Result, error) {
	if cb == nil {
		return Result{}, errors.New("node callback is required")
	}

	var sem chan struct{}
	if s.maxConcurrency > 1 {
		sem = make(chan struct{}, s.maxConcurrency-1)
	}

	var nextNodeID int64
	allocNodeID := func() int64 {
		return atomic.AddInt64(&nextNodeID, 1)
	}

	totalBytes, totalNodes, warnings, err := s.scanNode(ctx, s.root, "", 0, false, nil, sem, allocNodeID, cb)
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
	sem chan struct{},
	allocNodeID func() int64,
	emit NodeCallback,
) (int64, int64, int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, 0, err
	}

	var info fs.FileInfo
	var err error
	if entry != nil {
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

		spawned := false
		if sem != nil {
			select {
			case sem <- struct{}{}:
				spawned = true
			default:
			}
		}

		if spawned {
			wg.Add(1)
			go func(p string, e fs.DirEntry) {
				defer wg.Done()
				defer func() { <-sem }()
				sz, n, w, childErr := s.scanNode(ctx, p, path, nodeID, true, e, sem, allocNodeID, emit)
				apply(sz, n, w, childErr)
			}(childPath, childEntry)
			continue
		}

		sz, n, w, childErr := s.scanNode(ctx, childPath, path, nodeID, true, childEntry, sem, allocNodeID, emit)
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
