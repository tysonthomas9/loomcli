//go:build darwin

package skillmat

import "golang.org/x/sys/unix"

func ensureAtomicProjectionSupported() error { return nil }

func swapAt(firstFD int, first string, secondFD int, second string) error {
	return unix.RenameatxNp(firstFD, first, secondFD, second, unix.RENAME_SWAP)
}
