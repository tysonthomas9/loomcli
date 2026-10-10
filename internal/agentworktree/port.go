package agentworktree

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
)

// ErrNotImplemented reports a Workspace operation a later ticket completes
// (Publish in 2.10). No runtime path calls it yet.
var ErrNotImplemented = errors.New("agentworktree: not implemented yet")

// Port adapts Worktrees to the loomagent.Workspace port.
type Port struct{ W *Worktrees }

var _ loomagent.Workspace = Port{}

// Ensure implements loomagent.Workspace with Worktrees.Ensure.
func (p Port) Ensure(ctx context.Context, s loomagent.WorkspaceSpec) (loomagent.WorkingCopy, error) {
	wt, err := p.W.Ensure(ctx, Spec(s))
	return loomagent.WorkingCopy(wt), err
}

// Status implements loomagent.Workspace with Worktrees.Status.
func (p Port) Status(ctx context.Context, s loomagent.WorkspaceSpec) (loomagent.WorkspaceStatus, error) {
	st, err := p.W.Status(ctx, Spec(s))
	if errors.Is(err, ErrNotOwned) {
		err = fmt.Errorf("%w: %w", loomagent.ErrWorkspaceNotOwned, err)
	}
	return loomagent.WorkspaceStatus(st), err
}

// CheckBase implements loomagent.Workspace with Worktrees.CheckBase.
func (p Port) CheckBase(ctx context.Context, repo, ref string) error {
	return p.W.CheckBase(ctx, repo, ref)
}

// Remove implements loomagent.Workspace with Worktrees.Remove.
func (p Port) Remove(ctx context.Context, s loomagent.WorkspaceSpec) error {
	return p.W.Remove(ctx, Spec(s))
}

// Checkpoint implements loomagent.Workspace with Worktrees.Checkpoint.
func (p Port) Checkpoint(ctx context.Context, s loomagent.WorkspaceSpec, ref string) error {
	return p.W.Checkpoint(ctx, Spec(s), ref)
}

// Publish implements loomagent.Workspace; ticket 2.10 completes it.
func (Port) Publish(context.Context, loomagent.PublishRequest) (loomagent.PublishResult, error) {
	return loomagent.PublishResult{}, ErrNotImplemented
}
