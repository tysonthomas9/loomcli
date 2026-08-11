package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/taskdelivery"
)

func resolveDaemonTaskPrompt(ctx context.Context, issues backend.IssueBackend, agentName, taskID string, ws *config.WorkspaceConfig, backendName string) (string, taskdelivery.Plan, error) {
	if taskID == "" {
		return GenerateTaskPrompt(agentName, ws, taskParentID, backendName), taskdelivery.Plan{}, nil
	}
	plan, err := resolveDaemonTaskDelivery(ctx, issues, taskID)
	if err != nil {
		return "", taskdelivery.Plan{}, err
	}
	return GenerateFleetTaskPromptForHostDelivery(agentName, taskID, ws, backendName, plan.Requirement), plan, nil
}

func mustResolveDaemonTaskPrompt(issues backend.IssueBackend, agentName, taskID string) (string, taskdelivery.Plan) {
	ws, _ := config.ResolveActiveWorkspace()
	prompt, plan, err := resolveDaemonTaskPrompt(cmdstore.RootContext(), issues, agentName, taskID, ws, cli.GetBackendName())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: resolve task delivery: %v\n", err)
		cli.ExitWithFlush(1)
	}
	return prompt, plan
}

func recordTaskDeliveryPlan(session *sessions.Session, plan taskdelivery.Plan) {
	if session == nil || plan.PlanID == "" {
		return
	}
	session.Meta.TaskDeliveryPlanID = plan.PlanID
	session.Meta.TaskDeliveryRequirement = string(plan.Requirement)
	session.Meta.TaskDeliveryPolicySource = string(plan.PolicySource)
}

func resolveDaemonTaskDelivery(ctx context.Context, issues backend.IssueBackend, taskID string) (taskdelivery.Plan, error) {
	handle, err := cmdstore.OpenStore(ctx)
	if err != nil {
		return taskdelivery.Plan{}, fmt.Errorf("open task delivery settings: %w", err)
	}
	defer func() { _ = handle.Close() }()

	workspaceKey, err := cmdstore.ActiveWorkspace(ctx, handle.Store)
	if err != nil {
		return taskdelivery.Plan{}, fmt.Errorf("resolve task delivery workspace: %w", err)
	}
	workspace, err := handle.Store.Workspaces().Get(ctx, workspaceKey)
	if err != nil {
		return taskdelivery.Plan{}, fmt.Errorf("load task delivery workspace: %w", err)
	}

	repoName := strings.TrimSpace(os.Getenv("LOOM_AGENT_REPO"))
	if issues != nil && taskID != "" {
		if issue, issueErr := issues.Get(ctx, taskID); issueErr == nil && issue != nil && issue.SourceRepo != "" {
			repoName = issue.SourceRepo
		}
	}
	repo, repoName, err := resolveDeliveryRepo(ctx, handle.Store, workspaceKey, repoName)
	if err != nil {
		return taskdelivery.Plan{}, err
	}

	resolution, err := taskdelivery.Resolve(taskdelivery.ResolveInput{
		WorkspaceRequirement:  workspace.TaskDeliveryRequirement,
		RepositoryRequirement: repoRequirement(repo),
	})
	if err != nil {
		return taskdelivery.Plan{}, err
	}
	runID := strings.TrimSpace(os.Getenv("LOOM_SESSION_ID"))
	if runID == "" {
		runID = "daemon-task:" + taskID
	}
	return taskdelivery.Freeze(taskdelivery.FreezeInput{
		RunID:        runID,
		WorkspaceKey: workspaceKey,
		Repository:   repoName,
		Resolution:   resolution,
	})
}

func resolveDeliveryRepo(ctx context.Context, s store.Store, workspaceKey, selector string) (*domain.Repo, string, error) {
	repos, err := s.Repos().List(ctx, workspaceKey)
	if err != nil {
		return nil, "", fmt.Errorf("list task delivery repositories: %w", err)
	}
	if selector != "" {
		for _, repo := range repos {
			if repo.Name == selector || repo.SourceRepoID == selector {
				return repo, repo.Name, nil
			}
		}
		return nil, "", fmt.Errorf("task delivery repository %q is not registered", selector)
	}
	if len(repos) == 1 {
		return repos[0], repos[0].Name, nil
	}
	return nil, "", nil
}

func repoRequirement(repo *domain.Repo) domain.TaskDeliveryRequirement {
	if repo == nil {
		return ""
	}
	return repo.TaskDeliveryRequirement
}

func finalizeDaemonTaskDelivery(ctx context.Context, issues backend.IssueBackend, plan taskdelivery.Plan, taskID, worktreePath, beforeSHA string) (taskdelivery.Receipt, error) {
	afterSHA, err := cli.RunGitCommand(worktreePath, "rev-parse", "HEAD")
	if err != nil {
		return taskdelivery.Receipt{}, fmt.Errorf("read delivered checkout HEAD: %w", err)
	}
	status, err := cli.RunGitCommand(worktreePath, "status", "--porcelain")
	if err != nil {
		return taskdelivery.Receipt{}, fmt.Errorf("read delivered checkout status: %w", err)
	}
	receipt, err := taskdelivery.AcceptCommittedCheckout(plan, beforeSHA, strings.TrimSpace(afterSHA), daemonCheckoutClean(status))
	if err != nil {
		return taskdelivery.Receipt{}, err
	}
	if issues == nil {
		return taskdelivery.Receipt{}, fmt.Errorf("issue backend required to close delivered task")
	}
	if _, err := issues.Close(ctx, taskID, backend.CloseParams{
		Reason:  "completed after host-verified task delivery",
		Session: os.Getenv("LOOM_SESSION_ID"),
	}); err != nil {
		return taskdelivery.Receipt{}, fmt.Errorf("close host-delivered task: %w", err)
	}
	if err := writeCompletionSignal(worktreePath); err != nil {
		return taskdelivery.Receipt{}, fmt.Errorf("signal host-delivered task completion: %w", err)
	}
	return receipt, nil
}

// daemonCheckoutClean reports whether the agent left a clean, committed
// checkout. Loom's daemon writes lock and checkpoint bookkeeping at the root of
// the same worktree, so those untracked files are not delivery changes. Only
// untracked entries with the exact Loom-owned root filenames are ignored;
// tracked changes and similarly named files elsewhere remain dirty.
func daemonCheckoutClean(status string) bool {
	ignored := map[string]struct{}{
		cli.LockFileName:                   {},
		cli.LockFileName + ".flock":        {},
		config.CheckpointFileName:          {},
		config.CheckpointFileName + ".tmp": {},
	}
	for _, line := range strings.Split(status, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "?? ") {
			if _, ok := ignored[strings.TrimPrefix(line, "?? ")]; ok {
				continue
			}
		}
		return false
	}
	return true
}
