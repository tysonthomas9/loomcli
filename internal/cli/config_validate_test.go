package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateProjectConfig_ValidConfig(t *testing.T) {
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			RestartPolicy: RestartPolicy{
				MaxRetries:     intPtr(3),
				BackoffInitial: intPtr(2),
				BackoffMax:     intPtr(300),
				OutputTimeout:  intPtr(900),
			},
		},
		Roles: map[string]RoleConfig{
			"reviewer": {TaskFilter: "has_design"},
		},
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "plan"},
			{Worktree: "nova", Role: "task"},
			{Worktree: "ember", Role: "reviewer"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if r.HasErrors() {
		t.Errorf("expected no errors, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_NilConfig(t *testing.T) {
	r := ValidateProjectConfig(nil, "")
	if len(r.Issues) != 0 {
		t.Errorf("expected no issues for nil config, got %d", len(r.Issues))
	}
}

func TestValidateProjectConfig_InvalidBackend(t *testing.T) {
	dc := &DaemonConfig{
		Backend: "nonexistent-backend",
		Roles:   make(map[string]RoleConfig),
	}

	if len(ListBackends()) == 0 {
		t.Skip("no backends registered")
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for invalid backend")
	}
	found := false
	for _, issue := range r.Issues {
		if issue.Field == "backend" && strings.Contains(issue.Message, "nonexistent-backend") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected backend error, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_WorktreePathTraversal(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{Worktree: "../escape", Role: "plan"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for path traversal in worktree")
	}
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue.Message, "invalid characters") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected invalid characters error for path traversal, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_WorktreeAbsolutePath(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{Worktree: "/tmp/absolute", Role: "plan"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for absolute path in worktree")
	}
	// Caught by isValidWorktreeName since '/' is an invalid character
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue.Message, "invalid characters") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected invalid characters error for absolute path, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_WorktreeInvalidChars(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{Worktree: "bad name!", Role: "plan"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for invalid characters in worktree")
	}
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue.Message, "invalid characters") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected invalid characters error, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_DuplicateWorktrees(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "plan"},
			{Worktree: "falcon", Role: "task"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for duplicate worktrees")
	}
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue.Message, "duplicate") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate worktree error, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_InvalidRole(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "reviewer"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for undefined role")
	}
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue.Message, "not a built-in role") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected role error, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_InvalidTaskFilter(t *testing.T) {
	dc := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"reviewer": {TaskFilter: "invalid_filter"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for invalid task filter")
	}
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue.Message, "must be one of") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected task filter error, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_ValidTaskFilters(t *testing.T) {
	for _, filter := range []string{"needs_design", "has_design", "any"} {
		t.Run(filter, func(t *testing.T) {
			dc := &DaemonConfig{
				Roles: map[string]RoleConfig{
					"custom": {TaskFilter: filter},
				},
			}
			r := ValidateProjectConfig(dc, t.TempDir())
			for _, issue := range r.Issues {
				if issue.Field == "roles.custom.task_filter" && issue.Severity == "error" {
					t.Errorf("unexpected error for valid task filter %q: %s", filter, issue.Message)
				}
			}
		})
	}
}

func TestValidateProjectConfig_InvalidBackoffValues(t *testing.T) {
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			RestartPolicy: RestartPolicy{
				BackoffInitial: intPtr(30),
				BackoffMax:     intPtr(10),
			},
		},
		Roles: make(map[string]RoleConfig),
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for backoff_max < backoff_initial")
	}
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue.Message, "must be >= backoff_initial") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected backoff error, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_NegativeBackoffInitial(t *testing.T) {
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			RestartPolicy: RestartPolicy{
				BackoffInitial: intPtr(-1),
			},
		},
		Roles: make(map[string]RoleConfig),
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for negative backoff_initial")
	}
}

func TestValidateProjectConfig_NegativeOutputTimeout(t *testing.T) {
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			RestartPolicy: RestartPolicy{
				OutputTimeout: intPtr(-1),
			},
		},
		Roles: make(map[string]RoleConfig),
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for negative output_timeout")
	}
}

func TestValidateProjectConfig_NegativeMaxRetries(t *testing.T) {
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			RestartPolicy: RestartPolicy{
				MaxRetries: intPtr(-5),
			},
		},
		Roles: make(map[string]RoleConfig),
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for negative max_retries")
	}
}

