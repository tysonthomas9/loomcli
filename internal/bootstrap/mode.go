package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Env vars consumed by bootstrap. Defined as constants so callers can
// reference them in error messages and docs.
const (
	// EnvWorkspace selects the active workspace for one process. Set in
	// shell to scope a session to a specific workspace:
	// `LOOM_WORKSPACE=MYWS loom agent list`.
	EnvWorkspace = "LOOM_WORKSPACE"

	// EnvFleetDBURL switches loom into cloud mode — the embedded
	// fleet-db is not started; loom talks to the URL directly.
	EnvFleetDBURL = "LOOM_FLEET_DB_URL"
)

// Mode is the deployment shape detected at bootstrap.
type Mode int

const (
	// ModeLocal is single-user mode with an embedded fleet-db
	// auto-started by `loom serve`. State persists under LoomDir().
	ModeLocal Mode = iota

	// ModeCloud is multi-user mode with an external fleet-db pointed
	// at by LOOM_FLEET_DB_URL. Loom is purely a client.
	ModeCloud
)

// String renders the mode for logging.
func (m Mode) String() string {
	switch m {
	case ModeLocal:
		return "local"
	case ModeCloud:
		return "cloud"
	default:
		return fmt.Sprintf("mode(%d)", int(m))
	}
}

// DetectMode returns Cloud when LOOM_FLEET_DB_URL is set, Local otherwise.
// This is the only place that distinction is made — every other piece of
// code threads the Mode through.
func DetectMode() Mode {
	if os.Getenv(EnvFleetDBURL) != "" {
		return ModeCloud
	}
	return ModeLocal
}

// ErrNoActiveWorkspace is returned when ResolveActiveWorkspaceKey can't
// find an explicit workspace. Wrap-friendly so handlers can match via
// errors.Is.
var ErrNoActiveWorkspace = errors.New("no active workspace: set " + EnvWorkspace + " or pass --workspace")

// ResolveActiveWorkspaceKey returns the explicit loom workspace key the
// current invocation should operate on.
//
// Resolution priority:
//  1. LOOM_WORKSPACE env var
//  2. ErrNoActiveWorkspace
//
// When ws != nil the resolved key is validated against the store (so a
// stale env value returns ErrNotFound instead of silently routing to a
// missing key). Pass nil to skip validation.
func ResolveActiveWorkspaceKey(ctx context.Context, ws store.WorkspaceStore) (string, error) {
	key := os.Getenv(EnvWorkspace)
	if key == "" {
		return "", ErrNoActiveWorkspace
	}
	if ws == nil {
		return key, nil
	}
	if _, err := ws.Get(ctx, key); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", fmt.Errorf("active workspace %q not found in fleet-db: %w", key, err)
		}
		return "", fmt.Errorf("validate active workspace %q: %w", key, err)
	}
	return key, nil
}

// SetActiveWorkspaceKey persists the chosen workspace as the new
// LastWorkspace UI hint in the state cache. Used by
// `loom workspace use <name>`.
//
// Caller is responsible for ensuring the key exists in fleet-db before
// calling this — SetActiveWorkspaceKey does NOT validate. This keeps
// the function usable inside a single WithStateLock block alongside
// other cache mutations.
func SetActiveWorkspaceKey(key string) error {
	if key == "" {
		return errors.New("set active workspace: key must not be empty")
	}
	return WithStateLock(func() error {
		sc, err := LoadStateCache()
		if err != nil {
			return err
		}
		sc.LastWorkspace = key
		return SaveStateCache(sc)
	})
}

// ClearActiveWorkspaceKey removes the per-user selected-workspace hint.
func ClearActiveWorkspaceKey() error {
	return WithStateLock(func() error {
		sc, err := LoadStateCache()
		if err != nil {
			return err
		}
		sc.LastWorkspace = ""
		return SaveStateCache(sc)
	})
}
