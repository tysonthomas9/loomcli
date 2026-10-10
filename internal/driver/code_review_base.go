package driver

import (
	"context"
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// A task starts while its only open blocker's code awaits review when that
// blocker is in its epic (Tyson, 2026-10-09). Its task copy is built on the
// blocker's newest frozen revision, pinned as local lineage, whether a lead
// delegated it or not; otherwise it would run without the code it depends
// on. A task whose blockers are closed keeps its usual base.

// CodeReviewBaseLookup names the task whose frozen revision task is built on
// while that task's code awaits review; found is false for any other task.
type CodeReviewBaseLookup func(ctx context.Context, workspace, task string) (string, bool, error)

// FleetCodeReviewBase reads a task's code-review base from FleetDB.
func FleetCodeReviewBase(ctx context.Context, workspace, task string) (string, bool, error) {
	ctx = middleware.WithWorkspace(ctx, workspace)
	return backend.CodeReviewBase(ctx, cli.WorkspaceAwareIssueBackend()(ctx), task)
}

// codeReviewBase returns task's code-review base. A failed lookup stops the
// task copy: guessing would build the task without its blocker's code.
func (l StackLineageLookup) codeReviewBase(ctx context.Context, workspace, task string) (string, bool, error) {
	if l.CodeReviewBase == nil {
		return "", false, nil
	}
	predecessor, found, err := l.CodeReviewBase(ctx, workspace, task)
	if err != nil {
		return "", false, loomgit.NewError(loomgit.LineageUnresolved, "read the blocker whose code awaits review", err)
	}
	return predecessor, found, nil
}

type codeReviewBaseLookup interface {
	codeReviewBase(context.Context, string, string) (string, bool, error)
}

// choosesBase reports whether a delegated task names its own base: a base
// revision or a conflict resolution.
func choosesBase(input json.RawMessage) (bool, error) {
	_, hasRevision, err := baseRevisionFromInput(input)
	if err != nil || hasRevision {
		return hasRevision, err
	}
	_, hasResolution, err := conflictResolutionFromInput(input)
	return hasResolution, err
}
