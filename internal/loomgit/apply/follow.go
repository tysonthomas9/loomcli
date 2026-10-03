package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

type FollowResult struct {
	Applied []string `json:"applied"`
	Pending []string `json:"pending"`
	Paths   []string `json:"paths,omitempty"`
	// Spent lists approvals settled without applying: their apply request can
	// never apply again. A newer approval re-arms them.
	Spent []SpentApproval `json:"spent,omitempty"`
}

// SpentApproval is an approval that was settled as spent, with the reason.
type SpentApproval struct {
	Change   string `json:"change"`
	Revision int    `json:"revision"`
	Reason   string `json:"reason"`
}

// spentReason is the reviewer-facing reason carried by a spent apply error.
func spentReason(err error) string {
	var coded *loomgit.Error
	if errors.As(err, &coded) && coded.Message != "" {
		return coded.Message
	}
	return "its apply request can no longer apply"
}

func RecoverPending(ctx context.Context, store *journal.SQLite) error {
	return recoverPendingWithConfig(ctx, store, config.LoadConfig, nil)
}

// RecoverPendingExcept follows durable approvals for every lead except those
// skip holds back, such as a lead whose pull could not be recovered.
func RecoverPendingExcept(ctx context.Context, store *journal.SQLite, skip func(workspace, lead string) bool) error {
	return recoverPendingWithConfig(ctx, store, config.LoadConfig, skip)
}

