//go:build linux

package scan

import (
	"io/fs"
	"syscall"
)

type fileIdentity struct {
	device uint64
	inode  uint64
}

func allocationSupported() bool { return true }
func allocatedFile(info fs.FileInfo) (int64, fileIdentity, bool, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fileIdentity{}, false, false
	}
	return stat.Blocks * 512, fileIdentity{uint64(stat.Dev), stat.Ino}, stat.Nlink > 1, true
}
