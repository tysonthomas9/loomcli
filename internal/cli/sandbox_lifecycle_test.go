package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TestSandboxMock_SpawnRecordsCorrectArgs verifies that Spawn invokes the
// openshell binary with the expected "sandbox create" arguments.
// ---------------------------------------------------------------------------
func TestSandboxMock_SpawnRecordsCorrectArgs(t *testing.T) {
	mockBin, logFile := createMockOpenshell(t)

	strategy := &SandboxStrategy{
		cfg: SandboxConfig{
			Providers: []string{"claude", "github"},
			Network:   "open",
		},
		projectDir:   t.TempDir(),
		repoURL:      "git@github.com:test/repo.git",
		openshellBin: mockBin,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
	}

	cmd, err := strategy.Spawn(ap, nil, nil, nil)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	// The mock executes the bootstrap script (sh -c "set -e\ngit clone ..."),
	// which fails because there is no real git remote. We only care that the
	// mock was invoked with the right args, not that the script succeeds.
	_ = cmd.Wait()

	lines := readMockLog(t, logFile)
	// The mock is called twice: first for deleteSandbox (best-effort pre-cleanup)
	// and then for the actual sandbox create.  We want the create call.
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 mock invocations (delete + create), got %d: %v", len(lines), lines)
	}
	createLine := lines[1] // second call is the create

	// --name with loom- prefix
	if !strings.Contains(createLine, "--name loom-falcon-") {
		t.Errorf("expected --name loom-falcon-*, got: %s", createLine)
	}

	// --upload with :/sandbox/bin
	if !strings.Contains(createLine, ":/sandbox/bin") {
		t.Errorf("expected --upload with :/sandbox/bin, got: %s", createLine)
	}

	// --provider claude --provider github
	if !strings.Contains(createLine, "--provider claude") {
		t.Errorf("expected --provider claude, got: %s", createLine)
	}
	if !strings.Contains(createLine, "--provider github") {
		t.Errorf("expected --provider github, got: %s", createLine)
	}

	// --no-tty
	if !strings.Contains(createLine, "--no-tty") {
		t.Errorf("expected --no-tty, got: %s", createLine)
	}

	// trailing "-- sh -c"
	if !strings.Contains(createLine, "-- sh -c") {
		t.Errorf("expected trailing '-- sh -c', got: %s", createLine)
	}

	// --policy should NOT be present for network=open
	if strings.Contains(createLine, "--policy") {
		t.Errorf("--policy should not appear for network=open, got: %s", createLine)
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_BootstrapScriptContent verifies the bootstrap shell script
// generated for the trailing "sh -c" argument.
// ---------------------------------------------------------------------------
func TestSandboxMock_BootstrapScriptContent(t *testing.T) {
	strategy := &SandboxStrategy{
		cfg: SandboxConfig{
			Providers: []string{"claude"},
			Network:   "open",
			Backend:   "claude",
		},
		projectDir: t.TempDir(),
		repoURL:    "git@github.com:test/repo.git",
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
	}

	script := strategy.buildSandboxCommand(ap)

	checks := []string{
		"GIT_SSL_NO_VERIFY=1",
		"git clone",
		"loom task",
		"--auto --daemon-mode",
		"bd sync",
		"git push",
	}
	for _, want := range checks {
		if !strings.Contains(script, want) {
			t.Errorf("bootstrap script missing %q:\n%s", want, script)
		}
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_KillCallsDelete spawns a long-running sandbox (sleep)
// and verifies that Kill invokes "sandbox delete <name>".
// ---------------------------------------------------------------------------
func TestSandboxMock_KillCallsDelete(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls.log")
	scriptPath := filepath.Join(dir, "openshell")

	// This mock script runs "sleep 60" as the trailing command for create,
	// and for delete just logs and exits.
	script := `#!/bin/sh
printf '%s\0' "$*" >> "` + logFile + `"
case "$1 $2" in
    "sandbox create")
        shift; shift
        while [ $# -gt 0 ]; do
            if [ "$1" = "--" ]; then
                shift
                exec "$@"
            fi
            shift
        done
        exit 0
        ;;
    "sandbox delete")
        exit 0
        ;;
    *)
        exit 0
        ;;
esac
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("write mock: %v", err)
	}

	strategy := &SandboxStrategy{
		cfg: SandboxConfig{
			Providers: []string{"claude"},
			Network:   "open",
		},
		projectDir:   t.TempDir(),
		repoURL:      "git@github.com:test/repo.git",
		openshellBin: scriptPath,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
	}

	// Override buildSandboxCommand output so the trailing command is "sleep 60".
	// We cannot override the method, but we can make Spawn use our mock which
	// executes the trailing command.  We need the trailing command to be a
	// long-running process.  Unfortunately buildSandboxCommand is not injectable.
	//
	// Instead, Spawn calls buildCreateArgs which embeds the script.  The mock
	// will exec "sh -c <script>" — the script does git clone etc. which will
	// fail in test context. That's fine — the shell will exit quickly, but the
	// mock is what we're testing.  Let's spawn manually with a known name and
	// use the strategy's Kill method to test delete call.

	// Pre-set the sandbox name to something known.
	sandboxName := "loom-falcon-killtest"
	ap.sandboxName = sandboxName
	ap.mu.Lock()
	ap.pid = 99999 // fake PID so Kill attempts SIGTERM (will fail, that's fine)
	ap.mu.Unlock()

	strategy.Kill(ap)

	lines := readMockLog(t, logFile)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "sandbox delete") && strings.Contains(line, sandboxName) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected mock log to contain 'sandbox delete %s', got lines: %v", sandboxName, lines)
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_CleanupCallsDelete verifies that Cleanup calls
// "sandbox delete <name>" after the agent exits.
// ---------------------------------------------------------------------------
func TestSandboxMock_CleanupCallsDelete(t *testing.T) {
	mockBin, logFile := createMockOpenshell(t)

	strategy := &SandboxStrategy{
		cfg:          SandboxConfig{},
		projectDir:   t.TempDir(),
		repoURL:      "git@github.com:test/repo.git",
		openshellBin: mockBin,
	}

	sandboxName := "loom-falcon-cleanuptest"
	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
		sandboxName:  sandboxName,
	}

	// Cleanup calls git fetch and git merge which will fail in test context
	// (no real repo), but it should still proceed to deleteSandbox.
	_ = strategy.Cleanup(ap)

	lines := readMockLog(t, logFile)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "sandbox delete") && strings.Contains(line, sandboxName) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'sandbox delete %s' in mock log, got: %v", sandboxName, lines)
	}

	// Verify sandboxName was cleared after cleanup.
	ap.mu.Lock()
	remaining := ap.sandboxName
	ap.mu.Unlock()
	if remaining != "" {
		t.Errorf("expected sandboxName to be cleared after Cleanup, got %q", remaining)
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_SpawnFailure verifies that when the openshell binary exits
// non-zero immediately (before Start returns), Spawn propagates the error.
// We simulate this with a script that does exit 1 immediately — but note that
// The subprocess Start call only fails if the binary cannot be executed at all.
// So we test with a non-existent binary path instead.
// ---------------------------------------------------------------------------
func TestSandboxMock_SpawnFailure(t *testing.T) {
	strategy := &SandboxStrategy{
		cfg: SandboxConfig{
			Providers: []string{"claude"},
			Network:   "open",
		},
		projectDir:   t.TempDir(),
		repoURL:      "git@github.com:test/repo.git",
		openshellBin: "/nonexistent/openshell-binary-that-does-not-exist",
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
	}

	_, err := strategy.Spawn(ap, nil, nil, nil)
	if err == nil {
		t.Fatal("expected Spawn to fail with non-existent binary, got nil error")
	}
	if !strings.Contains(err.Error(), "openshell sandbox create") {
		t.Errorf("expected error to mention 'openshell sandbox create', got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_SpawnExitNonZero verifies that cmd.Wait returns an error
// when the mock openshell exits with a non-zero code.
// ---------------------------------------------------------------------------
func TestSandboxMock_SpawnExitNonZero(t *testing.T) {
	mockBin, _ := createMockOpenshellExit(t, 1)

	strategy := &SandboxStrategy{
		cfg: SandboxConfig{
			Providers: []string{"claude"},
			Network:   "open",
		},
		projectDir:   t.TempDir(),
		repoURL:      "git@github.com:test/repo.git",
		openshellBin: mockBin,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
	}

	cmd, err := strategy.Spawn(ap, nil, nil, nil)
	if err != nil {
		t.Fatalf("Spawn Start failed: %v", err)
	}
	err = cmd.Wait()
	if err == nil {
		t.Fatal("expected cmd.Wait to return error for exit code 1, got nil")
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_DeleteFailureSwallowed verifies that Kill does not panic
// when the sandbox delete command fails.
// ---------------------------------------------------------------------------
func TestSandboxMock_DeleteFailureSwallowed(t *testing.T) {
	mockBin, _ := createMockOpenshellExit(t, 1)

	strategy := &SandboxStrategy{
		cfg:          SandboxConfig{},
		projectDir:   t.TempDir(),
		repoURL:      "git@github.com:test/repo.git",
		openshellBin: mockBin,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
		sandboxName:  "loom-falcon-fail",
	}
	// A fake PID that doesn't correspond to a real process — SIGTERM will fail,
	// and then deleteSandbox is called.  Neither should panic.
	ap.mu.Lock()
	ap.pid = 99999
	ap.mu.Unlock()

	// Must not panic.
	strategy.Kill(ap)
}

// ---------------------------------------------------------------------------
// TestSandboxMock_CustomPolicy verifies that a non-"open" network value
// produces a --policy flag in the create args.
// ---------------------------------------------------------------------------
func TestSandboxMock_CustomPolicy(t *testing.T) {
	strategy := &SandboxStrategy{
		cfg: SandboxConfig{
			Providers: []string{"claude"},
			Network:   "./policies/restricted.yaml",
		},
		projectDir: t.TempDir(),
		repoURL:    "git@github.com:test/repo.git",
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
	}

	args := strategy.buildCreateArgs(ap, "loom-falcon-policy")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--policy ./policies/restricted.yaml") {
		t.Errorf("expected '--policy ./policies/restricted.yaml' in args, got: %s", joined)
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_FromImage verifies that a configured From image is
// passed as --from in the create args.
// ---------------------------------------------------------------------------
func TestSandboxMock_FromImage(t *testing.T) {
	strategy := &SandboxStrategy{
		cfg: SandboxConfig{
			Providers: []string{"claude"},
			Network:   "open",
			From:      "ghcr.io/custom/image:latest",
		},
		projectDir: t.TempDir(),
		repoURL:    "git@github.com:test/repo.git",
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
	}

	args := strategy.buildCreateArgs(ap, "loom-falcon-img")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--from ghcr.io/custom/image:latest") {
		t.Errorf("expected '--from ghcr.io/custom/image:latest' in args, got: %s", joined)
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_RaceCondition spawns a sandbox and then calls Kill and
// Cleanup concurrently from goroutines.  Run with -race to verify no
// data races exist.
// ---------------------------------------------------------------------------
func TestSandboxMock_RaceCondition(t *testing.T) {
	mockBin, logFile := createMockOpenshell(t)

	strategy := &SandboxStrategy{
		cfg: SandboxConfig{
			Providers: []string{"claude"},
			Network:   "open",
		},
		projectDir:   t.TempDir(),
		repoURL:      "git@github.com:test/repo.git",
		openshellBin: mockBin,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
		sandboxName:  "loom-falcon-race",
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		strategy.Kill(ap)
	}()

	go func() {
		defer wg.Done()
		_ = strategy.Cleanup(ap)
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for concurrent Kill/Cleanup to finish")
	}

	lines := readMockLog(t, logFile)
	deleteCount := 0
	for _, line := range lines {
		if strings.Contains(line, "sandbox delete") {
			deleteCount++
		}
	}
	if deleteCount == 0 {
		t.Error("expected at least one sandbox delete call")
	}
	// Double-delete is acceptable (best-effort cleanup from both Kill and Cleanup)
	t.Logf("sandbox delete called %d times (1 or 2 expected)", deleteCount)
}

// ---------------------------------------------------------------------------
// TestSandboxMock_OpenshellCmdDefault verifies the openshellCmd helper
// returns "openshell" when openshellBin is empty.
// ---------------------------------------------------------------------------
func TestSandboxMock_OpenshellCmdDefault(t *testing.T) {
	s := &SandboxStrategy{}
	if got := s.openshellCmd(); got != "openshell" {
		t.Errorf("openshellCmd() = %q, want %q", got, "openshell")
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_OpenshellCmdCustom verifies the openshellCmd helper
// returns the configured binary path.
// ---------------------------------------------------------------------------
func TestSandboxMock_OpenshellCmdCustom(t *testing.T) {
	s := &SandboxStrategy{openshellBin: "/usr/local/bin/my-openshell"}
	if got := s.openshellCmd(); got != "/usr/local/bin/my-openshell" {
		t.Errorf("openshellCmd() = %q, want %q", got, "/usr/local/bin/my-openshell")
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_SpawnSetsName verifies that Spawn populates ap.sandboxName
// with a name that has the loom-<worktree> prefix.
// ---------------------------------------------------------------------------
func TestSandboxMock_SpawnSetsName(t *testing.T) {
	mockBin, _ := createMockOpenshell(t)

	strategy := &SandboxStrategy{
		cfg: SandboxConfig{
			Providers: []string{"claude"},
			Network:   "open",
		},
		projectDir:   t.TempDir(),
		repoURL:      "git@github.com:test/repo.git",
		openshellBin: mockBin,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
	}

	cmd, err := strategy.Spawn(ap, nil, nil, nil)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	_ = cmd.Wait()

	if !strings.HasPrefix(ap.sandboxName, "loom-falcon-") {
		t.Errorf("expected sandboxName to start with 'loom-falcon-', got %q", ap.sandboxName)
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_CleanupNoopWhenEmpty verifies Cleanup is a no-op
// when sandboxName is already empty.
// ---------------------------------------------------------------------------
func TestSandboxMock_CleanupNoopWhenEmpty(t *testing.T) {
	mockBin, logFile := createMockOpenshell(t)

	strategy := &SandboxStrategy{
		cfg:          SandboxConfig{},
		projectDir:   t.TempDir(),
		repoURL:      "git@github.com:test/repo.git",
		openshellBin: mockBin,
	}

	ap := &AgentProcess{
		entry:        AgentEntry{Worktree: "falcon", Role: "task"},
		worktreePath: t.TempDir(),
		sandboxName:  "", // already empty
	}

	err := strategy.Cleanup(ap)
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}

	lines := readMockLog(t, logFile)
	if len(lines) != 0 {
		t.Errorf("expected no mock calls for empty sandboxName, got: %v", lines)
	}
}

// ---------------------------------------------------------------------------
// TestSandboxMock_DeleteSandboxEmpty verifies deleteSandbox is a no-op
// when called with an empty name.
// ---------------------------------------------------------------------------
func TestSandboxMock_DeleteSandboxEmpty(t *testing.T) {
	mockBin, logFile := createMockOpenshell(t)

	strategy := &SandboxStrategy{
		openshellBin: mockBin,
	}
	strategy.deleteSandbox("")

	lines := readMockLog(t, logFile)
	if len(lines) != 0 {
		t.Errorf("expected no mock calls for empty name, got: %v", lines)
	}
}
