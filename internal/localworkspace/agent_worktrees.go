package localworkspace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

// AgentWorktreePlan is what EnsureAgentWorktrees would do, reported without
// touching the filesystem. Both maps are keyed by repo name.
type AgentWorktreePlan struct {
	Agent    string
	Existing map[string]string
	ToCreate map[string]string
}

// EnsureAgentWorktrees creates (idempotently) every local git worktree an agent
// needs and returns repo name -> worktree path.
//
// An agent whose worktree does not exist is not a cosmetic gap: the supervisor
// treats it as a fatal boot error, so every caller that records an agent on a
// machine holding the workspace checkout must provision here first.
func EnsureAgentWorktrees(wsPath string, repos []Repo, agent domain.Agent) (map[string]string, error) {
	selected, err := selectProvisionableRepos(repos, agent)
	if err != nil {
		return nil, err
	}
	created := make(map[string]string, len(selected))
	for _, repo := range selected {
		target := AgentWorktreePath(wsPath, repo.Name, agent.Name)
		if err := EnsureGitWorktree(repo.Path, target, agent.Name); err != nil {
			return nil, fmt.Errorf("create worktree for repo %q: %w", repo.Name, err)
		}
		created[repo.Name] = target
	}
	if err := RememberAgentWorktree(agent.WorkspaceKey, agent.Name, FirstWorktreePath(created)); err != nil {
		return nil, fmt.Errorf("update local agent state: %w", err)
	}
	return created, nil
}

// PlanAgentWorktrees reports, without touching the filesystem, what
// EnsureAgentWorktrees would do. It returns the same hard errors, so a dry run
// refuses exactly the specs the real run would refuse.
func PlanAgentWorktrees(wsPath string, repos []Repo, agent domain.Agent) (AgentWorktreePlan, error) {
	plan := AgentWorktreePlan{
		Agent:    agent.Name,
		Existing: map[string]string{},
		ToCreate: map[string]string{},
	}
	selected, err := selectProvisionableRepos(repos, agent)
	if err != nil {
		return AgentWorktreePlan{}, err
	}
	for _, repo := range selected {
		target := AgentWorktreePath(wsPath, repo.Name, agent.Name)
		if _, statErr := os.Stat(filepath.Join(target, ".git")); statErr == nil {
			plan.Existing[repo.Name] = target
			continue
		}
		plan.ToCreate[repo.Name] = target
	}
	return plan, nil
}

// selectProvisionableRepos resolves an agent's repo affinity and rejects the
// two shapes that cannot yield a worktree, so both the ensure and the plan path
// fail on the same inputs.
func selectProvisionableRepos(repos []Repo, agent domain.Agent) ([]Repo, error) {
	selected, err := SelectAgentRepos(repos, agent)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("workspace %s has no repos for agent %q", agent.WorkspaceKey, agent.Name)
	}
	for _, repo := range selected {
		if repo.Path == "" {
			return nil, fmt.Errorf("repo %q has no local path on this machine", repo.Name)
		}
	}
	return selected, nil
}
