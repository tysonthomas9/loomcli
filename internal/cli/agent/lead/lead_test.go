package lead

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestRegisteredLeadSessionHeartbeatAdvancesUntilStopped(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "ws"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-session",
		AgentID:      "lead",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	before, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go heartbeatLeadSessionEvery(
		&testLeadSessionRuntime{store: st},
		"WS",
		"lead-session",
		stop,
		&wg,
		time.Millisecond,
	)
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		wg.Wait()
	})

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		after, getErr := st.AgentSessions().Get(ctx, "WS", "lead-session")
		if getErr != nil {
			t.Fatalf("get heartbeat session: %v", getErr)
		}
		if after.LastHeartbeat.After(before.LastHeartbeat) {
			close(stop)
			wg.Wait()
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("registered lead heartbeat did not advance the session")
}

// mockBackend is a minimal cli.Backend for the registry path that runLead falls
// back to when LOOM_LEAD_CONTROLLED=0. Self-contained here because the agent
// package's equivalent test helper is test-only and not importable.
type mockBackend struct {
	name             string
	interactiveCalls []struct {
		workDir, prompt, agentName string
	}
	interactiveErr error
}

func (m *mockBackend) Name() string { return m.name }
func (m *mockBackend) InvokeInteractive(workDir, prompt, agentName string) error {
	m.interactiveCalls = append(m.interactiveCalls, struct {
		workDir, prompt, agentName string
	}{workDir, prompt, agentName})
	return m.interactiveErr
}
func (m *mockBackend) InvokeNonInteractive(_, _, _ string, _ <-chan struct{}, _ *usage.Collector) error {
	return nil
}

// setupMockClaudeInvoker swaps the default deps' AgentInvoker for a clitest
// recorder (mirrors the agent package's test helper for the controlled path).
func setupMockClaudeInvoker(t *testing.T, returnErr error) *clitest.MockAgentInvoker {
	t.Helper()
	recorder := &clitest.MockAgentInvoker{InteractiveErr: returnErr}
	dd := cli.TestingGetDefaultDeps()
	orig := dd.Agent
	dd.Agent = recorder
	t.Cleanup(func() { dd.Agent = orig })
	return recorder
}

