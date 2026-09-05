//go:build linux

package skillmat

import "golang.org/x/sys/unix"

func ensureAtomicProjectionSupported() error { return nil }

func swapAt(firstFD int, first string, secondFD int, second string) error {
	return unix.Renameat2(firstFD, first, secondFD, second, unix.RENAME_EXCHANGE)
}