func TestValidateProjectConfig_PromptFileNotFound(t *testing.T) {
	dc := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"reviewer": {PromptFile: "prompts/nonexistent.md"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	// Should be a warning, not an error
	if r.HasErrors() {
		t.Error("expected only warnings for missing prompt file, got errors")
	}
	found := false
	for _, issue := range r.Issues {
		if issue.Severity == "warning" && strings.Contains(issue.Message, "does not exist") {
			found = true
		}
	}
	if !found {
		t.Error("expected warning for missing prompt file")
	}
}

func TestValidateProjectConfig_PromptFileExists(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "prompts")
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "reviewer.md"), []byte("prompt"), 0644); err != nil {
		t.Fatal(err)
	}

	dc := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"reviewer": {PromptFile: "prompts/reviewer.md"},
		},
	}

	r := ValidateProjectConfig(dc, dir)
	for _, issue := range r.Issues {
		if strings.Contains(issue.Field, "prompt_file") {
			t.Errorf("unexpected issue for existing prompt file: %s", issue.Message)
		}
	}
}

func TestValidateProjectConfig_MultipleErrors(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{Worktree: "../bad", Role: "plan"},
			{Worktree: "good", Role: "nonexistent"},
		},
		Daemon: DaemonSettings{
			RestartPolicy: RestartPolicy{
				MaxRetries: intPtr(-1),
			},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected errors")
	}
	// Should have at least 3 errors: invalid worktree chars, invalid role, negative max_retries
	errorCount := 0
	for _, issue := range r.Issues {
		if issue.Severity == "error" {
			errorCount++
		}
	}
	if errorCount < 3 {
		t.Errorf("expected at least 3 errors, got %d: %s", errorCount, r.FormatIssues())
	}
}

func TestValidateProjectConfig_BuiltInRolesAccepted(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "plan"},
			{Worktree: "nova", Role: "task"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	for _, issue := range r.Issues {
		if strings.Contains(issue.Field, ".role") && issue.Severity == "error" {
			t.Errorf("unexpected role error: %s", issue.Message)
		}
	}
}

func TestValidateProjectConfig_CustomRoleAccepted(t *testing.T) {
	dc := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"reviewer": {Description: "Code reviewer"},
		},
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "reviewer"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	for _, issue := range r.Issues {
		if strings.Contains(issue.Field, ".role") && issue.Severity == "error" {
			t.Errorf("unexpected role error for custom role: %s", issue.Message)
		}
	}
}

func TestValidateGlobalConfig_NilConfig(t *testing.T) {
	r := ValidateGlobalConfig(nil)
	if len(r.Issues) != 0 {
		t.Errorf("expected no issues for nil config, got %d", len(r.Issues))
	}
}

func TestValidateGlobalConfig_InvalidDefaultWorkspace(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "missing",
		Workspaces:       map[string]WorkspaceConfig{},
	}

	r := ValidateGlobalConfig(cfg)
	// Should be a warning to avoid breaking daemon startup
	if r.HasErrors() {
		t.Error("expected only warnings for invalid default_workspace, got errors")
	}
	found := false
	for _, issue := range r.Issues {
		if issue.Severity == "warning" && strings.Contains(issue.Message, "not defined in workspaces") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected default_workspace warning, got: %s", r.FormatIssues())
	}
}

func TestValidateGlobalConfig_ValidDefaultWorkspace(t *testing.T) {
	cfg := &LoomConfig{
		DefaultWorkspace: "myproject",
		Workspaces: map[string]WorkspaceConfig{
			"myproject": {Path: t.TempDir()},
		},
	}

	r := ValidateGlobalConfig(cfg)
	if r.HasErrors() {
		t.Errorf("expected no errors, got: %s", r.FormatIssues())
	}
}

func TestValidateGlobalConfig_MissingWorkspacePath(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myproject": {Path: "/nonexistent/workspace/path"},
		},
	}

	r := ValidateGlobalConfig(cfg)
	// Should be a warning, not an error
	if r.HasErrors() {
		t.Error("expected only warnings for missing workspace path, got errors")
	}
	found := false
	for _, issue := range r.Issues {
		if issue.Severity == "warning" && strings.Contains(issue.Message, "does not exist") {
			found = true
		}
	}
	if !found {
		t.Error("expected warning for missing workspace path")
	}
}

