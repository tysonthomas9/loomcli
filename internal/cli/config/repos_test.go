package config

import (
	"strings"
	"testing"
)

func TestResolveAgentRepos(t *testing.T) {
	repos := []RepoConfig{
		{Name: "api", SourceRepoID: "src-api", Groups: []string{"backend", "all"}},
		{Name: "worker", SourceRepoID: "src-worker", Groups: []string{"backend"}},
		{Name: "docs", SourceRepoID: "src-docs", Groups: []string{"all"}},
		{Name: "empty", Groups: []string{"backend"}},
	}

	got, err := ResolveAgentRepos(AgentEntry{
		Repos:      []string{"api", "raw-source", "api"},
		RepoGroups: []string{"backend", "missing"},
	}, repos)
	if err != nil {
		t.Fatalf("ResolveAgentRepos: %v", err)
	}
	want := []string{"src-api", "raw-source", "src-worker"}
	if len(got) != len(want) {
		t.Fatalf("resolved len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resolved[%d] = %q, want %q (all=%#v)", i, got[i], want[i], got)
		}
	}
}

func TestResolveAgentReposNoAffinityAndEmptyResolution(t *testing.T) {
	got, err := ResolveAgentRepos(AgentEntry{}, nil)
	if err != nil || got != nil {
		t.Fatalf("no affinity = %#v, %v; want nil, nil", got, err)
	}

	_, err = ResolveAgentRepos(AgentEntry{RepoGroups: []string{"missing"}}, []RepoConfig{
		{Name: "api", SourceRepoID: "src-api", Groups: []string{"backend"}},
	})
	if err == nil || !strings.Contains(err.Error(), "resolved to 0 repos") {
		t.Fatalf("empty resolution err = %v", err)
	}
}
