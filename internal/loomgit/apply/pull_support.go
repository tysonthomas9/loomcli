package apply

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

// RecordOwnLayer persists an already-discovered lead-owned layer after replay succeeds.
func (s *Service) RecordOwnLayer(ctx context.Context, layer *loomgit.AppliedLayer) error {
	return s.recordOwnLayer(ctx, layer)
}

// AttributeCommit reads the same commit attribution used by Apply.
func (s *Service) AttributeCommit(ctx context.Context, sha, change string) (loomgit.AppliedCommit, error) {
	return s.attribute(ctx, sha, change)
}

// SwapPrepared installs one previously replayed leaf while the repository lock is held.
func (s *Service) SwapPrepared(ctx context.Context, workspace, lead, requestID, old, next string) ([]string, error) {
	branch, err := git(ctx, s.runner, "symbolic-ref", "HEAD")
	if err != nil {
		return nil, err
	}
	want, err := refname.InteractiveBranch(workspace, lead)
	if err != nil {
		return nil, err
	}
	if branch != "refs/heads/"+want {
		return nil, loomgit.NewError(loomgit.Stale, "working area branch differs from lead", nil)
	}
	indexPath, err := git(ctx, s.runner, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(indexPath+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) //nolint:gosec // Git resolves the active index; O_EXCL reserves its lock.
	if errors.Is(err, os.ErrExist) {
		return nil, loomgit.NewError(loomgit.SwapHeld, "working area index is locked", err)
	}
	if err != nil {
		return nil, err
	}
	lockOwned, keepLock := true, false
	defer func() {
		_ = lock.Close()
		if lockOwned && !keepLock {
			_ = os.Remove(indexPath + ".lock")
		}
	}()
	actual, err := git(ctx, s.runner, "rev-parse", "HEAD")
	if err != nil || actual != old {
		return nil, errors.Join(errHeadMoved, err)
	}
	paths, err := s.pendingPaths(ctx, old, next)
	if err != nil {
		return nil, err
	}
	if len(paths) > 0 {
		return paths, loomgit.NewError(loomgit.SwapHeld, strings.Join(paths, ", "), nil)
	}
	if err := s.store.SaveApplied(ctx, loomgit.AppliedLayer{RequestID: requestID, Workspace: workspace,
		Lead: lead, Change: "pull", OldTip: old, NewTip: next}); err != nil {
		return nil, err
	}
	return nil, s.install(ctx, branch, indexPath, old, next, requestID, &lockOwned, &keepLock)
}
