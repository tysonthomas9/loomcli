package epic

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/stack"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// reconcileEpicStack runs the Stage-4 post-drain reconcile: once the epic has
// drained (every task pushed its canonical branch), it provisions an ephemeral
// checkout of the repo origin and runs the publisher to open/link one PR per
// task with each PR's base set to its predecessor's branch.
//
// Publish admission is acquired by PublishFromOrigin→Publish (FleetDB lease or
// the shared per-stack host flock). Manual `loom stack publish` / `restack` use the
// same guard — this path does not take a separate lock.
//
// It is fail-open at the call site: the task branches are already on origin, so
// a reconcile failure is a warning, not an epic failure — it is fully
// re-runnable via `loom stack publish <stack>`. It never uses os.Getwd(); the
// checkout is provisioned by PublishFromOrigin in a temp dir.
func reconcileEpicStack(ctx context.Context, loomStore store.Store, ws string, proj *EpicStackProjection) error {
	if proj == nil {
		return nil
	}
	if strings.TrimSpace(proj.RepoURL) == "" {
		return fmt.Errorf("stack %s has no repo origin url to reconcile from", proj.StackID)
	}
	token := resolveGitHubToken(ctx)
	if token == "" {
		return fmt.Errorf("no GitHub token (set GITHUB_TOKEN/GH_TOKEN or run `gh auth login`)")
	}
	sstore, err := stackstore.ForStore(loomStore)
	if err != nil {
		return fmt.Errorf("open stack store: %w", err)
	}
	rec := &stackpublish.Reconciler{
		Store:  sstore,
		Forge:  stackpublish.NewGitHubForge(token, nil, ""),
		Holder: stackpublish.HolderIdentity("epic-reconcile"),
	}
	opts := stackpublish.Options{Resolver: stack.HeadlessResolver()}

	report, err := rec.PublishFromOrigin(ctx, ws, proj.StackID, proj.RepoURL, token, opts)
	if err != nil {
		return err
	}

	if report != nil {
		fmt.Printf("[epic-run] reconciled stack %s: created=%d reparented=%d skipped=%d closed=%d merged=%d empty=%d\n",
			proj.StackID, len(report.Created), len(report.Reparented), len(report.Skipped),
			len(report.Closed), len(report.Merged), len(report.Empty))
		for task, url := range report.PRURLs {
			fmt.Printf("[epic-run]   %s  %s\n", task, url)
		}
	}
	return nil
}

// resolveGitHubToken mirrors `loom stack`'s token resolution: env first, then a
// local `gh auth token`. Returns "" when none is available.
func resolveGitHubToken(ctx context.Context) string {
	if t := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); t != "" {
		return t
	}
	if t := strings.TrimSpace(os.Getenv("GH_TOKEN")); t != "" {
		return t
	}
	if out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}
