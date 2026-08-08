package serve

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/webui/localredis"
)

// startLocalRedis boots an in-process miniredis for terminal-state persistence
// when no external Redis is configured.
func startLocalRedis(ctx context.Context, fleetMode bool) *localredis.Manager {
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
