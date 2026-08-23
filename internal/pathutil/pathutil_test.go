package pathutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeWithinRoot_DefaultRoot(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	_, err := NormalizeWithinRoot(root, "relative/path")
	if !errors.Is(err, ErrPathNotAbsolute) {
		t.Fatalf("expected ErrPathNotAbsolute, got %v", err)
	}
}

func TestNormalizeWithinRoot_RejectsOutsideRoot(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	outside := filepath.Clean(filepath.Join(root, ".."))
	_, err := NormalizeWithinRoot(root, outside)
	if !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("expected ErrPathOutsideRoot, got %v", err)
	}
}

func TestNormalizeWithinRoot_AcceptsRootPrefixCaseVariation(t *testing.T) {
	t.Parallel()
	if os.PathSeparator != '\\' {
		t.Skip("drive-letter and path case folding is a Windows path behavior")
	}
	root := filepath.Clean(t.TempDir())
	volume := filepath.VolumeName(root)
	variant := strings.ToLower(volume) + strings.ToUpper(root[len(volume):])
	if variant == root {
		t.Fatalf("expected case variant of %q", root)
	}

	// Windows filesystems treat case variants of the same directory as
	// equal, so a differently-cased prefix must stay navigable.
	got, err := NormalizeWithinRoot(root, filepath.Join(variant, "child"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := filepath.Join(variant, "child"); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNormalizeWithinRoot_RejectsCaseVariantOfDifferentDirectory(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	variant := strings.ToLower(root) + "-sibling"
	_, err := NormalizeWithinRoot(root, variant)
	if !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("expected ErrPathOutsideRoot, got %v", err)
	}
}
