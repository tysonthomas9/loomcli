package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// stepDetectBackends tests
// ---------------------------------------------------------------------------

func TestSetup_DetectBackends_FlagPreSelected(t *testing.T) {
	orig := setupBackend
	defer func() { setupBackend = orig }()
	setupBackend = "codex"

	got := stepDetectBackends()
	if got != "codex" {
		t.Errorf("stepDetectBackends() = %q, want %q", got, "codex")
	}
}

func TestSetup_DetectBackends_YesUsesFirstAvailable(t *testing.T) {
	origBackend := setupBackend
	origYes := setupYes
	defer func() { setupBackend = origBackend; setupYes = origYes }()
	setupBackend = ""
	setupYes = true

	// Mock lookPath so that only "codex" is found
	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()
	lookPath = func(file string) (string, error) {
		if file == "codex" {
			return "/usr/bin/codex", nil
		}
		return "", exec.ErrNotFound
	}

	got := stepDetectBackends()
	if got != "codex" {
		t.Errorf("stepDetectBackends() = %q, want %q (first available)", got, "codex")
	}
}

func TestSetup_DetectBackends_YesDefaultsToClaudeWhenNoneFound(t *testing.T) {
	origBackend := setupBackend
	origYes := setupYes
	defer func() { setupBackend = origBackend; setupYes = origYes }()
	setupBackend = ""
	setupYes = true

	// Mock lookPath so nothing is found
	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()
	lookPath = func(file string) (string, error) {
		return "", exec.ErrNotFound
	}

	got := stepDetectBackends()
	if got != "claude" {
		t.Errorf("stepDetectBackends() = %q, want %q (default when none found)", got, "claude")
	}
}

func TestSetup_DetectBackends_InteractivePrompt(t *testing.T) {
	origBackend := setupBackend
	origYes := setupYes
	defer func() { setupBackend = origBackend; setupYes = origYes }()
	setupBackend = ""
	setupYes = false

	// Mock lookPath — claude available
	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()
	lookPath = func(file string) (string, error) {
		if file == "claude" {
			return "/usr/bin/claude", nil
		}
		return "", exec.ErrNotFound
	}

	// User types "opencode"
	MockStdin(t, "opencode\n")

	got := stepDetectBackends()
	if got != "opencode" {
		t.Errorf("stepDetectBackends() = %q, want %q", got, "opencode")
	}
}

func TestSetup_DetectBackends_InteractiveEmpty(t *testing.T) {
	origBackend := setupBackend
	origYes := setupYes
	defer func() { setupBackend = origBackend; setupYes = origYes }()
	setupBackend = ""
	setupYes = false

	// Mock lookPath — opencode is first found
	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()
	lookPath = func(file string) (string, error) {
		if file == "opencode" {
			return "/usr/bin/opencode", nil
		}
		return "", exec.ErrNotFound
	}

	// User presses enter (empty) — should get default (opencode, first available)
	MockStdin(t, "\n")

	got := stepDetectBackends()
	if got != "opencode" {
		t.Errorf("stepDetectBackends() = %q, want %q (default from available)", got, "opencode")
	}
}

// ---------------------------------------------------------------------------
// stepGenerateLoomYaml tests
// ---------------------------------------------------------------------------

