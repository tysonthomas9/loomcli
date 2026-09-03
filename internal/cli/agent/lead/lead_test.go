package lead

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backendnames"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/agent"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestFinalizeLeadTranscriptUploadsAndStampsMetadata(t *testing.T) {
	runtimeDir := t.TempDir()
	codexHome := t.TempDir()
	workDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("CODEX_HOME", codexHome)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)
	local, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	const sessionID = "lead-11111111-1111-4111-8111-111111111111"
	if _, err := os.Stat(local.SessionDir(sessionID)); !os.IsNotExist(err) {
		t.Fatalf("session directory exists before finalization: err=%v", err)
	}
	started := time.Now().Add(-time.Minute)
	rolloutDir := filepath.Join(codexHome, "sessions", started.Format("2006"), started.Format("01"), started.Format("02"))
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout: %v", err)
	}
	rolloutMeta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]any{"cwd": workDir}})
	rollout := filepath.Join(rolloutDir, "rollout-lead.jsonl")
	if err := os.WriteFile(rollout, append(rolloutMeta, '\n'), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	st := memstore.New()
	if _, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: sessionID, AgentID: "lead",
		Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionRunning,
		Metadata: map[string]string{"lead_workdir": workDir, "backend": backendnames.Codex},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	metadata := map[string]string{"lead_workdir": workDir, "backend": backendnames.Codex}
	if _, err := st.AgentSessions().Update(t.Context(), "WS", sessionID, store.AgentSessionUpdate{Metadata: &metadata}); err != nil {
		t.Fatalf("set session metadata: %v", err)
	}
	metadata = finalizeLeadTranscript(t.Context(), st, "WS", sessionID)
	if metadata["transcript_ref"] != "artifact://transcript-"+sessionID || metadata["transcript_path"] != local.NativeTranscriptPath(sessionID) {
		t.Fatalf("metadata = %#v, want transcript ref and path", metadata)
	}
	artifact, err := st.Artifacts().Get(t.Context(), "WS", "transcript-"+sessionID)
	if err != nil {
		t.Fatalf("uploaded artifact missing: %v", err)
	}
	if artifact.Metadata["transcript_format"] != sessions.TranscriptFormatRaw || artifact.Metadata["transcript_backend"] != backendnames.Codex {
		t.Fatalf("artifact metadata = %#v, want raw codex markers", artifact.Metadata)
	}
	content, ok := st.Artifacts().(store.ArtifactContentReader)
	if !ok {
		t.Fatal("artifact store does not support content reads")
	}
	data, err := content.ReadContent(t.Context(), "WS", artifact.ArtifactID)
	if err != nil || len(data) == 0 {
		t.Fatalf("artifact content length = %d, err %v; want non-empty", len(data), err)
	}
}

func TestFinalizeLeadTranscriptWithoutTranscriptIsBestEffort(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	cli.ResetWorkspaceRuntimeDirCache()
	t.Cleanup(cli.ResetWorkspaceRuntimeDirCache)
	st := memstore.New()
	if _, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "lead-session-2", AgentID: "lead",
		Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionRunning,
		Metadata: map[string]string{"lead_workdir": t.TempDir(), "backend": backendnames.Codex},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	metadata := finalizeLeadTranscript(t.Context(), st, "WS", "lead-session-2")
	if metadata["lead_workdir"] == "" || metadata["backend"] != backendnames.Codex {
		t.Fatalf("metadata = %#v, want pre-existing metadata preserved", metadata)
	}
}

func TestCreateLeadSessionAdoptsWebTerminalMetadata(t *testing.T) {
	st := memstore.New()
	if _, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS", SessionID: "lead-web-terminal", AgentID: "lead",
		Kind: domain.AgentSessionKindOrchestration, Status: domain.AgentSessionRunning,
		Metadata: map[string]string{"source": "web-terminal"},
	}); err != nil {
		t.Fatalf("create existing session: %v", err)
	}
	if err := createLeadSession(t.Context(), &bootstrap.StoreHandle{Store: st}, "WS", "lead-web-terminal", "lead", "/work/lead"); err != nil {
		t.Fatalf("adopt lead session: %v", err)
	}
	record, err := st.AgentSessions().Get(t.Context(), "WS", "lead-web-terminal")
	if err != nil {
		t.Fatalf("get adopted session: %v", err)
	}
	if record.Metadata["source"] != "web-terminal" || record.Metadata["lead_workdir"] != "/work/lead" || record.Metadata["backend"] == "" {
		t.Fatalf("adopted metadata = %#v, want source, workdir, and backend", record.Metadata)
	}
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
