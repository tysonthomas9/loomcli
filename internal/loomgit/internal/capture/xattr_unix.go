//go:build darwin || linux

package capture

import (
	"strings"

	"golang.org/x/sys/unix"
)

func splitXattrNames(names []byte) []string {
	return strings.Split(strings.TrimRight(string(names), "\x00"), "\x00")
}

func countXattrs(path string) (int, error) {
	count, err := unix.Llistxattr(path, nil)
	if err == unix.ENOTSUP || err == unix.EOPNOTSUPP {
		return 0, nil
	}
	if err != nil || count == 0 {
		return count, err
	}
	names := make([]byte, count)
	_, err = unix.Llistxattr(path, names)
	if err != nil {
		return 0, err
	}
	for _, name := range splitXattrNames(names) {
		if !ambientXattr(name) {
			return 1, nil
		}
	}
	return 0, nil
}

func ambientXattr(name string) bool {
	// Host provenance and container SELinux labels are recreated by the platform.
	// security.capability, POSIX ACLs, and com.apple.quarantine can change
	// execution or access decisions, so they remain uncapturable.
	return name == "com.apple.provenance" || name == "security.selinux"
}
