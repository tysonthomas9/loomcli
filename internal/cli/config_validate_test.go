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

func TestIsValidRoleName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"simple", "plan", true},
		{"with hyphen", "my-role", true},
		{"with underscore", "code_123", true},
		{"uppercase", "Task", true},
		{"path traversal", "../evil", false},
		{"with slash", "a/b", false},
		{"empty", "", false},
		{"dot", ".", false},
		{"dotdot", "..", false},
		{"with space", "role name", false},
		{"with dot", "role.name", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidRoleName(tt.input); got != tt.want {
				t.Errorf("isValidRoleName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateProjectConfig_MaliciousRole(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "../../../etc/evil"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for malicious role in agent entry")
	}
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue.Message, "invalid characters") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'invalid characters' error for malicious role, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_MaliciousRoleKey(t *testing.T) {
	dc := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"../evil": {Description: "malicious role key"},
		},
		Agents: []AgentEntry{
			{Worktree: "falcon", Role: "plan"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for malicious role key in roles map")
	}
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue.Message, "invalid characters") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'invalid characters' error for malicious role key, got: %s", r.FormatIssues())
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

func TestValidateProjectConfig_DeterministicRoleOrder(t *testing.T) {
	dc := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"gamma": {TaskFilter: "invalid"},
			"alpha": {TaskFilter: "invalid"},
			"beta":  {TaskFilter: "invalid"},
		},
	}

	for i := 0; i < 10; i++ {
		r := ValidateProjectConfig(dc, t.TempDir())
		var errorFields []string
		for _, issue := range r.Issues {
			if issue.Severity == "error" {
				errorFields = append(errorFields, issue.Field)
			}
		}
		if len(errorFields) != 3 {
			t.Fatalf("iteration %d: expected 3 errors, got %d", i, len(errorFields))
		}
		if errorFields[0] != "roles.alpha.task_filter" ||
			errorFields[1] != "roles.beta.task_filter" ||
			errorFields[2] != "roles.gamma.task_filter" {
			t.Fatalf("iteration %d: expected alphabetical order [alpha, beta, gamma], got %v", i, errorFields)
		}
	}
}

func TestValidateProjectConfig_DeterministicTaskFilterList(t *testing.T) {
	dc := &DaemonConfig{
		Roles: map[string]RoleConfig{
			"myrole": {TaskFilter: "bogus"},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if len(r.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(r.Issues))
	}
	msg := r.Issues[0].Message
	if !strings.Contains(msg, "any, has_design, needs_design") {
		t.Errorf("expected sorted filter list 'any, has_design, needs_design' in message, got: %s", msg)
	}
}

func TestValidateGlobalConfig_ValidGroups(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: t.TempDir(),
				Repos: []RepoConfig{
					{Name: "repo1", Path: "/tmp/repo1", Groups: []string{"backend", "infra", "a", "123"}},
				},
			},
		},
	}
	r := ValidateGlobalConfig(cfg)
	for _, issue := range r.Issues {
		if strings.Contains(issue.Field, "groups") && issue.Severity == "error" {
			t.Errorf("unexpected group error: %s: %s", issue.Field, issue.Message)
		}
	}
}

func TestValidateGlobalConfig_InvalidGroupName(t *testing.T) {
	tests := []struct {
		name  string
		group string
	}{
		{"uppercase", "Backend"},
		{"space", "my group"},
		{"special chars", "infra!"},
		{"empty", ""},
		{"starts with hyphen", "-backend"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &LoomConfig{
				Workspaces: map[string]WorkspaceConfig{
					"ws": {
						Path: t.TempDir(),
						Repos: []RepoConfig{
							{Name: "repo1", Path: "/tmp/repo1", Groups: []string{tt.group}},
						},
					},
				},
			}
			r := ValidateGlobalConfig(cfg)
			found := false
			for _, issue := range r.Issues {
				if strings.Contains(issue.Field, "groups") && issue.Severity == "error" {
					found = true
				}
			}
			if !found {
				t.Errorf("expected error for group name %q, got: %s", tt.group, r.FormatIssues())
			}
		})
	}
}

func TestValidateGlobalConfig_DuplicateSourceRepoID(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: t.TempDir(),
				Repos: []RepoConfig{
					{Name: "repo1", Path: "/tmp/repo1", SourceRepoID: "shared-id"},
					{Name: "repo2", Path: "/tmp/repo2", SourceRepoID: "shared-id"},
				},
			},
		},
	}
	r := ValidateGlobalConfig(cfg)
	if !r.HasErrors() {
		t.Error("expected error for duplicate source_repo_id")
	}
	found := false
	for _, issue := range r.Issues {
		if strings.Contains(issue.Field, "source_repo_id") && strings.Contains(issue.Message, "duplicate") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected duplicate source_repo_id error, got: %s", r.FormatIssues())
	}
}

