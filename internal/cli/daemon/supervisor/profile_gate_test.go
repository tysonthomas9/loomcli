package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// newProfileGateSupervisor builds a supervisor whose project dir and log dir
// are both throwaway, so the gate can be driven end to end (refusal → state →
// agent log) without touching anything real.
func newProfileGateSupervisor(t *testing.T, projectDir string) *Supervisor {
	t.Helper()
	logDir := t.TempDir()
	return &Supervisor{
		ProjectDir:  projectDir,
		WorkspaceID: "ws",
		ConfigSnapshot: func() *cfgpkg.DaemonConfig {
			return &cfgpkg.DaemonConfig{
				Daemon:  cfgpkg.DaemonSettings{LogDir: logDir},
				Backend: "claude",
			}
		},
		Shutdown:      make(chan struct{}),
		StoppedAgents: make(map[string]struct{}),
		Agents:        make([]*AgentProcess, 0),
		EmitEvent:     func(events.Event) {},
	}
}

func newProfileGateAgent() *AgentProcess {
	return &AgentProcess{
		Entry:      cfgpkg.AgentEntry{Worktree: "observer", Role: "observer", Backend: "claude"},
		RoleConfig: cfgpkg.RoleConfig{Description: "test"},
	}
}

// driftedProfile provisions a profile whose manifest pins one harness version
// while the (stubbed) binary reports another, ACROSS a major — the drift that
// still refuses a boot under checkProfileManifest. Drift within a major is a
// warning there, not a refusal (it stopped the whole fleet on ordinary patch
// bumps), so a patch-level fixture would no longer exercise the gate at all.
func driftedProfile(t *testing.T, projectDir, worktree string) {
	t.Helper()
	stubHarnessVersion(t, map[string]string{"claude": "3.0.0 (Claude Code)"})
	writeProfile(t, projectDir, worktree, "2.1.236 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})
}

func TestPreFlightSetup_DriftedProfileRefusesBeforeClaim(t *testing.T) {
	projectDir := t.TempDir()
	driftedProfile(t, projectDir, "observer")

	mock := clitest.NewMockIssueBackend()
	mock.ReadyResult = []backend.IssueData{
		{ID: "task-1", IssueType: "task", Status: "open", Title: "Ready", Design: "design"},
	}
	s := newProfileGateSupervisor(t, projectDir)
	s.IssueBackend = mock
	ap := newProfileGateAgent()
	ap.WorktreePath = t.TempDir()

	if s.preFlightSetup(ap) {
		t.Fatal("preFlightSetup returned true, want a profile refusal")
	}
	// The whole point of gating here rather than at spawn: nothing is claimed,
	// so nothing is released, so no board churn is produced.
	if len(mock.Calls) != 0 {
		t.Fatalf("issue backend was called despite an invalid profile: %#v", mock.Calls)
	}
	if ap.AssignedTaskID != "" {
		t.Fatalf("AssignedTaskID = %q, want empty", ap.AssignedTaskID)
	}
	if ap.StopReason != StopReasonProfileInvalid {
		t.Fatalf("StopReason = %q, want %q", ap.StopReason, StopReasonProfileInvalid)
	}
	if ap.ProfileError == nil {
		t.Fatal("ProfileError = nil, want the drift message")
	}
	if !strings.Contains(ap.ProfileError.Message, "2.1.236 (Claude Code)") ||
		!strings.Contains(ap.ProfileError.Message, "3.0.0 (Claude Code)") {
		t.Fatalf("ProfileError.Message = %q, want both versions named", ap.ProfileError.Message)
	}
	if !strings.Contains(ap.ProfileError.Message, filepath.Join(".loom", AgentProfilesDirName, "observer", "claude")) {
		t.Fatalf("ProfileError.Message = %q, want the profile directory named", ap.ProfileError.Message)
	}
}

// TestProfileRefusalSurvivesNoWorkOverwrite is the regression that defines this
// ticket. The refusal used to live in ap.LastError, which setPreflightError
// overwrites unconditionally on the very next cycle ("no claimable tasks"); the
// 5-second state writer then only ever sampled the survivor, and a dead agent
// read as idle for ~5 hours. ProfileError is a separate slot precisely so that
// overwrite — which is correct for transient outcomes — cannot reach it.
func TestProfileRefusalSurvivesNoWorkOverwrite(t *testing.T) {
	projectDir := t.TempDir()
	driftedProfile(t, projectDir, "observer")

	s := newProfileGateSupervisor(t, projectDir)
	ap := newProfileGateAgent()
	s.Agents = append(s.Agents, ap)

	if err := s.gateProfileVerified(ap); err == nil {
		t.Fatal("gateProfileVerified returned nil for a drifted profile")
	}

	// The cycle that used to erase the diagnosis.
	s.setPreflightError(ap, agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome), "no claimable tasks")

	agents := s.GetAgents()
	if len(agents) != 1 {
		t.Fatalf("GetAgents returned %d agents, want 1", len(agents))
	}
	if agents[0].LastErrorClass != agenterr.OutcomeFromDomain(agenterr.NoWorkOutcome).String() {
		t.Fatalf("LastErrorClass = %q, want the NoWork overwrite to have happened", agents[0].LastErrorClass)
	}
	if !strings.Contains(agents[0].ProfileError, "3.0.0 (Claude Code)") {
		t.Fatalf("ProfileError = %q, want the drift text to have survived", agents[0].ProfileError)
	}
	if agents[0].StopReason != StopReasonProfileInvalid {
		t.Fatalf("StopReason = %q, want %q", agents[0].StopReason, StopReasonProfileInvalid)
	}
}

