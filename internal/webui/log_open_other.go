//go:build windows || wasm

package webui

import (
	"os"
)

// openLogFileSecure falls back to os.Open on platforms where O_NOFOLLOW is not
// available. The existing validatePathWithinDir check still provides protection.
func openLogFileSecure(path, allowedDir string) (*os.File, error) {
	return os.Open(path)
}
