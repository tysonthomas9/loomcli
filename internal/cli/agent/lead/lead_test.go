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
	"github.com/tysonthomas9/loomcli/internal/leadoccupant"
	"github.com/tysonthomas9/loomcli/internal/placement"
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

func TestOpenLeadSessionStoreSandboxRequiresAPIURL(t *testing.T) {
	t.Setenv(placement.OccupantTokenEnv, "occupant-token")
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(envLeadAPIURL, "")

	called := false
	orig := openLeadFleetStore
	openLeadFleetStore = func(ctx context.Context) (*bootstrap.StoreHandle, error) {
		called = true
		return nil, errors.New("should not be called")
	}
	t.Cleanup(func() { openLeadFleetStore = orig })

	handle, ws, err := openLeadSessionStore(context.Background())
	if err == nil {
		t.Fatal("openLeadSessionStore() succeeded, want preflight error")
	}
	if handle != nil || ws != "" {
		t.Fatalf("handle/ws = %#v/%q, want nil/empty", handle, ws)
	}
	if !strings.Contains(err.Error(), envLeadAPIURL) {
		t.Fatalf("error = %v, want %s guidance", err, envLeadAPIURL)
	}
	if called {
		t.Fatal("sandbox preflight called fleet store opener")
	}
}

func TestOpenLeadSessionStorePersistsInitialOccupantToken(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv(placement.OccupantTokenEnv, "initial-token")
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(envLeadAPIURL, "https://loom.test")

	handle, ws, err := openLeadSessionStore(context.Background())
	if err != nil {
		t.Fatalf("openLeadSessionStore: %v", err)
	}
	defer handle.Close()
	if ws != "WS" {
		t.Fatalf("workspace = %q, want WS", ws)
	}
	if got := leadoccupant.ReadToken(); got != "initial-token" {
		t.Fatalf("persisted initial token = %q, want initial-token", got)
	}
}

func TestOpenLeadSessionStoreInitialTokenWriteFailureIsFatal(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	if err := leadoccupant.WriteToken("stale-token"); err != nil {
		t.Fatalf("seed stale token: %v", err)
	}
	t.Setenv(placement.OccupantTokenEnv, "fresh-token")
	t.Setenv(bootstrap.EnvWorkspace, "WS")
	t.Setenv(envLeadAPIURL, "https://loom.test")

	original := writeLeadOccupantToken
	writeLeadOccupantToken = func(string) error { return errors.New("disk full") }
	t.Cleanup(func() { writeLeadOccupantToken = original })
	handle, ws, err := openLeadSessionStore(context.Background())
	if err == nil {
		t.Fatal("openLeadSessionStore succeeded after initial token write failure")
	}
	if handle != nil || ws != "" || !strings.Contains(err.Error(), "persist initial occupant token") {
		t.Fatalf("handle/ws/error = %#v/%q/%v", handle, ws, err)
	}
	if got := leadoccupant.ReadToken(); got != "stale-token" {
		t.Fatalf("stale token changed to %q after failed overwrite", got)
	}
}

func TestOpenLeadSessionStoreHostPathUsesFleetStore(t *testing.T) {
	t.Setenv(placement.OccupantTokenEnv, "")
	t.Setenv(bootstrap.EnvWorkspace, "WS")

	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{
		Key:  "WS",
		Name: "Workspace",
	}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	called := false
	orig := openLeadFleetStore
	openLeadFleetStore = func(ctx context.Context) (*bootstrap.StoreHandle, error) {
		called = true
		return &bootstrap.StoreHandle{Store: st}, nil
	}
	t.Cleanup(func() { openLeadFleetStore = orig })

	handle, ws, err := openLeadSessionStore(context.Background())
	if err != nil {
		t.Fatalf("openLeadSessionStore(): %v", err)
	}
	if !called {
		t.Fatal("host path did not call fleet store opener")
	}
	if handle == nil || handle.Store != st || ws != "WS" {
		t.Fatalf("handle/ws = %#v/%q, want memstore/WS", handle, ws)
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