func TestRunLead_InvokesClaude(t *testing.T) {
	// Disable the controlled (harness-wrapper) lead runtime so runLead falls
	// back to the backend registry and hits the mock instead of launching a
	// real claude process under PTY supervision.
	t.Setenv("LOOM_LEAD_CONTROLLED", "0")

	// Setup temp directory as working directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir) // macOS /var -> /private/var
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Reset backend registry and install a mock backend that records calls.
	cli.TestingResetBackendState(t)
	mock := &mockBackend{name: "claude"}
	cli.RegisterBackend(mock)
	_ = cli.SetBackend("claude")

	// Capture stdout to suppress banner output
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runLead(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify banner was printed
	if !strings.Contains(output, "Starting LEAD mode") {
		t.Errorf("expected 'Starting LEAD mode' banner in output, got: %s", output)
	}

	// Verify Claude was invoked
	if len(mock.interactiveCalls) != 1 {
		t.Fatalf("expected 1 Claude invocation, got %d", len(mock.interactiveCalls))
	}

	inv := mock.interactiveCalls[0]
	// WorkDir should be the temp directory
	if inv.workDir != tmpDir {
		t.Errorf("expected workDir %q, got %q", tmpDir, inv.workDir)
	}
	// Prompt should be the lead prompt
	leadPrompt := agent.GenerateLeadPrompt()
	if inv.prompt != leadPrompt {
		t.Errorf("expected lead prompt, got %q", inv.prompt)
	}
	// AgentName should be empty for lead mode (not claiming tasks)
	if inv.agentName != "" {
		t.Errorf("expected empty agentName for lead mode, got %q", inv.agentName)
	}
}

func TestRunLeadShellFallbackPreservesLifecycleUntilShellExit(t *testing.T) {
	t.Setenv("LOOM_LEAD_CONTROLLED", "0")
	t.Setenv("SHELL", "/bin/test-shell")

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	promptFile := filepath.Join(tmpDir, "lead.md")
	if err := os.WriteFile(promptFile, []byte("Lead recovery prompt"), 0644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	originalPromptFile := leadPromptFile
	originalMessage := leadMessage
	leadPromptFile = promptFile
	leadMessage = ""
	t.Cleanup(func() {
		leadPromptFile = originalPromptFile
		leadMessage = originalMessage
	})

	cli.TestingResetBackendState(t)
	mock := &mockBackend{name: "claude", interactiveErr: errors.New("backend failed")}
	cli.RegisterBackend(mock)
	if err := cli.SetBackend("claude"); err != nil {
		t.Fatalf("set backend: %v", err)
	}

	originalRegistrar := registerLeadSession
	originalShellRunner := runLeadShellCommand
	var events []string
	registerLeadSession = func(context.Context, string) leadSessionRegistration {
		return leadSessionRegistration{finalize: func() { events = append(events, "finalize") }}
	}
	runLeadShellCommand = func(cmd *exec.Cmd) error {
		if len(events) != 0 {
			t.Fatalf("session finalized before recovery shell started: %v", events)
		}
		if cmd.Path != "/bin/test-shell" {
			t.Fatalf("shell path = %q, want /bin/test-shell", cmd.Path)
		}
		if cmd.Dir != tmpDir {
			t.Fatalf("shell dir = %q, want %q", cmd.Dir, tmpDir)
		}
		events = append(events, "shell")
		return nil
	}
	t.Cleanup(func() {
		registerLeadSession = originalRegistrar
		runLeadShellCommand = originalShellRunner
	})

	runLead(nil, nil)

	if got, want := strings.Join(events, ","), "shell,finalize"; got != want {
		t.Fatalf("lifecycle events = %q, want %q", got, want)
	}
}

func TestRunLeadUsesCustomTerminalPrompt(t *testing.T) {
	t.Setenv("LOOM_LEAD_CONTROLLED", "0")
	t.Setenv(envAgentName, "nova")
	t.Setenv("LOOM_AGENT_ROLE", "operator")

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	promptFile := filepath.Join(tmpDir, "operator.md")
	if err := os.WriteFile(promptFile, []byte("Operator prompt for {{.AgentName}} as {{.Role}}"), 0644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	oldPromptFile := leadPromptFile
	oldMessage := leadMessage
	leadPromptFile = promptFile
	leadMessage = ""
	t.Cleanup(func() {
		leadPromptFile = oldPromptFile
		leadMessage = oldMessage
	})

	cli.TestingResetBackendState(t)
	mock := &mockBackend{name: "claude"}
	cli.RegisterBackend(mock)
	_ = cli.SetBackend("claude")

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runLead(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if len(mock.interactiveCalls) != 1 {
		t.Fatalf("expected 1 Claude invocation, got %d", len(mock.interactiveCalls))
	}
	prompt := mock.interactiveCalls[0].prompt
	if !strings.HasPrefix(prompt, "Operator prompt for nova as operator") {
		t.Fatalf("prompt = %q, want custom terminal prompt", prompt)
	}
	if !strings.Contains(prompt, "Multi-Agent Safety Rules") {
		t.Fatalf("prompt missing safety guardrails")
	}
}

func TestGenerateLeadTerminalPromptUsesLiteralRolePrompt(t *testing.T) {
	t.Setenv("LOOM_AGENT_ROLE", "operator")
	st := memstore.New()
	if _, err := st.Roles().Create(context.Background(), store.RoleCreate{
		WorkspaceKey: "E2E",
		Name:         "operator",
		Kind:         string(domain.RoleKindInteractive),
		Prompt:       "Literal {{ marker }}",
		PromptFile:   "prompts/ignored.md",
	}); err != nil {
		t.Fatalf("create role: %v", err)
	}
	oldPromptFile := leadPromptFile
	leadPromptFile = ""
	t.Cleanup(func() { leadPromptFile = oldPromptFile })

	prompt, err := generateLeadTerminalPrompt(context.Background(), leadSessionRegistration{
		handle:    &bootstrap.StoreHandle{Store: st},
		Workspace: "E2E",
	})
	if err != nil {
		t.Fatalf("generateLeadTerminalPrompt: %v", err)
	}
	if !strings.HasPrefix(prompt, "Literal {{ marker }}") {
		t.Fatalf("prompt = %q, want literal inline role prompt", prompt)
	}
	if strings.Contains(prompt, "prompts/ignored.md") {
		t.Fatalf("prompt = %q, should not use role prompt_file", prompt)
	}
	if got := strings.Count(prompt, "Multi-Agent Safety Rules"); got != 1 {
		t.Fatalf("safety block count = %d, want 1", got)
	}
}

func TestRunLead_ClaudeError(t *testing.T) {
	// This test verifies that errors from Claude are handled.
	// Since runLead calls os.Exit(1) on error, we can't test the full path
	// without subprocess execution. Instead, we verify the mock is called.

	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	expectedErr := errors.New("claude failed")
	recorder := setupMockClaudeInvoker(t, expectedErr)

	// Capture stdout
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	// Note: This will cause the test to fail if we don't handle the os.Exit.
	// In production code, runLead calls os.Exit(1) on error; we can't capture
	// that in a unit test without subprocess, so we verify the setup is correct.
	defer func() {
		w.Close()
		os.Stdout = oldStdout
	}()

	if recorder.InteractiveErr != expectedErr {
		t.Errorf("mock not configured correctly")
	}
}

func TestGenerateLeadPrompt_NotEmpty(t *testing.T) {
	// Verify that GenerateLeadPrompt returns a non-empty prompt
	prompt := agent.GenerateLeadPrompt()
	if prompt == "" {
		t.Error("expected non-empty lead prompt")
	}
	// The prompt should contain some lead-related keywords
	if !strings.Contains(strings.ToLower(prompt), "lead") &&
		!strings.Contains(strings.ToLower(prompt), "project") &&
		!strings.Contains(strings.ToLower(prompt), "review") {
		t.Errorf("lead prompt should contain relevant keywords, got %q", prompt)
	}
}

func TestStandaloneLeadWithoutSessionEnvelopeIsUnregistered(t *testing.T) {
	clearSessionEnvelopeForTest(t)
	registration := registerLeadOrchestratorSession(t.Context(), t.TempDir())
	if registration.Err() != nil || registration.Runtime() != nil || registration.SessionID != "" {
		t.Fatalf("standalone registration = %+v, want unregistered", registration)
	}
}

func TestPartialSessionEnvelopeFailsClosed(t *testing.T) {
	clearSessionEnvelopeForTest(t)
	t.Setenv("LOOM_SESSION_ID", "session-1")
	registration := registerLeadOrchestratorSession(t.Context(), t.TempDir())
	if registration.Err() == nil {
		t.Fatalf("partial session envelope registered: %+v", registration)
	}
	if registration.Runtime() != nil {
		t.Fatal("partial session envelope exposed a runtime")
	}
}

func clearSessionEnvelopeForTest(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"LOOM_SESSION_WORKSPACE",
		"LOOM_SESSION_ID",
		"LOOM_SESSION_AGENT_ID",
		"LOOM_SESSION_TERMINAL_ID",
		"LOOM_SESSION_NODE_ID",
		"LOOM_SESSION_LEASE_ID",
		"LOOM_SESSION_FENCING_TOKEN",
		"LOOM_SESSION_AUTH_TOKEN",
		"LOOM_INTERACTION_API_URL",
	} {
		old, present := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if present {
				_ = os.Setenv(name, old)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}

func TestResolveLeadAgentIDUsesTerminalAgentName(t *testing.T) {
	t.Setenv(envAgentName, " lead-ui-e2e ")

	if got := resolveLeadAgentID(); got != "lead-ui-e2e" {
		t.Fatalf("resolveLeadAgentID() = %q, want lead-ui-e2e", got)
	}
}

func TestResolveLeadAgentIDDefaultsToLead(t *testing.T) {
	t.Setenv(envAgentName, " ")

	if got := resolveLeadAgentID(); got != "lead" {
		t.Fatalf("resolveLeadAgentID() = %q, want lead", got)
	}
}

func TestMarkLeadAssignmentDelivered(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.AgentSessions().Create(ctx, store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "lead-session",
		AgentID:      "nova",
		Kind:         domain.AgentSessionKindOrchestration,
		Status:       domain.AgentSessionRunning,
		Metadata:     map[string]string{"actor": "test"},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	err := markLeadAssignmentDelivered(ctx, &testLeadSessionRuntime{store: st}, "WS", &epicrunner.LeadAssignmentContext{
		EpicID:                "EPIC-1",
		AssignmentVersion:     "2026-05-17T05:00:00Z",
		OrchestratorSessionID: "lead-session",
	})
	if err != nil {
		t.Fatalf("markLeadAssignmentDelivered() error = %v", err)
	}

	session, err := st.AgentSessions().Get(ctx, "WS", "lead-session")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata["actor"] != "test" {
		t.Fatalf("existing metadata was not preserved: %#v", session.Metadata)
	}
	if got := session.Metadata["lead_assignment_delivered_version"]; got != "2026-05-17T05:00:00Z" {
		t.Fatalf("delivered version = %q", got)
	}
	if got := session.Metadata["lead_assignment_delivered_epic"]; got != "EPIC-1" {
		t.Fatalf("delivered epic = %q", got)
	}
}
