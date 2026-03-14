package cli

import (
	"testing"
)

func TestDiffAgents_NoChanges(t *testing.T) {
	agents := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
		{Worktree: "agent2", Role: "plan"},
	}
	added, removed, modified := diffAgents(agents, agents)
	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(modified))
	}
}

func TestDiffAgents_Added(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
	}
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
		{Worktree: "agent2", Role: "plan"},
	}
	added, removed, modified := diffAgents(old, new)
	if len(added) != 1 || added[0].Worktree != "agent2" {
		t.Errorf("expected 1 added (agent2), got %v", added)
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(modified))
	}
}

func TestDiffAgents_Removed(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
		{Worktree: "agent2", Role: "plan"},
	}
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
	}
	added, removed, modified := diffAgents(old, new)
	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
	if len(removed) != 1 || removed[0].Worktree != "agent2" {
		t.Errorf("expected 1 removed (agent2), got %v", removed)
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(modified))
	}
}

func TestDiffAgents_Modified(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task", Backend: "openai"},
	}
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task", Backend: "anthropic"},
	}
	added, removed, modified := diffAgents(old, new)
	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
	if len(modified) != 1 || modified[0].Worktree != "agent1" {
		t.Errorf("expected 1 modified (agent1), got %v", modified)
	}
	if modified[0].Backend != "anthropic" {
		t.Errorf("expected modified entry to have new backend, got %s", modified[0].Backend)
	}
}

func TestDiffAgents_Mixed(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "keep", Role: "task"},
		{Worktree: "remove", Role: "plan"},
		{Worktree: "change", Role: "task", Auto: false},
	}
	new := []AgentEntry{
		{Worktree: "keep", Role: "task"},
		{Worktree: "add", Role: "plan"},
		{Worktree: "change", Role: "task", Auto: true},
	}
	added, removed, modified := diffAgents(old, new)
	if len(added) != 1 {
		t.Errorf("expected 1 added, got %d", len(added))
	}
	if len(removed) != 1 {
		t.Errorf("expected 1 removed, got %d", len(removed))
	}
	if len(modified) != 1 {
		t.Errorf("expected 1 modified, got %d", len(modified))
	}
}

func TestDiffAgents_ModifiedPathPatterns(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task", PathPatterns: []string{"*.go"}},
	}
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task", PathPatterns: []string{"*.go", "*.ts"}},
	}
	_, _, modified := diffAgents(old, new)
	if len(modified) != 1 {
		t.Errorf("expected 1 modified (path patterns changed), got %d", len(modified))
	}
}

func TestDiffAgents_ModifiedFallbackBackends(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task", FallbackBackends: []string{"fb1"}},
	}
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task", FallbackBackends: []string{"fb1", "fb2"}},
	}
	_, _, modified := diffAgents(old, new)
	if len(modified) != 1 {
		t.Errorf("expected 1 modified (fallback backends changed), got %d", len(modified))
	}
}

func TestDiffAgents_EmptyOld(t *testing.T) {
	new := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
	}
	added, removed, modified := diffAgents(nil, new)
	if len(added) != 1 {
		t.Errorf("expected 1 added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(modified))
	}
}

func TestDiffAgents_EmptyNew(t *testing.T) {
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
		{Worktree: "agent2", Role: "plan"},
	}
	added, removed, modified := diffAgents(old, nil)
	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
	if len(removed) != 2 {
		t.Errorf("expected 2 removed, got %d", len(removed))
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(modified))
	}
}

func TestComputeConfigHash_Deterministic(t *testing.T) {
	dc := &DaemonConfig{
		Backend: "anthropic",
		Agents: []AgentEntry{
			{Worktree: "agent1", Role: "task"},
		},
	}
	h1 := computeConfigHash(dc)
	h2 := computeConfigHash(dc)
	if h1 != h2 {
		t.Errorf("expected deterministic hash, got %s and %s", h1, h2)
	}
	if h1 == "" {
		t.Error("expected non-empty hash")
	}
}

func TestComputeConfigHash_DifferentConfigs(t *testing.T) {
	dc1 := &DaemonConfig{
		Backend: "anthropic",
		Agents: []AgentEntry{
			{Worktree: "agent1", Role: "task"},
		},
	}
	dc2 := &DaemonConfig{
		Backend: "openai",
		Agents: []AgentEntry{
			{Worktree: "agent1", Role: "task"},
		},
	}
	h1 := computeConfigHash(dc1)
	h2 := computeConfigHash(dc2)
	if h1 == h2 {
		t.Error("expected different hashes for different configs")
	}
}

func TestComputeConfigHash_AgentChanges(t *testing.T) {
	dc1 := &DaemonConfig{
		Agents: []AgentEntry{
			{Worktree: "agent1", Role: "task"},
		},
	}
	dc2 := &DaemonConfig{
		Agents: []AgentEntry{
			{Worktree: "agent1", Role: "task"},
			{Worktree: "agent2", Role: "plan"},
		},
	}
	h1 := computeConfigHash(dc1)
	h2 := computeConfigHash(dc2)
	if h1 == h2 {
		t.Error("expected different hashes when agents differ")
	}
}