func recoverPendingWithConfig(ctx context.Context, store *journal.SQLite, load func() (*config.LoomConfig, error), skip func(string, string) bool) error {
	targets, err := store.PendingApprovalTargets(ctx)
	if err != nil {
		return err
	}
	var cfg *config.LoomConfig
	var failures []error
	for _, target := range targets {
		if skip != nil && skip(target.Workspace, target.Lead) {
			continue
		}
		paused, err := store.FollowingPaused(ctx, target.Workspace, target.Lead)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		areas, err := store.WorkingAreas(ctx, target.Workspace, target.Lead)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if paused || len(areas) == 0 {
			continue
		}
		if cfg == nil {
			cfg, err = load()
			if err != nil {
				return err
			}
		}
		_, err = followWithStore(ctx, store, cfg, target.Workspace, target.Lead)
		var coded *loomgit.Error
		if errors.As(err, &coded) && (coded.Kind == loomgit.Conflict || coded.Kind == loomgit.ApplyPending || coded.Kind == loomgit.SwapHeld) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func FollowLocal(ctx context.Context, workspace, lead string) (FollowResult, error) {
	if workspace == "" || lead == "" {
		return FollowResult{}, errors.New("workspace and lead are required")
	}
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); err != nil {
		return FollowResult{}, err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return FollowResult{}, err
	}
	defer func() { _ = store.Close() }()
	cfg, err := config.LoadConfig()
	if err != nil {
		return FollowResult{}, err
	}
	return followWithStore(ctx, store, cfg, workspace, lead)
}

func followWithStore(ctx context.Context, store *journal.SQLite, cfg *config.LoomConfig, workspace, lead string) (FollowResult, error) {
	paused, err := store.FollowingPaused(ctx, workspace, lead)
	if err != nil {
		return FollowResult{}, err
	}
	pending, err := store.PendingApprovals(ctx, workspace, lead)
	if err != nil {
		return FollowResult{}, err
	}
	result := FollowResult{}
	if paused {
		for _, approval := range pending {
			result.Pending = append(result.Pending, approval.Change)
		}
		return result, nil
	}
	remaining, err := latestApprovals(ctx, store, pending)
	if err != nil {
		return result, err
	}
	return followApprovals(ctx, store, cfg, pending, remaining)
}

func followApprovals(ctx context.Context, store *journal.SQLite, cfg *config.LoomConfig,
	pending []journal.PendingApproval, remaining map[string]journal.PendingApproval) (FollowResult, error) {
	result := FollowResult{}
	for len(remaining) > 0 {
		progress := false
		waitingOnPredecessor := false
		for _, approval := range pending {
			selected, exists := remaining[approval.Change]
			if !exists || selected.Revision != approval.Revision {
				continue
			}
			if _, blocked := remaining[approval.Predecessor]; blocked {
				continue
			}
			ready, err := predecessorReady(ctx, store, approval)
			if err != nil {
				return result, err
			}
			if !ready {
				waitingOnPredecessor = true
				continue
			}
			if err := followOne(ctx, store, cfg, approval, &result); err != nil {
				return result, err
			}
			delete(remaining, approval.Change)
			progress = true
		}
		if !progress {
			if waitingOnPredecessor {
				for change := range remaining {
					result.Pending = append(result.Pending, change)
				}
				return result, nil
			}
			return result, loomgit.NewError(loomgit.AttentionRequired, "approved changes have a dependency cycle", nil)
		}
	}
	return result, nil
}

// followOne applies one ready approval. A spent request settles as spent
// without counting as applied; a newer approval re-arms the follow.
func followOne(ctx context.Context, store *journal.SQLite, cfg *config.LoomConfig,
	approval journal.PendingApproval, result *FollowResult) error {
	applied, err := applyApproval(ctx, store, cfg, approval)
	if errors.Is(err, ErrRequestSpent) {
		result.Spent = append(result.Spent, SpentApproval{Change: approval.Change, Revision: approval.Revision, Reason: spentReason(err)})
		return nil
	}
	if err != nil {
		result.Pending = append(result.Pending, approval.Change)
		result.Paths = applied.Paths
		return err
	}
	result.Applied = append(result.Applied, approval.Change)
	return nil
}

func latestApprovals(ctx context.Context, store *journal.SQLite, pending []journal.PendingApproval) (map[string]journal.PendingApproval, error) {
	latest := make(map[string]int)
	for _, approval := range pending {
		if approval.Revision > latest[approval.Change] {
			latest[approval.Change] = approval.Revision
		}
	}
	remaining := make(map[string]journal.PendingApproval)
	for _, approval := range pending {
		if approval.Revision != latest[approval.Change] {
			if err := store.SetApprovalFollow(ctx, approval, "superseded", nil); err != nil {
				return nil, err
			}
			continue
		}
		remaining[approval.Change] = approval
	}
	return remaining, nil
}

func predecessorReady(ctx context.Context, store *journal.SQLite, approval journal.PendingApproval) (bool, error) {
	if approval.Predecessor == "" {
		return true, nil
	}
	return store.PredecessorApplied(ctx, approval.Workspace, approval.Lead, approval.Predecessor)
}

func applyApproval(ctx context.Context, store *journal.SQLite, cfg *config.LoomConfig, approval journal.PendingApproval) (Result, error) {
	requestID := fmt.Sprintf("approval:%d", approval.VerdictID)
	alreadyApplied, err := store.ApprovalApplied(ctx, requestID)
	if err != nil {
		return Result{}, err
	}
	var result Result
	if !alreadyApplied {
		result, err = applyLocalWithStore(ctx, Request{Workspace: approval.Workspace, Lead: approval.Lead,
			Change: approval.Change, Revision: approval.Revision, RequestID: requestID}, store, cfg)
	}
	if errors.Is(err, ErrRequestSpent) {
		if setErr := store.SpendApprovalFollow(ctx, approval, spentReason(err)); setErr != nil {
			return result, setErr
		}
		return result, err
	}
	if err != nil {
		status := "conflict"
		var coded *loomgit.Error
		if errors.As(err, &coded) && coded.Kind == loomgit.ApplyPending {
			status = "apply_pending"
		}
		return result, errors.Join(err, store.SetApprovalFollow(ctx, approval, status, result.Paths))
	}
	return result, store.SetApprovalFollow(ctx, approval, "applied", nil)
}
