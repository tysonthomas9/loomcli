package fleetdb

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAgentWireToDomain_CoalescesNilRepoSlices proves an agent that fleet-db
// returned without repos/repo_groups (omitted on the wire → nil after decode)
// surfaces as empty, non-nil slices. Combined with the no-omitempty tags on
// domain.Agent, this guarantees the web response carries `"repos":[]` and
// `"repo_groups":[]` rather than null or an absent key — the shape the UI and
// the OpenAPI contract require. Regression for the "undefined is not an object
// (evaluating 'e.repo_groups')" crash on a freshly created agent.
func TestAgentWireToDomain_CoalescesNilRepoSlices(t *testing.T) {
	wire := agentWire{
		WorkspaceKey: "DEV-V5",
		Name:         "solo",
		RoleName:     "lead",
		CrossRepo:    true,
		// Repos and RepoGroups intentionally left nil (fleet-db omitted them).
	}

	got := wire.toDomain()

	if got.Repos == nil {
		t.Fatalf("Repos: got nil, want non-nil empty slice")
	}
	if len(got.Repos) != 0 {
		t.Fatalf("Repos: got %v, want empty", got.Repos)
	}
	if got.RepoGroups == nil {
		t.Fatalf("RepoGroups: got nil, want non-nil empty slice")
	}
	if len(got.RepoGroups) != 0 {
		t.Fatalf("RepoGroups: got %v, want empty", got.RepoGroups)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal agent: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"repos":[]`) {
		t.Errorf("serialized agent must contain \"repos\":[], got %s", body)
	}
	if !strings.Contains(body, `"repo_groups":[]`) {
		t.Errorf("serialized agent must contain \"repo_groups\":[], got %s", body)
	}
}

// TestAgentWireToDomain_PreservesPopulatedRepoSlices ensures the coalesce does
// not clobber real values.
func TestAgentWireToDomain_PreservesPopulatedRepoSlices(t *testing.T) {
	wire := agentWire{
		WorkspaceKey: "DEV-V5",
		Name:         "runner",
		RoleName:     "task",
		Repos:        []string{"cloudflare-agents"},
		RepoGroups:   []string{"backend"},
	}

	got := wire.toDomain()

	if len(got.Repos) != 1 || got.Repos[0] != "cloudflare-agents" {
		t.Errorf("Repos: got %v, want [cloudflare-agents]", got.Repos)
	}
	if len(got.RepoGroups) != 1 || got.RepoGroups[0] != "backend" {
		t.Errorf("RepoGroups: got %v, want [backend]", got.RepoGroups)
	}
}
