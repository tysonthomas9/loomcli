package config

import (
	"strings"
	"testing"
)

func TestAgentEntryKey(t *testing.T) {
	tests := []struct {
		name  string
		entry AgentEntry
		want  string
	}{
		{
			name:  "bare worktree when repo is empty",
			entry: AgentEntry{Worktree: "falcon"},
			want:  "falcon",
		},
		{
			name:  "compound when repo is set",
			entry: AgentEntry{Worktree: "falcon", Repo: "backend"},
			want:  "backend/falcon",
		},
		{
			name:  "compound with multi-segment repo name",
			entry: AgentEntry{Worktree: "nova", Repo: "github.com/org/repo-a"},
			want:  "github.com/org/repo-a/nova",
		},
		{
			name:  "empty worktree with repo",
			entry: AgentEntry{Worktree: "", Repo: "backend"},
			want:  "backend/",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.entry.Key(); got != tc.want {
				t.Errorf("Key() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateAgents_SameWorktreeDifferentRepos(t *testing.T) {
	agents := []AgentEntry{
		{Worktree: "falcon", Role: "task", Repo: "backend"},
		{Worktree: "falcon", Role: "task", Repo: "frontend"},
	}
	if err := validateAgents(agents, nil); err != nil {
		t.Errorf("validateAgents() returned error for agents with same worktree in different repos: %v", err)
	}
}

func TestValidateAgents_DuplicateCompoundKey(t *testing.T) {
	tests := []struct {
		name    string
		agents  []AgentEntry
		wantErr string
	}{
		{
			name: "same worktree same repo",
			agents: []AgentEntry{
				{Worktree: "falcon", Role: "task", Repo: "backend"},
				{Worktree: "falcon", Role: "task", Repo: "backend"},
			},
			wantErr: `worktree "falcon" (repo "backend") is a duplicate`,
		},
		{
			name: "same worktree no repo on either",
			agents: []AgentEntry{
				{Worktree: "falcon", Role: "task"},
				{Worktree: "falcon", Role: "task"},
			},
			wantErr: `worktree "falcon" is a duplicate`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAgents(tc.agents, nil)
			if err == nil {
				t.Fatalf("validateAgents() returned nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("validateAgents() error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidateAgents_BareAndScopedSameWorktreeCoexist(t *testing.T) {
	// An agent with Repo="backend" produces key "backend/falcon" while one with
	// empty Repo produces key "falcon" — they don't collide.
	agents := []AgentEntry{
		{Worktree: "falcon", Role: "task", Repo: "backend"},
		{Worktree: "falcon", Role: "task"},
	}
	if err := validateAgents(agents, nil); err != nil {
		t.Errorf("validateAgents() returned error for mixed legacy+workspace agents: %v", err)
	}
}
