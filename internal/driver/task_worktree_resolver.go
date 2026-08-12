package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver/taskworktree"
	"github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const ErrorClassLocalWorktreeUnprovisioned = "local_worktree_unprovisioned"

type TaskWorktree struct {
	Path         string
	RepoName     string
	SourceRepoID string
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

// TaskLineage is the per-task stack-lineage carrier. It rides inside the
// existing TaskExecRequest.Input payload under the namespaced "lineage" key so
// it travels verbatim to a runner, including a daytona sandbox that never reads
// the host stackstore.
type TaskLineage struct {
	StackID      string `json:"stackId,omitempty"`
	BaseRef      string `json:"baseRef,omitempty"`
	OutputBranch string `json:"outputBranch,omitempty"`
}

// Empty reports whether the carrier holds no lineage at all.
func (l TaskLineage) Empty() bool {
	return strings.TrimSpace(l.StackID) == "" &&
		strings.TrimSpace(l.BaseRef) == "" &&
		strings.TrimSpace(l.OutputBranch) == ""
}

type lineageEnvelope struct {
	Lineage *TaskLineage `json:"lineage,omitempty"`
}

// WithLineage merges lin into the "lineage" key of an existing Input payload,
// preserving every other key already present.
func WithLineage(input json.RawMessage, lin TaskLineage) (json.RawMessage, error) {
	if lin.Empty() {
		return input, nil
	}
	obj := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(input))) > 0 {
		if err := json.Unmarshal(input, &obj); err != nil {
			return input, nil
		}
	}
	encoded, err := json.Marshal(lin)
	if err != nil {
		return nil, err
	}
	obj["lineage"] = encoded
	merged, err := json.Marshal(obj)
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
	var env lineageEnvelope
	if err := json.Unmarshal(input, &env); err != nil || env.Lineage == nil {
		return TaskLineage{}, false
	}
	if env.Lineage.Empty() {
		return TaskLineage{}, false
	}
	return *env.Lineage, true
}

// StackLineageLookup adapts stackstore into the TaskLineageLookup consumed by
// the worktree resolver.
type StackLineageLookup struct {
	Store stackstore.Store
}

var _ TaskLineageLookup = StackLineageLookup{}

// BaseRefForTask returns the lineage base branch for taskID, scoped to repoName.
func (l StackLineageLookup) BaseRefForTask(ctx context.Context, workspaceKey, repoName, taskID string) (string, bool, error) {
	st, node, byTask, ok, err := findTaskStack(ctx, l.Store, workspaceKey, repoName, taskID)
	if err != nil || !ok {
		return "", false, err
	}
	base, err := stacklineage.BaseBranchSliding(st, node, byTask)
	if err != nil {
		return "", false, err
	}
	return base, true, nil
}

// DefaultStackLineageLookup returns a lineage lookup backed by the per-user loom
// stack store, or nil when the loom directory cannot be resolved.
func DefaultStackLineageLookup() TaskLineageLookup {
	store, err := stackstore.Default()
	if err != nil {
		return nil
	}
	return StackLineageLookup{Store: store}
}

// DefaultStackStore returns the per-user loom stack store, or nil when the loom
// directory cannot be resolved.
func DefaultStackStore() stackstore.Store {
	store, err := stackstore.Default()
	if err != nil {
		return nil
	}
	return store
}

