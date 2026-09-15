//go:build unix

package doctor

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// defaultDiskUsage reports free and total bytes for the filesystem holding
// path. x/sys/unix hides the darwin/linux Statfs_t field-type divergence that
// bare syscall would need build tags for.
func defaultDiskUsage(path string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	// Bsize is int32 on darwin and int64 on linux; Bavail/Blocks are uint64
	// on both.
	bsize := uint64(st.Bsize)
	return st.Bavail * bsize, st.Blocks * bsize, nil
}
