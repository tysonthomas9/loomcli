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
	ensureFleetDBEnvFromFleetEnv()
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

// rootCtx is the process-wide parent context for CLI commands. Set once by
// SetRootContext before rootCmd.Execute runs; read by SignalContext so any
// trace span (or other context-attached value) installed at the CLI entry
// point is inherited by every subcommand without each subcommand having to
// thread cmd.Context() down through every helper.
var rootCtx context.Context = context.Background()

// SetRootContext installs the parent context that SignalContext-derived
// contexts inherit from. Call from cli.Execute before dispatching to Cobra.
func SetRootContext(ctx context.Context) {
	if ctx != nil {
		rootCtx = ctx
	}
}

// RootContext returns the process-wide root context. Use this from helper
// functions that don't have access to a cobra cmd or a parent ctx but want
// to inherit the active trace span / cancellation chain set up at CLI
// entry. Prefer threading ctx through call sites where possible; this is
// for the long tail of utility helpers where that's not practical.
func RootContext() context.Context {
	return rootCtx
}

// SignalContext returns a context cancelled on SIGINT/SIGTERM. CLI
// commands use this so Ctrl+C propagates cleanly to fleet-db RPCs and
// the embedded subprocess shutdown. Inherits from the root context set
// by SetRootContext, which lets a trace span installed at CLI startup
// parent every command's context-attached spans.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(rootCtx, os.Interrupt, syscall.SIGTERM)
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
