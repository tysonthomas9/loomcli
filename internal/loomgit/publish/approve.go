package publish

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/gitexec"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
)

// ApprovalOutcome is what Approve and create PR did for one approved change.
type ApprovalOutcome struct {
	Change   string `json:"change"`
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	PRURL    string `json:"pr_url,omitempty"`
	PRNumber int    `json:"pr_number,omitempty"`
}

// NoProviderReason prefixes the outcome of an approval in a workspace with no
// Git provider: the change stays applied and no publish is retried.
const NoProviderReason = "not published: no provider"

// publishRetryDelay spaces background retries after a failed publish, such as
// a provider that is down, so the reconciler does not hammer it.
const publishRetryDelay = 30 * time.Second

var (
	approvalPublishChange   = publishApprovedChange
	approvalProviderMissing = providerMissing
	approvalNow             = time.Now
)

// PublishApproved opens the PR of every change whose Approve and create PR
// approval is now applied in lead's working area (D29). In stack mode the PR
// is the next layer of the lead's stack; in trunk mode it is its own PR to
// trunk. An approval whose apply is held publishes nothing until it applies.
func PublishApproved(ctx context.Context, workspace, lead string) ([]ApprovalOutcome, error) {
	if workspace == "" || lead == "" {
		return nil, errors.New("workspace and lead are required")
	}
	store, err := openLocalStore()
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	return publishApprovals(ctx, store, workspace, lead, true)
}

// ReconcileApprovalPublicationsAt retries open Approve and create PR intents
// in every workspace, for approvals that applied later or whose publish failed.
func ReconcileApprovalPublicationsAt(ctx context.Context, path string) error {
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
	_, err = publishApprovals(ctx, store, "", "", false)
	return err
}

func publishApprovals(ctx context.Context, store *journal.SQLite, workspace, lead string, immediate bool) ([]ApprovalOutcome, error) {
	intents, err := store.OpenApprovalPublications(ctx, workspace, lead)
	if err != nil {
		return nil, err
	}
	var outcomes []ApprovalOutcome
	var failures []error
	for _, intent := range intents {
		if !immediate && intent.AttemptedAt > 0 && approvalNow().Sub(time.Unix(intent.AttemptedAt, 0)) < publishRetryDelay {
			continue
		}
		outcome, attempted, err := publishIntent(ctx, store, intent)
		if attempted {
			outcomes = append(outcomes, outcome)
		}
		if err != nil {
			failures = append(failures, err)
		}
	}
	return outcomes, errors.Join(failures...)
}

func publishIntent(ctx context.Context, store *journal.SQLite, intent journal.ApprovalPublication) (ApprovalOutcome, bool, error) {
	outcome := ApprovalOutcome{Change: intent.Change}
	follow, err := store.ApprovalFollowStatus(ctx, intent.Workspace, intent.Lead, intent.Change, intent.Revision)
	if err != nil {
		return outcome, false, err
	}
	if follow == "superseded" {
		outcome.Status = "superseded"
		return outcome, true, finishIntent(ctx, store, intent, outcome)
	}
	applied, err := store.RevisionApplied(ctx, intent.Workspace, intent.Lead, intent.Change, intent.Revision)
	if err != nil || !applied {
		// Held (apply_pending, conflict, no working area): no PR until it applies.
		return outcome, false, err
	}
	if reason := approvalProviderMissing(ctx, store, intent.Workspace, intent.Change); reason != "" {
		outcome.Status, outcome.Reason = "not_published", NoProviderReason+" ("+reason+")"
		return outcome, true, finishIntent(ctx, store, intent, outcome)
	}
	result, publishErr := approvalPublishChange(ctx, intent.Workspace, intent.Lead, intent.Change)
	if publishErr != nil {
		outcome.Status, outcome.Reason = "pending", publishErr.Error()
		if errors.Is(publishErr, loomgit.NewError(loomgit.LineageUnresolved, "", nil)) {
			outcome.Status = "waiting"
			publishErr = nil
		}
		return outcome, true, errors.Join(publishErr, finishIntent(ctx, store, intent, outcome))
	}
	outcome.Status, outcome.PRURL, outcome.PRNumber = "published", result.PRURL, result.PRNumber
	return outcome, true, finishIntent(ctx, store, intent, outcome)
}

func finishIntent(ctx context.Context, store *journal.SQLite, intent journal.ApprovalPublication, outcome ApprovalOutcome) error {
	intent.Status, intent.Reason = outcome.Status, outcome.Reason
	intent.PRURL, intent.PRNumber = outcome.PRURL, outcome.PRNumber
	intent.AttemptedAt = approvalNow().Unix()
	err := store.SetApprovalPublication(ctx, intent)
	if errors.Is(err, journal.ErrStale) {
		// Another publisher finished this intent first.
		return nil
	}
	return err
}

// publishApprovedChange opens or refreshes the change's PR in the workspace's
// delivery mode, the same as Create PR.
func publishApprovedChange(ctx context.Context, workspace, lead, change string) (Result, error) {
	mode, err := DeliveryModeLocal(ctx, workspace)
	if err != nil {
		return Result{}, err
	}
	if mode == "stack" {
		return PublishLeadChangeLocal(ctx, workspace, lead, change)
	}
	return PublishLocal(ctx, workspace, lead, change)
}

// providerMissing names why the change's repository has no Git provider to
// open a PR on, or returns "" when one is configured.
func providerMissing(ctx context.Context, store *journal.SQLite, workspace, change string) string {
	if forge, _, _ := localPublishProvider(); forge != nil {
		return ""
	}
	repoName, err := store.RepoForChange(ctx, workspace, change)
	if err != nil {
		return ""
	}
	cfg, err := config.LoadConfig()
	if err != nil {
		return ""
	}
	_, configured, found := config.WorkspaceByID(cfg, workspace)
	if !found {
		return ""
	}
	for _, repo := range configured.Repos {
		if repo.Name != repoName {
			continue
		}
		runner, err := gitexec.New(repo.ResolveAbsPath(configured.Path), gitexec.Options{})
		if err != nil {
			return ""
		}
		remote, err := runner.Run(ctx, "remote", "get-url", "--push", "origin")
		if err != nil {
			return "the repository has no origin remote"
		}
		if _, err := GitHubSlug(strings.TrimSpace(string(remote))); err != nil {
			return "origin is not a GitHub repository"
		}
		if githubtoken.GitHub(ctx) == "" {
			return "no GitHub credential on this host"
		}
		return ""
	}
	return ""
}
