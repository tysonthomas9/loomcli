package localworkspace

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

// LocalWorkspaceView is the machine-local view of a workspace that agent
// worktree materialization needs: the workspace checkout root and the repos
// under it, each carrying its on-disk path. An empty Root means the workspace
// has no checkout on this machine.
type LocalWorkspaceView struct {
	Root  string
	Repos []Repo
}

// ResolveLocalWorkspaceFunc resolves the machine-local view of a workspace.
// Each surface supplies its own: the webui builds it from fleet-db-backed
// workspace data, the CLI reads the per-machine state cache. Errors are passed
// through to the caller untouched so every surface keeps its own error
// vocabulary for the lookups it owns.
type ResolveLocalWorkspaceFunc func(ctx context.Context, workspaceKey string) (LocalWorkspaceView, error)

// SkipAgentFunc reports whether an agent needs no local worktrees at all —
// today, agents on interactive roles. A nil SkipAgentFunc never skips.
type SkipAgentFunc func(ctx context.Context, agent domain.Agent) (bool, error)

// AgentWorktreeMaterializer creates the local git worktrees an agent needs.
// Its Materialize method has the shape callers inject as a materializer
// dependency: func(context.Context, domain.Agent) error, with the workspace
// and role lookups it needs closed over in the struct.
//
// Materialize deliberately carries no failure policy. It creates worktrees and
// reports what went wrong; whether a failure rolls the agent back, marks a
// step failed, or is retried later is the caller's decision.
type AgentWorktreeMaterializer struct {
	// ResolveWorkspace resolves the workspace's local root and repos. Required.
	ResolveWorkspace ResolveLocalWorkspaceFunc
	// SkipAgent short-circuits materialization for agents that need no
	// worktrees. Optional; nil means every agent is materialized.
	SkipAgent SkipAgentFunc
}

// RepoLister lists the repos registered in a workspace. Both CLI callers pass
// store.Store's repo-service List; taking the method rather than the store
// keeps this package free of a dependency on store.
type RepoLister func(ctx context.Context, workspaceKey string) ([]*domain.Repo, error)

// StateCacheMaterializer builds the materializer the CLI surfaces use: the
// per-machine state cache supplies the workspace root and each repo's on-disk
// path, listRepos supplies the roster. SkipAgent is left unset — every caller
// owns its own policy for which agents to skip, so it sets that field itself.
func StateCacheMaterializer(listRepos RepoLister) AgentWorktreeMaterializer {
	return AgentWorktreeMaterializer{
		ResolveWorkspace: func(ctx context.Context, workspaceKey string) (LocalWorkspaceView, error) {
			cache, err := bootstrap.LoadStateCache()
			if err != nil {
				return LocalWorkspaceView{}, fmt.Errorf("load local workspace state: %w", err)
			}
			local := cache.Workspaces[workspaceKey]
			if local.Path == "" {
				return LocalWorkspaceView{}, nil
			}
			repos, err := listRepos(ctx, workspaceKey)
			if err != nil {
				return LocalWorkspaceView{}, fmt.Errorf("list workspace repos: %w", err)
			}
			localRepos := make([]Repo, 0, len(repos))
			for _, repo := range repos {
				if repo == nil {
					continue
				}
				localRepos = append(localRepos, Repo{
					Name:   repo.Name,
					Path:   RepoPath(local, repo.Name),
					Groups: append([]string(nil), repo.Groups...),
				})
			}
			return LocalWorkspaceView{Root: local.Path, Repos: localRepos}, nil
		},
	}
}

// MaterializeErrorKind classifies an agent worktree materialization failure so
// each surface can map it onto its own error vocabulary (HTTP-facing service
// errors for the webui, plain wrapped errors for the CLI) without re-deriving
// the cause from a message string.
type MaterializeErrorKind string

const (
	// MaterializeRepoSelection: the agent's repo affinity could not be applied
	// to the workspace's repos.
	MaterializeRepoSelection MaterializeErrorKind = "repo_selection"
	// MaterializeNoRepos: the workspace has a local checkout but no repos to
	// give this agent.
	MaterializeNoRepos MaterializeErrorKind = "no_repos"
	// MaterializeRepoPathMissing: a selected repo has no on-disk path here.
	MaterializeRepoPathMissing MaterializeErrorKind = "repo_path_missing"
	// MaterializeWorktreeCreate: `git worktree add` (or its setup) failed.
	MaterializeWorktreeCreate MaterializeErrorKind = "worktree_create"
	// MaterializeLocalState: the worktrees exist but the local state cache
	// could not be updated to remember them.
	MaterializeLocalState MaterializeErrorKind = "local_state"
)

