package supervisor

import (
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// Every AgentError the supervisor synthesizes must name the code path that
// built it. An empty Evidence here is exactly the hole PUPPET-579 closed: a
// class with no origin, indistinguishable in the log from a log-derived one.
func TestSupervisorSynthesizedErrors_CarryEvidenceRule(t *testing.T) {
	cases := []struct {
		name string
		mark func(s *Supervisor, ap *AgentProcess)
		rule string
	}{
		{"no work", func(s *Supervisor, ap *AgentProcess) { s.markNoWork(ap, "claude") }, evidenceRuleNoWork},
		{"run duration exceeded", func(s *Supervisor, ap *AgentProcess) {
			s.markRunDurationExceeded(ap, 143, "claude")
		}, evidenceRuleRunDurationExceeded},
		{"incomplete run", func(s *Supervisor, ap *AgentProcess) {
			s.markIncompleteRun(ap, "PUPPET-1", "claude")
		}, evidenceRuleIncompleteRun},
		{"spawn failure", func(s *Supervisor, ap *AgentProcess) {
			s.markSpawnFailure(ap, os.ErrNotExist)
		}, evidenceRuleSpawnFailure},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSupervisor()
			ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "falcon", Backend: "claude"}}
			tc.mark(s, ap)

			ap.Mu.Lock()
			ae := ap.LastError
			ap.Mu.Unlock()
			if ae == nil {
				t.Fatal("LastError is nil, want a synthesized AgentError")
			}
			if ae.Evidence.Source != agenterr.EvidenceSupervisor {
				t.Errorf("Evidence.Source = %q, want %q", ae.Evidence.Source, agenterr.EvidenceSupervisor)
			}
			if ae.Evidence.Rule != tc.rule {
				t.Errorf("Evidence.Rule = %q, want %q", ae.Evidence.Rule, tc.rule)
			}
			if !strings.Contains(ae.Evidence.Summary(), "rule="+tc.rule) {
				t.Errorf("Summary() = %q, want it to name rule=%s", ae.Evidence.Summary(), tc.rule)
			}
		})
	}
}

// The ownership-loss kill is synthesized in a different file; it must carry
// provenance too, and it is the one path that stops an agent for a reason the
// agent's own output never mentions.
func TestKillAgentForOwnership_CarriesEvidenceRule(t *testing.T) {
	s := newTestSupervisor()
	s.Shutdown = make(chan struct{})
	s.StoppedAgents = make(map[string]struct{})
	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "falcon"}}

	s.killAgentForOwnership(ap, "lease lost", os.ErrDeadlineExceeded)

	ap.Mu.Lock()
	ae := ap.LastError
	ap.Mu.Unlock()
	if ae == nil || ae.Evidence.Rule != evidenceRuleOwnershipLost {
		t.Fatalf("Evidence.Rule = %v, want %q", ae, evidenceRuleOwnershipLost)
	}
}

// A fatal AuthFailure exit is the reproducer this ticket was written against:
// the checkpoint is often the only durable record of why an agent parked, so
// it must carry the provenance and not just the class.
func TestSaveAgentCheckpoint_RecordsErrorEvidence(t *testing.T) {
	tmpDir := t.TempDir()
	writeLockFile(t, tmpDir, &cli.LockInfo{
		PID:       os.Getpid(),
		Command:   "task",
		AgentName: "falcon",
		TaskID:    "PUPPET-579",
		StartedAt: time.Now(),
	})

	s := newTestSupervisor()
	ap := &AgentProcess{
		Entry:        config.AgentEntry{Worktree: "falcon"},
		WorktreePath: tmpDir,
		LastError: &agenterr.AgentError{
			Class:    agenterr.OutcomeFromHarness(wrapper.ErrAuth),
			ExitCode: 1,
			Evidence: agenterr.Evidence{
				Source: agenterr.EvidenceHarnessMarker,
				Rule:   "AuthRequiredMarker",
			},
		},
	}

	s.handleAgentCheckpoint(ap, 1)

	cp, err := config.LoadCheckpoint(cli.ResolveLockDir(tmpDir))
	if err != nil {
		t.Fatalf("LoadCheckpoint() error = %v", err)
	}
	if cp == nil {
		t.Fatal("no checkpoint saved")
	}
	if cp.ErrorEvidence == "" {
		t.Fatal("checkpoint ErrorEvidence is empty, want the classification provenance")
	}
	if !strings.Contains(cp.ErrorEvidence, "rule=AuthRequiredMarker") {
		t.Errorf("ErrorEvidence = %q, want it to name rule=AuthRequiredMarker", cp.ErrorEvidence)
	}
	if cp.ErrorClass == "" {
		t.Error("ErrorClass is empty; the evidence must accompany the class, not replace it")
	}
}

// The status projection is what `loom daemon status` and daemon-agents.json
// read. Class without provenance was the gap.
func TestSupervisedAgentStatus_ProjectsLastErrorEvidence(t *testing.T) {
	ap := &AgentProcess{
		Entry: config.AgentEntry{Worktree: "falcon", Backend: "claude"},
		LastError: &agenterr.AgentError{
			Class:    agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome),
			Evidence: supervisorEvidence(evidenceRuleNoWork),
		},
	}
	s := newTestSupervisor()
	s.Agents = []*AgentProcess{ap}

	statuses := s.GetAgents()
	if len(statuses) != 1 {
		t.Fatalf("len(statuses) = %d, want 1", len(statuses))
	}
	if want := "rule=" + evidenceRuleNoWork; !strings.Contains(statuses[0].LastErrorEvidence, want) {
		t.Errorf("LastErrorEvidence = %q, want it to contain %q", statuses[0].LastErrorEvidence, want)
	}
}

