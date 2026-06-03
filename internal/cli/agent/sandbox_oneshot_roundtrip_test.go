package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeOpenshell records each invocation's args to $OPENSHELL_LOG. It models the
// OpenShell v0.0.53 flow (create → upload → exec → delete); on "sandbox exec" —
// when the bootstrap actually runs — it simulates the agent doing work and
// pushing the branch back: it clones $FAKE_REMOTE at $FAKE_BRANCH, adds a marker
// file, commits, and pushes. This verifies the host-side round-trip
// (push → create → upload → exec → fetch + ff-merge) without a real gateway.
const fakeOpenshell = `#!/bin/sh
printf '%s\n' "$*" >> "$OPENSHELL_LOG"
if [ "$2" = "exec" ]; then
  work=$(mktemp -d)
  if ! git clone --branch "$FAKE_BRANCH" "$FAKE_REMOTE" "$work/clone" >/dev/null 2>&1; then
    echo "CLONE_FAILED" >> "$OPENSHELL_LOG"; exit 0
  fi
  cd "$work/clone" || exit 0
  echo sandbox-work > SANDBOX_MARKER
  git -c user.email=s@s.local -c user.name=sbx add -A
  git -c user.email=s@s.local -c user.name=sbx commit -m "sandbox agent work" >/dev/null 2>&1
  git push origin "$FAKE_BRANCH" >/dev/null 2>&1
fi
exit 0
`

// TestSandboxOneshot_RoundTrip exercises the full one-shot orchestration against
// a real local git remote with a fake openshell binary on PATH. It verifies the
// host pushes the branch, invokes openshell with the expected create args, and
// fast-forwards the sandbox's pushed work back into the local worktree.
func TestSandboxOneshot_RoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake openshell is a /bin/sh script")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()
	// Redirect os.TempDir() (used for the loom-binary upload copy) into the test sandbox.
	t.Setenv("TMPDIR", filepath.Join(root, "tmp"))
	mustMkdir(t, filepath.Join(root, "tmp"))

	const branch = "sbx"
	remote := filepath.Join(root, "remote.git")
	proj := filepath.Join(root, "proj")

	// Bare remote.
	runGit(t, root, "init", "--bare", remote)

	// Project repo with a .loom marker, on branch "sbx", origin → bare remote.
	mustMkdir(t, proj)
	runGit(t, proj, "init")
	runGit(t, proj, "config", "user.email", "dev@local")
	runGit(t, proj, "config", "user.name", "dev")
	mustMkdir(t, filepath.Join(proj, ".loom"))
	mustWrite(t, filepath.Join(proj, "README.md"), "seed\n")
	runGit(t, proj, "checkout", "-b", branch)
	runGit(t, proj, "add", "-A")
	runGit(t, proj, "commit", "-m", "seed")
	runGit(t, proj, "remote", "add", "origin", remote)

	// Fake openshell on PATH.
	binDir := filepath.Join(root, "bin")
	mustMkdir(t, binDir)
	mustWriteExec(t, filepath.Join(binDir, "openshell"), fakeOpenshell)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	openshellLog := filepath.Join(root, "openshell.log")
	t.Setenv("OPENSHELL_LOG", openshellLog)
	t.Setenv("FAKE_REMOTE", remote)
	t.Setenv("FAKE_BRANCH", branch)

	// FleetDB connectivity preconditions: isolate config so no real workspace is
	// resolved, then supply a container-reachable fleet-db URL + workspace via env.
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "loomcfg"))
	t.Setenv("LOOM_SANDBOX_FLEETDB_URL", "http://host.containers.internal:8080")
	t.Setenv("LOOM_WORKSPACE", "ws-test")

	// F1: the sandbox is Linux, so on a non-linux host the upload must be a linux
	// loom build. Point at a dummy file (the fake openshell only logs the upload).
	loomLinux := filepath.Join(root, "loom-linux")
	mustWrite(t, loomLinux, "#!fake-linux-loom\n")
	t.Setenv("LOOM_SANDBOX_LOOM_BIN", loomLinux)

	// Run the real one-shot flow.
	if err := runSandboxOneshot(SandboxOneshotConfig{
		AgentType:    "task",
		AgentName:    "falcon",
		WorktreePath: proj,
		ParentID:     "epic-9",
	}); err != nil {
		t.Fatalf("runSandboxOneshot: %v", err)
	}

	// 1. The sandbox's pushed work must have been fast-forwarded into the worktree.
	if _, err := os.Stat(filepath.Join(proj, "SANDBOX_MARKER")); err != nil {
		t.Errorf("merge-back failed: SANDBOX_MARKER not present in worktree: %v", err)
	}

	// 2. openshell must have been invoked with the expected create command.
	logBytes, err := os.ReadFile(openshellLog)
	if err != nil {
		t.Fatalf("read openshell log: %v", err)
	}
	log := string(logBytes)
	if strings.Contains(log, "CLONE_FAILED") {
		t.Fatalf("fake openshell could not clone the pushed branch:\n%s", log)
	}
	// The openshell *argv* log shows the orchestration. The bootstrap's contents
	// (git clone, --parent, LOOM_FLEET_DB_URL exports, …) live in the uploaded
	// bootstrap file — not in any argument — and are covered by the
	// buildOneshotCommand unit tests.
	for _, want := range []string{
		"sandbox create --name loom-falcon-", // create
		"--provider claude",                  // default providers
		"--provider github",
		"--auto-providers",
		"--policy",                    // RW4: auto-generated OPA policy (fleet-db + repo endpoints)
		"-- true",                     // F2: create runs a trivial command and returns (no shell attach)
		"sandbox upload loom-falcon-", // uploads (loom binary + bootstrap)
		"/sandbox/loom",               // loom binary destination
		"/sandbox/bootstrap.sh",       // F3: bootstrap uploaded as a file
		"sandbox exec -n loom-falcon-",
		"sh /sandbox/bootstrap.sh", // F3: run by path, not inline `-- sh -c <script>`
	} {
		if !strings.Contains(log, want) {
			t.Errorf("openshell invocation missing %q\n--- log ---\n%s", want, log)
		}
	}
	// v0.0.53 constraints: no --upload create flag; one-shot is interactive → no
	// --no-tty; and F3 — the bootstrap is never passed inline, so `sandbox exec`
	// never receives a multi-line `sh -c` argument. (--policy IS expected now:
	// RW4 auto-generates an OPA policy when none is supplied.)
	for _, absent := range []string{"--upload", ":/sandbox/bin", "--no-tty", "sh -c"} {
		if strings.Contains(log, absent) {
			t.Errorf("unexpected %q in openshell invocations\n%s", absent, log)
		}
	}

	// 3. delete was called for cleanup (stale + deferred).
	if n := strings.Count(log, "sandbox delete "); n < 1 {
		t.Errorf("expected sandbox delete to be invoked, log:\n%s", log)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:norawexec // test helper drives a real local git repo
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustWriteExec(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil { //nolint:gosec // test helper binary
		t.Fatalf("write %s: %v", path, err)
	}
}
