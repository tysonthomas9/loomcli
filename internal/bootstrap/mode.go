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

	// EnvServerURL points the issue-data path at a remote loom server
	// (see internal/cli/issue_backend_resolve.go). It does NOT configure
	// workspace/repo/agent metadata, which is read straight from
	// fleet-db — so on its own it is a misconfiguration for any command
	// that opens a store. See ServerWithoutFleetDB. Mirrored from --server.
	EnvServerURL = "LOOM_SERVER_URL"
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
//
// LOOM_SERVER_URL deliberately does not produce a third mode: the runtime
// storage paths are Local and Cloud, and anything else is a sub-option behind
// them (enforced by `make check-control-plane-paths`). It is still not a
// storage configuration — see ServerWithoutFleetDB, which OpenStore consults
// before opening a local store.
func DetectMode() Mode {
	if os.Getenv(EnvFleetDBURL) != "" {
		return ModeCloud
	}
	return ModeLocal
}

// ServerWithoutFleetDB reports the misconfiguration where the invocation is
// aimed at a remote loom server but names no fleet-db.
//
// Issue data resolves over the server's HTTP API, but workspace, repo and agent
// metadata do not — they are read directly from fleet-db. Opening a local store
// here starts an embedded fleet-db on an empty snapshot, and the active
// workspace then fails validation as if it did not exist.
func ServerWithoutFleetDB() bool {
	return os.Getenv(EnvFleetDBURL) == "" && os.Getenv(EnvServerURL) != ""
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
// calling this — SetActiveWorkspaceKey does NOT validate. It acquires
// the state lock itself (via MutateStateCache), so it must NOT be
// called from inside a WithStateLock block.
func SetActiveWorkspaceKey(key string) error {
	if key == "" {
		return errors.New("set active workspace: key must not be empty")
	}
	return MutateStateCache(func(sc *StateCache) error {
		sc.LastWorkspace = key
		return nil
	})
}

// ClearActiveWorkspaceKey removes the per-user selected-workspace hint.
func ClearActiveWorkspaceKey() error {
	return MutateStateCache(func(sc *StateCache) error {
		sc.LastWorkspace = ""
		return nil
	})
}
