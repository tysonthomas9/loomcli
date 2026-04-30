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
	// EnvWorkspace overrides the active workspace for one process.
	// Wins over the state cache. Set in shell to scope a session to a
	// specific workspace: `LOOM_WORKSPACE=MYWS loom agent list`.
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
// find a candidate workspace and the caller is not interactive (no TTY
// to prompt). Wrap-friendly so handlers can match via errors.Is.
var ErrNoActiveWorkspace = errors.New("no active workspace: set " + EnvWorkspace + " or run `loom workspace use <name>`")

// ResolveActiveWorkspaceKey returns the loom workspace key the current
// invocation should operate on.
//
// Resolution priority:
//  1. LOOM_WORKSPACE env var (highest — scopes a single shell)
//  2. StateCache.LastWorkspace (set by `loom workspace use <name>`)
//  3. ErrNoActiveWorkspace
//
// When ws != nil the resolved key is validated against the store (so a
// stale state cache pointing at a deleted workspace returns ErrNotFound
// instead of silently routing to a missing key). Pass nil to skip
// validation — useful for offline `loom workspace list` style commands
// where the resolved key isn't actually used for I/O.
func ResolveActiveWorkspaceKey(ctx context.Context, ws store.WorkspaceStore) (string, error) {
	key := os.Getenv(EnvWorkspace)
	if key == "" {
		sc, err := LoadStateCache()
		if err != nil {
			return "", fmt.Errorf("resolve active workspace: %w", err)
		}
		key = sc.LastWorkspace
	}
	if key == "" {
		return "", ErrNoActiveWorkspace
	}
	if ws == nil {
		return key, nil
	}
	if _, err := ws.Get(ctx, key); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", fmt.Errorf("active workspace %q not found in fleet-db (clear stale state with `loom workspace use <other>`): %w", key, err)
		}
		return "", fmt.Errorf("validate active workspace %q: %w", key, err)
	}
	return key, nil
}

// SetActiveWorkspaceKey persists the chosen workspace as the new
// LastWorkspace in the state cache. Used by `loom workspace use <name>`.
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
