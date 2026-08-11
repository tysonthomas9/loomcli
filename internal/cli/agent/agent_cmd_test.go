package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestMapTaskFilter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		filter      string
		wantErr     bool
		errContains string
	}{
		{
			name:   "needs_design returns a function",
			filter: "needs_design",
		},
		{
			name:   "has_design returns a function",
			filter: "has_design",
		},
		{
			name:   "any returns a function",
			filter: "any",
		},
		{
			name:   "bug returns a function",
			filter: "bug",
		},
		{
			name:   "empty string defaults to any",
			filter: "",
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
			errContains: "must be needs_design, has_design, bug, or any",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFn, err := mapTaskFilter(t.Context(), tt.filter, "")

			if tt.wantErr {
				if err == nil {
					t.Errorf("mapTaskFilter(t.Context(), %q, \"\") expected error, got nil", tt.filter)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("mapTaskFilter(t.Context(), %q, \"\") error = %q, want to contain %q", tt.filter, err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("mapTaskFilter(t.Context(), %q, \"\") unexpected error: %v", tt.filter, err)
				return
			}

			if gotFn == nil {
				t.Errorf("mapTaskFilter(t.Context(), %q, \"\") returned nil function", tt.filter)
				return
			}

			// Can't compare functions directly, but we can verify they're not nil
			// and that the right function type is returned
		})
	}
}

// TestMapTaskFilter_WithParentID verifies that mapTaskFilter returns closures
// that properly pass the parentID through to the underlying task functions.
func TestMapTaskFilter_WithParentID(t *testing.T) {
	// not parallel: uses installExecMock (global state)
	tests := []struct {
		name           string
		filter         string
		parentID       string
		expectedArgs   []string
		expectedResult bool
	}{
		{
			name:           "needs_design with parent ID",
			filter:         "needs_design",
			parentID:       "EPIC-111",
			expectedArgs:   []string{"issue-store", "ready", "--json", "--limit", "10000", "--parent", "EPIC-111"},
			expectedResult: true,
		},
		{
			name:           "needs_design without parent ID",
			filter:         "needs_design",
			parentID:       "",
			expectedArgs:   []string{"issue-store", "ready", "--json", "--limit", "10000"},
			expectedResult: true,
		},
		{
			name:           "has_design with parent ID",
			filter:         "has_design",
			parentID:       "EPIC-222",
			expectedArgs:   []string{"issue-store", "ready", "--json", "--limit", "10000", "--parent", "EPIC-222"},
			expectedResult: true,
		},
		{
			name:           "has_design without parent ID",
			filter:         "has_design",
			parentID:       "",
			expectedArgs:   []string{"issue-store", "ready", "--json", "--limit", "10000"},
			expectedResult: true,
		},
		{
			name:           "any with parent ID",
			filter:         "any",
			parentID:       "EPIC-333",
			expectedArgs:   []string{"issue-store", "ready", "--json", "--limit", "10000", "--parent", "EPIC-333"},
			expectedResult: true,
		},
		{
			name:           "any without parent ID",
			filter:         "any",
			parentID:       "",
			expectedArgs:   []string{"issue-store", "ready", "--json", "--limit", "10000"},
			expectedResult: true,
		},
		{
			name:           "bug with parent ID",
			filter:         "bug",
			parentID:       "EPIC-BUGS",
			expectedArgs:   []string{"issue-store", "ready", "--json", "--limit", "10000", "--parent", "EPIC-BUGS"},
			expectedResult: true,
		},
		{
			name:           "empty filter defaults to any with parent ID",
			filter:         "",
			parentID:       "EPIC-444",
			expectedArgs:   []string{"issue-store", "ready", "--json", "--limit", "10000", "--parent", "EPIC-444"},
			expectedResult: true,
		},
		{
			name:           "empty filter defaults to any without parent ID",
			filter:         "",
			parentID:       "",
			expectedArgs:   []string{"issue-store", "ready", "--json", "--limit", "10000"},
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedArgs []string
			callCount := 0
			installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
				callCount++
				// Capture only the first issue-store ready call for parentID verification.
				if callCount == 1 {
					capturedArgs = append([]string{name}, args...)
				}
				// Return appropriate mock data based on filter
				var mockIssue backend.IssueData
				if tt.filter == "needs_design" {
					mockIssue = backend.IssueData{ID: "T-1", Title: "Task", Status: "open", Design: ""}
				} else if tt.filter == "has_design" {
					mockIssue = backend.IssueData{ID: "T-2", Title: "Task with design", Status: "open", Design: "Implementation plan"}
				} else if tt.filter == "bug" {
					mockIssue = backend.IssueData{ID: "BUG-1", Title: "Bug", Status: "open", IssueType: "bug"}
				} else {
					mockIssue = backend.IssueData{ID: "T-3", Title: "Any task", Status: "open", Design: ""}
				}
				return CommandResult{Stdout: mustJSON([]backend.IssueData{mockIssue})}
			}})

			// Get the function from mapTaskFilter
			taskCheckFn, err := mapTaskFilter(t.Context(), tt.filter, tt.parentID)
			if err != nil {
				t.Fatalf("mapTaskFilter(t.Context(), %q, %q) unexpected error: %v", tt.filter, tt.parentID, err)
			}

			if taskCheckFn == nil {
				t.Fatalf("mapTaskFilter(t.Context(), %q, %q) returned nil function", tt.filter, tt.parentID)
			}

			// Call the returned function
			result, err := taskCheckFn()
			if err != nil {
				t.Fatalf("taskCheckFn() unexpected error: %v", err)
			}

			if result != tt.expectedResult {
				t.Errorf("taskCheckFn() = %v, want %v", result, tt.expectedResult)
			}

			// Verify the args match expected
			if len(capturedArgs) != len(tt.expectedArgs) {
				t.Errorf("mapTaskFilter(t.Context(), %q, %q) closure args length = %d, want %d\nGot: %v\nWant: %v",
					tt.filter, tt.parentID, len(capturedArgs), len(tt.expectedArgs), capturedArgs, tt.expectedArgs)
				return
			}

			for i, arg := range tt.expectedArgs {
				if capturedArgs[i] != arg {
					t.Errorf("mapTaskFilter(t.Context(), %q, %q) closure arg[%d] = %q, want %q\nGot: %v\nWant: %v",
						tt.filter, tt.parentID, i, capturedArgs[i], arg, capturedArgs, tt.expectedArgs)
				}
			}
		})
	}
}

