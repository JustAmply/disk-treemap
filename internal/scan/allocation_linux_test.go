//go:build linux

package scan

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSparseAllocationAndHardlinks(t *testing.T) {
	root := t.TempDir()
	filename := filepath.Join(root, "sparse.bin")
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(8 << 20); err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt([]byte("data"), 0); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(filename, filepath.Join(root, "alias.bin")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	nodes := map[string]NodeRecord{}
	result, err := New(root, 4).Scan(context.Background(), func(n NodeRecord) error { mu.Lock(); defer mu.Unlock(); nodes[n.Path] = n; return nil })
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filename)
	if err != nil {
		t.Fatal(err)
	}
	allocated, _, _, _ := allocatedFile(info)
	if nodes[root].AllocatedBytes == nil || *nodes[root].AllocatedBytes != allocated {
		t.Fatalf("root allocation not deduplicated: %+v expected %d", nodes[root], allocated)
	}
	if allocated >= info.Size() {
		t.Fatal("sparse file allocation unexpectedly equals logical length")
	}
	if result.TotalBytes != 2*info.Size() {
		t.Fatal("logical size should retain both entries")
	}
	for _, name := range []string{"sparse.bin", "alias.bin"} {
		if !nodes[filepath.Join(root, name)].Hardlink {
			t.Fatal("missing hardlink marker")
		}
	}
}
