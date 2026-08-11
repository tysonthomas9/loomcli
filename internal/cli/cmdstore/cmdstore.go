// Package cmdstore holds shared helpers for CLI commands that operate
// on the fleet-db-backed store (workspace/repo/agent/role/daemon CRUD).
//
// Helpers return errors so cobra commands can use RunE — that lets the
// framework own exit codes, SilenceErrors, SilenceUsage, and stdout
// flushing instead of each handler calling os.Exit directly.
package cmdstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// OpenStore opens a fleet-db-backed Store appropriate for the current
// Mode. Returns the handle so callers can `defer h.Close()`.
func OpenStore(ctx context.Context) (*bootstrap.StoreHandle, error) {
	return OpenStoreWithCapabilities(ctx, nil)
}

// OpenStoreWithCapabilities opens the shared FleetDB-backed Store after
// verifying the capability keys required by the caller's enabled slices. The
// caller owns this set; passing no keys preserves the pre-negotiation path.
func OpenStoreWithCapabilities(ctx context.Context, required []string) (*bootstrap.StoreHandle, error) {
	ensureFleetDBEnvFromFleetEnv()
	dataDir := bootstrap.LoomDir()
	if dataDir == "" {
		return nil, errors.New("cannot resolve loom directory (set HOME or LOOM_CONFIG_DIR)")
	}
	handle, err := bootstrap.OpenStoreWithOptions(ctx, dataDir, nil, bootstrap.OpenStoreOptions{
		RequiredFleetDBCapabilities: required,
	})
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	return handle, nil
}

func ensureFleetDBEnvFromFleetEnv() {
	if os.Getenv(bootstrap.EnvFleetDBURL) == "" {
		if v := os.Getenv("LOOM_FLEET_URL"); v != "" {
			_ = os.Setenv(bootstrap.EnvFleetDBURL, v)
		}
	}
	if os.Getenv(bootstrap.EnvFleetDBActor) == "" {
		if v := os.Getenv("LOOM_FLEET_ACTOR"); v != "" {
			_ = os.Setenv(bootstrap.EnvFleetDBActor, v)
		}
	}
}

// SignalContext returns a context cancelled on SIGINT/SIGTERM. CLI
// commands use this so Ctrl+C propagates cleanly to fleet-db RPCs and
// the embedded subprocess shutdown. The caller supplies the command context,
// preserving trace and cancellation ancestry without process-global state.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// ActiveWorkspace resolves the explicit active workspace key
// (LOOM_WORKSPACE / --workspace) and verifies it exists in the store.
// It intentionally does not consult state.json's last_workspace because
// runtime commands must not silently cross workspace boundaries.
func ActiveWorkspace(ctx context.Context, s store.Store) (string, error) {
	return bootstrap.ResolveActiveWorkspaceKey(ctx, s.Workspaces())
}

// IsNotFound is a convenience wrapper for matching domain.ErrNotFound,
// the only sentinel callers reliably need to check (e.g., to print a
// nicer "not found" message instead of the wrapped error chain).
func IsNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }

// WriteJSON encodes v as indented JSON to stdout. Returns the encode
// error so RunE handlers can propagate it; in practice an encode
// failure means stdout is broken and the framework will surface
// whatever it can.
func WriteJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	return nil
}

// WithStore opens a Store, runs fn, then closes the handle. Builds the
// signal-aware context internally. Use from RunE handlers that need
// the store but don't care about the active workspace.
func WithStore(parent context.Context, fn func(ctx context.Context, h *bootstrap.StoreHandle) error) error {
	ctx, cancel := SignalContext(parent)
	defer cancel()
	h, err := OpenStore(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()
	return fn(ctx, h)
}

// WithActiveWorkspace opens a Store, resolves the explicit active
// workspace key, and runs fn with both.
func WithActiveWorkspace(parent context.Context, fn func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error) error {
	return WithStore(parent, func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, err := ActiveWorkspace(ctx, h.Store)
		if err != nil {
			return err
		}
		return fn(ctx, h, ws)
	})
}
