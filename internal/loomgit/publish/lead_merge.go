package publish

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/githubtoken"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/journal"
	"github.com/tysonthomas9/loomcli/internal/loomgit/landing"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

const leadMergeAuthority = "lead_may_merge"

// Agent runtimes set these; a policy change carrying any of them is refused
// (advisory in local mode, D28).
var agentEnvMarkers = []string{"LOOM_AGENT_", "LOOM_TASK_RUN_", "LOOM_DRIVER_", "LOOM_LEAD_CONTROLLED", "LOOM_WORKTREE_PATH",
	"LOOM_ORCHESTRATOR_SESSION_ID"}

type leadMergeForge interface {
	Forge
	loomMergeForge
}

// whenGreenMerge authorizes a lead merge only while the human-set policy is on.
type whenGreenMerge struct {
	Store *journal.SQLite
	SetBy string
}

func (authority whenGreenMerge) AuthorizeMerge(ctx context.Context, request StackRequest, _ string) error {
	policy, err := authority.Store.LeadMayMerge(ctx, request.Workspace)
	if err != nil {
		return err
	}
	if policy.Value != "when_green" {
		return errors.New("lead_may_merge is off")
	}
	return nil
}

// SetWorkspacePolicy changes lead_may_merge. Only a human may call it.
func SetWorkspacePolicy(ctx context.Context, store *journal.SQLite, workspace, leadMayMerge string,
	actor review.Actor, env []string) (string, error) {
	if err := requireHumanPolicyActor(actor, env); err != nil {
		return "", err
	}
	if err := store.SetLeadMayMerge(ctx, workspace, leadMayMerge, actor.ID); err != nil {
		return "", err
	}
	if leadMayMerge == "when_green" {
		return journal.LeadMayMergeWarning, nil
	}
	return "", nil
}

// requireHumanPolicyActor refuses a policy change that is not a human's or
// that carries an agent runtime marker.
func requireHumanPolicyActor(actor review.Actor, env []string) error {
	if actor.Kind != "human" || strings.TrimSpace(actor.ID) == "" {
		return loomgit.NewError(loomgit.MergeNotAuthorized, "only a human can change workspace policy", nil)
	}
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		for _, marker := range agentEnvMarkers {
			if strings.HasPrefix(name, marker) {
				return loomgit.NewError(loomgit.MergeNotAuthorized, "workspace policy change carries agent marker "+name, nil)
			}
		}
	}
	return nil
}

func SetWorkspacePolicyLocal(ctx context.Context, workspace, leadMayMerge string, actor review.Actor, env []string) (string, error) {
	store, err := openLocalStore()
	if err != nil {
		return "", err
	}
	defer func() { _ = store.Close() }()
	return SetWorkspacePolicy(ctx, store, workspace, leadMayMerge, actor, env)
}

func LeadMayMergeLocal(ctx context.Context, workspace string) (journal.LeadMergePolicy, error) {
	store, err := openLocalStore()
	if err != nil {
		return journal.LeadMergePolicy{}, err
	}
	defer func() { _ = store.Close() }()
	return store.LeadMayMerge(ctx, workspace)
}

func ReconcileLeadMerges(ctx context.Context) error {
	return ReconcileLeadMergesAt(ctx, filepath.Join(config.GetConfigDir(), "loomgit", "store.db"),
		stackpublish.NewConfiguredGitHubForge(githubtoken.GitHub(ctx)))
}

