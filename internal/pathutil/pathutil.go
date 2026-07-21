package pathutil

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

var (
	ErrRootNotAbsolute = errors.New("root path must be absolute")
	ErrPathNotAbsolute = errors.New("requested path must be absolute")
	ErrPathOutsideRoot = errors.New("requested path outside root")
)

func NormalizeWithinRoot(root, requestedPath string) (string, error) {
	cleanRoot := filepath.Clean(root)
	if !filepath.IsAbs(cleanRoot) {
		return "", fmt.Errorf("%w: %q", ErrRootNotAbsolute, cleanRoot)
	}

	if requestedPath == "" {
		return cleanRoot, nil
	}
	if !filepath.IsAbs(requestedPath) {
		return "", fmt.Errorf("%w: %q", ErrPathNotAbsolute, requestedPath)
	}

	cleanRequested := filepath.Clean(requestedPath)
	rel, err := filepath.Rel(cleanRoot, cleanRequested)
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrPathOutsideRoot, requestedPath)
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrPathOutsideRoot, requestedPath)
	}

	return cleanRequested, nil
}
