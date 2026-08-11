package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const ErrorClassLocalWorktreeUnprovisioned = "local_worktree_unprovisioned"

type TaskWorktree struct {
	Path             string
	RepoName         string
	SourceRepoID     string
	RepositoryRemote string
}

type TaskWorktreeResolver interface {
	ResolveTaskWorktree(ctx context.Context, req TaskExecRequest, fallbackPath string) (TaskWorktree, error)
}

// TaskLineageLookup resolves the lineage-derived git base ref a task's worktree
// should be cut from: its predecessor's output branch, or the stack's root base
// for the root unit. ok=false means "no lineage applies to this task" and the
// caller falls back to the repo default branch (current pre-stacking behavior).
// It deliberately returns a *branch ref*, never a commit SHA, so the existing
// remote-first fetch in EnsureDetachedGitWorktreeFromBranch keeps working.
type TaskLineageLookup interface {
	BaseRefForTask(ctx context.Context, workspaceKey, repoName, taskID string) (baseRef string, ok bool, err error)
}

// TaskLineage is the per-task stack-lineage carrier stored in TaskRun input.
type TaskLineage struct {
	StackID      string `json:"stackId,omitempty"`
	BaseRef      string `json:"baseRef,omitempty"`
	OutputBranch string `json:"outputBranch,omitempty"`
}

func (lineage TaskLineage) empty() bool {
	return strings.TrimSpace(lineage.StackID) == "" &&
		strings.TrimSpace(lineage.BaseRef) == "" &&
		strings.TrimSpace(lineage.OutputBranch) == ""
}

type taskLineageEnvelope struct {
	Lineage *TaskLineage `json:"lineage,omitempty"`
}

// WithLineage merges lin into the "lineage" key of an existing Input payload,
// preserving every other key already present.
func WithLineage(input json.RawMessage, lin TaskLineage) (json.RawMessage, error) {
	if lin.empty() {
		return input, nil
	}
	object := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(input))) > 0 {
		if err := json.Unmarshal(input, &object); err != nil {
			return input, nil
		}
	}
	encoded, err := json.Marshal(lin)
	if err != nil {
		return nil, err
	}
	object["lineage"] = encoded
	merged, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	return merged, nil
}

// LineageFromInput extracts the lineage carrier from an Input payload. ok=false
// means no lineage key was present, so callers keep pre-stacking behavior.
func LineageFromInput(input json.RawMessage) (TaskLineage, bool) {
	if len(strings.TrimSpace(string(input))) == 0 {
		return TaskLineage{}, false
	}
	var envelope taskLineageEnvelope
	if err := json.Unmarshal(input, &envelope); err != nil || envelope.Lineage == nil {
		return TaskLineage{}, false
	}
	if envelope.Lineage.empty() {
		return TaskLineage{}, false
	}
	return *envelope.Lineage, true
}

// StackLineageLookup narrows Source Control's stack binding query to the base
// ref lookup consumed by the worktree resolver.
type StackLineageLookup struct {
	Bindings sourcecontrol.StackBindingResolver
}

var _ TaskLineageLookup = StackLineageLookup{}

func (lookup StackLineageLookup) BaseRefForTask(
	ctx context.Context,
	workspaceKey,
	repoName,
	taskID string,
) (string, bool, error) {
	if lookup.Bindings == nil {
		return "", false, nil
	}
	binding, ok, err := lookup.Bindings.ResolveTaskStackBinding(ctx, workspaceKey, repoName, taskID)
	return binding.BaseRef, ok, err
}

// resolveStackRepoName resolves the workspace repo Name a non-local task targets.
func (e HostBridgeTaskExecutor) resolveStackRepoName(ctx context.Context, req TaskExecRequest) string {
	if e.Store == nil {
		return ""
	}
	repos, err := e.Store.Repos().List(ctx, req.WorkspaceKey)
	if err != nil || len(repos) == 0 {
		return ""
	}
	for _, sel := range taskWorktreeRepoSelectors(req) {
		if r := findRepoBySelector(repos, sel); r != nil {
			return r.Name
		}
	}
	if len(repos) == 1 {
		return repos[0].Name
	}
	return ""
}

