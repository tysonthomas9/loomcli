package loomgit

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
)

// CheckoutNewBranch opens a clone's interactive branch through the Git boundary.
func CheckoutNewBranch(ctx context.Context, repo, branch, base string) error {
	runner, err := gitexec.New(repo, gitexec.Options{FallbackIdentity: gitexec.Identity{Name: "Loom", Email: "loom@localhost"}})
	if err != nil {
		return err
	}
	_, err = runner.Run(ctx, "checkout", "-b", branch, base)
	return err
}
