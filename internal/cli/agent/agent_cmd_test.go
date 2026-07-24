package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
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
			gotFn, err := mapTaskFilter(tt.filter, "")

			if tt.wantErr {
				if err == nil {
					t.Errorf("mapTaskFilter(%q, \"\") expected error, got nil", tt.filter)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("mapTaskFilter(%q, \"\") error = %q, want to contain %q", tt.filter, err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("mapTaskFilter(%q, \"\") unexpected error: %v", tt.filter, err)
				return
			}

			if gotFn == nil {
				t.Errorf("mapTaskFilter(%q, \"\") returned nil function", tt.filter)
				return
			}

			// Can't compare functions directly, but we can verify they're not nil
			// and that the right function type is returned
		})
	}
}

func TestValidateTaskFilterExecutionMode(t *testing.T) {
	for _, tt := range []struct {
		name       string
		filter     string
		daemonMode bool
		wantErr    string
	}{
		{name: "bug daemon", filter: "bug", daemonMode: true},
		{name: "bug unbound", filter: "bug", wantErr: "supervisor-assigned daemon run"},
		{name: "existing unbound filter", filter: "has_design"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTaskFilterExecutionMode(tt.filter, tt.daemonMode)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("validation: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("validation error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestBindCustomDaemonTask_BugPreservesReviewHandoff(t *testing.T) {
	got := bindCustomDaemonTask("triage this bug", "BUG-42", "bug")
	for _, want := range []string{"BUG-42", "Leave the bug in\nReview", "do NOT run 'loom complete'", "triage this bug"} {
		if !strings.Contains(got, want) {
			t.Fatalf("bug daemon prompt missing %q:\n%s", want, got)
		}
	}

	regular := bindCustomDaemonTask("implement this task", "TASK-42", "has_design")
	if !strings.Contains(regular, "run 'loom complete' and exit") {
		t.Fatalf("regular custom daemon lost completion handoff:\n%s", regular)
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
			taskCheckFn, err := mapTaskFilter(tt.filter, tt.parentID)
			if err != nil {
				t.Fatalf("mapTaskFilter(%q, %q) unexpected error: %v", tt.filter, tt.parentID, err)
			}

			if taskCheckFn == nil {
				t.Fatalf("mapTaskFilter(%q, %q) returned nil function", tt.filter, tt.parentID)
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
				t.Errorf("mapTaskFilter(%q, %q) closure args length = %d, want %d\nGot: %v\nWant: %v",
					tt.filter, tt.parentID, len(capturedArgs), len(tt.expectedArgs), capturedArgs, tt.expectedArgs)
				return
			}

			for i, arg := range tt.expectedArgs {
				if capturedArgs[i] != arg {
					t.Errorf("mapTaskFilter(%q, %q) closure arg[%d] = %q, want %q\nGot: %v\nWant: %v",
						tt.filter, tt.parentID, i, capturedArgs[i], arg, capturedArgs, tt.expectedArgs)
				}
			}
		})
	}
}

func TestValidateAssignedTaskFilter(t *testing.T) {
	t.Run("preserves existing filters without a read", func(t *testing.T) {
		if err := validateAssignedTaskFilter(context.Background(), nil, "", "has_design", false); err != nil {
			t.Fatalf("non-bug preflight: %v", err)
		}
	})

	t.Run("accepts a verified bug assignment", func(t *testing.T) {
		issueBackend := NewMockIssueBackend()
		issueBackend.GetResult = &backend.IssueDetailData{
			IssueData: backend.IssueData{ID: "BUG-1", IssueType: "bug"},
		}
		if err := validateAssignedTaskFilter(context.Background(), issueBackend, "BUG-1", "bug", true); err != nil {
			t.Fatalf("bug preflight: %v", err)
		}
		if len(issueBackend.Calls) != 1 || issueBackend.Calls[0].Method != "Get" {
			t.Fatalf("backend calls = %+v, want one Get", issueBackend.Calls)
		}
	})

	for _, tt := range []struct {
		name      string
		taskID    string
		issue     *backend.IssueDetailData
		backendOK bool
		readOnly  bool
		want      string
	}{
		{name: "writable role", taskID: "BUG-1", backendOK: true, want: "requires read-only execution"},
		{name: "missing assignment", backendOK: true, readOnly: true, want: "requires a supervisor-assigned task"},
		{name: "missing backend", taskID: "BUG-1", readOnly: true, want: "backend is unavailable"},
		{name: "missing card", taskID: "BUG-1", backendOK: true, readOnly: true, want: "issue was not returned"},
		{
			name: "non-bug assignment", taskID: "TASK-1", backendOK: true, readOnly: true,
			issue: &backend.IssueDetailData{IssueData: backend.IssueData{ID: "TASK-1", IssueType: "task"}},
			want:  `issue type "task"`,
		},
		{
			name: "missing issue type", taskID: "TASK-2", backendOK: true, readOnly: true,
			issue: &backend.IssueDetailData{IssueData: backend.IssueData{ID: "TASK-2"}},
			want:  `issue type ""`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var issueBackend backend.IssueBackend
			if tt.backendOK {
				mock := NewMockIssueBackend()
				mock.GetResult = tt.issue
				issueBackend = mock
			}
			err := validateAssignedTaskFilter(context.Background(), issueBackend, tt.taskID, "bug", tt.readOnly)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("preflight error = %v, want %q", err, tt.want)
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
	taskCheckFn, err := mapTaskFilter("needs_design", parentID)
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

func TestMakeCustomPromptGen_PrependsReadOnlyPolicy(t *testing.T) {
	t.Setenv("LOOM_READ_ONLY", "1")
	promptFile := filepath.Join(t.TempDir(), "read-only-prompt.txt")
	if err := os.WriteFile(promptFile, []byte("Inspect {{.WorktreeName}}."), 0600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	result := makeCustomPromptGen(promptFile)("falcon", nil)

	if !strings.HasPrefix(result, "IMPORTANT: You are running in READ-ONLY mode.") {
		t.Fatalf("custom role prompt missing read-only preamble: %q", result)
	}
	if !strings.Contains(result, "Inspect falcon.") {
		t.Fatalf("custom role prompt missing role body: %q", result)
	}
}

func TestRunAgentDaemon_BindsAssignedTaskAndFinalizesHeadlessSession(t *testing.T) {
	// Not parallel: the daemon path owns process-wide backend/session env and
	// leaves its lock for the supervisor to inspect.
	worktree := t.TempDir()
	createGitRepo(t, worktree)
	writeLockInfo(t, worktree, cli.LockInfo{
		PID:             -1,
		Command:         "agent",
		AgentName:       "triage-agent",
		TaskID:          "BUG-OLD",
		TaskStartedAt:   time.Now(),
		ClaudeSessionID: "session-for-old-task",
		RunID:           "run-old",
	})
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOOM_SESSION_ID", "")
	t.Setenv("LOOM_ASSIGNED_TASK_ID", "BUG-42")
	t.Setenv("LOOM_DAEMON_LEAF", "")
	t.Setenv("LOOM_BACKEND", "gemini")
	t.Setenv("LOOM_READ_ONLY", "1")
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)
	backends.ClearResumeSessionID()
	t.Cleanup(backends.ClearResumeSessionID)

	oldTaskFilter, oldParentID := agentTaskFilter, agentParentID
	agentTaskFilter, agentParentID = "bug", ""
	t.Cleanup(func() {
		agentTaskFilter, agentParentID = oldTaskFilter, oldParentID
	})

	deps, _, _, _, issueBackend := NewTestDeps(t)
	status := "in_progress"
	issueBackend.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{IssueData: backend.IssueData{
			ID: "BUG-42", IssueType: "bug", Status: status,
		}}, nil
	}
	recorder := SetupMockAgentInvokerOn(t, deps, nil)
	resumeAtInvoke := ""
	recorder.NonInteractiveFunc = func(string, string, string, <-chan struct{}, *usage.Collector) error {
		resumeAtInvoke = backends.GetResumeSessionID()
		status = "review"
		return nil
	}

	err := runAgentDaemon(deps, worktree, "triage-agent", func(string, *WorkspaceConfig) string {
		return "Investigate the assigned bug and post a triage comment."
	})
	if err != nil {
		t.Fatalf("runAgentDaemon: %v", err)
	}

	if len(recorder.NonInteractiveCalls) != 1 {
		t.Fatalf("non-interactive calls = %d, want 1", len(recorder.NonInteractiveCalls))
	}
	if len(recorder.InteractiveCalls) != 0 {
		t.Fatalf("interactive calls = %d, want 0", len(recorder.InteractiveCalls))
	}
	if resumeAtInvoke != "" {
		t.Fatalf("task B resumed task A session %q", resumeAtInvoke)
	}
	gotPrompt := recorder.NonInteractiveCalls[0].Prompt
	for _, want := range []string{
		"BUG-42",
		"already claimed",
		"Do NOT claim or select another task",
		"Investigate the assigned bug and post a triage comment.",
	} {
		if !strings.Contains(gotPrompt, want) {
			t.Errorf("daemon prompt missing %q:\n%s", want, gotPrompt)
		}
	}

	info, err := cli.ReadLockFile(worktree)
	if err != nil {
		t.Fatalf("read daemon lock: %v", err)
	}
	if info.TaskID != "BUG-42" {
		t.Fatalf("lock task ID = %q, want BUG-42", info.TaskID)
	}

	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("open session store: %v", err)
	}
	records, err := store.SessionsByTask("BUG-42")
	if err != nil {
		t.Fatalf("query task sessions: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("task session count = %d, want 1", len(records))
	}
	if records[0].Status != sessions.StatusCompleted || records[0].ExitCode != 0 {
		t.Fatalf("session outcome = (%s, %d), want (completed, 0)", records[0].Status, records[0].ExitCode)
	}
	if records[0].Phase != "implementation" {
		t.Fatalf("session phase = %q, want implementation", records[0].Phase)
	}
}

func TestRunAgentDaemon_BugFilterRejectsWritableRoleBeforeInvocation(t *testing.T) {
	// Not parallel: this path owns process-wide daemon flags and session env.
	worktree := t.TempDir()
	createGitRepo(t, worktree)
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOOM_SESSION_ID", "")
	t.Setenv("LOOM_ASSIGNED_TASK_ID", "BUG-WRITABLE")
	t.Setenv("LOOM_DAEMON_LEAF", "")
	t.Setenv("LOOM_BACKEND", "gemini")
	t.Setenv("LOOM_READ_ONLY", "")
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)

	oldTaskFilter, oldParentID := agentTaskFilter, agentParentID
	agentTaskFilter, agentParentID = "bug", ""
	t.Cleanup(func() {
		agentTaskFilter, agentParentID = oldTaskFilter, oldParentID
	})

	deps, _, _, _, issueBackend := NewTestDeps(t)
	issueBackend.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) {
		t.Fatal("writable bug role must fail before reading or invoking")
		return nil, nil
	}
	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	err := runAgentDaemon(deps, worktree, "writable-triage-agent", func(string, *WorkspaceConfig) string {
		return "This prompt must not execute."
	})
	if err == nil || !strings.Contains(err.Error(), "requires read-only execution") {
		t.Fatalf("runAgentDaemon error = %v, want read-only guard", err)
	}
	if len(recorder.NonInteractiveCalls) != 0 || len(recorder.InteractiveCalls) != 0 {
		t.Fatalf(
			"model calls = non-interactive:%d interactive:%d, want zero",
			len(recorder.NonInteractiveCalls), len(recorder.InteractiveCalls),
		)
	}
	info, lockErr := cli.ReadLockFile(worktree)
	if lockErr != nil {
		t.Fatalf("read daemon lock: %v", lockErr)
	}
	if info.TaskID != "BUG-WRITABLE" {
		t.Fatalf("rejected assignment lock task = %q, want BUG-WRITABLE", info.TaskID)
	}
}

func TestRunAgentDaemon_BugNoOpFailsSessionAndRetainsResume(t *testing.T) {
	// Not parallel: this path owns process-wide daemon flags and session env.
	worktree := t.TempDir()
	createGitRepo(t, worktree)
	writeLockInfo(t, worktree, cli.LockInfo{
		PID:             -1,
		Command:         "agent",
		AgentName:       "triage-agent",
		TaskID:          "BUG-NOOP",
		TaskStartedAt:   time.Now(),
		ClaudeSessionID: "resume-noop",
		RunID:           "run-noop",
	})
	runtimeDir := t.TempDir()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", runtimeDir)
	t.Setenv("LOOM_SESSION_ID", "")
	t.Setenv("LOOM_ASSIGNED_TASK_ID", "BUG-NOOP")
	t.Setenv("LOOM_DAEMON_LEAF", "")
	t.Setenv("LOOM_BACKEND", "gemini")
	t.Setenv("LOOM_READ_ONLY", "1")
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)
	backends.ClearResumeSessionID()
	t.Cleanup(backends.ClearResumeSessionID)

	oldTaskFilter, oldParentID := agentTaskFilter, agentParentID
	agentTaskFilter, agentParentID = "bug", ""
	t.Cleanup(func() {
		agentTaskFilter, agentParentID = oldTaskFilter, oldParentID
	})

	deps, _, _, _, issueBackend := NewTestDeps(t)
	issueBackend.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{
		ID: "BUG-NOOP", IssueType: "bug", Status: "in_progress",
	}}
	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	err := runAgentDaemon(deps, worktree, "triage-agent", func(string, *WorkspaceConfig) string {
		return "Exit without performing the required handoff."
	})
	if err == nil || !strings.Contains(err.Error(), `task status "in_progress"; expected "review"`) {
		t.Fatalf("runAgentDaemon error = %v, want missing Review handoff", err)
	}
	if len(recorder.NonInteractiveCalls) != 1 {
		t.Fatalf("non-interactive calls = %d, want 1", len(recorder.NonInteractiveCalls))
	}

	info, lockErr := cli.ReadLockFile(worktree)
	if lockErr != nil {
		t.Fatalf("read daemon lock: %v", lockErr)
	}
	if info.ClaudeSessionID != "resume-noop" {
		t.Fatalf("failed run cleared resume session = %q, want resume-noop", info.ClaudeSessionID)
	}

	store, storeErr := sessions.NewStore(runtimeDir)
	if storeErr != nil {
		t.Fatalf("open session store: %v", storeErr)
	}
	records, recordsErr := store.SessionsByTask("BUG-NOOP")
	if recordsErr != nil {
		t.Fatalf("query task sessions: %v", recordsErr)
	}
	if len(records) != 1 {
		t.Fatalf("task session count = %d, want 1", len(records))
	}
	if records[0].Status != sessions.StatusFailed || records[0].ExitCode == 0 {
		t.Fatalf(
			"session outcome = (%s, %d), want failed with non-zero exit",
			records[0].Status, records[0].ExitCode,
		)
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
	result := gen("nova", nil)

	if result != rawContent {
		t.Errorf("expected raw content, got: %s", result)
	}
}

func TestMakeCustomPromptGen_MissingFile(t *testing.T) {
	t.Parallel()
	gen := makeCustomPromptGen("/nonexistent/path/prompt.txt")
	result := gen("spark", nil)

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
	result := gen("falcon", nil)

	// Should fallback to raw content since template parsing failed
	// but we can still read the file
	if result != badContent {
		t.Errorf("expected raw content fallback, got: %s", result)
	}
}

func TestMakeCustomPromptGen_WithWorkspace(t *testing.T) {
	t.Parallel()
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
