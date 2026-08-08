package stack

import (
	"context"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli"
	stackpublish "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolpublisher"
)

// rebaseConflictPrompt instructs the agent to resolve the conflicted files only —
// the restack engine drives `git add` + `git rebase --continue` itself, so the
// agent must not run git commands.
func rebaseConflictPrompt(branch, onto string, conflicts []string) string {
	return fmt.Sprintf(`You are resolving a git rebase conflict while restacking branch %q onto %q.

These files have conflict markers (<<<<<<<, =======, >>>>>>>):
  - %s

Resolve every conflict by editing each file to the correct combined content and
removing all conflict markers. Do NOT run any git commands (no add / commit /
rebase / push) — only edit the files. When you finish, the working tree must have
no remaining conflict markers.`, branch, onto, strings.Join(conflicts, "\n  - "))
}

// interactiveResolver resolves conflicts with a live agent session (terminal).
type interactiveResolver struct{ deps *cli.Deps }

func (r interactiveResolver) ResolveRebaseConflicts(_ context.Context, repoPath, branch, onto string, conflicts []string) error {
	return r.deps.Agent.InvokeInteractive(repoPath, rebaseConflictPrompt(branch, onto, conflicts), "")
}

// headlessResolver resolves conflicts with a non-interactive agent run, for
// unattended/orchestrated publishes (e.g. the epic-runner).
type headlessResolver struct{ deps *cli.Deps }

func (r headlessResolver) ResolveRebaseConflicts(ctx context.Context, repoPath, branch, onto string, conflicts []string) error {
	return r.deps.Agent.InvokeNonInteractive(repoPath, rebaseConflictPrompt(branch, onto, conflicts), "", ctx.Done(), nil)
}

// newResolver returns the agent-backed conflict resolver for the stack publisher.
func newResolver(headless bool) stackpublish.ConflictResolver {
	deps := cli.GetDeps(nil)
	if headless {
		return headlessResolver{deps: deps}
	}
	return interactiveResolver{deps: deps}
}

// HeadlessResolver is the unattended agent-backed conflict resolver used by
// orchestrated publishes (the epic post-drain reconcile). Exposed so the epic
// command can reuse the same resolver the `loom stack publish --auto-rebase`
// path uses, instead of duplicating the prompt + agent wiring.
func HeadlessResolver() stackpublish.ConflictResolver { return newResolver(true) }
