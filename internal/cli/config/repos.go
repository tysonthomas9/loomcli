package config

import (
	"fmt"
	"log"
)

// resolveAgentRepos expands an agent's declared repo affinity (explicit Repos
// names + RepoGroups group bindings) into a deduplicated list of SourceRepoID
// strings. Returns (nil, nil) when both Repos and RepoGroups are empty,
// signaling "all repos" to the caller. Returns an error when the agent declares
// repo affinity but resolution yields zero results, preventing the agent from
// spawning in an unsafe unfiltered state.
func ResolveAgentRepos(agent AgentEntry, repos []RepoConfig) ([]string, error) {
	if len(agent.Repos) == 0 && len(agent.RepoGroups) == 0 {
		return nil, nil
	}

	seen := make(map[string]bool)
	result := make([]string, 0)

	add := func(id string) {
		if id == "" {
			return
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}

	// Build name→SourceRepoID lookup for explicit repo resolution.
	nameToID := make(map[string]string, len(repos))
	for _, rc := range repos {
		if rc.SourceRepoID != "" {
			nameToID[rc.Name] = rc.SourceRepoID
		}
	}

	// Explicit repos first — resolve name to SourceRepoID when available.
	for _, r := range agent.Repos {
		if id, ok := nameToID[r]; ok {
			add(id)
		} else {
			add(r) // fallback: use as-is (may already be a SourceRepoID)
		}
	}

	// Expand repo groups.
	for _, group := range agent.RepoGroups {
		if !expandRepoGroup(group, repos, add) {
			log.Printf("[repos] Warning: no repos found for group %q", group)
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("agent declared repo affinity but resolved to 0 repos: repos=%v repo_groups=%v", agent.Repos, agent.RepoGroups)
	}
	return result, nil
}

// expandRepoGroup finds repos belonging to the named group and adds their
// SourceRepoIDs via the add callback. Returns true if at least one repo matched.
func expandRepoGroup(group string, repos []RepoConfig, add func(string)) bool {
	matched := false
	for _, rc := range repos {
		for _, g := range rc.Groups {
			if g == group {
				if rc.SourceRepoID == "" {
					log.Printf("[repos] Warning: repo %q matched group %q but has empty SourceRepoID, skipping", rc.Name, group)
				} else {
					add(rc.SourceRepoID)
				}
				matched = true
				break
			}
		}
	}
	return matched
}

// declaresRepoAffinity reports whether the agent asks to be bound to a subset
// of repos at all. An agent that declares none is legitimately fleet-wide, so
// a failure to resolve a binding it never declared is not an error.
func declaresRepoAffinity(agent AgentEntry) bool {
	return len(agent.Repos) > 0 || len(agent.RepoGroups) > 0
}

// ResolveAgentReposFromActiveWorkspace populates agent.SourceRepos from the
// active workspace's repo list. It returns an error when the agent declares
// repo affinity that resolves to zero repos, or when affinity is declared and
// no workspace is configured.
//
// It exists so the preview paths (`loom queue`, the webui agent-queue panel)
// resolve a binding the same way the supervisor's claim does, instead of each
// keeping its own copy that drifts. Callers must treat the error as fatal for
// a bound agent: continuing with an unresolved binding drops both the fetch
// filter and the router gate, silently promoting the agent to fleet-wide.
func ResolveAgentReposFromActiveWorkspace(agent *AgentEntry) error {
	if agent == nil {
		return fmt.Errorf("nil agent entry")
	}
	if !declaresRepoAffinity(*agent) {
		agent.SourceRepos = nil
		return nil
	}

	ws, err := ResolveActiveWorkspace()
	if err != nil {
		return fmt.Errorf("resolve active workspace: %w", err)
	}
	return resolveAgentReposFromWorkspace(agent, ws)
}

// resolveAgentReposFromWorkspace is the workspace-independent half of
// ResolveAgentReposFromActiveWorkspace, split out so the resolution rules can
// be tested without an active workspace on the machine running the tests.
func resolveAgentReposFromWorkspace(agent *AgentEntry, ws *WorkspaceConfig) error {
	if agent == nil {
		return fmt.Errorf("nil agent entry")
	}
	if !declaresRepoAffinity(*agent) {
		agent.SourceRepos = nil
		return nil
	}
	if ws == nil || len(ws.Repos) == 0 {
		return fmt.Errorf("agent declares repo affinity (repos=%v repo_groups=%v) but no workspace repos are configured", agent.Repos, agent.RepoGroups)
	}

	sourceRepos, err := ResolveAgentRepos(*agent, ws.Repos)
	if err != nil {
		return err
	}
	agent.SourceRepos = sourceRepos
	return nil
}
