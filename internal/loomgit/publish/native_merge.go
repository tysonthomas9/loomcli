package publish

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

type nativeMergeStore interface {
	Store
	BeginNativeMerge(context.Context, journal.NativeMerge) error
	OpenNativeMerges(context.Context) ([]journal.NativeMerge, error)
	AdvanceNativeMerge(context.Context, journal.NativeMerge, string, string) error
	RecordNativeMergeRequest(context.Context, journal.NativeMerge, string) error
	BlockNativeMerge(context.Context, journal.NativeMerge, string) error
	LandingStatus(context.Context, string, string) (journal.LandingStatus, error)
	StackBackend(context.Context, string, string) (string, error)
	LeadMayMerge(context.Context, string) (journal.LeadMergePolicy, error)
}

type nativeMergeForge interface {
	PullByNumber(context.Context, string, string, int) (stackpublish.PR, error)
	MergeNativePull(context.Context, string, string, int, string) (stackpublish.NativeMergeResult, error)
	RecoverNativePull(context.Context, string, string, int, string) (stackpublish.NativeMergeResult, error)
	NativeMergeStatus(context.Context, string, string, int, string) (stackpublish.NativeMergeResult, error)
}

func beginNativeMerge(ctx context.Context, store Store, request StackRequest, target string) error {
	mergeStore, ok := store.(nativeMergeStore)
	if !ok {
		return errors.New("store cannot journal native merges")
	}
	if request.Workspace == "" || request.StackID == "" || len(request.Changes) == 0 {
		return errors.New("workspace, stack and changes are required")
	}
	mode, err := store.DeliveryMode(ctx, request.Workspace)
	if err != nil {
		return err
	}
	if mode != "stack" {
		return loomgit.NewError(loomgit.ModeMismatch, "merge requires stack delivery mode", nil)
	}
	backend, err := mergeStore.StackBackend(ctx, request.Workspace, request.StackID)
	if err != nil {
		return err
	}
	if backend != "native" {
		return loomgit.NewError(loomgit.ModeMismatch, "stack is not native", nil)
	}
	index := -1
	for current, change := range request.Changes {
		if change == target {
			index = current
		}
		publication, found, err := store.Publication(ctx, request.Workspace, change)
		if err != nil {
			return err
		}
		if !found || publication.StackID != request.StackID || publication.Phase != "done" {
			return loomgit.NewError(loomgit.MergeBlocked, "stack publication is incomplete", nil)
		}
		if current > index && index >= 0 {
			continue
		}
		if err := requireNativeVerdict(ctx, store, publication); err != nil {
			return err
		}
	}
	if index < 0 {
		return loomgit.NewError(loomgit.Stale, "merge target is not in stack", nil)
	}
	merge := journal.NativeMerge{Workspace: request.Workspace, StackID: request.StackID, Target: target, Changes: request.Changes[:index+1]}
	if policy, ok := request.MergeAuthority.(whenGreenMerge); ok {
		merge.Authority, merge.SetBy = leadMergeAuthority, policy.SetBy
	}
	return mergeStore.BeginNativeMerge(ctx, merge)
}

func requireNativeVerdict(ctx context.Context, store Store, publication journal.Publication) error {
	revision, err := store.RevisionByHead(ctx, publication.Workspace, publication.Change, publication.Head)
	if err != nil {
		return loomgit.NewError(loomgit.ReviewRequired, "native layer has no reviewed revision", err)
	}
	return review.RequireVerdict(ctx, store, publication.Workspace, publication.Change,
		revision.Number, publication.Head, "publish", "")
}

func ReconcileNativeMerges(ctx context.Context, store nativeMergeStore, forge nativeMergeForge) error {
	merges, err := store.OpenNativeMerges(ctx)
	if err != nil {
		return err
	}
	for _, merge := range merges {
		if err := reconcileNativeMerge(ctx, store, forge, merge); err != nil {
			var coded *loomgit.Error
			if errors.As(err, &coded) && (coded.Kind == loomgit.Stale || coded.Kind == loomgit.MergeBlocked || coded.Kind == loomgit.ReviewRequired || coded.Kind == loomgit.Protected) {
				if blockErr := store.BlockNativeMerge(ctx, merge, err.Error()); blockErr != nil {
					return blockErr
				}
			}
			return err
		}
	}
	return nil
}

func ReconcileNativeAt(ctx context.Context) error {
	path := filepath.Join(config.GetConfigDir(), "loomgit", "store.db")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	merges, err := store.OpenNativeMerges(ctx)
	if err != nil || len(merges) == 0 {
		return err
	}
	token := githubtoken.GitHub(ctx)
	if token == "" {
		return errors.New("GitHub host credential unavailable")
	}
	return ReconcileNativeMerges(ctx, store, stackpublish.NewConfiguredGitHubForge(token))
}

