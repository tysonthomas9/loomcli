//go:build windows

package browserauth

import "io/fs"

// ownedByCurrentUser is not enforced on Windows, where the local desktop
// operator bridge (Unix socket + peer credentials) is unsupported.
func ownedByCurrentUser(fs.FileInfo) bool { return true }
