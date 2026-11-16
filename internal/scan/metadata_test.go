package scan

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestExclusionsSkipSubtreesButRemainVisible(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested", "cache"), 0755); err != nil {
		t.Fatal(err)
	}
	writeSizedFile(t, filepath.Join(root, "nested", "cache", "large.bin"), 500)
	writeSizedFile(t, filepath.Join(root, "nested", "keep.bin"), 10)
	writeSizedFile(t, filepath.Join(root, "temp.tmp"), 30)
	scanner := New(root, 4)
	if err := scanner.SetExclusions([]string{"cache", "*.tmp"}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	nodes := map[string]NodeRecord{}
	result, err := scanner.Scan(context.Background(), func(n NodeRecord) error { mu.Lock(); defer mu.Unlock(); nodes[n.Path] = n; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalBytes != 10 || result.ExcludedCount != 2 {
		t.Fatalf("unexpected totals: %+v", result)
	}
	if !nodes[filepath.Join(root, "nested", "cache")].Excluded {
		t.Fatal("excluded folder missing marker")
	}
	if _, ok := nodes[filepath.Join(root, "nested", "cache", "large.bin")]; ok {
		t.Fatal("traversed excluded folder")
	}
}
func TestInvalidExclusions(t *testing.T) {
	for _, pattern := range []string{"../private", "/absolute", "a/../b", "**/cache", "[", "a\\b", ""} {
		if err := ValidateExclusions([]string{pattern}); err == nil {
			t.Errorf("accepted %q", pattern)
		}
	}
}
func TestWarningsMarkAncestorsIncomplete(t *testing.T) {
	root := t.TempDir()
	state := newScanState(root)
	filename := filepath.Join(root, "missing")
	state.warn(filename, "stat", fs.ErrPermission)
	var recorded NodeRecord
	if err := state.wrap(func(n NodeRecord) error { recorded = n; return nil })(NodeRecord{Path: root, Kind: "dir"}); err != nil {
		t.Fatal(err)
	}
	if !recorded.Incomplete || len(state.warnings) != 1 || state.warnings[0].Path != filename {
		t.Fatalf("missing warning/incomplete marker: %+v %+v", recorded, state.warnings)
	}
}
func TestMissingEntryRecordsWarning(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "missing")
	state := newScanState(root)
	_, _, warnings, err := New(root, 1).scanNode(context.Background(), filename, root, nil, nil, nil, nil, func(NodeRecord) error { return nil }, state)
	if err != nil || warnings != 1 || len(state.warnings) != 1 || state.warnings[0].Operation != "stat" {
		t.Fatalf("warnings=%d err=%v details=%+v", warnings, err, state.warnings)
	}
}
