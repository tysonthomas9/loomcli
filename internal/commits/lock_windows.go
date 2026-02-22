//go:build windows

package commits

import "os"

// On Windows, file locking is not trivially available via syscall.Flock.
// The append-only nature of the file and short write duration make
// corruption extremely unlikely. Accept the small risk for simplicity.
func lockFile(f *os.File) error {
	_ = f
	return nil
}

func unlockFile(f *os.File) error {
	_ = f
	return nil
}