// stackBindingForTask returns the task's stack binding when the task belongs to
// a stack for repoName.
func stackBindingForTask(
	ctx context.Context,
	resolver sourcecontrol.StackBindingResolver,
	workspaceKey,
	repoName,
	taskID string,
) (TaskLineage, bool, error) {
	if resolver == nil {
		return TaskLineage{}, false, nil
	}
	binding, ok, err := resolver.ResolveTaskStackBinding(ctx, workspaceKey, repoName, taskID)
	if err != nil || !ok {
		return TaskLineage{}, ok, err
	}
	return TaskLineage{
		StackID: binding.StackID, BaseRef: binding.BaseRef, OutputBranch: binding.OutputBranch,
	}, true, nil
}

// finalizeStackNode records the completed task's stack node state/SHA before
// ExecuteTask returns, so dependents read a durable predecessor node.
func (e HostBridgeTaskExecutor) finalizeStackNode(ctx context.Context, req TaskExecRequest, wt TaskWorktree, result TaskExecResult, runErr error) {
	if e.TaskOutcomes == nil || runErr != nil || result.Status != domain.TaskRunCompleted {
		return
	}
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		return
	}
	repoName := strings.TrimSpace(wt.RepoName)
	if repoName == "" {
		repoName = e.resolveStackRepoName(ctx, req)
	}
	if repoName == "" {
		return
	}
	if _, err := e.TaskOutcomes.RecordTaskOutcome(
		ctx,
		sourcecontrol.TaskOutcomeCommand{
			WorkspaceKey: req.WorkspaceKey,
			Repository:   repoName,
			TaskID:       taskID,
			Metadata:     result.RuntimeMetadata,
		},
	); err != nil {
		slog.WarnContext(ctx, "stack finalize barrier: record node failed", "task", taskID, "repo", repoName, "err", err)
	}
}

type LocalTaskWorktreeResolver struct {
	Store taskWorktreeStore
	// SourceControl is the authority-free application materializer. Production
	// task runs fail closed when it is unavailable; neither this resolver nor
	// the local preparer receives Local Settings or a credential source.
	SourceControl sourcecontrol.Materializer
	// Lineage is optional. When nil (the two pre-stacking construction sites and
	// all tests), the worktree base stays the repo default branch. When set, the
	// per-task worktree is cut from the task's lineage base so each stacked task
	// diffs on top of its predecessor — making the worktree base equal the PR base.
	Lineage TaskLineageLookup
}

type taskWorktreeStore interface {
	Repos() store.RepoStore
	WorkerProfiles() store.WorkerProfileStore
}

//nolint:funlen // Resolution validates workspace state, repo selection, checkout, lineage base, and worktree creation.
func (r LocalTaskWorktreeResolver) ResolveTaskWorktree(ctx context.Context, req TaskExecRequest, _ string) (TaskWorktree, error) {
	if r.Store == nil {
		return TaskWorktree{}, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	workspaceKey := strings.TrimSpace(req.WorkspaceKey)
	if workspaceKey == "" {
		return TaskWorktree{}, fmt.Errorf("workspace key required: %w", domain.ErrInvalid)
	}
	taskRunID := strings.TrimSpace(req.TaskRunID)
	if taskRunID == "" {
		return TaskWorktree{}, fmt.Errorf("task run id required: %w", domain.ErrInvalid)
	}
	repos, err := r.Store.Repos().List(ctx, workspaceKey)
	if err != nil {
		return TaskWorktree{}, fmt.Errorf("list workspace repos: %w", err)
	}
	if len(repos) == 0 {
		return TaskWorktree{}, fmt.Errorf("workspace %q has no repos", workspaceKey)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })

	selected, err := r.selectRepo(ctx, workspaceKey, repos, req)
	if err != nil {
		return TaskWorktree{}, err
	}
	if r.SourceControl == nil {
		return TaskWorktree{}, fmt.Errorf("source control materializer required: %w", sourcecontrol.ErrUnavailable)
	}
	repositoryRef := firstNonEmpty(selected.SourceRepoID, selected.Name)
	baseBranch, err := r.baseBranchForTask(ctx, workspaceKey, selected, req)
	if err != nil {
		return TaskWorktree{}, err
	}
	materialized, err := r.SourceControl.PrepareTaskCheckout(
		ctx,
		sourcecontrol.TaskCheckoutCommand{
			WorkspaceKey: workspaceKey, TaskRunID: taskRunID,
			RepositoryRef: repositoryRef,
			BaseBranch:    baseBranch,
		},
	)
	if err != nil {
		return TaskWorktree{}, fmt.Errorf("materialize task run repository %q: %w", selected.Name, err)
	}
	if materialized == nil ||
		materialized.WorkspaceKey != workspaceKey ||
		materialized.TaskRunID != taskRunID ||
		materialized.RepositoryRef != repositoryRef ||
		materialized.CheckoutPath == "" ||
		materialized.BaseRef == "" ||
		materialized.BaseCommit == "" {
		return TaskWorktree{}, fmt.Errorf("source control returned different task checkout coordinates: %w", sourcecontrol.ErrInvalidMaterialization)
	}
	local, err := openTaskWorktreePreparer(workspaceKey)
	if err != nil {
		return TaskWorktree{}, err
	}
	target, err := local.prepare(
		ctx,
		selected,
		materialized.CheckoutPath,
		taskRunID,
		materialized.BaseRef,
		materialized.BaseCommit,
	)
	if err != nil {
		return TaskWorktree{}, err
	}
	return TaskWorktree{
		Path:             target,
		RepoName:         selected.Name,
		SourceRepoID:     firstNonEmpty(selected.SourceRepoID, selected.Name),
		RepositoryRemote: firstNonEmpty(selected.RemoteURL, selected.Remote),
	}, nil
}

