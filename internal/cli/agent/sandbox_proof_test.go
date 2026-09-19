package agent

// Fix-verification tests for the PR #126 vetting findings. Each test asserts the
// corrected behavior after the Phase 1 (leak) and Phase 2 (in-container target +
// identity) changes:
//
//  1. TestFix_CleanupOnAgentFailure: a non-zero in-sandbox agent exit now returns
//     (exitCode, nil) WITHOUT os.Exit, so deferred cleanup runs — the scoped API
//     key is revoked and the sandbox is deleted.
//  2. TestFix_BootstrapUsesAbsolutePath: the bootstrap targets /sandbox/repo (not
//     worktrees/<agent>) and exports LOOM_AGENT_NAME.
//  3. TestFix_AbsoluteTargetCarriesAgentName: an absolute target resolves with
//     zero fleet-db calls and honors LOOM_AGENT_NAME instead of collapsing to
//     filepath.Base.
//  4. TestRelativeTargetModeCloudUsesScopedWorkspaceAndSuggestsAbsolutePath:
//     Phase 4 flipped the old relative-target behavior: it resolves its active
//     workspace through the scoped route, never the admin workspace list, then
//     tells an in-container caller without a local Path to use an absolute path.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/workspace"
	"github.com/tysonthomas9/loomcli/internal/sandbox"
)

// recordingFleetDB is a fake fleet-db that records every request line and serves
// the apikey provision/revoke routes, a scoped workspace, and a 403 admin
// workspace list — matching what real fleet-db (PR #76, authz on) returns for
// a workspace-scoped key.
type recordingFleetDB struct {
	mu   sync.Mutex
	reqs []string
}

func (r *recordingFleetDB) record(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req.Method+" "+req.URL.Path)
}

func (r *recordingFleetDB) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.reqs...)
}

func (r *recordingFleetDB) count(prefix string) int {
	n := 0
	for _, req := range r.requests() {
		if strings.HasPrefix(req, prefix) {
			n++
		}
	}
	return n
}

func (r *recordingFleetDB) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.record(req)
		switch {
		case req.Method == "POST" && req.URL.Path == "/api/v1/admin/apikeys":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"key":"scoped-developer-key-123"}`)
		case req.Method == "DELETE" && strings.HasPrefix(req.URL.Path, "/api/v1/admin/apikeys/"):
			w.WriteHeader(http.StatusNoContent)
		case req.Method == "GET" && req.URL.Path == "/api/v1/admin/workspaces":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":"forbidden: workspace.list requires a global role"}`)
		case req.Method == "GET" && req.URL.Path == "/api/v1/ws-test/workspace":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"key":"ws-test","name":"Workspace Test"}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{}`)
		}
	})
}

// setupOneshotFixture builds the round-trip fixture: a project repo on a named
// branch with a bare origin, a fake openshell on PATH whose exec exits execExit,
// and isolated config/tmp dirs.
func setupOneshotFixture(t *testing.T, root string, execExit int) (proj string, openshellLog string) {
	t.Helper()
	t.Setenv("TMPDIR", filepath.Join(root, "tmp"))
	mustMkdir(t, filepath.Join(root, "tmp"))

	const branch = "sbx"
	remote := filepath.Join(root, "remote.git")
	proj = filepath.Join(root, "proj")

	runGit(t, root, "init", "--bare", remote)
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

	binDir := filepath.Join(root, "bin")
	mustMkdir(t, binDir)
	fakeOpenshellExit := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> "$OPENSHELL_LOG"
if [ "$2" = "exec" ]; then
  exit %d
fi
exit 0
`, execExit)
	mustWriteExec(t, filepath.Join(binDir, "openshell"), fakeOpenshellExit)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	openshellLog = filepath.Join(root, "openshell.log")
	t.Setenv("OPENSHELL_LOG", openshellLog)
	t.Setenv("LOOM_SANDBOX_REPO_URL", "https://git.example.com/repo.git")
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "loomcfg"))
	t.Setenv("LOOM_WORKSPACE", "ws-test")

	loomLinux := filepath.Join(root, "loom-linux")
	mustWrite(t, loomLinux, "#!fake-linux-loom\n")
	t.Setenv("LOOM_SANDBOX_LOOM_BIN", loomLinux)
	return proj, openshellLog
}

