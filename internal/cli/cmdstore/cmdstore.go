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
	dataDir := bootstrap.LoomDir()
	if dataDir == "" {
		return nil, errors.New("cannot resolve loom directory (set HOME or LOOM_CONFIG_DIR)")
	}
	handle, err := bootstrap.OpenStore(ctx, dataDir, nil)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	return handle, nil
}

// SignalContext returns a context cancelled on SIGINT/SIGTERM. CLI
// commands use this so Ctrl+C propagates cleanly to fleet-db RPCs and
// the embedded subprocess shutdown.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// ActiveWorkspace resolves the active workspace key (LOOM_WORKSPACE env
// > state cache) and verifies it exists in the store. Returns an
// actionable error on any failure path.
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
	defer h.Close()
	return fn(ctx, h)
}

// WithActiveWorkspace opens a Store, resolves the active workspace key
// (LOOM_WORKSPACE env > state cache), and runs fn with both. Most
// noun-verb subcommands operate inside an implicit workspace and want
// this exact composition.
func WithActiveWorkspace(fn func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error) error {
	return WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, err := ActiveWorkspace(ctx, h.Store)
		if err != nil {
			return err
		}
		return fn(ctx, h, ws)
	})
}
