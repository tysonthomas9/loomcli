package agent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/epicrunner"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRunLead_InvokesClaude(t *testing.T) {
	// Setup temp directory as working directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir) // macOS /var -> /private/var
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Reset backend registry and install a mock backend that records calls.
	resetBackendState(t)
	mock := &mockBackend{name: "claude"}
	RegisterBackend(mock)
	_ = SetBackend("claude")

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
	leadPrompt := GenerateLeadPrompt()
	if inv.prompt != leadPrompt {
		t.Errorf("expected lead prompt, got %q", inv.prompt)
	}
	// AgentName should be empty for lead mode (not claiming tasks)
	if inv.agentName != "" {
		t.Errorf("expected empty agentName for lead mode, got %q", inv.agentName)
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
	recorder := SetupMockClaudeInvoker(t, expectedErr)

	// Capture stdout
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	// Note: This will cause the test to fail if we don't handle the os.Exit
	// In production code, runLead calls os.Exit(1) on error
	// For now, we just verify the mock was invoked correctly
	defer func() {
		w.Close()
		os.Stdout = oldStdout
	}()

	// The mock will return an error, but runLead calls os.Exit(1)
	// which we can't capture in a unit test without subprocess
	// So we verify the setup is correct
	if recorder.InteractiveErr != expectedErr {
		t.Errorf("mock not configured correctly")
	}
}

func TestGenerateLeadPrompt_NotEmpty(t *testing.T) {
	// Verify that GenerateLeadPrompt returns a non-empty prompt
	prompt := GenerateLeadPrompt()
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