func reconcileNativeMerge(ctx context.Context, store nativeMergeStore, forge nativeMergeForge, merge journal.NativeMerge) error {
	publication, found, err := store.Publication(ctx, merge.Workspace, merge.Target)
	if err != nil {
		return err
	}
	if !found || publication.StackID != merge.StackID || publication.PRNumber == 0 {
		return loomgit.NewError(loomgit.MergeBlocked, "native target publication is missing", nil)
	}
	parts := strings.Split(publication.Slug, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("native repository slug is invalid")
	}
	switch merge.Phase {
	case "ready":
		if merge.Authority == leadMergeAuthority {
			policy, err := store.LeadMayMerge(ctx, merge.Workspace)
			if err != nil {
				return err
			}
			if policy.Value != "when_green" {
				return store.BlockNativeMerge(ctx, merge, "cancelled: lead_may_merge is off")
			}
			if green, err := nativeLeadPrefixGreen(ctx, store, forge, merge, parts); err != nil || !green {
				return err
			}
		}
		return submitNativeMerge(ctx, store, forge, merge, publication, parts)
	case "dispatching":
		return recoverNativeDispatch(ctx, store, forge, merge, publication, parts)
	case "sent":
		return pollNativeMerge(ctx, store, forge, merge, publication, parts)
	default:
		return fmt.Errorf("native merge phase %q is invalid", merge.Phase)
	}
}

func submitNativeMerge(ctx context.Context, store nativeMergeStore, forge nativeMergeForge,
	merge journal.NativeMerge, publication journal.Publication, parts []string) error {
	if err := checkNativePrefix(ctx, store, forge, merge, parts); err != nil {
		return err
	}
	if err := store.AdvanceNativeMerge(ctx, merge, "dispatching", publication.Head); err != nil {
		return err
	}
	merge.Phase, merge.Head = "dispatching", publication.Head
	result, err := forge.MergeNativePull(ctx, parts[0], parts[1], publication.PRNumber, publication.Head)
	if err != nil {
		if errors.Is(err, stackpublish.ErrMergeQueueRequired) {
			message := "GitHub requires this PR to merge through its merge queue"
			if blockErr := store.BlockNativeMerge(ctx, merge, message); blockErr != nil {
				return blockErr
			}
			return loomgit.NewError(loomgit.MergeQueueRequired, message, err)
		}
		return err
	}
	if result.Status == "failed" {
		message := "GitHub rejected the native stack merge: " + result.Details.Message
		if err := store.BlockNativeMerge(ctx, merge, message); err != nil {
			return err
		}
		return loomgit.NewError(loomgit.MergeBlocked, message, nil)
	}
	return store.RecordNativeMergeRequest(ctx, merge, result.Details.UUID)
}

func checkNativePrefix(ctx context.Context, store nativeMergeStore, forge nativeMergeForge,
	merge journal.NativeMerge, parts []string) error {
	if len(merge.Changes) == 0 || merge.Changes[len(merge.Changes)-1] != merge.Target {
		return loomgit.NewError(loomgit.Stale, "native merge target differs from prefix", nil)
	}
	if err := requireChangesLanded(ctx, store, merge.Workspace, merge.Changes, mergePredecessors); err != nil {
		return err
	}
	for _, change := range merge.Changes {
		publication, found, err := store.Publication(ctx, merge.Workspace, change)
		if err != nil {
			return err
		}
		if !found || publication.StackID != merge.StackID || publication.Phase != "done" {
			return loomgit.NewError(loomgit.MergeBlocked, "native stack publication is incomplete", nil)
		}
		pr, err := forge.PullByNumber(ctx, parts[0], parts[1], publication.PRNumber)
		if err != nil {
			return err
		}
		if pr.Merged || pr.State != "open" || pr.Head != publication.Branch || pr.HeadSHA != publication.Head {
			return loomgit.NewError(loomgit.Stale, "native stack PR moved before merge", nil)
		}
		if err := requireNativeVerdict(ctx, store, publication); err != nil {
			return err
		}
	}
	return nil
}

