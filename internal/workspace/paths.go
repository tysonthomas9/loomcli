package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrMissingWorkspaceID is returned by the central path resolvers when the
// caller has not supplied a workspace UUID. There is no implicit fallback —
// data would land in the wrong bucket silently, which is what the central
// layout exists to prevent.
var ErrMissingWorkspaceID = errors.New("workspace ID is required")

// CentralSessionsDir returns ~/.loom/sessions/<wsID>/. Returns
// ErrMissingWorkspaceID on empty wsID; the directory itself is created by
// the caller (sessions.NewStoreForWorkspace).
func CentralSessionsDir(wsID string) (string, error) {
	return centralKindDir("sessions", wsID)
}

// CentralUsageDir returns ~/.loom/usage/<wsID>/.
func CentralUsageDir(wsID string) (string, error) {
	return centralKindDir("usage", wsID)
}

func centralKindDir(kind, wsID string) (string, error) {
	if wsID == "" {
		return "", ErrMissingWorkspaceID
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".loom", kind, wsID), nil
}