func TestValidateGlobalConfig_DuplicateImplicitSourceRepoID(t *testing.T) {
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws": {
				Path: t.TempDir(),
				Repos: []RepoConfig{
					{Name: "myrepo", Path: "/tmp/repo1"},
					{Name: "myrepo", Path: "/tmp/repo2"},
				},
			},
		},
	}
	r := ValidateGlobalConfig(cfg)
	if !r.HasErrors() {
		t.Error("expected error for duplicate implicit source_repo_id (same Name)")
	}
}

func TestValidateGlobalConfig_SourceRepoIDUniqueAcrossWorkspaces(t *testing.T) {
	// Repos in different workspaces CAN share the same source_repo_id
	cfg := &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"ws1": {
				Path: t.TempDir(),
				Repos: []RepoConfig{
					{Name: "repo1", Path: "/tmp/repo1", SourceRepoID: "shared-id"},
				},
			},
			"ws2": {
				Path: t.TempDir(),
				Repos: []RepoConfig{
					{Name: "repo2", Path: "/tmp/repo2", SourceRepoID: "shared-id"},
				},
			},
		},
	}
	r := ValidateGlobalConfig(cfg)
	for _, issue := range r.Issues {
		if strings.Contains(issue.Field, "source_repo_id") && issue.Severity == "error" {
			t.Errorf("unexpected error for cross-workspace source_repo_id: %s", issue.Message)
		}
	}
}

func TestValidateProjectConfig_AgentReposValid(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{
				Worktree:   "falcon",
				Role:       "plan",
				Repos:      []string{"backend", "frontend"},
				RepoGroups: []string{"infra", "data-pipeline"},
				CrossRepo:  true,
			},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if r.HasErrors() {
		t.Errorf("expected no errors, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_AgentReposEmptyEntry(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{
				Worktree: "falcon",
				Role:     "plan",
				Repos:    []string{"backend", ""},
			},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for empty repo entry")
	}
	found := false
	for _, issue := range r.Issues {
		if issue.Field == "agents[0].repos[1]" && strings.Contains(issue.Message, "must not be empty") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected repos empty entry error, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_AgentRepoGroupsEmptyEntry(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{
				Worktree:   "falcon",
				Role:       "plan",
				RepoGroups: []string{""},
			},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for empty repo group entry")
	}
	found := false
	for _, issue := range r.Issues {
		if issue.Field == "agents[0].repo_groups[0]" && strings.Contains(issue.Message, "must not be empty") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected repo_groups empty entry error, got: %s", r.FormatIssues())
	}
}

func TestValidateProjectConfig_AgentRepoGroupsInvalidName(t *testing.T) {
	dc := &DaemonConfig{
		Roles: make(map[string]RoleConfig),
		Agents: []AgentEntry{
			{
				Worktree:   "falcon",
				Role:       "plan",
				RepoGroups: []string{"Valid-group", "UPPER"},
			},
		},
	}

	r := ValidateProjectConfig(dc, t.TempDir())
	if !r.HasErrors() {
		t.Error("expected error for invalid repo group name")
	}
	// Both should fail: "Valid-group" has uppercase V, "UPPER" is all uppercase
	errorCount := 0
	for _, issue := range r.Issues {
		if strings.Contains(issue.Field, "repo_groups") && issue.Severity == "error" {
			errorCount++
		}
	}
	if errorCount != 2 {
		t.Errorf("expected 2 repo_groups errors, got %d: %s", errorCount, r.FormatIssues())
	}
}

func TestValidateIssueBackend_Valid(t *testing.T) {
	for _, value := range []string{"beads", "fleetdb", "fleet", ""} {
		t.Run(value, func(t *testing.T) {
			r := &ValidationResult{}
			validateIssueBackend(r, value)
			if len(r.Issues) != 0 {
				t.Errorf("expected no issues for %q, got: %s", value, r.FormatIssues())
			}
		})
	}
}

func TestValidateIssueBackend_Invalid(t *testing.T) {
	r := &ValidationResult{}
	validateIssueBackend(r, "postgres")
	if len(r.Issues) != 1 {
		t.Fatalf("expected 1 error, got %d: %s", len(r.Issues), r.FormatIssues())
	}
	if r.Issues[0].Severity != "error" {
		t.Errorf("expected error severity, got %s", r.Issues[0].Severity)
	}
	if !strings.Contains(r.Issues[0].Message, "must be one of") {
		t.Errorf("expected 'must be one of' in message, got %q", r.Issues[0].Message)
	}
}

func TestValidateFleetSettings_ValidHTTPS(t *testing.T) {
	r := &ValidationResult{}
	validateFleetSettings(r, &FleetSettings{
		URL: "https://fleet.example.com",
	})
	if len(r.Issues) != 0 {
		t.Errorf("expected no issues for https URL, got: %s", r.FormatIssues())
	}
}

func TestValidateFleetSettings_ValidHTTP(t *testing.T) {
	r := &ValidationResult{}
	validateFleetSettings(r, &FleetSettings{
		URL: "http://fleet.example.com",
	})
	if len(r.Issues) != 0 {
		t.Errorf("expected no issues for http URL, got: %s", r.FormatIssues())
	}
}

func TestValidateFleetSettings_InvalidProtocol(t *testing.T) {
	r := &ValidationResult{}
	validateFleetSettings(r, &FleetSettings{
		URL: "ftp://fleet.example.com",
	})
	if len(r.Issues) != 1 {
		t.Fatalf("expected 1 error, got %d: %s", len(r.Issues), r.FormatIssues())
	}
	if r.Issues[0].Severity != "error" {
		t.Errorf("expected error severity, got %s", r.Issues[0].Severity)
	}
	if !strings.Contains(r.Issues[0].Message, "http://") {
		t.Errorf("expected 'http://' in message, got %q", r.Issues[0].Message)
	}
}

func TestValidateFleetSettings_InvalidWorkspace(t *testing.T) {
	r := &ValidationResult{}
	validateFleetSettings(r, &FleetSettings{
		Workspace: "bad/name",
	})
	if len(r.Issues) != 1 {
		t.Fatalf("expected 1 warning, got %d: %s", len(r.Issues), r.FormatIssues())
	}
	if r.Issues[0].Severity != "warning" {
		t.Errorf("expected warning severity, got %s", r.Issues[0].Severity)
	}
	if !strings.Contains(r.Issues[0].Message, "invalid characters") {
		t.Errorf("expected 'invalid characters' in message, got %q", r.Issues[0].Message)
	}
}

func TestValidateFleetSettings_EmptyFields(t *testing.T) {
	r := &ValidationResult{}
	validateFleetSettings(r, &FleetSettings{})
	if len(r.Issues) != 0 {
		t.Errorf("expected no issues for empty fleet settings, got: %s", r.FormatIssues())
	}
}

func TestValidateCrossField_FleetNoURL(t *testing.T) {
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			IssueBackend: IssueBackendFleet,
			// Fleet is nil — no URL configured
		},
		Roles: make(map[string]RoleConfig),
	}
	r := ValidateProjectConfig(dc, t.TempDir())
	found := false
	for _, issue := range r.Issues {
		if issue.Field == "daemon.fleet.url" && issue.Severity == "warning" && strings.Contains(issue.Message, "fleet") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about missing fleet URL when issue_backend=fleet, got: %s", r.FormatIssues())
	}
}

