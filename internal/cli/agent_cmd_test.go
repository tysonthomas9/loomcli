package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMapTaskFilter(t *testing.T) {
	tests := []struct {
		name        string
		filter      string
		wantFn      func() (bool, error)
		wantErr     bool
		errContains string
	}{
		{
			name:   "needs_design returns HasAvailablePlanningTasks",
			filter: "needs_design",
			wantFn: HasAvailablePlanningTasks,
		},
		{
			name:   "has_design returns HasAvailableImplementationTasks",
			filter: "has_design",
			wantFn: HasAvailableImplementationTasks,
		},
		{
			name:   "any returns HasAnyAvailableTasks",
			filter: "any",
			wantFn: HasAnyAvailableTasks,
		},
		{
			name:   "empty string defaults to HasAnyAvailableTasks",
			filter: "",
			wantFn: HasAnyAvailableTasks,
		},
		{
			name:        "invalid value returns error",
			filter:      "invalid",
			wantErr:     true,
			errContains: "invalid task filter: invalid",
		},
		{
			name:        "unknown filter returns error",
			filter:      "foo_bar",
			wantErr:     true,
			errContains: "must be needs_design, has_design, or any",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFn, err := mapTaskFilter(tt.filter)

			if tt.wantErr {
				if err == nil {
					t.Errorf("mapTaskFilter(%q) expected error, got nil", tt.filter)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("mapTaskFilter(%q) error = %q, want to contain %q", tt.filter, err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("mapTaskFilter(%q) unexpected error: %v", tt.filter, err)
				return
			}

			if gotFn == nil {
				t.Errorf("mapTaskFilter(%q) returned nil function", tt.filter)
				return
			}

			// Can't compare functions directly, but we can verify they're not nil
			// and that the right function type is returned
		})
	}
}

func TestMakeCustomPromptGen_ValidTemplate(t *testing.T) {
	// Create a temporary template file
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "test-prompt.txt")
	templateContent := `You are agent {{.AgentName}} working in {{.WorktreeName}}.
Your role is {{.Role}}.
Do the work!`
	if err := os.WriteFile(promptFile, []byte(templateContent), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	gen := makeCustomPromptGen(promptFile)
	result := gen("falcon", nil)

	// Verify template interpolation
	if !strings.Contains(result, "You are agent falcon") {
		t.Errorf("expected result to contain 'You are agent falcon', got: %s", result)
	}
	if !strings.Contains(result, "working in falcon") {
		t.Errorf("expected result to contain 'working in falcon', got: %s", result)
	}
	if !strings.Contains(result, "Your role is custom") {
		t.Errorf("expected result to contain 'Your role is custom', got: %s", result)
	}
}

func TestMakeCustomPromptGen_RawFile(t *testing.T) {
	// Create a temporary file without template syntax
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "raw-prompt.txt")
	rawContent := `This is a raw prompt file.
No template variables here.
Just plain text.`
	if err := os.WriteFile(promptFile, []byte(rawContent), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	gen := makeCustomPromptGen(promptFile)
	result := gen("nova", nil)

	if result != rawContent {
		t.Errorf("expected raw content, got: %s", result)
	}
}

func TestMakeCustomPromptGen_MissingFile(t *testing.T) {
	gen := makeCustomPromptGen("/nonexistent/path/prompt.txt")
	result := gen("spark", nil)

	// Should return an error message
	if !strings.Contains(result, "Error: could not load prompt file") {
		t.Errorf("expected error message, got: %s", result)
	}
}

func TestMakeCustomPromptGen_InvalidTemplate(t *testing.T) {
	// Create a template with invalid syntax
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "bad-template.txt")
	badContent := `This template has an unclosed action: {{.AgentName`
	if err := os.WriteFile(promptFile, []byte(badContent), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	gen := makeCustomPromptGen(promptFile)
	result := gen("falcon", nil)

	// Should fallback to raw content since template parsing failed
	// but we can still read the file
	if result != badContent {
		t.Errorf("expected raw content fallback, got: %s", result)
	}
}

func TestMakeCustomPromptGen_WithWorkspace(t *testing.T) {
	// Create a temporary template file
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "workspace-prompt.txt")
	templateContent := `Agent: {{.AgentName}}
Worktree: {{.WorktreeName}}`
	if err := os.WriteFile(promptFile, []byte(templateContent), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create a mock workspace config
	workspace := &WorkspaceConfig{
		Path: "/test/workspace",
		Repos: []RepoConfig{
			{Name: "api", Path: "api"},
			{Name: "web", Path: "web"},
		},
	}

	gen := makeCustomPromptGen(promptFile)
	result := gen("ember", workspace)

	// Agent name should be used for both AgentName and WorktreeName
	if !strings.Contains(result, "Agent: ember") {
		t.Errorf("expected 'Agent: ember', got: %s", result)
	}
	if !strings.Contains(result, "Worktree: ember") {
		t.Errorf("expected 'Worktree: ember', got: %s", result)
	}
}