// TestFix_InvalidRepoURLHasNoSideEffects verifies transport rejection occurs
// before credential provisioning and before any OpenShell process is spawned.
func TestFix_InvalidRepoURLHasNoSideEffects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake openshell is a /bin/sh script")
	}
	fleet := &recordingFleetDB{}
	srv := httptest.NewServer(fleet.handler())
	defer srv.Close()

	root := t.TempDir()
	proj, openshellLog := setupOneshotFixture(t, root, 0)
	t.Setenv("LOOM_SANDBOX_REPO_URL", "file:///tmp/repo.git")
	t.Setenv("LOOM_FLEET_DB_URL", srv.URL)
	t.Setenv("LOOM_FLEET_DB_API_KEY", "admin-key")
	t.Setenv("LOOM_SANDBOX_FLEETDB_URL", "http://host.containers.internal:8080")

	_, err := runSandboxOneshot(SandboxOneshotConfig{
		AgentType: "task", AgentName: "falcon", WorktreePath: proj,
	})
	if err == nil || !strings.Contains(err.Error(), "LOOM_SANDBOX_REPO_URL") {
		t.Fatalf("runSandboxOneshot error = %v, want transport error naming LOOM_SANDBOX_REPO_URL", err)
	}
	if got := len(fleet.requests()); got != 0 {
		t.Errorf("expected zero credential-provisioning HTTP calls, saw %v", fleet.requests())
	}
	if log, readErr := os.ReadFile(openshellLog); readErr == nil && len(log) != 0 {
		t.Errorf("expected zero openshell invocations, log:\n%s", log)
	} else if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read openshell log: %v", readErr)
	}
}

// TestFix_CleanupOnAgentFailure: a failing in-sandbox agent must not leak. After
// the fix runSandboxOneshot returns (17, nil) and its deferred cleanup revokes
// the scoped key and deletes the sandbox.
func TestFix_CleanupOnAgentFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake openshell is a /bin/sh script")
	}
	fleet := &recordingFleetDB{}
	srv := httptest.NewServer(fleet.handler())
	defer srv.Close()

	root := t.TempDir()
	proj, openshellLog := setupOneshotFixture(t, root, 17)

	// Admin credentials on the host → ProvisionCredential mints a scoped key.
	t.Setenv("LOOM_FLEET_DB_URL", srv.URL)
	t.Setenv("LOOM_FLEET_DB_API_KEY", "admin-key")
	t.Setenv("LOOM_SANDBOX_FLEETDB_URL", "http://host.containers.internal:8080")

	exitCode, err := runSandboxOneshot(SandboxOneshotConfig{
		AgentType:    "task",
		AgentName:    "falcon",
		WorktreePath: proj,
	})
	if err != nil {
		t.Fatalf("runSandboxOneshot returned error: %v", err)
	}
	if exitCode != 17 {
		t.Errorf("exit code = %d, want 17 (agent's code preserved)", exitCode)
	}

	logBytes, readErr := os.ReadFile(openshellLog)
	if readErr != nil {
		t.Fatalf("read openshell log: %v", readErr)
	}
	logStr := string(logBytes)
	execIdx := strings.Index(logStr, "sandbox exec")
	if execIdx == -1 {
		t.Fatalf("fake openshell exec was never invoked:\n%s", logStr)
	}
	if !strings.Contains(logStr[execIdx:], "sandbox delete") {
		t.Errorf("FIX REGRESSED: no sandbox delete after failed exec:\n%s", logStr)
	} else {
		t.Logf("VERIFIED: sandbox deleted after the failed exec")
	}
	if got := fleet.count("DELETE /api/v1/admin/apikeys/"); got != 1 {
		t.Errorf("FIX REGRESSED: expected exactly one key revoke on failure, got %d (reqs: %v)", got, fleet.requests())
	} else {
		t.Logf("VERIFIED: scoped developer key revoked on the failure path")
	}
}

// TestFix_ControlCleanupOnSuccess: the success path still revokes + deletes.
func TestFix_ControlCleanupOnSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake openshell is a /bin/sh script")
	}
	fleet := &recordingFleetDB{}
	srv := httptest.NewServer(fleet.handler())
	defer srv.Close()

	root := t.TempDir()
	proj, openshellLog := setupOneshotFixture(t, root, 0)
	t.Setenv("LOOM_FLEET_DB_URL", srv.URL)
	t.Setenv("LOOM_FLEET_DB_API_KEY", "admin-key")
	t.Setenv("LOOM_SANDBOX_FLEETDB_URL", "http://host.containers.internal:8080")

	exitCode, err := runSandboxOneshot(SandboxOneshotConfig{
		AgentType:    "task",
		AgentName:    "falcon",
		WorktreePath: proj,
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("runSandboxOneshot = (%d, %v), want (0, nil)", exitCode, err)
	}
	logBytes, _ := os.ReadFile(openshellLog)
	logStr := string(logBytes)
	execIdx := strings.Index(logStr, "sandbox exec")
	if execIdx == -1 || !strings.Contains(logStr[execIdx:], "sandbox delete") {
		t.Errorf("expected sandbox delete after successful exec:\n%s", logStr)
	}
	if got := fleet.count("DELETE /api/v1/admin/apikeys/"); got != 1 {
		t.Errorf("expected exactly one revoke on success, got %d", got)
	}
	// There must be no leftover pre-create "stale" delete (dead code removed).
	if got := strings.Count(logStr, "sandbox delete"); got != 1 {
		t.Errorf("expected exactly one delete total (the deferred teardown), got %d:\n%s", got, logStr)
	} else {
		t.Logf("VERIFIED: exactly one delete (deferred teardown); dead pre-create delete removed")
	}
}

