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
