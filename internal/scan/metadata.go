package scan

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

type Warning struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Message   string `json:"message"`
}

// ValidateExclusions accepts slash-separated root-relative globs. A pattern
// without a slash matches a basename at any depth; ** is not supported.
func ValidateExclusions(patterns []string) error {
	for _, pattern := range patterns {
		if pattern == "" || strings.Contains(pattern, "\\") || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "**") || filepath.IsAbs(pattern) {
			return fmt.Errorf("invalid exclusion pattern %q: use relative slash-separated globs", pattern)
		}
		for _, part := range strings.Split(pattern, "/") {
			if part == ".." || part == "." || part == "" {
				return fmt.Errorf("invalid exclusion pattern %q", pattern)
			}
		}
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("invalid exclusion pattern %q: %w", pattern, err)
		}
	}
	return nil
}

func (s *Scanner) SetExclusions(patterns []string) error {
	if err := ValidateExclusions(patterns); err != nil {
		return err
	}
	s.exclusions = append([]string(nil), patterns...)
	return nil
}

func (s *Scanner) isExcluded(filename string) bool {
	relative, _ := filepath.Rel(s.root, filename)
	relative = filepath.ToSlash(relative)
	for _, pattern := range s.exclusions {
		candidate := relative
		if !strings.Contains(pattern, "/") {
			candidate = path.Base(relative)
		}
		if matched, _ := path.Match(pattern, candidate); matched {
			return true
		}
	}
	return false
}

type directoryAllocation struct {
	bytes   int64
	unknown bool
}
type scanState struct {
	mu          sync.Mutex
	root        string
	warnings    []Warning
	incomplete  map[string]bool
	directories map[string]directoryAllocation
	identities  map[fileIdentity]struct{}
	excluded    int64
}

func newScanState(root string) *scanState {
	return &scanState{root: root, incomplete: make(map[string]bool), directories: make(map[string]directoryAllocation), identities: make(map[fileIdentity]struct{})}
}
func (s *scanState) warn(filename, operation string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnings = append(s.warnings, Warning{filename, operation, err.Error()})
	for current := filename; ; current = filepath.Dir(current) {
		s.incomplete[current] = true
		if current == s.root || filepath.Dir(current) == current {
			break
		}
	}
}
func (s *scanState) allocation(info fs.FileInfo) (*int64, bool) {
	bytes, identity, linked, supported := allocatedFile(info)
	if !supported {
		return nil, false
	}
	if linked {
		s.mu.Lock()
		if _, seen := s.identities[identity]; seen {
			bytes = 0
		} else {
			s.identities[identity] = struct{}{}
		}
		s.mu.Unlock()
	}
	return &bytes, linked
}
func (s *scanState) wrap(callback NodeCallback) NodeCallback {
	return func(node NodeRecord) error {
		s.mu.Lock()
		node.Incomplete = s.incomplete[node.Path]
		if node.Excluded {
			s.excluded++
			zero := int64(0)
			node.AllocatedBytes = &zero
		}
		if node.Kind == "dir" && !node.Excluded {
			total := s.directories[node.Path]
			if !total.unknown && allocationSupported() {
				value := total.bytes
				node.AllocatedBytes = &value
			}
			delete(s.directories, node.Path)
		}
		if node.ParentPath != "" {
			total := s.directories[node.ParentPath]
			if node.AllocatedBytes == nil {
				total.unknown = true
			} else {
				total.bytes += *node.AllocatedBytes
			}
			s.directories[node.ParentPath] = total
		}
		s.mu.Unlock()
		return callback(node)
	}
}
