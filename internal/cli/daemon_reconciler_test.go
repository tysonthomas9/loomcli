package cli

import (
	"sync"
	"testing"
	"time"
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

func TestDiffAgents_IgnoresSourceRepos(t *testing.T) {
	t.Parallel()
	old := []AgentEntry{
		{Worktree: "agent1", Role: "task"},
	}
	newEntries := []AgentEntry{
		{Worktree: "agent1", Role: "task", SourceRepos: []string{"repo-a", "repo-b"}},
	}
	added, removed, modified := diffAgents(old, newEntries)
	if len(added) != 0 {
		t.Errorf("expected 0 added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
	if len(modified) != 0 {
		t.Errorf("expected 0 modified, got %d: SourceRepos should be ignored", len(modified))
	}
}

func TestAgentEntry_Equal(t *testing.T) {
	t.Parallel()

	base := AgentEntry{
		Worktree:         "wt",
		Role:             "task",
		Repo:             "myrepo",
		Auto:             true,
		Backend:          "anthropic",
		FallbackBackends: []string{"openai"},
		PathPatterns:     []string{"*.go"},
		Repos:            []string{"repo1"},
		RepoGroups:       []string{"group1"},
		CrossRepo:        true,
		Parent:           "epic-1",
	}

	tests := []struct {
		name   string
		modify func(e AgentEntry) AgentEntry
		want   bool
	}{
		{"identical", func(e AgentEntry) AgentEntry { return e }, true},
		{"SourceRepos differ", func(e AgentEntry) AgentEntry {
			e.SourceRepos = []string{"repo-a", "repo-b"}
			return e
		}, true},
		{"Worktree differs", func(e AgentEntry) AgentEntry { e.Worktree = "other"; return e }, false},
		{"Role differs", func(e AgentEntry) AgentEntry { e.Role = "plan"; return e }, false},
		{"Repo differs", func(e AgentEntry) AgentEntry { e.Repo = "other"; return e }, false},
		{"Auto differs", func(e AgentEntry) AgentEntry { e.Auto = false; return e }, false},
		{"Backend differs", func(e AgentEntry) AgentEntry { e.Backend = "openai"; return e }, false},
		{"FallbackBackends differs", func(e AgentEntry) AgentEntry {
			e.FallbackBackends = []string{"openai", "extra"}
			return e
		}, false},
		{"PathPatterns differs", func(e AgentEntry) AgentEntry {
			e.PathPatterns = []string{"*.ts"}
			return e
		}, false},
		{"Repos differs", func(e AgentEntry) AgentEntry {
			e.Repos = []string{"repo2"}
			return e
		}, false},
		{"RepoGroups differs", func(e AgentEntry) AgentEntry {
			e.RepoGroups = []string{"group2"}
			return e
		}, false},
		{"CrossRepo differs", func(e AgentEntry) AgentEntry { e.CrossRepo = false; return e }, false},
		{"Parent differs", func(e AgentEntry) AgentEntry { e.Parent = "epic-2"; return e }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			other := tt.modify(base)
			if got := base.Equal(other); got != tt.want {
				t.Errorf("Equal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConcurrentDrainAdd_DrainCalledOnce(t *testing.T) {
	t.Parallel()

	// Create a Daemon with one agent that has initialized channels.
	done := make(chan struct{})
	close(done) // pre-close so drainAgent does not block

	ap := &AgentProcess{
		entry:  AgentEntry{Worktree: "agent1", Role: "task"},
		stopCh: make(chan struct{}),
		done:   done,
	}

	d := &Daemon{
		config:   &DaemonConfig{},
		agents:   []*AgentProcess{ap},
		shutdown: make(chan struct{}),
	}

	// Fire 10 goroutines that all try to drain "agent1" under drainAddMu.
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.drainAddMu.Lock()
			err := d.drainAgent("agent1")
			d.drainAddMu.Unlock()
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 successful drain, got %d", successCount)
	}
}

func TestConcurrentDrainAdd_SerializesAccess(t *testing.T) {
	t.Parallel()

	// Verify that drainAddMu serializes access: concurrent goroutines
	// cannot be in the drain/add section simultaneously.
	d := &Daemon{
		config:   &DaemonConfig{},
		agents:   []*AgentProcess{},
		shutdown: make(chan struct{}),
	}

	var inside, maxInside int
	var mu sync.Mutex

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.drainAddMu.Lock()
			mu.Lock()
			inside++
			if inside > maxInside {
				maxInside = inside
			}
			mu.Unlock()

			// Simulate work
			time.Sleep(time.Millisecond)

			mu.Lock()
			inside--
			mu.Unlock()
			d.drainAddMu.Unlock()
		}()
	}

	wg.Wait()

	if maxInside != 1 {
		t.Errorf("expected max 1 goroutine inside critical section at a time, got %d", maxInside)
	}
}
