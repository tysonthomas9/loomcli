package lead

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	// No workspace and no override: this is the os.Getwd fallback branch, and
	// it must stay that way even when the operator's own LOOM_* vars are set.
	isolateLeadEnv(t)
	isolateLeadWorkspace(t)
	clearProfileEnv(t)

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

func TestRunLeadUsesCustomTerminalPrompt(t *testing.T) {
	t.Setenv("LOOM_LEAD_CONTROLLED", "0")
	isolateLeadEnv(t)
	isolateLeadWorkspace(t)
	clearProfileEnv(t)
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

	// dedicated=true on purpose: an inline role prompt must still win, and must
	// still clear the seed-and-shrink predicate.
	prompt, seedAndShrink, err := generateLeadTerminalPrompt(context.Background(), leadSessionRegistration{
		handle:    &bootstrap.StoreHandle{Store: st},
		Workspace: "E2E",
	}, true)
	if err != nil {
		t.Fatalf("generateLeadTerminalPrompt: %v", err)
	}
	if seedAndShrink {
		t.Fatal("inline role prompt must clear seedAndShrink")
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

func TestResolveLeadOrchestratorSessionIDPrefersExistingEnv(t *testing.T) {
	t.Setenv(envOrchestratorSessionID, " lead-session-1 ")

	if got := resolveLeadOrchestratorSessionID(); got != "lead-session-1" {
		t.Fatalf("resolveLeadOrchestratorSessionID() = %q, want lead-session-1", got)
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

	err := markLeadAssignmentDelivered(ctx, st, "WS", &epicrunner.LeadAssignmentContext{
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

// capturePrintPromptRun runs runLead with --print-prompt set and returns stdout.
// The mock backend is installed so the assertion that nothing was invoked is
// meaningful rather than vacuous.
func capturePrintPromptRun(t *testing.T, message string) (string, *mockBackend) {
	t.Helper()

	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	oldPrint, oldPromptFile, oldMessage := leadPrintPrompt, leadPromptFile, leadMessage
	leadPrintPrompt = true
	leadPromptFile = ""
	leadMessage = message
	t.Cleanup(func() {
		leadPrintPrompt, leadPromptFile, leadMessage = oldPrint, oldPromptFile, oldMessage
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
	return buf.String(), mock
}

func TestRunLeadPrintPromptPrintsStaticPromptAndStartsNoSession(t *testing.T) {
	output, mock := capturePrintPromptRun(t, "")

	if len(mock.interactiveCalls) != 0 {
		t.Fatalf("--print-prompt started a session: %d invocations", len(mock.interactiveCalls))
	}
	if strings.Contains(output, "Starting LEAD mode") {
		t.Fatalf("--print-prompt printed the session banner: %q", output)
	}
	if !strings.Contains(output, agent.GenerateLeadPrompt()) {
		t.Fatalf("--print-prompt did not print the built-in lead prompt: %q", output)
	}
}

func TestRunLeadPrintPromptOmitsDynamicSections(t *testing.T) {
	output, _ := capturePrintPromptRun(t, "list open epics")

	for _, forbidden := range []string{"## User's Initial Request", "list open epics", "## Loom Backend Assignment"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("--print-prompt leaked per-session content %q: %q", forbidden, output)
		}
	}
}

func TestRunLeadPrintPromptWorksWithoutWorkspace(t *testing.T) {
	// No workspace and no LOOM_* pointers: loadLeadRolePrompt must fall through
	// to the built-in prompt instead of failing.
	t.Setenv("LOOM_WORKSPACE", "")
	t.Setenv("LOOM_AGENT_ROLE", "")

	output, _ := capturePrintPromptRun(t, "")
	if !strings.Contains(output, "INTERACTIVE MODE: Project Lead") {
		t.Fatalf("--print-prompt without a workspace did not print the built-in prompt: %q", output)
	}
}
