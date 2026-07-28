package supervisor

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

func labelPipeline(value string) *domain.AgentHooks {
	return &domain.AgentHooks{
		OnComplete: []domain.AgentHookAction{{Type: domain.AgentHookActionAddLabel, Value: value}},
	}
}

func firstLabel(h *domain.AgentHooks) string {
	if h.IsEmpty() {
		return ""
	}
	return h.OnComplete[0].Value
}

// `agentdef update --on-complete-*` was silently inert against a running agent:
// the pipeline was bound when the AgentProcess was constructed, so the CLI
// reported success, fleet-db stored it, and the run did nothing until a daemon
// restart (DOGFOOD-69).
func TestCurrentCompletionHooks_PrefersLiveConfigOverCapturedEntry(t *testing.T) {
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "planner", Hooks: labelPipeline("stale")}}
	s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig {
		return &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{
			{Worktree: "other", Hooks: labelPipeline("wrong-agent")},
			{Worktree: "planner", Hooks: labelPipeline("fresh")},
		}}
	}}

	if got := firstLabel(s.currentCompletionHooks(ap)); got != "fresh" {
		t.Errorf("hooks = %q, want the live config value %q", got, "fresh")
	}
}

// Hooks cleared through the CLI must actually stop firing.
func TestCurrentCompletionHooks_LiveClearIsHonored(t *testing.T) {
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "planner", Hooks: labelPipeline("stale")}}
	s := &Supervisor{ConfigSnapshot: func() *cfgpkg.DaemonConfig {
		return &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{Worktree: "planner"}}}
	}}

	if !s.currentCompletionHooks(ap).IsEmpty() {
		t.Error("a pipeline cleared in config must not keep firing from the captured Entry")
	}
}

// Pins the wiring, not just the helper: completionHookTarget must consult live
// config. Without this, reverting the gate to ap.Entry.Hooks leaves every test
// above passing while the bug is fully restored.
func TestCompletionHookTarget_UsesLiveConfigPipeline(t *testing.T) {
	ap := newHookAgentProcess(t, "T-1", labelPipeline("stale"))
	s := &Supervisor{
		IssueBackend: clitest.NewMockIssueBackend(),
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			// newHookAgentProcess names the worktree "critic".
			return &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{
				{Worktree: "critic", Hooks: labelPipeline("fresh")},
			}}
		},
	}

	hooks, taskID, ok := s.completionHookTarget(ap, 0)
	if !ok {
		t.Fatal("a clean task-bearing run should be hook-eligible")
	}
	if taskID != "T-1" {
		t.Errorf("taskID = %q, want %q", taskID, "T-1")
	}
	if got := firstLabel(hooks); got != "fresh" {
		t.Errorf("pipeline = %q, want the live config value %q", got, "fresh")
	}
}

// A vanishing config entry must not silently drop the pipeline the run started
// with — that would trade one silent failure for another.
func TestCurrentCompletionHooks_FallsBackWhenAgentAbsent(t *testing.T) {
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "planner", Hooks: labelPipeline("captured")}}

	cases := map[string]*Supervisor{
		"agent missing from snapshot": {ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{Agents: []cfgpkg.AgentEntry{{Worktree: "other"}}}
		}},
		"nil snapshot":     {ConfigSnapshot: func() *cfgpkg.DaemonConfig { return nil }},
		"no snapshot func": {},
	}

	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			if got := firstLabel(s.currentCompletionHooks(ap)); got != "captured" {
				t.Errorf("hooks = %q, want the captured Entry value %q", got, "captured")
			}
		})
	}
}