func TestSetup_GenerateLoomYaml_WritesValid(t *testing.T) {
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	origYes := setupYes
	setupYes = true
	defer func() { setupYes = origYes }()

	stepGenerateLoomYaml("codex", []string{"falcon", "nova"})

	// Read the written file
	data, err := os.ReadFile(filepath.Join(tmpDir, "loom.yaml"))
	if err != nil {
		t.Fatalf("failed to read loom.yaml: %v", err)
	}
	content := string(data)

	// Should have the header comment
	if !strings.Contains(content, "# loom.yaml") {
		t.Error("loom.yaml should contain header comment")
	}

	// Parse YAML to verify structure
	var pf ProjectFile
	// Strip the comment lines before parsing
	if err := yaml.Unmarshal(data, &pf); err != nil {
		t.Fatalf("failed to parse loom.yaml: %v", err)
	}

	if pf.Version != CurrentConfigVersion {
		t.Errorf("version = %d, want %d", pf.Version, CurrentConfigVersion)
	}
	if pf.Backend != "codex" {
		t.Errorf("backend = %q, want %q", pf.Backend, "codex")
	}
	if len(pf.Agents) != 2 {
		t.Fatalf("len(agents) = %d, want 2", len(pf.Agents))
	}
	if pf.Agents[0].Worktree != "falcon" {
		t.Errorf("agents[0].worktree = %q, want %q", pf.Agents[0].Worktree, "falcon")
	}
	if pf.Agents[1].Worktree != "nova" {
		t.Errorf("agents[1].worktree = %q, want %q", pf.Agents[1].Worktree, "nova")
	}
	if pf.Agents[0].Role != "task" {
		t.Errorf("agents[0].role = %q, want %q", pf.Agents[0].Role, "task")
	}
	if !pf.Agents[0].Auto {
		t.Error("agents[0].auto should be true")
	}
	if pf.Daemon == nil {
		t.Fatal("daemon settings should not be nil")
	}
}

func TestSetup_GenerateLoomYaml_IdempotentWithYes(t *testing.T) {
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	origYes := setupYes
	setupYes = true
	defer func() { setupYes = origYes }()

	// First run
	stepGenerateLoomYaml("claude", []string{"falcon"})

	// Second run with different backend and additional worktree — merges agents
	stepGenerateLoomYaml("codex", []string{"falcon", "nova"})

	data, err := os.ReadFile(filepath.Join(tmpDir, "loom.yaml"))
	if err != nil {
		t.Fatalf("failed to read loom.yaml: %v", err)
	}

	var pf ProjectFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		t.Fatalf("failed to parse loom.yaml: %v", err)
	}

	if pf.Backend != "codex" {
		t.Errorf("backend = %q, want %q after second run", pf.Backend, "codex")
	}
	// Should have 2 agents: falcon (from first run, not duplicated) + nova (added)
	if len(pf.Agents) != 2 {
		t.Errorf("len(agents) = %d, want 2 (merged, not duplicated)", len(pf.Agents))
	}
}

func TestSetup_GenerateLoomYaml_PreservesRoles(t *testing.T) {
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	origYes := setupYes
	setupYes = true
	defer func() { setupYes = origYes }()

	// Write an existing loom.yaml with roles
	existing := &ProjectFile{
		Version: CurrentConfigVersion,
		Backend: "claude",
		Roles: map[string]RoleConfig{
			"planner": {Description: "Plans tasks"},
		},
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "task", Auto: true},
		},
	}
	existingData, _ := yaml.Marshal(existing)
	os.WriteFile("loom.yaml", existingData, 0644)

	// Run stepGenerateLoomYaml — should preserve roles
	stepGenerateLoomYaml("codex", []string{"falcon", "nova"})

	data, err := os.ReadFile(filepath.Join(tmpDir, "loom.yaml"))
	if err != nil {
		t.Fatalf("failed to read loom.yaml: %v", err)
	}

	var pf ProjectFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		t.Fatalf("failed to parse loom.yaml: %v", err)
	}

	if len(pf.Roles) != 1 {
		t.Fatalf("len(roles) = %d, want 1 (preserved)", len(pf.Roles))
	}
	if pf.Roles["planner"].Description != "Plans tasks" {
		t.Errorf("role planner description = %q, want %q", pf.Roles["planner"].Description, "Plans tasks")
	}
}

func TestSetup_GenerateLoomYaml_EmptyWorktrees(t *testing.T) {
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)

	tmpDir := t.TempDir()
	os.Chdir(tmpDir)

	origYes := setupYes
	setupYes = true
	defer func() { setupYes = origYes }()

	stepGenerateLoomYaml("claude", nil)

	data, err := os.ReadFile(filepath.Join(tmpDir, "loom.yaml"))
	if err != nil {
		t.Fatalf("failed to read loom.yaml: %v", err)
	}

	var pf ProjectFile
	if err := yaml.Unmarshal(data, &pf); err != nil {
		t.Fatalf("failed to parse loom.yaml: %v", err)
	}

	if len(pf.Agents) != 0 {
		t.Errorf("len(agents) = %d, want 0 for nil worktrees", len(pf.Agents))
	}
}

