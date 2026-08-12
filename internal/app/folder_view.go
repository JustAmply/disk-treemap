package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/justamply/disk-treemap/internal/pathutil"
	"github.com/justamply/disk-treemap/internal/store"
)

const (
	folderViewExpandedDirLimit = 12
	folderViewBranchLimit      = 14
	folderViewDescendantBudget = 240
	folderViewSyntheticMinByte = 1
	largestDefaultLimit        = 100
	largestMaxLimit            = 1000
)

type FolderViewRequest struct {
	Path    string
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

type FolderViewSummary struct {
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

type FolderViewTreemapNode struct {
	Name            string                  `json:"name"`
	Path            string                  `json:"path,omitempty"`
	Type            string                  `json:"type"`
	SizeBytes       int64                   `json:"size_bytes"`
	Clickable       bool                    `json:"clickable"`
	Synthetic       bool                    `json:"synthetic,omitempty"`
	HiddenItemCount int64                   `json:"hidden_item_count,omitempty"`
	Children        []FolderViewTreemapNode `json:"children,omitempty"`
}

type FolderViewResponse struct {
	ScanID  int64                 `json:"scan_id"`
	Path    string                `json:"path"`
	Summary FolderViewSummary     `json:"summary"`
	Items   []store.Node          `json:"items"`
	Treemap FolderViewTreemapNode `json:"treemap"`
}

type folderView struct {
	rootPath    string
	maxChildren int
	store       *store.Store
}

func newFolderView(rootPath string, maxChildren int, st *store.Store) *folderView {
	if maxChildren < 1 {
		maxChildren = 1
	}
	return &folderView{
		rootPath:    rootPath,
		maxChildren: maxChildren,
		store:       st,
	}
}

func (v *folderView) children(ctx context.Context, scanID int64, request FolderViewRequest) (ChildrenResponse, error) {
	normalized, err := v.normalize(request, v.maxChildren, v.maxChildren)
	if err != nil {
		return ChildrenResponse{}, err
	}

	node, err := v.store.GetNode(ctx, scanID, normalized.Path)
	if err != nil {
		return ChildrenResponse{}, err
	}
	children, err := v.store.ListChildrenWithOptions(ctx, scanID, normalized.Path, normalized.storeOptions())
	if err != nil {
		return ChildrenResponse{}, err
	}

	return ChildrenResponse{
		ScanID:     scanID,
		Path:       normalized.Path,
		TotalBytes: node.SizeBytes,
		Children:   children,
	}, nil
}

func (v *folderView) largest(ctx context.Context, scanID int64, request FolderViewRequest) (LargestResponse, error) {
	normalized, err := v.normalize(request, largestMaxLimit, largestDefaultLimit)
	if err != nil {
		return LargestResponse{}, err
	}

	items, err := v.store.ListLargestInPathWithOptions(ctx, scanID, normalized.Path, normalized.storeOptions())
	if err != nil {
		return LargestResponse{}, err
	}

	return LargestResponse{
		ScanID: scanID,
		Path:   normalized.Path,
		Items:  items,
	}, nil
}

func (v *folderView) read(ctx context.Context, scanID int64, request FolderViewRequest) (FolderViewResponse, error) {
	normalized, err := v.normalize(request, v.maxChildren, v.maxChildren)
	if err != nil {
		return FolderViewResponse{}, err
	}

	currentNode, err := v.store.GetNode(ctx, scanID, normalized.Path)
	if err != nil {
		return FolderViewResponse{}, err
	}
	items, err := v.store.ListChildrenWithOptions(ctx, scanID, normalized.Path, normalized.storeOptions())
	if err != nil {
		return FolderViewResponse{}, err
	}
	aggregate, err := v.store.AggregateChildrenWithOptions(ctx, scanID, normalized.Path, normalized.storeOptionsWithoutLimit())
	if err != nil {
		return FolderViewResponse{}, err
	}

	summary := summarizeFolderView(currentNode, items, aggregate, normalized)
	treemap, err := v.buildTreemap(ctx, scanID, currentNode, items, aggregate, summary.HasActiveFilters)
	if err != nil {
		return FolderViewResponse{}, err
	}

	return FolderViewResponse{
		ScanID:  scanID,
		Path:    normalized.Path,
		Summary: summary,
		Items:   items,
		Treemap: treemap,
	}, nil
}

func (v *folderView) normalize(request FolderViewRequest, maxLimit, defaultLimit int) (FolderViewRequest, error) {
	path, err := pathutil.NormalizeWithinRoot(v.rootPath, request.Path)
	if err != nil {
		return FolderViewRequest{}, err
	}
	request.Path = path
	request.Query = strings.TrimSpace(request.Query)
	request.Kind = strings.TrimSpace(request.Kind)
	request.Sort = strings.TrimSpace(request.Sort)

	if request.Limit <= 0 {
		request.Limit = defaultLimit
	}
	if request.Limit > maxLimit {
		request.Limit = maxLimit
	}

	switch request.Kind {
	case "", "file", "dir":
	default:
		return FolderViewRequest{}, fmt.Errorf("%w: unsupported type filter %q", ErrInvalidInput, request.Kind)
	}

	switch request.Sort {
	case "", "size_desc", "size_asc", "name_asc", "name_desc":
	default:
		return FolderViewRequest{}, fmt.Errorf("%w: unsupported sort %q", ErrInvalidInput, request.Sort)
	}
	if request.Sort == "" {
		request.Sort = "size_desc"
	}
	if request.MinSize < 0 {
		return FolderViewRequest{}, fmt.Errorf("%w: min_size must be >= 0", ErrInvalidInput)
	}
	return request, nil
}

func (request FolderViewRequest) storeOptions() store.NodeQueryOptions {
	return store.NodeQueryOptions{
		Limit:   request.Limit,
		Query:   request.Query,
		Kind:    request.Kind,
		MinSize: request.MinSize,
		Sort:    request.Sort,
	}
}

func (request FolderViewRequest) storeOptionsWithoutLimit() store.NodeQueryOptions {
	options := request.storeOptions()
	options.Limit = 0
	return options
}

func summarizeFolderView(current store.Node, items []store.Node, aggregate store.ChildAggregate, request FolderViewRequest) FolderViewSummary {
	summary := FolderViewSummary{
		Name:              current.Name,
		TotalBytes:        current.SizeBytes,
		VisibleBytes:      aggregate.TotalBytes,
		MatchingItemCount: aggregate.Count,
		ReturnedItemCount: len(items),
		HiddenItemCount:   maxInt64(aggregate.Count-int64(len(items)), 0),
		HasActiveFilters:  request.Query != "" || request.Kind != "" || request.MinSize > 0,
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
	return summary
}

func (v *folderView) buildTreemap(
	ctx context.Context,
	scanID int64,
	rootNode store.Node,
	items []store.Node,
	aggregate store.ChildAggregate,
	hasFilters bool,
) (FolderViewTreemapNode, error) {
	root := toFolderViewTreemapNode(rootNode)
	root.Clickable = false
	visibleItemLimit := minInt(len(items), folderViewDescendantBudget)
	if aggregate.Count > int64(visibleItemLimit) && visibleItemLimit == folderViewDescendantBudget {
		visibleItemLimit--
	}
	root.Children = make([]FolderViewTreemapNode, 0, visibleItemLimit+1)

	var renderedBytes int64
	for _, item := range items[:visibleItemLimit] {
		root.Children = append(root.Children, toFolderViewTreemapNode(item))
		renderedBytes += item.SizeBytes
	}

	hiddenRootItems := aggregate.Count - int64(visibleItemLimit)
	hiddenRootBytes := aggregate.TotalBytes - renderedBytes
	if hiddenRootBytes < 0 {
		hiddenRootBytes = 0
	}
	if hiddenRootItems > 0 {
		root.Children = append(root.Children, buildSyntheticFolderViewNode(hiddenRootItems, hiddenRootBytes))
	}

	if hasFilters || len(root.Children) == 0 {
		return root, nil
	}

	remainingBudget := folderViewDescendantBudget - len(root.Children)
	if remainingBudget <= 1 {
		return root, nil
	}

	expandedDirs := 0
	for idx := range root.Children {
		child := &root.Children[idx]
		if child.Type != "dir" || child.Synthetic {
			continue
		}
		if expandedDirs >= folderViewExpandedDirLimit || remainingBudget <= 1 {
			break
		}

		aggregate, err := v.store.AggregateChildrenWithOptions(ctx, scanID, child.Path, store.NodeQueryOptions{})
		if err != nil {
			return FolderViewTreemapNode{}, err
		}
		if aggregate.Count == 0 {
			continue
		}

		branchLimit := minInt(folderViewBranchLimit, remainingBudget)
		if aggregate.Count > int64(branchLimit) && branchLimit == remainingBudget {
			branchLimit--
		}
		if branchLimit < 1 {
			break
		}
		grandchildren, err := v.store.ListChildrenWithOptions(ctx, scanID, child.Path, store.NodeQueryOptions{
			Limit: branchLimit,
			Sort:  "size_desc",
		})
		if err != nil {
			return FolderViewTreemapNode{}, err
		}
		if len(grandchildren) == 0 {
			continue
		}

		child.Children = make([]FolderViewTreemapNode, 0, len(grandchildren)+1)
		var renderedGrandchildBytes int64
		for _, grandchild := range grandchildren {
			child.Children = append(child.Children, toFolderViewTreemapNode(grandchild))
			renderedGrandchildBytes += grandchild.SizeBytes
		}

		hiddenGrandchildren := aggregate.Count - int64(len(grandchildren))
		hiddenGrandchildBytes := child.SizeBytes - renderedGrandchildBytes
		if hiddenGrandchildBytes < 0 {
			hiddenGrandchildBytes = 0
		}
		if hiddenGrandchildren > 0 {
			child.Children = append(child.Children, buildSyntheticFolderViewNode(hiddenGrandchildren, hiddenGrandchildBytes))
		}

		expandedDirs++
		remainingBudget -= len(child.Children)
	}

	return root, nil
}

func toFolderViewTreemapNode(node store.Node) FolderViewTreemapNode {
	return FolderViewTreemapNode{
		Name:      node.Name,
		Path:      node.Path,
		Type:      node.Kind,
		SizeBytes: node.SizeBytes,
		Clickable: node.Kind == "dir",
	}
}

func buildSyntheticFolderViewNode(hiddenCount, hiddenBytes int64) FolderViewTreemapNode {
	if hiddenBytes < folderViewSyntheticMinByte {
		hiddenBytes = folderViewSyntheticMinByte
	}

	label := "remaining item"
	if hiddenCount != 1 {
		label = "remaining items"
	}

	return FolderViewTreemapNode{
		Name:            fmt.Sprintf("%d %s", hiddenCount, label),
		Type:            "group",
		SizeBytes:       hiddenBytes,
		Clickable:       false,
		Synthetic:       true,
		HiddenItemCount: hiddenCount,
	}
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
