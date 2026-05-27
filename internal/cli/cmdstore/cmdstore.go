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
	"github.com/tysonthomas9/loomcli/internal/runtimectx"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// OpenStore opens a fleet-db-backed Store appropriate for the current
// Mode. Returns the handle so callers can `defer h.Close()`.
func OpenStore(ctx context.Context) (*bootstrap.StoreHandle, error) {
	ensureFleetDBEnvFromFleetEnv()
	if os.Getenv("LOOM_SERVER_URL") != "" {
		return nil, errors.New("LOOM_SERVER_URL is set; direct FleetDB store commands are disabled in remote server mode")
	}
	dataDir := bootstrap.LoomDir()
	if dataDir == "" {
		return nil, errors.New("cannot resolve loom directory (set HOME or LOOM_CONFIG_DIR)")
	}
	handle, err := bootstrap.OpenStore(ctx, dataDir, nil)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	// Wrap the store so every method call emits a
	// `service.Store.<SubStore>.<Method>` span. nil-safe; no-op when
	// tracing is disabled. Lives on the cmdstore side because
	// internal/cli (where the spec asked for it) cannot be imported by
	// cmdstore without a cycle. See store_tracing.go.
	handle.Store = WrapStoreWithTracing(handle.Store)
	return handle, nil
}

func ensureFleetDBEnvFromFleetEnv() {
	// Legacy LOOM_FLEET_* variables are intentionally ignored. Store routing
	// must only use the canonical LOOM_FLEET_DB_* names; otherwise two call
	// paths can resolve different backing stores in the same shell.
}

// SetRootContext is a thin alias for runtimectx.SetRootContext kept so
// existing cli-layer callers don't need to migrate. The backing store
// lives in runtimectx so infra packages can read RootContext without
// importing cli/cmdstore.
func SetRootContext(ctx context.Context) {
	runtimectx.SetRootContext(ctx)
}

// RootContext is a thin alias for runtimectx.RootContext kept so
// existing cli-layer callers don't need to migrate. New code should
// prefer runtimectx.RootContext directly (or, better, thread ctx through).
func RootContext() context.Context {
	return runtimectx.RootContext()
}

// SignalContext returns a context cancelled on SIGINT/SIGTERM. CLI
// commands use this so Ctrl+C propagates cleanly to fleet-db RPCs and
// the embedded subprocess shutdown. Inherits from the root context set
// by SetRootContext, which lets a trace span installed at CLI startup
// parent every command's context-attached spans.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(runtimectx.RootContext(), os.Interrupt, syscall.SIGTERM)
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
func WithStore(fn func(ctx context.Context, h *bootstrap.StoreHandle) error) error {
	ctx, cancel := SignalContext()
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
func WithActiveWorkspace(fn func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error) error {
	return WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, err := ActiveWorkspace(ctx, h.Store)
		if err != nil {
			return err
		}
		return fn(ctx, h, ws)
	})
}
