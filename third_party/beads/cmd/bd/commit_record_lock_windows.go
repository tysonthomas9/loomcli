//go:build windows

package main

import "os"

func lockCommitFile(f *os.File) error {
	_ = f
	return nil
}

func unlockCommitFile(f *os.File) {
	_ = f
}