// ReconcileLeadMergesAt starts a merge up to the highest green layer of each
// Loom stack whose workspace lets the lead merge when green. The P3.9 merge
// machine then lands it one layer at a time.
func ReconcileLeadMergesAt(ctx context.Context, path string, forge leadMergeForge) error {
	store, err := journal.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	stacks, err := store.LeadMergeStacks(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, stack := range stacks {
		if err := startLeadMerge(ctx, store, forge, stack); err != nil {
			slog.Warn("lead merge not started", "workspace", stack.Workspace, "stack", stack.StackID, "err", err)
			failures = append(failures, fmt.Errorf("lead merge %s/%s: %w", stack.Workspace, stack.StackID, err))
		}
	}
	prs, err := store.LeadMergeTrunkPRs(ctx)
	if err != nil {
		return errors.Join(append(failures, err)...)
	}
	for _, pr := range prs {
		if err := startTrunkLeadMerge(ctx, store, forge, pr); err != nil {
			slog.Warn("lead merge not started", "workspace", pr.Workspace, "change", pr.Change, "err", err)
			failures = append(failures, fmt.Errorf("lead merge %s/%s: %w", pr.Workspace, pr.Change, err))
		}
	}
	return errors.Join(failures...)
}

// leadMergeActor is how a lead merge under the when_green setting is recorded
// on a PR to trunk: it merges on the human's setting, never on its own say.
func leadMergeActor(setBy string) string { return "lead under setting set by " + setBy }

// startTrunkLeadMerge merges a PR to trunk (PR per task) on its own, with no
// stack order, once it is green: Loom approved its head, its blockers have
// landed and the provider's required checks pass. It records a lead-owned
// merge approval that the Approve and merge machine then merges once and
// follows. A PR that is not green gets nothing, so it holds back no other PR.
// A finished merge for the same head (cancelled by a human, stale) is not
// restarted, except one cancelled only because the setting was off.
func startTrunkLeadMerge(ctx context.Context, store *journal.SQLite, forge leadMergeForge, trunk journal.LeadMergeTrunkPR) error {
	publication, found, err := store.Publication(ctx, trunk.Workspace, trunk.Change)
	if err != nil || !found {
		return err
	}
	existing, found, err := store.MergeApproval(ctx, trunk.Workspace, trunk.Change)
	if err != nil || (found && (journal.MergeApprovalActive(existing.Status) ||
		(existing.ApprovedHead == publication.Head && !policyCancelled(existing)))) {
		return err
	}
	if green, err := trunkLeadMergeReady(ctx, store, forge, publication); err != nil || !green {
		return err
	}
	approval := journal.MergeApproval{Workspace: trunk.Workspace, Change: trunk.Change, Head: publication.Head,
		ActorKind: "lead", ActorID: leadMergeActor(trunk.SetBy), Status: MergeApprovalWaiting,
		CreatedAt: mergeApprovalNow().UnixNano()}
	recorded, err := store.RecordMergeApproval(ctx, approval)
	if errors.Is(err, journal.ErrStale) {
		// A human's Approve and merge got there first.
		return nil
	}
	if err != nil {
		return err
	}
	return advanceMergeApproval(ctx, store, forge, recorded)
}

// policyCancelled reports a lead merge stopped only because the setting was
// off; it starts again once the human turns the setting back on.
func policyCancelled(approval journal.MergeApproval) bool {
	return approval.ActorKind == "lead" && approval.Status == MergeApprovalCancelled &&
		approval.Reason == "cancelled: lead_may_merge is off"
}

// trunkLeadMergeReady reports whether the lead may merge publication's PR now.
func trunkLeadMergeReady(ctx context.Context, store *journal.SQLite, forge leadMergeForge, publication journal.Publication) (bool, error) {
	if hold, err := trunkLeadMergeHold(ctx, store, publication); err != nil || hold != "" {
		return false, err
	}
	owner, repo, ok := strings.Cut(publication.Slug, "/")
	if !ok || publication.DriftSHA != "" {
		return false, nil
	}
	pr, err := forge.PullByNumber(ctx, owner, repo, publication.PRNumber)
	if err != nil || pr.Merged || pr.State != "open" || pr.HeadSHA != publication.Head {
		return false, err
	}
	statuses, err := forge.PRStatuses(ctx, owner, repo, publication.Branch)
	if err != nil {
		return false, err
	}
	status, found := statuses[publication.Branch]
	return found && status.Number == pr.Number && greenStatus(status), nil
}

// trunkLeadMergeHold says why Loom itself holds a lead merge of publication's
// PR back, or "": the human setting, Loom's approval of the PR head, and the
// task's blockers having landed are re-read every time.
func trunkLeadMergeHold(ctx context.Context, store *journal.SQLite, publication journal.Publication) (string, error) {
	policy, err := store.LeadMayMerge(ctx, publication.Workspace)
	if err != nil {
		return "", err
	}
	if policy.Value != "when_green" {
		return "cancelled: lead_may_merge is off", nil
	}
	if publication.StackID != "" {
		return "cancelled: the PR is part of a stack", nil
	}
	if requireNativeVerdict(ctx, store, publication) != nil {
		return "the PR head has no approved revision", nil
	}
	status, found, err := landing.TaskDependencies(ctx, store, publication.Workspace, publication.Change, mergePredecessors)
	if err != nil {
		return "", err
	}
	if found && status.State != "success" {
		return status.Description, nil
	}
	return "", nil
}

func startLeadMerge(ctx context.Context, store *journal.SQLite, forge leadMergeForge, stack journal.LeadMergeStack) error {
	if stack.Backend == "native" {
		return startNativeLeadMerge(ctx, store, forge, stack)
	}
	existing, err := store.LoomMerge(ctx, stack.Workspace, stack.StackID)
	if err == nil && existing.Phase != "done" && existing.Phase != "blocked" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if stack.Lead == "" {
		return nil
	}
	run, err := greenRun(ctx, store, forge, stack)
	if err != nil || len(run) == 0 {
		return err
	}
	target := run[len(run)-1]
	if existing.Target == target {
		return nil
	}
	request, err := leadMergeRequest(ctx, store, forge, stack, target)
	if err != nil {
		return err
	}
	return beginLoomMerge(ctx, store, request, target)
}

// startNativeLeadMerge hands the provider one merge of the unmerged green
// prefix. A finished merge gives way to the next prefix; an open one, or one
// that already covered this target, is left alone.
func startNativeLeadMerge(ctx context.Context, store *journal.SQLite, forge leadMergeForge, stack journal.LeadMergeStack) error {
	existing, err := store.NativeMerge(ctx, stack.Workspace, stack.StackID)
	if err == nil && existing.Phase != "done" && existing.Phase != "blocked" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if stack.Lead == "" {
		return nil
	}
	run, err := greenRun(ctx, store, forge, stack)
	if err != nil || len(run) == 0 {
		return err
	}
	target := run[len(run)-1]
	if existing.Target == target {
		return nil
	}
	request, err := leadMergeRequest(ctx, store, forge, stack, target)
	if err != nil {
		return err
	}
	request.Changes = run
	return GitHubStackBackend{Store: store}.MergeUpTo(ctx, request, target)
}

func leadMergeRequest(ctx context.Context, store *journal.SQLite, forge leadMergeForge,
	stack journal.LeadMergeStack, target string) (StackRequest, error) {
	publication, _, err := store.Publication(ctx, stack.Workspace, target)
	if err != nil {
		return StackRequest{}, err
	}
	view, err := appliedMergeView(ctx, store, stack.Workspace, stack.Lead, stack.StackID, target, publication)
	if err != nil {
		return StackRequest{}, err
	}
	request, err := mergeEntryRequest(ctx, store, stack.Workspace, stack.Lead, stack.StackID, view, forge)
	request.MergeAuthority = whenGreenMerge{Store: store, SetBy: stack.SetBy}
	return request, err
}

// greenRun walks the stack bottom-up and returns the unmerged layers of the
// unbroken run whose required checks and reviews pass on the provider.
func greenRun(ctx context.Context, store *journal.SQLite, forge loomMergeForge, stack journal.LeadMergeStack) ([]string, error) {
	publications, err := store.StackPublications(ctx, stack.Workspace, stack.StackID)
	if err != nil {
		return nil, err
	}
	var run []string
	for _, publication := range publications {
		owner, repo, ok := strings.Cut(publication.Slug, "/")
		if !ok || publication.Phase != "done" || publication.PRNumber == 0 {
			return run, nil
		}
		pr, err := forge.PullByNumber(ctx, owner, repo, publication.PRNumber)
		if err != nil {
			return nil, err
		}
		if pr.Merged {
			continue
		}
		statuses, err := forge.PRStatuses(ctx, owner, repo, publication.Branch)
		if err != nil {
			return nil, err
		}
		status, found := statuses[publication.Branch]
		if !found || status.Number != pr.Number || pr.State != "open" || pr.HeadSHA != publication.Head ||
			!greenStatus(status) || requireNativeVerdict(ctx, store, publication) != nil {
			return run, nil
		}
		run = append(run, publication.Change)
	}
	return run, nil
}

func greenStatus(status stackpublish.PRStatus) bool {
	return requiredChecksPass(status) && reviewMet(status) && status.Mergeable == "mergeable"
}

// requiredChecksPass follows branch protection: the provider's merge state
// clears a PR whose only failing or pending checks are optional.
func requiredChecksPass(status stackpublish.PRStatus) bool {
	switch status.MergeState {
	case "clean", "unstable", "has_hooks":
		return true
	}
	return status.Checks == "passing" || status.Checks == "none"
}

// reviewMet follows the provider's rule: "none" means the repo requires no review.
func reviewMet(status stackpublish.PRStatus) bool {
	return status.Review == "approved" || status.Review == "none"
}

// cancelLeadMerge stops a queued lead merge once the human turns the policy off.
// A layer that may already have reached the provider is left to finish.
func cancelLeadMerge(ctx context.Context, store *journal.SQLite, merge journal.LoomMerge) (bool, error) {
	queued := merge.Phase == "ready" || (merge.Phase == "dispatching" && merge.DispatchAttempts == 0 && merge.ProviderRequestID == "")
	if merge.Authority != leadMergeAuthority || !queued {
		return false, nil
	}
	policy, err := store.LeadMayMerge(ctx, merge.Workspace)
	if err != nil || policy.Value == "when_green" {
		return false, err
	}
	return true, setLoomPhase(ctx, store, merge, "blocked", merge.Index, "cancelled: lead_may_merge is off")
}

// recordLayerMerged notes who merged a layer before the machine moves on.
func recordLayerMerged(ctx context.Context, store *journal.SQLite, merge journal.LoomMerge) error {
	if merge.Authority == leadMergeAuthority {
		slog.Info("merged by lead under setting set by "+merge.PolicySetBy,
			"workspace", merge.Workspace, "stack", merge.StackID, "change", merge.Layers[merge.Index].Change)
		layers := append([]journal.LoomMergeLayer(nil), merge.Layers...)
		layers[merge.Index].MergedBy = "lead under setting set by " + merge.PolicySetBy
		merge.Layers = layers
	}
	if merge.Authority == humanApprovalAuthority {
		layers := append([]journal.LoomMergeLayer(nil), merge.Layers...)
		layers[merge.Index].MergedBy = "Approve and merge by " + merge.PolicySetBy
		merge.Layers = layers
	}
	return setLoomPhase(ctx, store, merge, "landing", merge.Index, "")
}