// TestFix_BootstrapUsesAbsolutePath: the generated bootstrap must invoke loom
// against /sandbox/repo and export LOOM_AGENT_NAME, never worktrees/<agent>.
func TestFix_BootstrapUsesAbsolutePath(t *testing.T) {
	cfg := SandboxOneshotConfig{
		AgentType: "task", AgentName: "falcon", WorkspaceID: "ws-123",
		FleetDBURL: "http://host.docker.internal:8080", FleetDBKey: "sk", FleetDBActor: "sandbox:ws-123:falcon:1",
	}
	script := buildOneshotCommand("feature/x", cfg, "https://github.com/o/r.git", sandbox.Config{Backend: "claude"})

	if !strings.Contains(script, "'task' '/sandbox/repo'") {
		t.Errorf("bootstrap must target /sandbox/repo:\n%s", script)
	}
	if strings.Contains(script, "worktrees/") {
		t.Errorf("bootstrap must NOT reference worktrees/<agent>:\n%s", script)
	}
	if !strings.Contains(script, "export LOOM_AGENT_NAME='falcon'") {
		t.Errorf("bootstrap must export LOOM_AGENT_NAME:\n%s", script)
	}
	t.Logf("VERIFIED: bootstrap targets /sandbox/repo and carries LOOM_AGENT_NAME")
}

// TestFix_AbsoluteTargetCarriesAgentName: resolving an absolute target makes zero
// fleet-db calls and uses LOOM_AGENT_NAME for identity (not filepath.Base).
func TestFix_AbsoluteTargetCarriesAgentName(t *testing.T) {
	fleet := &recordingFleetDB{}
	srv := httptest.NewServer(fleet.handler())
	defer srv.Close()

	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_FLEET_DB_URL", srv.URL)
	t.Setenv("LOOM_FLEET_DB_API_KEY", "scoped-developer-key-123")
	t.Setenv("LOOM_WORKSPACE", "ws-test")
	t.Setenv("LOOM_AGENT_NAME", "falcon")

	repo := filepath.Join(t.TempDir(), "repo")
	mustMkdir(t, repo)

	target, err := workspace.ResolveAgentTarget(repo, "")
	if err != nil {
		t.Fatalf("absolute target should resolve locally: %v", err)
	}
	if got := len(fleet.requests()); got != 0 {
		t.Errorf("expected zero fleet-db calls for an absolute target, saw: %v", fleet.requests())
	}
	if target.AgentName != "falcon" {
		t.Errorf("agent name = %q, want %q (from LOOM_AGENT_NAME)", target.AgentName, "falcon")
	} else {
		t.Logf("VERIFIED: absolute target resolved with zero fleet-db calls and agent name %q", target.AgentName)
	}
}

// TestRelativeTargetModeCloudUsesScopedWorkspaceAndSuggestsAbsolutePath pins
// Phase 4's replacement for the old admin-list hazard. Relative targets resolve
// their active workspace through the scoped route; without a host-local Path,
// they then receive an actionable absolute-path remedy.
func TestRelativeTargetModeCloudUsesScopedWorkspaceAndSuggestsAbsolutePath(t *testing.T) {
	fleet := &recordingFleetDB{}
	srv := httptest.NewServer(fleet.handler())
	defer srv.Close()

	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	t.Setenv("LOOM_FLEET_DB_URL", srv.URL)
	t.Setenv("LOOM_FLEET_DB_API_KEY", "scoped-developer-key-123")
	t.Setenv("LOOM_FLEET_DB_ACTOR", "sandbox:ws-test:falcon:1")
	t.Setenv("LOOM_WORKSPACE", "ws-test")

	_, err := workspace.ResolveAgentTarget("worktrees/falcon", "")
	if err == nil {
		t.Fatal("expected relative target without a local workspace path to fail")
	}
	if got := fleet.count("GET /api/v1/admin/workspaces"); got != 0 {
		t.Errorf("admin workspace route hits = %d, want 0; saw: %v", got, fleet.requests())
	}
	if got := fleet.count("GET /api/v1/ws-test/workspace"); got != 1 {
		t.Errorf("scoped workspace route hits = %d, want 1; saw: %v", got, fleet.requests())
	}
	if !strings.Contains(err.Error(), "absolute path") || !strings.Contains(err.Error(), "/sandbox/repo") {
		t.Errorf("relative target error = %q, want actionable absolute-path remedy", err)
	} else {
		t.Logf("VERIFIED: Phase 4 scopes relative-target resolution and returns an absolute-path remedy: %v", err)
	}
}
