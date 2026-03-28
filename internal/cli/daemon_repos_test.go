package cli

import (
	"testing"
)

func TestResolveAgentRepos(t *testing.T) {
	repos := []RepoConfig{
		{Name: "api-server", SourceRepoID: "api-server", Groups: []string{"backend"}},
		{Name: "worker", SourceRepoID: "worker", Groups: []string{"backend", "infra"}},
		{Name: "web-app", SourceRepoID: "web-app", Groups: []string{"frontend"}},
		{Name: "deploy-scripts", SourceRepoID: "deploy-scripts", Groups: []string{"infra"}},
	}

	tests := []struct {
		name    string
		agent   AgentEntry
		repos   []RepoConfig
		want    []string // nil means expect nil
		wantNil bool
		wantErr bool
	}{
		{
			name:    "empty repos and groups returns nil",
			agent:   AgentEntry{},
			repos:   repos,
			wantNil: true,
		},
		{
			name:  "explicit repos only",
			agent: AgentEntry{Repos: []string{"repo-a", "repo-b"}},
			repos: repos,
			want:  []string{"repo-a", "repo-b"},
		},
		{
			name:  "groups only - single group",
			agent: AgentEntry{RepoGroups: []string{"backend"}},
			repos: repos,
			want:  []string{"api-server", "worker"},
		},
		{
			name:  "groups only - multiple groups",
			agent: AgentEntry{RepoGroups: []string{"backend", "infra"}},
			repos: repos,
			want:  []string{"api-server", "worker", "deploy-scripts"},
		},
		{
			name:  "merge and dedup explicit repos with groups",
			agent: AgentEntry{Repos: []string{"api-server"}, RepoGroups: []string{"backend"}},
			repos: repos,
			want:  []string{"api-server", "worker"},
		},
		{
			name:    "all groups unknown returns error",
			agent:   AgentEntry{RepoGroups: []string{"nonexistent"}},
			repos:   repos,
			wantErr: true,
		},
		{
			name:  "unknown group with explicit repos still succeeds",
			agent: AgentEntry{Repos: []string{"my-repo"}, RepoGroups: []string{"nonexistent"}},
			repos: repos,
			want:  []string{"my-repo"},
		},
		{
			name:  "nil repo configs with explicit repos",
			agent: AgentEntry{Repos: []string{"repo-a"}},
			repos: nil,
			want:  []string{"repo-a"},
		},
		{
			name:    "nil repo configs with no agent repos",
			agent:   AgentEntry{},
			repos:   nil,
			wantNil: true,
		},
		{
			name:  "group matches multiple repos",
			agent: AgentEntry{RepoGroups: []string{"infra"}},
			repos: repos,
			want:  []string{"worker", "deploy-scripts"},
		},
		{
			name:  "explicit repos come first then group-expanded",
			agent: AgentEntry{Repos: []string{"deploy-scripts"}, RepoGroups: []string{"backend"}},
			repos: repos,
			want:  []string{"deploy-scripts", "api-server", "worker"},
		},
		{
			name:  "group matches only empty SourceRepoID repos returns error",
			agent: AgentEntry{RepoGroups: []string{"empty-group"}},
			repos: []RepoConfig{
				{Name: "bad-repo", SourceRepoID: "", Groups: []string{"empty-group"}},
			},
			wantErr: true,
		},
		{
			name:  "explicit repo name resolved to SourceRepoID",
			agent: AgentEntry{Repos: []string{"my-app"}},
			repos: []RepoConfig{
				{Name: "my-app", SourceRepoID: "org/my-app", Groups: []string{"frontend"}},
			},
			want: []string{"org/my-app"},
		},
		{
			name:  "explicit repo name with different SourceRepoID deduped with group",
			agent: AgentEntry{Repos: []string{"api-server"}, RepoGroups: []string{"backend"}},
			repos: []RepoConfig{
				{Name: "api-server", SourceRepoID: "core/api", Groups: []string{"backend"}},
				{Name: "worker", SourceRepoID: "core/worker", Groups: []string{"backend"}},
			},
			want: []string{"core/api", "core/worker"},
		},
		{
			name:  "partial group failure with some results no error",
			agent: AgentEntry{RepoGroups: []string{"backend", "nonexistent"}},
			repos: repos,
			want:  []string{"api-server", "worker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveAgentRepos(tt.agent, tt.repos)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (result=%v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("length mismatch: got %v, want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
