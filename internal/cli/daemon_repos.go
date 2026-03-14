package cli

import "log"

// resolveAgentRepos expands an agent's declared repo affinity (explicit Repos
// names + RepoGroups group bindings) into a deduplicated list of SourceRepoID
// strings. Returns nil when both Repos and RepoGroups are empty, signaling
// "all repos" to the caller.
func resolveAgentRepos(agent AgentEntry, repos []RepoConfig) []string {
	if len(agent.Repos) == 0 && len(agent.RepoGroups) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var result []string

	add := func(id string) {
		if id == "" {
			return
		}
		if !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}

	// Explicit repos first.
	for _, r := range agent.Repos {
		add(r)
	}

	// Expand repo groups.
	for _, group := range agent.RepoGroups {
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
		if !matched {
			log.Printf("[repos] Warning: no repos found for group %q", group)
		}
	}

	return result
}
