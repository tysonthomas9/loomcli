package apply

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

// Reconcile finishes an interrupted swap while holding the repository lock.
func (s *Service) Reconcile(ctx context.Context, workspace, lead string) error {
	return s.repo.WithLock(ctx, func(ctx context.Context) error {
		layers, err := s.store.OpenApplied(ctx, workspace, lead)
		if err != nil {
			return err
		}
		for _, layer := range layers {
			if err := s.reconcileLayer(ctx, layer); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) reconcileLayer(ctx context.Context, layer loomgit.AppliedLayer) error {
	if !validRecoveryPhase(layer.Phase) {
		return attention(layer, "unknown journal phase "+layer.Phase, nil)
	}
	branch, err := refname.InteractiveBranch(layer.Workspace, layer.Lead)
	if err != nil {
		return err
	}
	branch = "refs/heads/" + branch
	actualBranch, err := git(ctx, s.runner, "symbolic-ref", "HEAD")
	if err != nil || actualBranch != branch {
		return attention(layer, "working area branch changed", err)
	}
	indexPath, err := git(ctx, s.runner, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return err
	}
	tmpPath, ownerPath := recoveryPaths(indexPath, layer.RequestID)
	lockPath := indexPath + ".lock"
	owned, committed, err := checkOwnership(lockPath, indexPath, ownerPath)
	if err != nil {
		return err
	}
	if layer.Phase == "prepared" && !owned && !committed {
		if _, err := os.Stat(lockPath); errors.Is(err, os.ErrNotExist) {
			return s.store.AdvanceApplied(ctx, layer.RequestID, "prepared", "not_applied")
		}
	}
	if layer.Phase == "installing" {
		return attention(layer, "checkout update may be incomplete", nil)
	}
	if !owned && !committed {
		return attention(layer, "index lock is not owned by this apply", nil)
	}
	head, err := git(ctx, s.runner, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if head != layer.OldTip && head != layer.NewTip {
		return attention(layer, "HEAD changed", nil)
	}
	if layer.Phase == "prepared" {
		if err := os.Remove(lockPath); err != nil {
			return err
		}
		_ = os.Remove(ownerPath)
		_ = os.Remove(tmpPath)
		return s.store.AdvanceApplied(ctx, layer.RequestID, "prepared", "not_applied")
	}
	return s.finishLayer(ctx, layer, branch, indexPath, tmpPath, ownerPath, committed, head)
}

func validRecoveryPhase(phase string) bool {
	switch phase {
	case "prepared", "installing", "files_updated", "ref_updated":
		return true
	default:
		return false
	}
}

func (s *Service) finishLayer(ctx context.Context, layer loomgit.AppliedLayer, branch, indexPath,
	tmpPath, ownerPath string, committed bool, head string) error {
	if !committed {
		if _, err := os.Stat(tmpPath); err != nil {
			return attention(layer, "temporary index is missing", err)
		}
	}
	if layer.Phase == "files_updated" && head == layer.OldTip {
		if s.beforeRecoverCAS != nil {
			s.beforeRecoverCAS()
		}
		if err := s.runner.UpdateRef(ctx, branch, layer.NewTip, layer.OldTip); err != nil {
			return err
		}
	}
	if layer.Phase == "files_updated" {
		if err := s.store.AdvanceApplied(ctx, layer.RequestID, "files_updated", "ref_updated"); err != nil {
			return err
		}
	}
	if !committed {
		if err := commitIndex(indexPath+".lock", indexPath, tmpPath); err != nil {
			return err
		}
	}
	if err := s.store.AdvanceApplied(ctx, layer.RequestID, "ref_updated", "done"); err != nil {
		return err
	}
	_ = os.Remove(ownerPath)
	_ = os.Remove(tmpPath)
	return nil
}

func checkOwnership(lockPath, indexPath, ownerPath string) (bool, bool, error) {
	owner, err := os.Stat(ownerPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if lock, err := os.Stat(lockPath); err == nil {
		return os.SameFile(owner, lock), false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, false, err
	}
	index, err := os.Stat(indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	return false, err == nil && os.SameFile(owner, index), err
}

func commitIndex(lockPath, indexPath, tmpPath string) error {
	data, err := os.ReadFile(tmpPath) //nolint:gosec // Journaled temporary index path.
	if err != nil {
		return err
	}
	if err := os.WriteFile(lockPath, data, 0600); err != nil { //nolint:gosec // Ownership verified by inode before writing.
		return err
	}
	return os.Rename(lockPath, indexPath)
}

func attention(layer loomgit.AppliedLayer, reason string, cause error) error {
	return loomgit.NewError(loomgit.AttentionRequired,
		fmt.Sprintf("apply %s: %s (old %s, new %s)", layer.RequestID, reason, layer.OldTip, layer.NewTip), cause)
}
