package workspacemgr

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
)

func recoverPullThenApply(ctx context.Context, recoverApply func(context.Context) error) error {
	if err := pull.Recover(ctx); err != nil {
		return err
	}
	return recoverApply(ctx)
}