func (r LocalTaskWorktreeResolver) selectRepo(ctx context.Context, workspaceKey string, repos []*workspacemodule.Repository, req TaskExecRequest) (*workspacemodule.Repository, error) {
	taskSelectors := taskWorktreeRepoSelectors(req)
	var profileSelectors []string
	if req.WorkerProfileID != "" {
		if r.Store == nil {
			return nil, fmt.Errorf("worker profile store required for repo scope: %w", domain.ErrInvalid)
		}
		profile, err := r.Store.WorkerProfiles().Get(ctx, workspaceKey, req.WorkerProfileID)
		if err != nil {
			return nil, fmt.Errorf("get worker profile %q for repo scope: %w", req.WorkerProfileID, err)
		}
		if profile != nil {
			profileSelectors = normalizeStringList(profile.Repos)
		}
	}

	// Task/run placement is the selector; WorkerProfile.Repos is an allowed
	// scope, not an ordered fallback. Combining them used to make a repo-less
	// task with profile scope [alpha,beta] silently choose alpha.
	for _, selector := range taskSelectors {
		if repo := findRepoBySelector(repos, selector); repo != nil {
			if len(profileSelectors) > 0 {
				inScope := false
				for _, profileSelector := range profileSelectors {
					if repoMatchesExactSelector(repo, profileSelector) {
						inScope = true
						break
					}
				}
				if !inScope {
					return nil, fmt.Errorf("task repo selector %q is outside worker profile %q repo scope", selector, req.WorkerProfileID)
				}
			}
			return repo, nil
		}
	}
	if len(taskSelectors) > 0 {
		return nil, fmt.Errorf("no workspace repo matches task repo selector %q", strings.Join(taskSelectors, ", "))
	}

	if len(profileSelectors) > 0 {
		matches := make([]*workspacemodule.Repository, 0, len(repos))
		for _, repo := range repos {
			for _, selector := range profileSelectors {
				if repoMatchesExactSelector(repo, selector) {
					matches = append(matches, repo)
					break
				}
			}
		}
		switch len(matches) {
		case 0:
			return nil, fmt.Errorf("no workspace repo matches worker profile %q repo scope %q", req.WorkerProfileID, strings.Join(profileSelectors, ", "))
		case 1:
			return matches[0], nil
		default:
			return nil, fmt.Errorf("task repo selector required: worker profile %q scope matches %d workspace repos", req.WorkerProfileID, len(matches))
		}
	}
	if len(repos) == 1 {
		return repos[0], nil
	}
	return nil, fmt.Errorf("task repo selector required: workspace %q has %d repos", workspaceKey, len(repos))
}

func taskWorktreeRepoSelectors(req TaskExecRequest) []string {
	var out []string
	out = append(out, req.RunnerPlacement.RepoRef, req.SandboxPlacement.RepoRef)
	out = append(out, taskInputRepoSelectors(req.Input)...)
	return normalizeStringList(out)
}

func taskInputRepoSelectors(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	var out []string
	collectRepoSelectors(value, 0, &out)
	return out
}

func collectRepoSelectors(value any, depth int, out *[]string) {
	if depth > 3 {
		return
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key, value := range obj {
		if repoSelectorKey(key) {
			if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
				*out = append(*out, s)
			}
			continue
		}
		collectRepoSelectors(value, depth+1, out)
	}
}