func TestGateProfileVerified_HealthyProfileClearsRefusal(t *testing.T) {
	projectDir := t.TempDir()
	stubHarnessVersion(t, map[string]string{"claude": "2.1.236 (Claude Code)"})
	writeProfile(t, projectDir, "observer", "2.1.236 (Claude Code)", map[string]string{
		"settings.json": `{"model":"opus"}`,
	})

	s := newProfileGateSupervisor(t, projectDir)
	ap := newProfileGateAgent()
	ap.ProfileError = &agenterr.AgentError{Message: "stale refusal from a previous cycle"}
	ap.StopReason = StopReasonProfileInvalid

	if err := s.gateProfileVerified(ap); err != nil {
		t.Fatalf("gateProfileVerified = %v, want nil for a verified profile", err)
	}
	if ap.ProfileError != nil {
		t.Fatalf("ProfileError = %+v, want cleared", ap.ProfileError)
	}
	if ap.StopReason != "" {
		t.Fatalf("StopReason = %q, want cleared", ap.StopReason)
	}
}

func TestGateProfileVerified_NoProfileIsNotARefusal(t *testing.T) {
	stubHarnessVersion(t, nil)
	projectDir := t.TempDir()
	s := newProfileGateSupervisor(t, projectDir)
	ap := newProfileGateAgent()

	if err := s.gateProfileVerified(ap); err != nil {
		t.Fatalf("an agent with no profile directory must keep booting, got %v", err)
	}
	if ap.ProfileError != nil {
		t.Fatalf("ProfileError = %+v, want nil", ap.ProfileError)
	}
}

// The daemon log is the second place an operator looks when an agent goes
// quiet. Without this write it is empty for a structural reason: it is only
// opened while wiring a child process, and a refused agent has no child.
func TestGateProfileVerified_WritesRefusalToAgentLog(t *testing.T) {
	projectDir := t.TempDir()
	driftedProfile(t, projectDir, "observer")

	s := newProfileGateSupervisor(t, projectDir)
	ap := newProfileGateAgent()

	if err := s.gateProfileVerified(ap); err == nil {
		t.Fatal("gateProfileVerified returned nil for a drifted profile")
	}
	if ap.LogFilePath == "" {
		t.Fatal("LogFilePath = empty, want the agent log path the logs command reads")
	}
	raw, err := os.ReadFile(ap.LogFilePath)
	if err != nil {
		t.Fatalf("reading agent log: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "PROFILE VERIFICATION FAILED") {
		t.Fatalf("agent log = %q, want the refusal banner", body)
	}
	if !strings.Contains(body, "3.0.0 (Claude Code)") {
		t.Fatalf("agent log = %q, want the drift detail", body)
	}
}

// A manifest is not repaired by restarting, so the refusal must neither erode
// the restart budget nor let the supervise goroutine exit: the agent stays
// visibly blocked and self-resumes once the profile is re-provisioned.
func TestShouldRestart_ProfileInvalidBlocksUncounted(t *testing.T) {
	s := newProfileGateSupervisor(t, t.TempDir())
	s.backendRecheckInterval = 5 * time.Second
	ap := newProfileGateAgent()
	ap.ProfileError = &agenterr.AgentError{Message: "profile harness version drift"}
	ap.StopReason = StopReasonProfileInvalid

	if !s.shouldRestart(ap) {
		t.Fatal("shouldRestart = false, want the agent to keep re-checking")
	}
	if ap.RestartCount != 0 {
		t.Fatalf("RestartCount = %d, want 0 (a profile fault must not erode the budget)", ap.RestartCount)
	}
	if ap.StopReason != StopReasonProfileInvalid {
		t.Fatalf("StopReason = %q, want %q", ap.StopReason, StopReasonProfileInvalid)
	}
	if got := s.computeBackoff(ap); got != s.backendRecheckBackoff() {
		t.Fatalf("computeBackoff = %s, want the fixed recheck interval %s", got, s.backendRecheckBackoff())
	}
}
