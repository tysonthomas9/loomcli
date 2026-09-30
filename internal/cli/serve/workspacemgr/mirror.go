package workspacemgr

import (
	"context"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/mirror"
	"github.com/tysonthomas9/loomcli/internal/loomgit/remotecapture"
)

func StartLoomGitMirror(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if err := mirror.RunOnce(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("loom git mirror pass failed", "err", err)
			}
			if err := remotecapture.Recover(ctx, ""); err != nil && ctx.Err() == nil {
				slog.Warn("loom git remote capture recovery failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