func repoSelectorKey(key string) bool {
	switch strings.ToLower(strings.ReplaceAll(key, "_", "")) {
	case "sourcerepo", "reporef", "reponame", "repo", "repository", "githubrepo":
		return true
	default:
		return false
	}
}

func findRepoBySelector(repos []*workspacemodule.Repository, selector string) *workspacemodule.Repository {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil
	}
	want := normalizedRepoToken(selector)
	wantBase := normalizedRepoToken(repoBasename(selector))
	var exact []*workspacemodule.Repository
	for _, repo := range repos {
		if repoMatchesExactSelector(repo, want) {
			exact = append(exact, repo)
		}
	}
	if len(exact) == 1 {
		return exact[0]
	}
	if len(exact) > 1 {
		return nil
	}

	// Backward-compatible basename fallback is allowed only when it identifies
	// exactly one workspace repo. Qualified selectors such as org-a/app must
	// never silently select org-b/app just because both basenames are "app".
	var fallback []*workspacemodule.Repository
	for _, repo := range repos {
		if repo == nil {
			continue
		}
		candidates := []string{
			repo.Name,
			firstNonEmpty(repo.SourceRepoID, repo.Name),
			repo.RemoteURL,
			repo.Remote,
			repoBasename(repo.RemoteURL),
			repoBasename(repo.Remote),
		}
		for _, candidate := range candidates {
			got := normalizedRepoToken(candidate)
			if got != "" && got == wantBase {
				fallback = append(fallback, repo)
				break
			}
		}
	}
	if len(fallback) == 1 {
		return fallback[0]
	}
	return nil
}

func repoMatchesExactSelector(repo *workspacemodule.Repository, selector string) bool {
	if repo == nil {
		return false
	}
	want := normalizedRepoToken(selector)
	if want == "" {
		return false
	}
	for _, candidate := range []string{
		repo.Name,
		firstNonEmpty(repo.SourceRepoID, repo.Name),
		repo.RemoteURL,
		repo.Remote,
		repoRemotePath(repo.RemoteURL),
		repoRemotePath(repo.Remote),
	} {
		if got := normalizedRepoToken(candidate); got != "" && got == want {
			return true
		}
	}
	return false
}

func repoRemotePath(value string) string {
	value = strings.TrimSpace(strings.TrimSuffix(value, ".git"))
	value = strings.TrimRight(value, "/")
	if value == "" {
		return ""
	}
	if scheme := strings.Index(value, "://"); scheme >= 0 {
		rest := value[scheme+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			return rest[slash+1:]
		}
		return ""
	}
	if colon := strings.Index(value, ":"); colon >= 0 && !strings.Contains(value[:colon], "/") {
		return value[colon+1:]
	}
	if slash := strings.Index(value, "/"); slash >= 0 && strings.Contains(value[:slash], ".") {
		return value[slash+1:]
	}
	return value
}

func normalizedRepoToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".git")
	value = strings.Trim(value, "/")
	return value
}

func repoBasename(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".git")
	value = strings.TrimRight(value, "/")
	if value == "" {
		return ""
	}
	if idx := strings.LastIndexAny(value, "/:"); idx >= 0 && idx+1 < len(value) {
		return value[idx+1:]
	}
	return value
}

// baseBranchForTask returns a lineage base when the task belongs to a stack and
// the repository default branch when it does not. An actual lookup failure is
// not equivalent to "no binding" and fails closed instead of silently running
// the task from the wrong base.
func (r LocalTaskWorktreeResolver) baseBranchForTask(
	ctx context.Context,
	workspaceKey string,
	selected *workspacemodule.Repository,
	req TaskExecRequest,
) (string, error) {
	defaultBranch := repoDefaultBranch(selected)
	if r.Lineage == nil {
		return defaultBranch, nil
	}
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		return defaultBranch, nil
	}
	ref, ok, err := r.Lineage.BaseRefForTask(ctx, workspaceKey, selected.Name, taskID)
	if err != nil {
		return "", fmt.Errorf("resolve task stack lineage for %q: %w", taskID, err)
	}
	if ok && strings.TrimSpace(ref) != "" {
		return strings.TrimSpace(ref), nil
	}
	return defaultBranch, nil
}

func repoDefaultBranch(repo *workspacemodule.Repository) string {
	if repo == nil {
		return ""
	}
	return strings.TrimSpace(repo.DefaultBranch)
}