// ---------------------------------------------------------------------------
// stepConfigureSecrets tests
// ---------------------------------------------------------------------------

func TestSetup_ConfigureSecrets_SkipsWhenEnvVarSet(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-key")
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	origYes := setupYes
	setupYes = false
	defer func() { setupYes = origYes }()

	// Capture stdout to verify message
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	stepConfigureSecrets("claude")

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "already set via environment variable") {
		t.Errorf("expected 'already set via environment variable' message, got: %s", output)
	}

	// Verify no secrets file was created
	secretsDir := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "secrets")
	if _, err := os.Stat(secretsDir); !os.IsNotExist(err) {
		t.Error("secrets directory should not have been created")
	}
}

func TestSetup_ConfigureSecrets_SkipsWhenLoomSecretEnvSet(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv("LOOM_SECRET_ANTHROPIC_API_KEY", "sk-loom-secret")

	origYes := setupYes
	setupYes = false
	defer func() { setupYes = origYes }()

	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	stepConfigureSecrets("claude")

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "LOOM_SECRET_") {
		t.Errorf("expected LOOM_SECRET_ message, got: %s", output)
	}
}

func TestSetup_ConfigureSecrets_SkipsWhenSecretFileExists(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	// Create the secret file
	secretsDir := filepath.Join(configDir, "secrets")
	os.MkdirAll(secretsDir, 0700)
	os.WriteFile(filepath.Join(secretsDir, "anthropic-api-key"), []byte("sk-existing\n"), 0600)

	origYes := setupYes
	setupYes = false
	defer func() { setupYes = origYes }()

	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	stepConfigureSecrets("claude")

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Secret file already exists") {
		t.Errorf("expected 'Secret file already exists' message, got: %s", output)
	}
}

func TestSetup_ConfigureSecrets_SkipsInYesMode(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	origYes := setupYes
	setupYes = true
	defer func() { setupYes = origYes }()

	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	stepConfigureSecrets("claude")

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "non-interactive mode") {
		t.Errorf("expected 'non-interactive mode' message, got: %s", output)
	}

	// Verify no secrets file was created
	secretPath := filepath.Join(configDir, "secrets", "anthropic-api-key")
	if _, err := os.Stat(secretPath); !os.IsNotExist(err) {
		t.Error("secret file should not have been created in --yes mode")
	}
}

func TestSetup_ConfigureSecrets_CreatesWithCorrectPermissions(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	origYes := setupYes
	setupYes = false
	defer func() { setupYes = origYes }()

	// Simulate user entering an API key
	MockStdin(t, "sk-test-my-key-123\n")

	stepConfigureSecrets("claude")

	// Verify directory permissions
	secretsDir := filepath.Join(configDir, "secrets")
	dirInfo, err := os.Stat(secretsDir)
	if err != nil {
		t.Fatalf("secrets directory not created: %v", err)
	}
	if dirInfo.Mode().Perm() != 0700 {
		t.Errorf("secrets dir perm = %o, want 0700", dirInfo.Mode().Perm())
	}

	// Verify file permissions
	secretPath := filepath.Join(secretsDir, "anthropic-api-key")
	fileInfo, err := os.Stat(secretPath)
	if err != nil {
		t.Fatalf("secret file not created: %v", err)
	}
	if fileInfo.Mode().Perm() != 0600 {
		t.Errorf("secret file perm = %o, want 0600", fileInfo.Mode().Perm())
	}

	// Verify content
	data, _ := os.ReadFile(secretPath)
	if string(data) != "sk-test-my-key-123\n" {
		t.Errorf("secret content = %q, want %q", string(data), "sk-test-my-key-123\n")
	}
}

func TestSetup_ConfigureSecrets_UnknownBackend(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	origYes := setupYes
	setupYes = false
	defer func() { setupYes = origYes }()

	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	stepConfigureSecrets("unknown-backend")

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "No known API key") {
		t.Errorf("expected 'No known API key' message, got: %s", output)
	}
}

