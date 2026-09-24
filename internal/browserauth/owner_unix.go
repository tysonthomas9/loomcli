//go:build !windows

package browserauth

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether info belongs to the calling user.
func ownedByCurrentUser(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
