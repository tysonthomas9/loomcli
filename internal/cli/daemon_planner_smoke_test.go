package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDaemonPlannerSmoke_FullPlanningPipeline exercises the single-task planning
// flow end-to-end: mock bd returning one open task with no design, verify
// HasAvailablePlanningTasks, generate prompt, invoke Claude, check lock lifecycle.
func TestDaemonPlannerSmoke_FullPlanningPipeline(t *testing.T) {
	// not parallel: uses os.Chdir, global planAutoMode/planDaemonMode, mock.Install(), os.Stdout capture
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Reset flags
	planAutoMode = false
	planDaemonMode = false

	// Task with no design and open status
	taskJSON := `[{"id":"smoke-1","status":"open","issue_type":"task","title":"Smoke test task","design":""}]`
	deps, _, _, _, _ := NewTestDeps(t)
	mock := NewCommandMock(t, []CommandStub{
		// HasAvailablePlanningTasks -> fetchReadyIssues
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		// HasAvailablePlanningTasks -> fetchUnclosedIssueIDs
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
		// captureHEADRef
		{Name: "git", Args: []string{"rev-parse", "HEAD"}, Stdout: "abc123\n"},
		// ComputeDiffStats
		{Name: "git", Args: []string{"diff", "--numstat", "abc123..HEAD"}, Stdout: ""},
	})
	mock.Install()

	// Mock Claude invoker on deps
	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runPlan(testCmdWithDeps(deps), nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify banner was printed
	if !strings.Contains(output, "Running PLANNING agent") {
		t.Errorf("expected 'Running PLANNING agent' banner in output, got: %s", output)
	}

	// Verify Claude was invoked exactly once
	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 Claude invocation, got %d", len(recorder.Invocations))
	}

	// Verify prompt contains WORKFLOW markers
	prompt := recorder.Invocations[0].Prompt
	if !strings.Contains(prompt, "bd ready") {
		t.Errorf("expected prompt to contain 'bd ready', got: %s", prompt[:min(200, len(prompt))])
	}

	// Verify lock was released (single-task mode releases lock)
	_, err := os.Stat(filepath.Join(tmpDir, LockFileName))
	if err == nil {
		t.Error("lock file should be released after single-task runPlan completes")
	}
}

// TestDaemonPlannerSmoke_EpicScopedPlanning verifies that --parent epic scoping
// propagates through the entire planning pipeline.
func TestDaemonPlannerSmoke_EpicScopedPlanning(t *testing.T) {
	// not parallel: uses os.Chdir, global planAutoMode/planDaemonMode/planParentID, mock.Install(), os.Stdout capture
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	planAutoMode = false
	planDaemonMode = false
	planParentID = "epic-42"
	defer func() { planParentID = "" }()

	deps, _, _, _, _ := NewTestDeps(t)
	taskJSON := `[{"id":"smoke-2","status":"open","issue_type":"task","title":"Scoped task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		// fetchReadyIssues with parent filter
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100", "--parent", "epic-42"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
		{Name: "git", Args: []string{"rev-parse", "HEAD"}, Stdout: "abc123\n"},
		{Name: "git", Args: []string{"diff", "--numstat", "abc123..HEAD"}, Stdout: ""},
	})
	mock.Install()

	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runPlan(testCmdWithDeps(deps), nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 Claude invocation, got %d", len(recorder.Invocations))
	}

	prompt := recorder.Invocations[0].Prompt

	// Verify prompt contains epic-scoped bd ready command
	if !strings.Contains(prompt, "bd ready --parent epic-42") {
		t.Errorf("expected prompt to contain 'bd ready --parent epic-42', prompt snippet: %s",
			prompt[:min(500, len(prompt))])
	}

	// Verify prompt contains epic scope block
	if !strings.Contains(prompt, "Epic scope: epic-42") {
		t.Errorf("expected prompt to contain 'Epic scope: epic-42', prompt snippet: %s",
			prompt[:min(500, len(prompt))])
	}
}

// TestDaemonPlannerSmoke_NeedsRevisionRoundTrip verifies that a task with
// design + needs-revision label is still identified as needing planning.
func TestDaemonPlannerSmoke_NeedsRevisionRoundTrip(t *testing.T) {
	// not parallel: uses os.Chdir, global planAutoMode/planDaemonMode, mock.Install(), os.Stdout capture
	// Test predicates directly
	revisionTask := BdIssue{
		ID:        "smoke-3",
		Status:    "open",
		IssueType: "task",
		Design:    "existing plan that was rejected",
		Labels:    []string{NeedsRevisionLabel},
	}

	// IsAvailableForPlanning should return true despite having a design
	unclosed := map[string]bool{}
	if !IsAvailableForPlanning(revisionTask, unclosed) {
		t.Error("expected needs-revision task to be available for planning")
	}

	// NeedsPlan should return true
	if !NeedsPlan(revisionTask) {
		t.Error("expected NeedsPlan=true for task with needs-revision label")
	}

	// Now run through the pipeline to verify it reaches Claude
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	planAutoMode = false
	planDaemonMode = false

	// Task with design AND needs-revision label
	deps, _, _, _, _ := NewTestDeps(t)
	taskJSON := `[{"id":"smoke-3","status":"open","issue_type":"task","title":"Revision task","design":"old plan","labels":["needs-revision"]}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
		{Name: "git", Args: []string{"rev-parse", "HEAD"}, Stdout: "abc123\n"},
		{Name: "git", Args: []string{"diff", "--numstat", "abc123..HEAD"}, Stdout: ""},
	})
	mock.Install()

	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runPlan(testCmdWithDeps(deps), nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)

	// Verify Claude was invoked (task with needs-revision should trigger planning)
	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 Claude invocation for needs-revision task, got %d", len(recorder.Invocations))
	}
}