// waitForAgentInfo must NOT emit: the emit moved to spawnAndWait so the event
// can name a class that has not been decided at wait time. If it ever emits
// again, this test and the one below together mean two events per exit.
func TestWaitForAgentInfo_EmitsNothing(t *testing.T) {
	var mu sync.Mutex
	var seen []events.Event
	s := &Supervisor{
		ConfigSnapshot: func() *config.DaemonConfig { return &config.DaemonConfig{} },
		Shutdown:       make(chan struct{}),
		StoppedAgents:  make(map[string]struct{}),
		EmitEvent: func(e events.Event) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, e)
		},
	}

	cmd := exec.Command("true") //nolint:norawexec
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	ap := &AgentProcess{
		Entry: config.AgentEntry{Worktree: "falcon", Role: "worker"},
		Cmd:   cmd,
		Pid:   cmd.Process.Pid,
	}

	exit := s.waitForAgentInfo(ap)
	if exit.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", exit.ExitCode)
	}
	// The pid is captured before waitForAgentInfo clears it — that capture is
	// the whole reason the result struct exists.
	if exit.PID == 0 {
		t.Error("PID = 0; the exiting pid must survive into the emit site")
	}
	if exit.Worktree != "falcon" || exit.Role != "worker" {
		t.Errorf("identity = %q/%q, want falcon/worker", exit.Worktree, exit.Role)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, e := range seen {
		if e.Type == events.AgentStopped {
			t.Fatal("waitForAgentInfo emitted agent.stopped; the emit belongs after classification")
		}
	}
}

// Exactly one agent.stopped per exit, carrying the class and the evidence.
func TestEmitAgentStopped_CarriesClassAndEvidence(t *testing.T) {
	var mu sync.Mutex
	var stopped []events.Event
	s := newTestSupervisor()
	s.EmitEvent = func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		if e.Type == events.AgentStopped {
			stopped = append(stopped, e)
		}
	}

	ap := &AgentProcess{
		Entry: config.AgentEntry{Worktree: "falcon", Role: "worker"},
		LastError: &agenterr.AgentError{
			Class:    agenterr.OutcomeFromHarness(wrapper.ErrAuth),
			ExitCode: 1,
			Evidence: agenterr.Evidence{
				Source: agenterr.EvidenceHarnessMarker,
				Rule:   "AuthRequiredMarker",
			},
		},
	}

	s.emitAgentStopped(ap, agentExitInfo{ExitCode: 1, PID: 4242, Worktree: "falcon", Role: "worker", EpicID: "E1"})

	mu.Lock()
	defer mu.Unlock()
	if len(stopped) != 1 {
		t.Fatalf("emitted %d agent.stopped events, want exactly 1", len(stopped))
	}
	data := decodeAgentStopped(t, stopped[0])
	if data.PID != 4242 || data.ExitCode != 1 {
		t.Errorf("pid/exit = %d/%d, want 4242/1", data.PID, data.ExitCode)
	}
	if data.ErrorClass == "" {
		t.Error("ErrorClass is empty, want the classified class")
	}
	if !strings.Contains(data.Evidence, "rule=AuthRequiredMarker") {
		t.Errorf("Evidence = %q, want it to name rule=AuthRequiredMarker", data.Evidence)
	}
}

// A clean exit classifies to no error at all; the event must still be emitted,
// and must simply carry no class and no evidence.
func TestEmitAgentStopped_CleanExitHasNoClass(t *testing.T) {
	var stopped []events.Event
	s := newTestSupervisor()
	s.EmitEvent = func(e events.Event) {
		if e.Type == events.AgentStopped {
			stopped = append(stopped, e)
		}
	}

	ap := &AgentProcess{Entry: config.AgentEntry{Worktree: "falcon"}}
	s.emitAgentStopped(ap, agentExitInfo{ExitCode: 0, PID: 7, Worktree: "falcon", Role: "worker"})

	if len(stopped) != 1 {
		t.Fatalf("emitted %d agent.stopped events, want exactly 1", len(stopped))
	}
	data := decodeAgentStopped(t, stopped[0])
	if data.ErrorClass != "" || data.Evidence != "" {
		t.Errorf("clean exit carried class=%q evidence=%q, want both empty", data.ErrorClass, data.Evidence)
	}
}

// decodeAgentStopped unwraps the event payload through the same DecodeData
// switch production consumers use, so a missing case would fail here too.
func decodeAgentStopped(t *testing.T, ev events.Event) events.AgentStoppedData {
	t.Helper()
	v, err := ev.DecodeData()
	if err != nil {
		t.Fatalf("DecodeData() error = %v", err)
	}
	data, ok := v.(*events.AgentStoppedData)
	if !ok {
		t.Fatalf("DecodeData() = %T, want *events.AgentStoppedData", v)
	}
	return *data
}
