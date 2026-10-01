package agentworktree

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
)

// ErrNotImplemented reports a Workspace operation a later ticket completes
// (Status and Remove in 1.2, Publish in 2.10). No runtime path calls it yet.
var ErrNotImplemented = errors.New("agentworktree: not implemented yet")

// Port adapts Worktrees to the loomagent.Workspace port.
type Port struct{ W *Worktrees }

var _ loomagent.Workspace = Port{}

// Ensure implements loomagent.Workspace with Worktrees.Ensure.
func (p Port) Ensure(ctx context.Context, s loomagent.WorkspaceSpec) (loomagent.WorkingCopy, error) {
	wt, err := p.W.Ensure(ctx, Spec(s))
	return loomagent.WorkingCopy(wt), err
}

// Status implements loomagent.Workspace; ticket 1.2 completes it.
func (Port) Status(context.Context, loomagent.WorkspaceSpec) (loomagent.WorkspaceStatus, error) {
	return loomagent.WorkspaceStatus{}, ErrNotImplemented
}

// Remove implements loomagent.Workspace; ticket 1.2 completes it.
func (Port) Remove(context.Context, loomagent.WorkspaceSpec) error {
	return ErrNotImplemented
}

// Publish implements loomagent.Workspace; ticket 2.10 completes it.
func (Port) Publish(context.Context, loomagent.PublishRequest) (loomagent.PublishResult, error) {
	return loomagent.PublishResult{}, ErrNotImplemented
}
