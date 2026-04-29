package scan

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScannerComputesDirectorySizesAndSkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSizedFile(t, filepath.Join(root, "dir", "a.bin"), 10)
	writeSizedFile(t, filepath.Join(root, "root.bin"), 20)

	symlinkSupported := true
	if err := os.Symlink(filepath.Join(root, "root.bin"), filepath.Join(root, "root.link")); err != nil {
		symlinkSupported = false
	}

	s := New(root, 4)
	nodes := make(map[string]NodeRecord)
	result, err := s.Scan(context.Background(), func(node NodeRecord) error {
		nodes[node.Path] = node
		return nil
	})
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if got := nodes[filepath.Join(root, "dir")].SizeBytes; got != 10 {
		t.Fatalf("expected dir size 10, got %d", got)
	}
	if got := nodes[root].SizeBytes; got != 30 {
		t.Fatalf("expected root size 30, got %d", got)
	}
	if result.TotalNodes != 4 {
		t.Fatalf("expected 4 nodes, got %d", result.TotalNodes)
	}
	if symlinkSupported {
		if _, ok := nodes[filepath.Join(root, "root.link")]; ok {
			t.Fatalf("expected symlink node to be skipped")
		}
	}
}

func TestScannerPermissionDeniedIsNonFatal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission behavior is not portable on windows")
	}

	root := t.TempDir()
	secret := filepath.Join(root, "secret")
	if err := os.Mkdir(secret, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSizedFile(t, filepath.Join(secret, "hidden.bin"), 16)

	if err := os.Chmod(secret, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() {
		_ = os.Chmod(secret, 0o700)
	}()

	s := New(root, 2)
	result, err := s.Scan(context.Background(), func(node NodeRecord) error { return nil })
	if err != nil {
		t.Fatalf("expected non-fatal permission handling, got %v", err)
	}
	if result.WarningCount == 0 {
		t.Skip("environment did not produce a permission warning")
	}
}

func TestScannerAllowsConcurrentCallbacks(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 64; i++ {
		writeSizedFile(t, filepath.Join(root, fmt.Sprintf("file-%d.bin", i)), 1)
	}

	var current int32
	var maxSeen int32

	s := New(root, 16)
	_, err := s.Scan(context.Background(), func(node NodeRecord) error {
		n := atomic.AddInt32(&current, 1)
		for {
			m := atomic.LoadInt32(&maxSeen)
			if n <= m {
				break
			}
			if atomic.CompareAndSwapInt32(&maxSeen, m, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt32(&current, -1)
		_ = node
		return nil
	})
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	if maxSeen < 2 {
		t.Fatalf("expected concurrent callbacks, max concurrency observed=%d", maxSeen)
	}
}

func TestScannerAllowsAdaptiveConcurrencyLimitChanges(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 24; i++ {
		writeSizedFile(t, filepath.Join(root, fmt.Sprintf("file-%d.bin", i)), 1)
	}

	s := New(root, 8)
	if got := s.SetConcurrencyLimit(1); got != 1 {
		t.Fatalf("expected initial limit 1, got %d", got)
	}

	firstCallback := make(chan struct{})
	releaseCallbacks := make(chan struct{})
	done := make(chan error, 1)
	var firstOnce sync.Once
	var current int32
	var maxSeen int32

	go func() {
		_, err := s.Scan(context.Background(), func(node NodeRecord) error {
			if node.Kind != "file" {
				return nil
			}
			firstOnce.Do(func() { close(firstCallback) })
			n := atomic.AddInt32(&current, 1)
			for {
				m := atomic.LoadInt32(&maxSeen)
				if n <= m {
					break
				}
				if atomic.CompareAndSwapInt32(&maxSeen, m, n) {
					break
				}
			}
			<-releaseCallbacks
			atomic.AddInt32(&current, -1)
			return nil
		})
		done <- err
	}()

	select {
	case <-firstCallback:
	case <-time.After(2 * time.Second):
		t.Fatalf("scan did not reach first callback")
	}

	if got := s.SetConcurrencyLimit(4); got != 4 {
		t.Fatalf("expected raised limit 4, got %d", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&maxSeen) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(releaseCallbacks)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("scan failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("scan did not finish")
	}

	if maxSeen < 2 {
		t.Fatalf("expected adaptive limit change to allow concurrent callbacks, max=%d", maxSeen)
	}
}

func TestScannerEmitsNodeAndParentIDs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "a.bin")
	writeSizedFile(t, file, 10)

	s := New(root, 2)
	var mu sync.Mutex
	nodes := make(map[string]NodeRecord)
	_, err := s.Scan(context.Background(), func(node NodeRecord) error {
		mu.Lock()
		defer mu.Unlock()
		nodes[node.Path] = node
		return nil
	})
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	rootNode := nodes[root]
	dirNode := nodes[dir]
	fileNode := nodes[file]
	if rootNode.NodeID == 0 || dirNode.NodeID == 0 || fileNode.NodeID == 0 {
		t.Fatalf("expected non-zero node ids: root=%d dir=%d file=%d", rootNode.NodeID, dirNode.NodeID, fileNode.NodeID)
	}
	if rootNode.ParentID != nil {
		t.Fatalf("expected root parent id to be nil, got %d", *rootNode.ParentID)
	}
	if dirNode.ParentID == nil || *dirNode.ParentID != rootNode.NodeID {
		t.Fatalf("expected dir parent id %d, got %+v", rootNode.NodeID, dirNode.ParentID)
	}
	if fileNode.ParentID == nil || *fileNode.ParentID != dirNode.NodeID {
		t.Fatalf("expected file parent id %d, got %+v", dirNode.NodeID, fileNode.ParentID)
	}
}

func writeSizedFile(t *testing.T, path string, size int) {
	t.Helper()
	data := make([]byte, size)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}
