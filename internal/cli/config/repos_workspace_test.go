package config

import (
	"strings"
	"testing"
)

func testWorkspace() *WorkspaceConfig {
	return &WorkspaceConfig{
		Repos: []RepoConfig{
			{Name: "api-server", SourceRepoID: "sr-api", Groups: []string{"backend"}},
			{Name: "worker", SourceRepoID: "sr-worker", Groups: []string{"backend", "infra"}},
			{Name: "web-app", SourceRepoID: "sr-web", Groups: []string{"frontend"}},
		},
	}
}

func TestResolveAgentReposFromWorkspace(t *testing.T) {
	tests := []struct {
		name    string
		agent   AgentEntry
		ws      *WorkspaceConfig
		want    []string
		wantErr string
	}{
		{
			name:  "no affinity declared resolves to nil, no error",
			agent: AgentEntry{Worktree: "nova"},
			ws:    testWorkspace(),
		},
		{
			name:  "no affinity declared needs no workspace",
			agent: AgentEntry{Worktree: "nova"},
			ws:    nil,
		},
		{
			name:  "explicit repo names resolve to SourceRepoIDs",
			agent: AgentEntry{Repos: []string{"api-server", "web-app"}},
			ws:    testWorkspace(),
			want:  []string{"sr-api", "sr-web"},
		},
		{
			name:  "repo groups expand",
			agent: AgentEntry{RepoGroups: []string{"backend"}},
			ws:    testWorkspace(),
			want:  []string{"sr-api", "sr-worker"},
		},
		{
			name:    "declared affinity with no workspace is an error",
			agent:   AgentEntry{Repos: []string{"api-server"}},
			ws:      nil,
			wantErr: "no workspace repos are configured",
		},
		{
			name:    "declared affinity with an empty workspace is an error",
			agent:   AgentEntry{Repos: []string{"api-server"}},
			ws:      &WorkspaceConfig{},
			wantErr: "no workspace repos are configured",
		},
		{
			name:    "group that matches nothing is an error, not a silent widening",
			agent:   AgentEntry{RepoGroups: []string{"nope"}},
			ws:      testWorkspace(),
			wantErr: "resolved to 0 repos",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := tt.agent
			// A stale value must never survive a failed resolution as a
			// usable binding.
			agent.SourceRepos = []string{"stale"}

			err := resolveAgentReposFromWorkspace(&agent, tt.ws)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("err = nil, want one containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(agent.SourceRepos) != len(tt.want) {
				t.Fatalf("SourceRepos = %v, want %v", agent.SourceRepos, tt.want)
			}
			for i, w := range tt.want {
				if agent.SourceRepos[i] != w {
					t.Errorf("SourceRepos[%d] = %q, want %q", i, agent.SourceRepos[i], w)
				}
			}
		})
	}
}

func TestResolveAgentReposFromWorkspace_NilAgent(t *testing.T) {
	if err := resolveAgentReposFromWorkspace(nil, testWorkspace()); err == nil {
		t.Error("err = nil, want an error for a nil agent entry")
	}
}

func TestResolveAgentReposFromActiveWorkspace_UnboundAgentNeedsNoWorkspace(t *testing.T) {
	// An unbound agent short-circuits before any store access, so this stays
	// deterministic regardless of what workspace the host machine has active.
	agent := AgentEntry{Worktree: "nova", SourceRepos: []string{"stale"}}
	if err := ResolveAgentReposFromActiveWorkspace(&agent); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if agent.SourceRepos != nil {
		t.Errorf("SourceRepos = %v, want nil", agent.SourceRepos)
	}
}