// findTaskStack locates the single stack scoped to repoName that contains taskID.
func findTaskStack(ctx context.Context, store stackstore.Store, workspaceKey, repoName, taskID string) (stacklineage.Stack, stacklineage.Node, map[string]stacklineage.Node, bool, error) {
	taskID = strings.TrimSpace(taskID)
	repoName = strings.TrimSpace(repoName)
	if store == nil || taskID == "" || repoName == "" {
		return stacklineage.Stack{}, stacklineage.Node{}, nil, false, nil
	}
	stacks, err := store.ListStacks(ctx, workspaceKey)
	if err != nil {
		return stacklineage.Stack{}, stacklineage.Node{}, nil, false, err
	}
	var (
		foundStack  stacklineage.Stack
		foundNode   stacklineage.Node
		foundByTask map[string]stacklineage.Node
		found       bool
	)
	for _, st := range stacks {
		if strings.TrimSpace(st.RepoName) == "" || st.RepoName != repoName {
			continue
		}
		nodes, err := store.ListNodes(ctx, workspaceKey, st.ID)
		if err != nil {
			return stacklineage.Stack{}, stacklineage.Node{}, nil, false, err
		}
		byTask := stacklineage.ByTask(nodes)
		node, ok := byTask[taskID]
		if !ok {
			continue
		}
		if found {
			return stacklineage.Stack{}, stacklineage.Node{}, nil, false, nil
		}
		foundStack, foundNode, foundByTask, found = st, node, byTask, true
	}
	if !found {
		return stacklineage.Stack{}, stacklineage.Node{}, nil, false, nil
	}
	return foundStack, foundNode, foundByTask, true, nil
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
func stackBindingForTask(ctx context.Context, store stackstore.Store, workspaceKey, repoName, taskID string) (TaskLineage, bool, error) {
	st, node, byTask, ok, err := findTaskStack(ctx, store, workspaceKey, repoName, taskID)
	if err != nil || !ok {
		return TaskLineage{}, false, err
	}
	base, err := stacklineage.BaseBranchSliding(st, node, byTask)
	if err != nil {
		return TaskLineage{}, false, err
	}
	return TaskLineage{
		StackID:      string(st.ID),
		BaseRef:      base,
		OutputBranch: node.OutputBranch,
	}, true, nil
}

// finalizeStackNode records the completed task's stack node state/SHA before
// ExecuteTask returns, so dependents read a durable predecessor node.
func (e HostBridgeTaskExecutor) finalizeStackNode(ctx context.Context, req TaskExecRequest, wt TaskWorktree, result TaskExecResult, runErr error) {
	if e.StackStore == nil || runErr != nil || result.Status != domain.TaskRunCompleted {
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
	state, sha, ok := stackOutcome(result.RuntimeMetadata)
	if !ok {
		return
	}
	if _, err := recordStackOutput(ctx, e.StackStore, req.WorkspaceKey, repoName, taskID, state, sha); err != nil {
		slog.WarnContext(ctx, "stack finalize barrier: record node failed", "task", taskID, "repo", repoName, "err", err)
	}
}

// stackOutcome maps runtime metadata to the stack-node state the finalize
// barrier should record.
func stackOutcome(meta map[string]string) (state stacklineage.NodeState, outputSHA string, ok bool) {
	if meta == nil {
		return "", "", false
	}
	sha := firstNonEmpty(meta["github_commit_sha"], meta["github_head_sha"], meta["head_sha"], meta["output_sha"])
	switch {
	case strings.TrimSpace(meta["github_branch"]) != "" || meta["delivery"] == "pull_request":
		return stacklineage.NodeStatePublished, sha, true
	case meta["delivery"] == "pull_request_skipped_no_changes" || meta["files_changed"] == "0":
		return stacklineage.NodeStateEmpty, "", true
	default:
		return "", "", false
	}
}

// recordStackOutput records the task node state in the stack store.
func recordStackOutput(ctx context.Context, store stackstore.Store, workspaceKey, repoName, taskID string, state stacklineage.NodeState, outputSHA string) (recorded bool, err error) {
	if store == nil || state == "" {
		return false, nil
	}
	st, _, _, ok, err := findTaskStack(ctx, store, workspaceKey, repoName, taskID)
	if err != nil || !ok {
		return false, err
	}
	now := time.Now().UTC()
	updateErr := store.UpdateNode(ctx, workspaceKey, st.ID, taskID, func(n *stacklineage.Node) error {
		n.State = state
		if strings.TrimSpace(outputSHA) != "" {
			n.OutputSHA = strings.TrimSpace(outputSHA)
		}
		if state == stacklineage.NodeStatePublished {
			n.LastPublishedAt = &now
		}
		return nil
	})
	if updateErr != nil {
		return false, updateErr
	}
	return true, nil
}

type LocalTaskWorktreeResolver struct {
	Store store.Store
	// LocalSettingsDir is passed to the taskworktree boundary, which resolves
	// private HTTPS credentials just in time for clone and fetch. Empty
	// preserves anonymous/SSH/local git behavior.
	LocalSettingsDir string
	// Lineage is optional. When nil (the two pre-stacking construction sites and
	// all tests), the worktree base stays the repo default branch. When set, the
	// per-task worktree is cut from the task's lineage base so each stacked task
	// diffs on top of its predecessor — making the worktree base equal the PR base.
	Lineage TaskLineageLookup
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
	local, err := taskworktree.OpenWithLocalSettings(workspaceKey, r.LocalSettingsDir)
	if err != nil {
		return TaskWorktree{}, err
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
	target, err := local.Prepare(ctx, selected, taskRunID, func() string {
		return r.baseBranchForTask(ctx, workspaceKey, selected, req)
	})
	if err != nil {
		return TaskWorktree{}, err
	}
	return TaskWorktree{
		Path:         target,
		RepoName:     selected.Name,
		SourceRepoID: firstNonEmpty(selected.SourceRepoID, selected.Name),
	}, nil
}

func (r LocalTaskWorktreeResolver) selectRepo(ctx context.Context, workspaceKey string, repos []*domain.Repo, req TaskExecRequest) (*domain.Repo, error) {
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
		matches := make([]*domain.Repo, 0, len(repos))
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

func findRepoBySelector(repos []*domain.Repo, selector string) *domain.Repo {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil
	}
	want := normalizedRepoToken(selector)
	wantBase := normalizedRepoToken(repoBasename(selector))
	var exact []*domain.Repo
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
	var fallback []*domain.Repo
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

func repoMatchesExactSelector(repo *domain.Repo, selector string) bool {
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

// baseBranchForTask returns the git ref the task's worktree should be cut from.
// With no lineage lookup wired (or no lineage for the task) it returns the repo
// default branch — byte-identical to the pre-stacking behavior. With lineage, it
// returns the predecessor's output branch (or the stack root base). A lookup that
// cannot resolve a lineage base (e.g. the predecessor has not published its branch
// yet) falls back to the default branch rather than failing the run; the Stage-2
// finalize barrier is what guarantees the predecessor branch exists before a
// dependent dispatches, and the Stage-2 sliding resolver handles empty ancestors.
func (r LocalTaskWorktreeResolver) baseBranchForTask(ctx context.Context, workspaceKey string, selected *domain.Repo, req TaskExecRequest) string {
	fallback := repoDefaultBranch(selected)
	if r.Lineage == nil {
		return fallback
	}
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		return fallback
	}
	ref, ok, err := r.Lineage.BaseRefForTask(ctx, workspaceKey, selected.Name, taskID)
	if err != nil {
		// Lineage resolution is best-effort on the task-dispatch hot path: a
		// corrupt/unreadable stack store or a corrupt lineage graph must not fail
		// an otherwise-valid task run (pre-stacking, this path read no store at
		// all). Log so corruption stays observable, then fall back to the default
		// branch — byte-identical to pre-stacking behavior.
		slog.WarnContext(ctx, "lineage base lookup failed; using repo default branch",
			"task", taskID, "repo", selected.Name, "err", err)
		return fallback
	}
	if ok && strings.TrimSpace(ref) != "" {
		return strings.TrimSpace(ref)
	}
	return fallback
}

func repoDefaultBranch(repo *domain.Repo) string {
	if repo == nil {
		return ""
	}
	return strings.TrimSpace(repo.DefaultBranch)
}
