package cli

import (
	"sync"
	"sync/atomic"
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

// TestConcurrentDrainAdd_Serialized verifies that the drainAddMu mutex
// serializes concurrent drainAgent calls, preventing double SIGTERM/SIGKILL
// races against the same agent process. Without serialization, two concurrent
// reloadAndReconcile calls could both find the same agent and drain it
// simultaneously (the bug described in loomcli-5y1sd.9).
func TestConcurrentDrainAdd_Serialized(t *testing.T) {
	d := &Daemon{
		agents:   make([]*AgentProcess, 0),
		shutdown: make(chan struct{}),
		config:   &DaemonConfig{},
	}

	stopCh := make(chan struct{})
	done := make(chan struct{})

	ap := &AgentProcess{
		entry:  AgentEntry{Worktree: "agent-X", Role: "task"},
		stopCh: stopCh,
		done:   done,
	}
	d.agents = append(d.agents, ap)

	// Simulate superviseAgent goroutine: close done shortly after stopCh is signaled.
	go func() {
		<-stopCh
		close(done)
	}()

	var successCount atomic.Int32
	var notFoundCount atomic.Int32
	var wg sync.WaitGroup

	const goroutines = 10
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Simulate what reloadAndReconcile does: acquire drainAddMu around drain
			d.drainAddMu.Lock()
			err := d.drainAgent("agent-X")
			d.drainAddMu.Unlock()
			if err == nil {
				successCount.Add(1)
			} else {
				notFoundCount.Add(1)
			}
		}()
	}

	wg.Wait()

	if got := successCount.Load(); got != 1 {
		t.Errorf("expected exactly 1 successful drain, got %d", got)
	}
	if got := notFoundCount.Load(); got != goroutines-1 {
		t.Errorf("expected %d not-found errors, got %d", goroutines-1, got)
	}

	d.agentsMu.RLock()
	agentCount := len(d.agents)
	d.agentsMu.RUnlock()
	if agentCount != 0 {
		t.Errorf("expected 0 agents after drain, got %d", agentCount)
	}
}

// TestConcurrentDrainAdd_AddNoDuplicate verifies the drainAddMu + agentsMu
// locking pattern that reloadAndReconcile uses during agent addition.
// It exercises the check+insert logic in isolation (not the full addAgent,
// which requires filesystem I/O via ResolveAgentTarget) to confirm that
// serialization via drainAddMu prevents duplicate agent entries.
func TestConcurrentDrainAdd_AddNoDuplicate(t *testing.T) {
	d := &Daemon{
		agents:   make([]*AgentProcess, 0),
		shutdown: make(chan struct{}),
		config: &DaemonConfig{
			Roles: map[string]RoleConfig{
				"task": {Description: "test role"},
			},
		},
	}

	// addAgent calls ResolveAgentTarget which needs real filesystem,
	// so we test the duplicate-prevention logic directly by pre-building
	// AgentProcess entries and racing the agent-slice insertion.
	entry := AgentEntry{Worktree: "agent-Y", Role: "task"}

	var successCount atomic.Int32
	var dupCount atomic.Int32
	var wg sync.WaitGroup

	const goroutines = 10
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.drainAddMu.Lock()
			// Simulate addAgent's critical section: check+insert under agentsMu
			d.agentsMu.Lock()
			duplicate := false
			for _, existing := range d.agents {
				if existing.entry.Worktree == entry.Worktree {
					duplicate = true
					break
				}
			}
			if !duplicate {
				ap := &AgentProcess{
					entry:  entry,
					stopCh: make(chan struct{}),
					done:   make(chan struct{}),
				}
				d.agents = append(d.agents, ap)
				successCount.Add(1)
			} else {
				dupCount.Add(1)
			}
			d.agentsMu.Unlock()
			d.drainAddMu.Unlock()
		}()
	}

	wg.Wait()

	if got := successCount.Load(); got != 1 {
		t.Errorf("expected exactly 1 successful add, got %d", got)
	}
	if got := dupCount.Load(); got != goroutines-1 {
		t.Errorf("expected %d duplicate errors, got %d", goroutines-1, got)
	}

	d.agentsMu.RLock()
	agentCount := len(d.agents)
	d.agentsMu.RUnlock()
	if agentCount != 1 {
		t.Errorf("expected 1 agent, got %d", agentCount)
	}
}