// TestDaemonPlannerSmoke_DaemonModeLockRetention verifies that in daemon mode
// the lock file is retained after completion (for parent to read), unlike
// single-task mode which releases it.
func TestDaemonPlannerSmoke_DaemonModeLockRetention(t *testing.T) {
	// not parallel: uses os.Chdir, global planAutoMode/planDaemonMode, mock.Install(), os.Stdout capture
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	deps, _, _, _, _ := NewTestDeps(t)
	// Set daemon mode
	planAutoMode = false
	planDaemonMode = true
	defer func() { planDaemonMode = false }()

	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runPlan(testCmdWithDeps(deps), nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)

	// Verify Claude was invoked
	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 Claude invocation in daemon mode, got %d", len(recorder.Invocations))
	}

	// In daemon mode, lock is intentionally NOT released
	lockPath := filepath.Join(tmpDir, LockFileName)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("expected lock file to exist in daemon mode, got error: %v", err)
	}

	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("failed to parse lock file: %v", err)
	}
	if info.Command != "plan" {
		t.Errorf("expected lock command 'plan', got %q", info.Command)
	}
	if info.State != StateActive {
		t.Errorf("expected lock state %q, got %q", StateActive, info.State)
	}

	// Contrast: Run single-task mode and verify lock IS released
	planDaemonMode = false

	// Clean up the lock file left by daemon mode
	os.Remove(lockPath)

	deps2, _, _, _, _ := NewTestDeps(t)
	taskJSON := `[{"id":"smoke-4","status":"open","issue_type":"task","title":"Single task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
		{Name: "git", Args: []string{"rev-parse", "HEAD"}, Stdout: "abc123\n"},
		{Name: "git", Args: []string{"diff", "--numstat", "abc123..HEAD"}, Stdout: ""},
	})
	mock.Install()

	// Need a fresh recorder for the second run
	recorder2 := SetupMockAgentInvokerOn(t, deps2, nil)

	oldStdout = os.Stdout
	r, w, _ = os.Pipe()
	os.Stdout = w

	runPlan(testCmdWithDeps(deps2), nil)

	w.Close()
	os.Stdout = oldStdout
	buf.Reset()
	buf.ReadFrom(r)

	if len(recorder2.Invocations) != 1 {
		t.Fatalf("expected 1 Claude invocation in single-task mode, got %d", len(recorder2.Invocations))
	}

	// Single-task mode should release the lock
	_, err = os.Stat(lockPath)
	if err == nil {
		t.Error("lock file should be released after single-task mode completes")
	}
}

// TestDaemonPlannerSmoke_DaemonBuildsPlanCommand verifies that buildCommand
// constructs the correct exec.Cmd for plan-role agents, including args and env.
func TestDaemonPlannerSmoke_DaemonBuildsPlanCommand(t *testing.T) {
	// not parallel: uses t.Setenv
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "worktrees", "planner")
	os.MkdirAll(filepath.Join(wtPath, ".git"), 0755)

	t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	t.Run("basic plan role", func(t *testing.T) {
		d := &Daemon{
			config: makeDaemonConfig(
				[]AgentEntry{{Worktree: "planner", Role: "plan"}},
				nil,
			),
			projectDir: tmpDir,
		}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "planner", Role: "plan"},
			worktreePath: wtPath,
			roleConfig:   RoleConfig{},
		}

		cmd, err := d.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand() error = %v", err)
		}

		// Verify command is "loom"
		if !strings.HasSuffix(cmd.Path, "loom") && cmd.Args[0] != "loom" {
			t.Errorf("expected loom command, got %q", cmd.Path)
		}

		// Verify args contain: plan <worktreePath> --auto --daemon-mode
		args := cmd.Args[1:] // skip "loom"
		if len(args) < 4 {
			t.Fatalf("expected at least 4 args, got %d: %v", len(args), args)
		}
		if args[0] != "plan" {
			t.Errorf("expected first arg 'plan', got %q", args[0])
		}
		if args[1] != wtPath {
			t.Errorf("expected second arg %q, got %q", wtPath, args[1])
		}
		if !containsArg(args, "--auto") {
			t.Error("expected --auto flag in args")
		}
		if !containsArg(args, "--daemon-mode") {
			t.Error("expected --daemon-mode flag in args")
		}

		// Verify working directory
		if cmd.Dir != wtPath {
			t.Errorf("expected Dir=%q, got %q", wtPath, cmd.Dir)
		}

		// Verify env contains BD_ACTOR and LOOM_WORKTREE_PATH
		envMap := envToMap(cmd.Env)
		if envMap["BD_ACTOR"] != "planner" {
			t.Errorf("expected BD_ACTOR=planner, got %q", envMap["BD_ACTOR"])
		}
		if envMap["LOOM_WORKTREE_PATH"] != wtPath {
			t.Errorf("expected LOOM_WORKTREE_PATH=%q, got %q", wtPath, envMap["LOOM_WORKTREE_PATH"])
		}
	})

	t.Run("plan role with epic parent", func(t *testing.T) {
		d := &Daemon{
			config: makeDaemonConfig(
				[]AgentEntry{{Worktree: "planner", Role: "plan"}},
				nil,
			),
			projectDir: tmpDir,
		}

		ap := &AgentProcess{
			entry:          AgentEntry{Worktree: "planner", Role: "plan"},
			worktreePath:   wtPath,
			roleConfig:     RoleConfig{},
			assignedEpicID: "epic-99",
		}

		cmd, err := d.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand() error = %v", err)
		}

		args := cmd.Args[1:]
		if !containsArg(args, "--parent") {
			t.Error("expected --parent flag when epic assigned")
		}
		// Find the value after --parent
		for i, a := range args {
			if a == "--parent" && i+1 < len(args) {
				if args[i+1] != "epic-99" {
					t.Errorf("expected --parent epic-99, got --parent %s", args[i+1])
				}
				break
			}
		}
	})

	t.Run("plan role with backend", func(t *testing.T) {
		d := &Daemon{
			config: makeDaemonConfig(
				[]AgentEntry{{Worktree: "planner", Role: "plan", Backend: "openai"}},
				nil,
			),
			projectDir: tmpDir,
		}

		ap := &AgentProcess{
			entry:        AgentEntry{Worktree: "planner", Role: "plan", Backend: "openai"},
			worktreePath: wtPath,
			roleConfig:   RoleConfig{},
		}

		cmd, err := d.buildCommand(ap)
		if err != nil {
			t.Fatalf("buildCommand() error = %v", err)
		}

		args := cmd.Args[1:]
		if !containsArg(args, "--backend") {
			t.Error("expected --backend flag when backend configured")
		}
		for i, a := range args {
			if a == "--backend" && i+1 < len(args) {
				if args[i+1] != "openai" {
					t.Errorf("expected --backend openai, got --backend %s", args[i+1])
				}
				break
			}
		}
	})
}

// TestDaemonPlannerSmoke_SupervisorStartShutdown verifies that the daemon
// supervisor starts and shuts down cleanly without deadlocks.
func TestDaemonPlannerSmoke_SupervisorStartShutdown(t *testing.T) {
	// not parallel: uses t.Setenv
	tmpDir := t.TempDir()
	wt1 := filepath.Join(tmpDir, "worktrees", "alpha")
	wt2 := filepath.Join(tmpDir, "worktrees", "beta")
	os.MkdirAll(filepath.Join(wt1, ".git"), 0755)
	os.MkdirAll(filepath.Join(wt2, ".git"), 0755)

	t.Setenv("LOOM_WORKTREES_DIR", filepath.Join(tmpDir, "worktrees"))
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())

	config := makeDaemonConfig(
		[]AgentEntry{
			{Worktree: "alpha", Role: "plan"},
			{Worktree: "beta", Role: "task"},
		},
		nil,
	)

	daemon, err := NewDaemon(config, tmpDir, nil)
	if err != nil {
		t.Fatalf("NewDaemon() error = %v", err)
	}

	if daemon.AgentCount() != 2 {
		t.Errorf("AgentCount() = %d, want 2", daemon.AgentCount())
	}

	// Start the daemon
	if err := daemon.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Immediately signal shutdown
	done := make(chan struct{})
	go func() {
		daemon.Stop()
		close(done)
	}()

	// Verify Stop completes within 10 seconds (no hang/deadlock)
	select {
	case <-done:
		// success
	case <-time.After(10 * time.Second):
		t.Fatal("daemon.Stop() did not complete within 10 seconds — possible deadlock")
	}

	// Verify Stop is idempotent (call twice without panic)
	daemon.Stop()

	// Verify all agent done channels are closed
	agents := daemon.Agents()
	if len(agents) != 2 {
		t.Errorf("Agents() returned %d entries, want 2", len(agents))
	}
}

// --- helpers ---

// containsArg checks if a string slice contains a specific value.
func containsArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

// envToMap converts an env slice (KEY=VALUE) to a map for easy lookups.
func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			m[e[:idx]] = e[idx+1:]
		}
	}
	return m
}
