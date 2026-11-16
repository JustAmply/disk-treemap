//go:build !linux

package scan

import "io/fs"

type fileIdentity struct{}

func allocationSupported() bool { return false }
func allocatedFile(info fs.FileInfo) (int64, fileIdentity, bool, bool) {
	return 0, fileIdentity{}, false, false
}
