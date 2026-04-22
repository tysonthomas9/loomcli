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

func TestBuildAgentLogFilename(t *testing.T) {
	tests := []struct {
		name     string
		role     string
		repo     string
		worktree string
		want     string
	}{
		{
			name:     "legacy form when repo is empty",
			role:     "task",
			repo:     "",
			worktree: "falcon",
			want:     "task-falcon.log",
		},
		{
			name:     "workspace form with repo",
			role:     "task",
			repo:     "backend",
			worktree: "falcon",
			want:     "task-backend-falcon.log",
		},
		{
			name:     "different repo yields distinct filename",
			role:     "task",
			repo:     "frontend",
			worktree: "falcon",
			want:     "task-frontend-falcon.log",
		},
		{
			name:     "repo path traversal sanitized",
			role:     "task",
			repo:     "../../../etc",
			worktree: "falcon",
			want:     "task-etc-falcon.log",
		},
		{
			name:     "role path traversal sanitized",
			role:     "../../../evil",
			repo:     "backend",
			worktree: "falcon",
			want:     "evil-backend-falcon.log",
		},
		{
			name:     "worktree path traversal sanitized",
			role:     "task",
			repo:     "backend",
			worktree: "../../../x",
			want:     "task-backend-x.log",
		},
		{
			name:     "repo that sanitizes to dotdot falls back to legacy form",
			role:     "task",
			repo:     "..",
			worktree: "falcon",
			want:     "task-falcon.log",
		},
		{
			name:     "repo that sanitizes to single dot falls back to legacy form",
			role:     "task",
			repo:     ".",
			worktree: "falcon",
			want:     "task-falcon.log",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildAgentLogFilename(tc.role, tc.repo, tc.worktree)
			if got != tc.want {
				t.Errorf("BuildAgentLogFilename(%q,%q,%q) = %q, want %q", tc.role, tc.repo, tc.worktree, got, tc.want)
			}
			if strings.Contains(got, "..") {
				t.Errorf("result contains unsanitized path traversal: %q", got)
			}
		})
	}
}

func TestBuildAgentLogFilename_CollisionFree(t *testing.T) {
	// The bug: backend/falcon and frontend/falcon both produced "task-falcon.log"
	// and interleaved their output. Post-fix they must produce distinct filenames.
	a := BuildAgentLogFilename("task", "backend", "falcon")
	b := BuildAgentLogFilename("task", "frontend", "falcon")
	if a == b {
		t.Fatalf("same-worktree agents in different repos still collide: a=%s b=%s", a, b)
	}
	if a == "task-falcon.log" || b == "task-falcon.log" {
		t.Errorf("workspace-mode filename must not equal legacy form: a=%s b=%s", a, b)
	}
}
