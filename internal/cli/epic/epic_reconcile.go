package epic

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/stack"
	"github.com/tysonthomas9/loomcli/internal/configlock"
	stackpublish "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolpublisher"
	stackstore "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolstackstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
)

// reconcileEpicStack runs the Stage-4 post-drain reconcile: once the epic has
// drained (every task pushed its canonical branch), it provisions an ephemeral
// checkout of the repo origin under a per-stack lock, fetches the stack's
// branches, and runs the publisher to open/link one PR per task with each PR's
// base set to its predecessor's branch.
//
// It is fail-open at the call site: the task branches are already on origin, so
// a reconcile failure is a warning, not an epic failure — it is fully
// re-runnable via `loom stack publish <stack>`. It never uses os.Getwd(); the
// checkout is provisioned by PublishFromOrigin in a temp dir.
//
//nolint:funlen // Reconciliation is an ordered transaction across the stack projection and Git refs.
func reconcileEpicStack(ctx context.Context, ws string, proj *EpicStackProjection) error {
	if proj == nil {
		return nil
	}
	if strings.TrimSpace(proj.RepoURL) == "" {
		return fmt.Errorf("stack %s has no repo origin url to reconcile from", proj.StackID)
	}
	token := resolveGitHubToken(ctx)
	forge, origin, err := stackpublish.NewForgeForOrigin(proj.RepoURL, token)
	if err != nil {
		return err
	}
	if origin.Kind == stackpublish.OriginKindGitHub && token == "" {
		return fmt.Errorf("no GitHub token (set GITHUB_TOKEN/GH_TOKEN or run `gh auth login`)")
	}
	sstore, err := stackstore.Default()
	if err != nil {
		return fmt.Errorf("open stack store: %w", err)
	}
	stacks, err := sourcecontrol.NewStackLifecycle(sstore, time.Now)
	if err != nil {
		return err
	}
	rec := &stackpublish.Reconciler{
		Stacks: stacks,
		Forge:  forge,
	}
	opts := stackpublish.Options{Resolver: stack.HeadlessResolver()}

	lockDir, err := stackReconcileLockDir(proj.StackID)
	if err != nil {
		return err
	}

	// Per-stack lock: all three reconcile trigger sites (this post-drain hook, a
	// manual `loom stack publish`, an epic re-run) share the deterministic stack
	// id, so the lock serializes them on the same key.
	var report *stackpublish.Report
	if lockErr := configlock.WithLock(lockDir, func() error {
		r, perr := rec.PublishFromOrigin(ctx, ws, proj.StackID, proj.RepoURL, token, opts)
		report = r
		return perr
	}); lockErr != nil {
		return lockErr
	}

	if report != nil {
		fmt.Printf("[epic-run] reconciled stack %s: created=%d reparented=%d skipped=%d closed=%d merged=%d empty=%d pushed=%d\n",
			proj.StackID, len(report.Created), len(report.Reparented), len(report.Skipped),
			len(report.Closed), len(report.Merged), len(report.Empty), len(report.Pushed))
		if msg := report.Message; msg != "" {
			fmt.Printf("[epic-run]   %s\n", msg)
		}
		for task, url := range report.PRURLs {
			fmt.Printf("[epic-run]   %s  %s\n", task, url)
		}
	}
	return nil
}

// stackReconcileLockDir returns a per-stack lock directory under the loom dir, so
// concurrent reconciles of different stacks never contend while same-stack
// triggers serialize.
func stackReconcileLockDir(id sourcecontrol.StackID) (string, error) {
	loomDir := bootstrap.LoomDir()
	if loomDir == "" {
		return "", stackstore.ErrLoomDirMissing
	}
	dir := filepath.Join(loomDir, "stack-locks", sanitizeLockSegment(id))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create stack lock dir: %w", err)
	}
	return dir, nil
}

func sanitizeLockSegment(v string) string {
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "stack"
	}
	return out
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
