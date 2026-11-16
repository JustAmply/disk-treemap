package app

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/justamply/disk-treemap/internal/store"
)

func (s *Service) ListSnapshots(ctx context.Context) ([]store.ScanRun, error) {
	return s.store.ListSnapshots(ctx)
}

type SnapshotDiff struct {
	Items      []store.SnapshotChange `json:"items"`
	HasMore    bool                   `json:"has_more"`
	NextOffset int                    `json:"next_offset"`
}

func (s *Service) CompareSnapshots(ctx context.Context, baseID, scanID int64, request FolderViewRequest) (SnapshotDiff, error) {
	req, err := s.folders.normalize(request, 1000, 100)
	if err != nil {
		return SnapshotDiff{}, err
	}
	for _, id := range []int64{baseID, scanID} {
		run, err := s.store.GetScanRun(ctx, id)
		if err != nil {
			return SnapshotDiff{}, err
		}
		if run.Status != store.ScanCompleted || filepath.Clean(run.RootPath) != filepath.Clean(s.cfg.AnalyzeRoot) {
			return SnapshotDiff{}, fmt.Errorf("%w: comparison requires completed snapshots of the configured root", ErrInvalidInput)
		}
	}
	items, err := s.store.CompareSnapshots(ctx, baseID, scanID, req.Path, req.Limit+1, req.Offset)
	if err != nil {
		return SnapshotDiff{}, err
	}
	more := len(items) > req.Limit
	if more {
		items = items[:req.Limit]
	}
	return SnapshotDiff{Items: items, HasMore: more, NextOffset: req.Offset + len(items)}, nil
}

type ExtensionSummary struct {
	Extension  string `json:"extension"`
	Count      int64  `json:"count"`
	TotalBytes int64  `json:"total_bytes"`
}

func (s *Service) SummarizeExtensions(ctx context.Context, scanID int64, request FolderViewRequest) ([]ExtensionSummary, error) {
	req, err := s.folders.normalize(request, 1000, 100)
	if err != nil {
		return nil, err
	}
	req.Kind = "file"
	groups := map[string]*ExtensionSummary{}
	err = s.store.VisitNodes(ctx, scanID, req.Path, true, req.storeOptions(), func(n store.Node) error {
		ext := strings.ToLower(filepath.Ext(n.Name))
		g := groups[ext]
		if g == nil {
			g = &ExtensionSummary{Extension: ext}
			groups[ext] = g
		}
		g.Count++
		g.TotalBytes += n.SizeBytes
		return nil
	})
	if err != nil {
		return nil, err
	}
	items := make([]ExtensionSummary, 0, len(groups))
	for _, g := range groups {
		items = append(items, *g)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].TotalBytes != items[j].TotalBytes {
			return items[i].TotalBytes > items[j].TotalBytes
		}
		return items[i].Extension < items[j].Extension
	})
	return items, nil
}

func (s *Service) ExportNodes(ctx context.Context, scanID int64, request FolderViewRequest, recursive bool, visit func(store.Node) error) (store.ScanRun, error) {
	req, err := s.folders.normalize(request, 1000, 100)
	if err != nil {
		return store.ScanRun{}, err
	}
	run, err := s.store.GetScanRun(ctx, scanID)
	if err != nil {
		return store.ScanRun{}, err
	}
	if run.Status != store.ScanCompleted {
		return store.ScanRun{}, fmt.Errorf("%w: export requires a completed snapshot", ErrInvalidInput)
	}
	err = s.store.VisitNodes(ctx, scanID, req.Path, recursive, req.storeOptions(), visit)
	return run, err
}

func (s *Service) GetScanDiagnostics(ctx context.Context, scanID int64) (store.ScanDiagnostics, error) {
	return s.store.GetScanDiagnostics(ctx, scanID)
}