func TestValidateGlobalConfig_MissingRepoPath(t *testing.T) {
	wsDir := t.TempDir()
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myproject": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "backend", Path: "/nonexistent/repo"},
				},
			},
		},
	}

	r := ValidateGlobalConfig(cfg)
	if r.HasErrors() {
		t.Error("expected only warnings for missing repo path, got errors")
	}
	found := false
	for _, issue := range r.Issues {
		if issue.Severity == "warning" && strings.Contains(issue.Message, "does not exist") {
			found = true
		}
	}
	if !found {
		t.Error("expected warning for missing repo path")
	}
}

func TestValidateGlobalConfig_RelativeRepoPath(t *testing.T) {
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "myrepo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"myproject": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "myrepo", Path: "myrepo"},
				},
			},
		},
	}

	r := ValidateGlobalConfig(cfg)
	for _, issue := range r.Issues {
		if strings.Contains(issue.Field, "repos[0].path") {
			t.Errorf("unexpected issue for existing relative repo path: %s", issue.Message)
		}
	}
}

func TestValidationResult_ErrorFormatting(t *testing.T) {
	r := &ValidationResult{}
	r.addError("backend", "\"foo\" is not a registered backend")
	r.addWarning("roles.reviewer.prompt_file", "\"prompts/rev.md\" does not exist")
	r.addError("agents[0].role", "\"bad\" is not a built-in role")

	errStr := r.FormatIssues()
	if !strings.Contains(errStr, "config validation errors:") {
		t.Error("expected header in error string")
	}
	if !strings.Contains(errStr, "[error]") {
		t.Error("expected [error] severity tag")
	}
	if !strings.Contains(errStr, "[warning]") {
		t.Error("expected [warning] severity tag")
	}
	if !strings.Contains(errStr, "backend") {
		t.Error("expected backend field in error string")
	}
}

func TestValidationResult_EmptyError(t *testing.T) {
	r := &ValidationResult{}
	if r.FormatIssues() != "" {
		t.Errorf("expected empty string for no issues, got %q", r.FormatIssues())
	}
}

func TestValidationResult_HasErrors(t *testing.T) {
	t.Run("no issues", func(t *testing.T) {
		r := &ValidationResult{}
		if r.HasErrors() {
			t.Error("expected false for empty result")
		}
	})

	t.Run("warnings only", func(t *testing.T) {
		r := &ValidationResult{}
		r.addWarning("field", "msg")
		if r.HasErrors() {
			t.Error("expected false for warnings-only result")
		}
	})

	t.Run("has errors", func(t *testing.T) {
		r := &ValidationResult{}
		r.addError("field", "msg")
		if !r.HasErrors() {
			t.Error("expected true when errors present")
		}
	})
}

func TestIsValidWorktreeName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"alphanumeric", "falcon", true},
		{"with hyphen", "my-worktree", true},
		{"with underscore", "my_worktree", true},
		{"uppercase", "Falcon", true},
		{"with space", "my worktree", false},
		{"with dot", "my.worktree", false},
		{"with slash", "my/worktree", false},
		{"with bang", "bad!", false},
		{"empty", "", true}, // empty is valid (caught elsewhere as required)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidWorktreeName(tt.input); got != tt.want {
				t.Errorf("isValidWorktreeName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateProjectConfig_AgentBackendOverride(t *testing.T) {
	// Only meaningful if backends are registered
	if len(ListBackends()) == 0 {
		t.Skip("no backends registered")
	}

	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "plan", Backend: "nonexistent-agent-backend"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for invalid agent backend override")
	}
	found := false
	for _, issue := range r.Issues {
		if issue.Field == "agents[0].backend" && strings.Contains(issue.Message, "nonexistent-agent-backend") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected agent backend error, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_ZeroRestartValues(t *testing.T) {
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			RestartPolicy: RestartPolicy{
				MaxRetries:     intPtr(0),
				BackoffInitial: intPtr(0),
				BackoffMax:     intPtr(0),
				OutputTimeout:  intPtr(0),
			},
		},
		Roles: make(map[string]RoleConfig),
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if r.HasErrors() {
		t.Errorf("expected no errors for zero values, got: %s", r.FormatIssues())
	}
}