func TestSetup_ConfigureSecrets_SkipsOnEmptyInput(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)

	origYes := setupYes
	setupYes = false
	defer func() { setupYes = origYes }()

	// User presses enter (empty input)
	MockStdin(t, "\n")

	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	stepConfigureSecrets("codex")

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Skipped") {
		t.Errorf("expected 'Skipped' message when user enters empty key, got: %s", output)
	}

	// No file should be created
	secretPath := filepath.Join(configDir, "secrets", "openai-api-key")
	if _, err := os.Stat(secretPath); !os.IsNotExist(err) {
		t.Error("secret file should not be created when user skips")
	}
}

func TestSetup_ConfigureSecrets_CodexBackend(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", configDir)
	t.Setenv("OPENAI_API_KEY", "sk-openai-existing")

	origYes := setupYes
	setupYes = false
	defer func() { setupYes = origYes }()

	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	stepConfigureSecrets("codex")

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "OPENAI_API_KEY") {
		t.Errorf("expected OPENAI_API_KEY message for codex backend, got: %s", output)
	}
}

// ---------------------------------------------------------------------------
// helper function tests
// ---------------------------------------------------------------------------

func TestSetup_AgentEntryExists(t *testing.T) {
	agents := []AgentEntry{
		{Worktree: "falcon", Role: "task"},
		{Worktree: "nova", Role: "plan"},
	}

	if !agentEntryExists(agents, "falcon") {
		t.Error("agentEntryExists should find 'falcon'")
	}
	if !agentEntryExists(agents, "nova") {
		t.Error("agentEntryExists should find 'nova'")
	}
	if agentEntryExists(agents, "spark") {
		t.Error("agentEntryExists should not find 'spark'")
	}
	if agentEntryExists(nil, "falcon") {
		t.Error("agentEntryExists should return false for nil agents")
	}
}

func TestSetup_IsKnownBackend(t *testing.T) {
	if !isKnownBackend("claude") {
		t.Error("claude should be known")
	}
	if !isKnownBackend("codex") {
		t.Error("codex should be known")
	}
	if !isKnownBackend("opencode") {
		t.Error("opencode should be known")
	}
	if isKnownBackend("unknown") {
		t.Error("unknown should not be known")
	}
}

// ---------------------------------------------------------------------------
// showSetupSummary tests
// ---------------------------------------------------------------------------

func TestSetup_ShowSummary_PrintsExpected(t *testing.T) {
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	showSetupSummary("codex", []string{"falcon", "nova"})

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	expected := []string{
		"Setup complete!",
		"Backend:    codex",
		"Config:     loom.yaml",
		"Worktrees:  2 (falcon, nova)",
		"loom plan falcon",
		"loom task falcon",
		"loom daemon",
		"loom monitor",
	}

	for _, s := range expected {
		if !strings.Contains(output, s) {
			t.Errorf("showSetupSummary output missing %q", s)
		}
	}
}

func TestSetup_ShowSummary_EmptyWorktrees(t *testing.T) {
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	showSetupSummary("claude", nil)

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Worktrees:  0 ()") {
		t.Errorf("showSetupSummary with nil names should show 0 worktrees, got: %s", output)
	}
	// getFirstName returns "falcon" for nil/empty
	if !strings.Contains(output, "loom plan falcon") {
		t.Error("showSetupSummary with nil names should fallback to 'falcon'")
	}
}

func TestSetup_ShowSummary_SingleWorktree(t *testing.T) {
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	showSetupSummary("opencode", []string{"alpha"})

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Backend:    opencode") {
		t.Error("showSetupSummary should show 'opencode' backend")
	}
	if !strings.Contains(output, "Worktrees:  1 (alpha)") {
		t.Errorf("showSetupSummary should show 1 worktree, got: %s", output)
	}
	if !strings.Contains(output, "loom plan alpha") {
		t.Error("showSetupSummary should use 'alpha' in next steps")
	}
}