func TestValidateCrossField_FleetWithEmptyURL(t *testing.T) {
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			IssueBackend: IssueBackendFleet,
			Fleet:        &FleetSettings{URL: ""},
		},
		Roles: make(map[string]RoleConfig),
	}
	r := ValidateProjectConfig(dc, t.TempDir())
	found := false
	for _, issue := range r.Issues {
		if issue.Field == "daemon.fleet.url" && issue.Severity == "warning" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about missing fleet URL when Fleet.URL is empty, got: %s", r.FormatIssues())
	}
}

func TestValidateCrossField_FleetWithURL_NoWarning(t *testing.T) {
	dc := &DaemonConfig{
		Daemon: DaemonSettings{
			IssueBackend: IssueBackendFleet,
			Fleet:        &FleetSettings{URL: "https://fleet.example.com"},
		},
		Roles: make(map[string]RoleConfig),
	}
	r := ValidateProjectConfig(dc, t.TempDir())
	for _, issue := range r.Issues {
		if issue.Field == "daemon.fleet.url" {
			t.Errorf("unexpected fleet URL issue when URL is configured: %s", issue.Message)
		}
	}
}

func TestIsValidGroupName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"lowercase", "backend", true},
		{"with hyphen", "my-group", true},
		{"numeric", "123", true},
		{"alphanumeric", "group1", true},
		{"single char", "a", true},
		{"empty", "", false},
		{"uppercase", "Backend", false},
		{"with space", "my group", false},
		{"with underscore", "my_group", false},
		{"starts with hyphen", "-group", false},
		{"with dot", "my.group", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidGroupName(tt.input); got != tt.want {
				t.Errorf("isValidGroupName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