// TestMapTaskFilter_ParentIDCapturedInClosure verifies that the parentID is properly
// captured in the closure and persists across multiple calls to the returned function.
func TestMapTaskFilter_ParentIDCapturedInClosure(t *testing.T) {
	// not parallel: uses installExecMock (global state)
	var readyCapturedArgs [][]string

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		fullArgs := append([]string{name}, args...)
		// Only capture issue-store ready calls.
		if len(args) > 0 && args[0] == "ready" {
			readyCapturedArgs = append(readyCapturedArgs, fullArgs)
		}
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task", Status: "open", Design: ""},
			}),
		}
	}})

	// Create a closure with a specific parentID
	parentID := "EPIC-555"
	taskCheckFn, err := mapTaskFilter(t.Context(), "needs_design", parentID)
	if err != nil {
		t.Fatalf("mapTaskFilter() unexpected error: %v", err)
	}

	// Call the function multiple times
	for i := 0; i < 3; i++ {
		_, err := taskCheckFn()
		if err != nil {
			t.Fatalf("taskCheckFn() call %d unexpected error: %v", i, err)
		}
	}

	// Verify issue-store ready was called 3 times (once per taskCheckFn call).
	if len(readyCapturedArgs) != 3 {
		t.Errorf("Expected 3 issue-store ready calls, got %d", len(readyCapturedArgs))
	}

	// Verify all issue-store ready calls included the parentID.
	expectedArgs := []string{"issue-store", "ready", "--json", "--limit", "10000", "--parent", "EPIC-555"}
	for i, capturedArgs := range readyCapturedArgs {
		if len(capturedArgs) != len(expectedArgs) {
			t.Errorf("Call %d: args length = %d, want %d", i, len(capturedArgs), len(expectedArgs))
			continue
		}
		for j, arg := range expectedArgs {
			if capturedArgs[j] != arg {
				t.Errorf("Call %d: arg[%d] = %q, want %q", i, j, capturedArgs[j], arg)
			}
		}
	}
}

func TestMakeCustomPromptGen_ValidTemplate(t *testing.T) {
	t.Parallel()
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
	result := gen("falcon")

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

func TestMakeCustomPromptGen_PrependsReadOnlyPolicy(t *testing.T) {
	t.Setenv("LOOM_READ_ONLY", "1")
	promptFile := filepath.Join(t.TempDir(), "read-only-prompt.txt")
	if err := os.WriteFile(promptFile, []byte("Inspect {{.WorktreeName}}."), 0600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	result := makeCustomPromptGen(promptFile)("falcon")

	if !strings.HasPrefix(result, "IMPORTANT: You are running in REPOSITORY READ-ONLY mode.") {
		t.Fatalf("custom role prompt missing read-only preamble: %q", result)
	}
	if !strings.Contains(result, "Loom task-data operations required by the workflow remain authorized") {
		t.Fatalf("custom role prompt missing task-data authorization: %q", result)
	}
	if !strings.Contains(result, "Inspect falcon.") {
		t.Fatalf("custom role prompt missing role body: %q", result)
	}
}

func TestMakeCustomPromptGen_RawFile(t *testing.T) {
	t.Parallel()
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
	result := gen("nova")

	if result != rawContent {
		t.Errorf("expected raw content, got: %s", result)
	}
}

func TestMakeCustomPromptGen_MissingFile(t *testing.T) {
	t.Parallel()
	gen := makeCustomPromptGen("/nonexistent/path/prompt.txt")
	result := gen("spark")

	// Should return an error message
	if !strings.Contains(result, "Error: could not load prompt file") {
		t.Errorf("expected error message, got: %s", result)
	}
}

func TestMakeCustomPromptGen_InvalidTemplate(t *testing.T) {
	t.Parallel()
	// Create a template with invalid syntax
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "bad-template.txt")
	badContent := `This template has an unclosed action: {{.AgentName`
	if err := os.WriteFile(promptFile, []byte(badContent), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	gen := makeCustomPromptGen(promptFile)
	result := gen("falcon")

	// Should fallback to raw content since template parsing failed
	// but we can still read the file
	if result != badContent {
		t.Errorf("expected raw content fallback, got: %s", result)
	}
}

func TestMakeCustomPromptGen_RendersAgentVariables(t *testing.T) {
	t.Parallel()
	// Create a temporary template file
	tmpDir := t.TempDir()
	promptFile := filepath.Join(tmpDir, "workspace-prompt.txt")
	templateContent := `Agent: {{.AgentName}}
Worktree: {{.WorktreeName}}`
	if err := os.WriteFile(promptFile, []byte(templateContent), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	gen := makeCustomPromptGen(promptFile)
	result := gen("ember")

	// Agent name should be used for both AgentName and WorktreeName
	if !strings.Contains(result, "Agent: ember") {
		t.Errorf("expected 'Agent: ember', got: %s", result)
	}
	if !strings.Contains(result, "Worktree: ember") {
		t.Errorf("expected 'Worktree: ember', got: %s", result)
	}
}
