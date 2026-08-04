//go:build windows || wasm

package misc

import (
	"os"
)

// OpenLogFileSecure falls back to os.Open on platforms where O_NOFOLLOW is not
// available. The existing validatePathWithinDir check still provides protection.
func OpenLogFileSecure(path, allowedDir string) (*os.File, error) {
	return os.Open(path)
}
