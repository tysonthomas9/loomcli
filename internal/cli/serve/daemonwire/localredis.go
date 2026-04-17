package daemonwire

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/webui/localredis"
)

// StartLocalRedis boots an in-process miniredis for terminal-state persistence
// when no external Redis is configured. It resolves the snapshot path under
// the loom config dir, spins up the manager, and schedules shutdown via the
// given context. Returns nil if startup fails — the caller logs a warning and
// continues without persistence. Callers read the bind address via mgr.Addr().
func StartLocalRedis(ctx context.Context, fleetMode bool) *localredis.Manager {
	snapshotPath := ""
	if dir := config.GetConfigDir(); dir != "" {
		snapshotPath = filepath.Join(dir, "terminal-state", "snapshot.json")
	}
	mgr, err := localredis.NewManager(snapshotPath, fleetMode, nil)
	if err != nil {
		slog.Warn("failed to start in-process Redis; tab metadata and terminal state will not persist across restarts", "err", err)
		return nil
	}
	mgr.Start(ctx)
	context.AfterFunc(ctx, func() {
		if err := mgr.Close(); err != nil {
			slog.Warn("local Redis shutdown error", "err", err)
		}
	})
	if snapshotPath != "" {
		slog.Info("Redis: using in-process miniredis", "addr", mgr.Addr(), "snapshot", snapshotPath)
	} else {
		slog.Info("Redis: using in-process miniredis (no snapshot path — state is ephemeral)", "addr", mgr.Addr())
	}
	return mgr
}
