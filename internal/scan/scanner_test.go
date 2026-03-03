package scan

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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

func writeSizedFile(t *testing.T, path string, size int) {
	t.Helper()
	data := make([]byte, size)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}