// MaterializeError is the classified failure returned by
// AgentWorktreeMaterializer.Materialize. Callers may use errors.As to map it;
// its message text is the plain (CLI) rendering of the failure.
type MaterializeError struct {
	Kind         MaterializeErrorKind
	WorkspaceKey string
	AgentName    string
	Repo         string
	Err          error
}

func (e *MaterializeError) Error() string {
	switch e.Kind {
	case MaterializeNoRepos:
		return fmt.Sprintf("workspace %s has no repos for agent %q", e.WorkspaceKey, e.AgentName)
	case MaterializeRepoPathMissing:
		return fmt.Sprintf("repo %q has no local path on this machine", e.Repo)
	case MaterializeWorktreeCreate:
		return fmt.Sprintf("create worktree for repo %q: %v", e.Repo, e.Err)
	default:
		if e.Err != nil {
			return e.Err.Error()
		}
		return string(e.Kind)
	}
}

func (e *MaterializeError) Unwrap() error { return e.Err }

func materializeErr(agent domain.Agent, kind MaterializeErrorKind, repo string, err error) *MaterializeError {
	return &MaterializeError{
		Kind:         kind,
		WorkspaceKey: agent.WorkspaceKey,
		AgentName:    agent.Name,
		Repo:         repo,
		Err:          err,
	}
}

// Materialize creates the local git worktrees for agent, one per repo the
// agent's affinity selects, and remembers the first of them as the agent's
// local worktree. It is idempotent: existing worktrees are left untouched.
func (m AgentWorktreeMaterializer) Materialize(ctx context.Context, agent domain.Agent) error {
	if m.SkipAgent != nil {
		skip, err := m.SkipAgent(ctx, agent)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	if m.ResolveWorkspace == nil {
		return fmt.Errorf("localworkspace: no workspace resolver configured")
	}
	view, err := m.ResolveWorkspace(ctx, agent.WorkspaceKey)
	if err != nil {
		return err
	}
	if view.Root == "" {
		// Distributed/cloud workspaces can be managed by this server without a
		// checkout mounted locally. In that shape the agent assignment is still
		// valid fleet-db state; local worktrees are created only on machines that
		// have workspace paths.
		return nil
	}
	repos, err := SelectAgentRepos(view.Repos, agent)
	if err != nil {
		return materializeErr(agent, MaterializeRepoSelection, "", err)
	}
	if len(repos) == 0 {
		return materializeErr(agent, MaterializeNoRepos, "", nil)
	}
	created, err := addAgentWorktrees(view.Root, repos, agent)
	if err != nil {
		return err
	}
	if err := RememberAgentWorktree(agent.WorkspaceKey, agent.Name, FirstWorktreePath(created)); err != nil {
		return materializeErr(agent, MaterializeLocalState, "", err)
	}
	return nil
}

// AgentBranchName returns the git branch an agent's worktrees are created on.
// Namespacing by workspace key keeps two workspaces that share a source repo
// from colliding on the same checked-out branch name.
func AgentBranchName(workspaceKey, agentName string) string {
	return workspaceKey + "/" + agentName
}

// addAgentWorktrees creates one worktree per selected repo under root and
// returns the repo-name keyed worktree paths.
func addAgentWorktrees(root string, repos []Repo, agent domain.Agent) (map[string]string, error) {
	created := make(map[string]string, len(repos))
	for _, repo := range repos {
		if repo.Path == "" {
			return nil, materializeErr(agent, MaterializeRepoPathMissing, repo.Name, nil)
		}
		target := AgentWorktreePath(root, repo.Name, agent.Name)
		if err := EnsureGitWorktree(repo.Path, target, AgentBranchName(agent.WorkspaceKey, agent.Name)); err != nil {
			return nil, materializeErr(agent, MaterializeWorktreeCreate, repo.Name, err)
		}
		created[repo.Name] = target
	}
	return created, nil
}