func recoverNativeDispatch(ctx context.Context, store nativeMergeStore, forge nativeMergeForge,
	merge journal.NativeMerge, publication journal.Publication, parts []string) error {
	pr, err := forge.PullByNumber(ctx, parts[0], parts[1], publication.PRNumber)
	if err != nil {
		return err
	}
	if pr.HeadSHA != merge.Head {
		return loomgit.NewError(loomgit.Stale, "native merge target head changed", nil)
	}
	if pr.Merged {
		if err := checkNativeMergedPrefix(ctx, store, forge, merge, parts); err != nil {
			return err
		}
		return store.RecordNativeMergeRequest(ctx, merge, "")
	}
	if pr.State != "open" {
		return loomgit.NewError(loomgit.Stale, "native merge target closed before recovery", nil)
	}
	result, err := forge.RecoverNativePull(ctx, parts[0], parts[1], publication.PRNumber, merge.Head)
	if err != nil {
		return loomgit.NewError(loomgit.AttentionRequired, "native merge submission outcome is unknown", err)
	}
	return store.RecordNativeMergeRequest(ctx, merge, result.Details.UUID)
}

func checkNativeMergedPrefix(ctx context.Context, store nativeMergeStore, forge nativeMergeForge,
	merge journal.NativeMerge, parts []string) error {
	for _, change := range merge.Changes {
		publication, found, err := store.Publication(ctx, merge.Workspace, change)
		if err != nil {
			return err
		}
		if !found {
			return loomgit.NewError(loomgit.MergeBlocked, "native downstack publication is missing", nil)
		}
		pr, err := forge.PullByNumber(ctx, parts[0], parts[1], publication.PRNumber)
		if err != nil {
			return err
		}
		if !pr.Merged || pr.HeadSHA != publication.Head {
			return loomgit.NewError(loomgit.Stale, "native downstack PR differs after merge", nil)
		}
	}
	return nil
}

func pollNativeMerge(ctx context.Context, store nativeMergeStore, forge nativeMergeForge,
	merge journal.NativeMerge, publication journal.Publication, parts []string) error {
	if merge.UUID != "" {
		result, err := forge.NativeMergeStatus(ctx, parts[0], parts[1], publication.PRNumber, merge.UUID)
		if err != nil {
			return loomgit.NewError(loomgit.AttentionRequired, "native async merge result unavailable", err)
		}
		if result.Details.ExpectedHeadSHA != "" && result.Details.ExpectedHeadSHA != merge.Head {
			return loomgit.NewError(loomgit.Stale, "native async merge head differs", nil)
		}
		if result.Details.BypassRules {
			return loomgit.NewError(loomgit.Protected, "native merge used a rules bypass", nil)
		}
		if result.Status == "pending" {
			return nil
		}
		if result.Status == "failed" {
			return loomgit.NewError(loomgit.MergeBlocked, "GitHub native merge failed: "+result.Details.Message, nil)
		}
	}
	pr, err := forge.PullByNumber(ctx, parts[0], parts[1], publication.PRNumber)
	if err != nil {
		return err
	}
	if pr.HeadSHA != merge.Head || (pr.State != "open" && !pr.Merged) {
		return loomgit.NewError(loomgit.Stale, "native target PR changed after submission", nil)
	}
	if !pr.Merged {
		return nil
	}
	for _, change := range merge.Changes {
		status, err := store.LandingStatus(ctx, merge.Workspace, change)
		if err != nil {
			return err
		}
		if status.State != "landed" {
			return nil
		}
	}
	if merge.Authority == leadMergeAuthority {
		slog.Info("merged by lead under setting set by "+merge.SetBy, "workspace", merge.Workspace, "stack", merge.StackID, "target", merge.Target)
	}
	return store.AdvanceNativeMerge(ctx, merge, "done", merge.Head)
}

// nativeLeadPrefixGreen re-reads the provider just before a lead-policy merge is
// submitted: every layer must still pass its required checks and reviews.
func nativeLeadPrefixGreen(ctx context.Context, store nativeMergeStore, forge nativeMergeForge,
	merge journal.NativeMerge, parts []string) (bool, error) {
	statusForge, ok := forge.(interface {
		PRStatuses(context.Context, string, string, string) (map[string]stackpublish.PRStatus, error)
	})
	if !ok {
		return false, loomgit.NewError(loomgit.AttentionRequired, "provider cannot report required checks", nil)
	}
	for _, change := range merge.Changes {
		publication, found, err := store.Publication(ctx, merge.Workspace, change)
		if err != nil || !found {
			return false, errors.Join(err, loomgit.NewError(loomgit.MergeBlocked, "native stack publication is incomplete", nil))
		}
		statuses, err := statusForge.PRStatuses(ctx, parts[0], parts[1], publication.Branch)
		if err != nil {
			return false, err
		}
		status, found := statuses[publication.Branch]
		if found && status.Number == publication.PRNumber && status.Review == "changes_requested" {
			return false, loomgit.NewError(loomgit.MergeBlocked, "changes requested on "+change, nil)
		}
		if !found || status.Number != publication.PRNumber || !greenStatus(status) {
			return false, nil
		}
	}
	return true, nil
}
