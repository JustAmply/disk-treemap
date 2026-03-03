package pathutil

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestNormalizeWithinRoot_DefaultRoot(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	got, err := NormalizeWithinRoot(root, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != root {
		t.Fatalf("expected %q, got %q", root, got)
	}
}

func TestNormalizeWithinRoot_RejectsRelativePath(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	_, err := NormalizeWithinRoot(root, "relative/path")
	if !errors.Is(err, ErrPathNotAbsolute) {
		t.Fatalf("expected ErrPathNotAbsolute, got %v", err)
	}
}

func TestNormalizeWithinRoot_RejectsOutsideRoot(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	outside := filepath.Clean(filepath.Join(root, ".."))
	_, err := NormalizeWithinRoot(root, outside)
	if !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("expected ErrPathOutsideRoot, got %v", err)
	}
}