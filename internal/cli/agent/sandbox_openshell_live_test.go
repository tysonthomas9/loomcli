package agent

// This file is an operator-gated integration test. It deliberately reuses the
// Phase 5 FleetDB/Git fixtures, but replaces fake OpenShell with the real CLI and
// a real gateway. No test state is hand-seeded below the product HTTP APIs.

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sandbox"
)

func TestE2E_OpenShellRBACLiveMatrix(t *testing.T) {
	gateway, fleetDBBin := strings.TrimSpace(os.Getenv("OPENSHELL_GATEWAY_ENDPOINT")), strings.TrimSpace(os.Getenv("FLEET_DB_BIN"))
	openshell, oshErr := exec.LookPath("openshell")
	if gateway == "" || fleetDBBin == "" || oshErr != nil {
		t.Skipf("live OpenShell RBAC matrix requires all three: OPENSHELL_GATEWAY_ENDPOINT (operator-managed gateway), FLEET_DB_BIN, and real openshell on PATH")
	}
	if runtime.GOOS == "windows" {
		t.Skip("live OpenShell RBAC matrix requires POSIX sh and git")
	}
	if _, err := exec.LookPath("redis-cli"); err != nil {
		t.Skip("live OpenShell RBAC matrix requires redis-cli on PATH and Redis on 127.0.0.1:6379")
	}
	version, err := exec.Command(openshell, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("openshell --version: %v: %s", err, version)
	}
	t.Logf("OpenShell contract under test: %s; gateway=%s", strings.TrimSpace(string(version)), gateway)

	if n := redisDBSize(t, 9); n != 0 {
		t.Skipf("live OpenShell RBAC matrix requires empty Redis db 9; DBSIZE=%d", n)
	}
	t.Cleanup(func() { deleteRedisDBKeys(t, 9) })
	root := t.TempDir()
	loomBin := buildSandboxE2ELinuxLoom(t, root)
	hostLoomBin := buildSandboxE2ELoom(t, root)
	fleetHostURL, fleetContainerURL, adminKey := startLiveMatrixFleetDB(t, fleetDBBin, root)
	const workspace = "WS-E2E"
	client := &http.Client{Timeout: 10 * time.Second}
	postJSONStatus(t, client, fleetHostURL+"/api/v1/admin/workspaces", adminKey,
		map[string]any{"key": workspace, "name": "OpenShell live RBAC E2E"}, http.StatusCreated)
	baselineActors := listSandboxE2EAPIKeyActors(t, client, fleetHostURL, adminKey)

	gitRoot := filepath.Join(root, "git")
	mustMkdir(t, gitRoot)
	proj := setupSandboxE2EGitRepo(t, gitRoot)
	gitHostURL, gitContainerURL := serveLiveMatrixGitHTTP(t, gitRoot)
	runGit(t, proj, "remote", "add", "origin", gitHostURL+"/remote.git")

	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "loom-config"))
	t.Setenv("LOOM_FLEET_DB_URL", fleetHostURL)
	t.Setenv("LOOM_FLEET_DB_API_KEY", adminKey)
	t.Setenv("LOOM_FLEET_DB_ACTOR", "sandbox-e2e-admin")
	t.Setenv("LOOM_WORKSPACE", workspace)
	t.Setenv("LOOM_SANDBOX_PROVIDERS", "")

	t.Run("generated-policy-create-and-direct-fleetdb", func(t *testing.T) {
		taskID := seedSandboxE2ETask(t, client, fleetHostURL, adminKey, workspace, "live-direct")
		actor, sandboxName := runLiveMatrixLeg(t, loomBin, fleetContainerURL, "", gitContainerURL, workspace, taskID, false)
		assertSandboxE2ETaskClosed(t, client, fleetHostURL, adminKey, workspace, taskID)
		assertLiveMatrixActorRevoked(t, client, fleetHostURL, adminKey, actor, baselineActors)
		t.Logf("generated sandbox.WritePolicy policy accepted by create; sandbox %s reached Ready; direct RBAC battery=200,403,401", sandboxName)
	})

	t.Run("serve-proxy-denies-direct-fleetdb", func(t *testing.T) {
		proxyHostURL, proxyContainerURL := startLiveMatrixServeProxy(t, hostLoomBin, fleetHostURL)
		_ = proxyHostURL
		taskID := seedSandboxE2ETask(t, client, fleetHostURL, adminKey, workspace, "live-proxy")
		actor, _ := runLiveMatrixLeg(t, loomBin, proxyContainerURL, fleetContainerURL, gitContainerURL, workspace, taskID, true)
		assertSandboxE2ETaskClosed(t, client, fleetHostURL, adminKey, workspace, taskID)
		assertLiveMatrixActorRevoked(t, client, fleetHostURL, adminKey, actor, baselineActors)
		t.Log("proxy path completed scoped claim/close; direct FleetDB egress was denied by generated serve+git-only policy")
	})
}

func runLiveMatrixLeg(t *testing.T, loomBin, fleetURL, deniedFleetURL, repoURL, workspace, taskID string, proxy bool) (string, string) {
	t.Helper()
	cfg := SandboxOneshotConfig{AgentName: "falcon", FleetDBURL: fleetURL, WorkspaceID: workspace}
	revoke, err := provisionSandboxCredential(t.Context(), &cfg)
	if err != nil {
		t.Fatalf("provision scoped credential: %v", err)
	}
	actor := cfg.FleetDBActor
	name := fmt.Sprintf("loom-live-%d", time.Now().UnixNano())
	created := false
	revoked := false
	t.Cleanup(func() {
		if !revoked {
			revoke()
		}
		if created {
			sandbox.DeleteSandbox(name)
		}
	})

	eps := sandbox.PolicyEndpoints(fleetURL, repoURL)
	policy, cleanup, err := sandbox.WritePolicy(eps, []string{sandbox.LoomPath, "/usr/bin/git", "/usr/bin/curl"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	oshCfg := sandbox.DefaultConfig()
	oshCfg.Network = policy
	if err := sandbox.RunOpenshell(sandbox.BuildCreateArgs(name, oshCfg)); err != nil {
		t.Fatalf("generated-policy create: %v", err)
	}
	created = true
	if err := waitLiveSandboxReady(name); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.RunOpenshell([]string{"sandbox", "upload", name, loomBin, sandbox.LoomPath}); err != nil {
		t.Fatalf("upload loom: %v", err)
	}
	script := liveMatrixBootstrap(repoURL, fleetURL, deniedFleetURL, cfg.FleetDBKey, cfg.FleetDBActor, workspace, taskID, proxy)
	scriptPath, removeScript, err := sandbox.WriteBootstrapScript(script)
	if err != nil {
		t.Fatal(err)
	}
	defer removeScript()
	if err := sandbox.RunOpenshell([]string{"sandbox", "upload", name, scriptPath, sandbox.BootstrapPath}); err != nil {
		t.Fatalf("upload bootstrap: %v", err)
	}
	if code, err := sandbox.RunOpenshellExit([]string{"sandbox", "exec", "-n", name, "--", "sh", sandbox.BootstrapPath}); err != nil || code != 0 {
		t.Fatalf("live bootstrap exit=%d err=%v", code, err)
	}
	revoke()
	revoked = true
	sandbox.DeleteSandbox(name)
	created = false
	return actor, name
}

func liveMatrixBootstrap(repoURL, fleetURL, deniedURL, key, actor, workspace, taskID string, proxy bool) string {
	deny := ""
	if proxy {
		deny = fmt.Sprintf("if curl -fsS --max-time 5 -H 'X-API-Key: %s' %s/api/v1/%s/workspace; then echo direct-fleetdb-was-not-denied >&2; exit 91; fi\n", sandbox.ShellQuote(key), sandbox.ShellQuote(deniedURL), sandbox.ShellQuote(workspace))
	}
	return fmt.Sprintf(`set -eu
chmod +x %[1]s
export LOOM_FLEET_DB_URL=%[2]s LOOM_FLEET_DB_API_KEY=%[3]s LOOM_FLEET_DB_ACTOR=%[4]s LOOM_WORKSPACE=%[5]s LOOM_ISSUE_BACKEND=fleetdb
scoped=$(curl -sS -o /dev/null -w '%%{http_code}' -H "X-API-Key: $LOOM_FLEET_DB_API_KEY" "$LOOM_FLEET_DB_URL/api/v1/$LOOM_WORKSPACE/workspace")
admin=$(curl -sS -o /dev/null -w '%%{http_code}' -H "X-API-Key: $LOOM_FLEET_DB_API_KEY" "$LOOM_FLEET_DB_URL/api/v1/admin/workspaces")
anon=$(curl -sS -o /dev/null -w '%%{http_code}' "$LOOM_FLEET_DB_URL/api/v1/$LOOM_WORKSPACE/workspace")
[ "$scoped,$admin,$anon" = "200,403,401" ] || { echo "RBAC=$scoped,$admin,$anon" >&2; exit 90; }
%[6]s
git clone %[7]s /sandbox/repo
cd /sandbox/repo
%[1]s data claim %[8]s
%[1]s data close %[8]s --reason 'OpenShell live matrix'
`, sandbox.LoomPath, sandbox.ShellQuote(fleetURL), sandbox.ShellQuote(key), sandbox.ShellQuote(actor), sandbox.ShellQuote(workspace), deny, sandbox.ShellQuote(repoURL), sandbox.ShellQuote(taskID))
}

func waitLiveSandboxReady(name string) error {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command(sandbox.OpenshellBinary(), "sandbox", "get", name).CombinedOutput()
		if err == nil && strings.Contains(strings.ToLower(string(out)), "ready") {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("sandbox %s did not report Ready within 90s", name)
}

func buildSandboxE2ELinuxLoom(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(root, "loom-linux")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/loom")
	cmd.Dir = sandboxE2ERepoRoot(t)
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Linux loom: %v\n%s", err, out)
	}
	return bin
}

func startLiveMatrixFleetDB(t *testing.T, bin, root string) (string, string, string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	const adminKey = "sandbox-e2e-admin-key"
	cmd := exec.Command(bin, "--addr", fmt.Sprintf("0.0.0.0:%d", port), "--redis-addr", "127.0.0.1:6379", "--redis-db", "9", "--rpc-enabled=false", "--auth-enabled", "--authz-enabled", "--auth-bootstrap-admin-actor", "sandbox-e2e-admin", "--auth-bootstrap-admin-key", adminKey)
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	host := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitHTTPReady(t, host+"/readyz", &log)
	return host, fmt.Sprintf("http://%s:%d", sandbox.HostGateway(), port), adminKey
}

func serveLiveMatrixGitHTTP(t *testing.T, root string) (string, string) {
	t.Helper()
	gitHTTP, err := exec.LookPath("git-http-backend")
	if err != nil {
		gitHTTP = strings.TrimSpace(sandboxE2EGitOutput(t, root, "--exec-path")) + "/git-http-backend"
	}
	h := &cgi.Handler{Path: gitHTTP, Dir: root, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(h)
	s.Listener = ln
	s.Start()
	t.Cleanup(s.Close)
	port := ln.Addr().(*net.TCPAddr).Port
	return fmt.Sprintf("http://127.0.0.1:%d", port), fmt.Sprintf("http://%s:%d", sandbox.HostGateway(), port)
}

func startLiveMatrixServeProxy(t *testing.T, loomBin, upstream string) (string, string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	cmd := exec.Command(loomBin, "serve", "--bind", "0.0.0.0", "--port", fmt.Sprint(port), "--no-daemon", "--fleet-db-proxy-url", upstream)
	cmd.Env = append(os.Environ(), "LOOM_CONFIG_DIR="+t.TempDir())
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start loom serve FleetDB proxy: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	host := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(15 * time.Second)
	listening := false
	for time.Now().Before(deadline) {
		resp, requestErr := http.Get(host + "/api/v1/" + os.Getenv("LOOM_WORKSPACE") + "/workspace")
		if requestErr == nil {
			_ = resp.Body.Close()
			listening = true
			break // 401 is expected and proves the real proxy route is listening.
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !listening {
		t.Fatalf("loom serve FleetDB proxy did not listen at %s:\n%s", host, log.String())
	}
	return host, fmt.Sprintf("http://%s:%d", sandbox.HostGateway(), port)
}

func waitHTTPReady(t *testing.T, url string, log *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := http.Get(url); err == nil {
			_ = r.Body.Close()
			if r.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("service not ready at %s:\n%s", url, log.String())
}

func assertLiveMatrixActorRevoked(t *testing.T, c *http.Client, baseURL, key, actor string, baseline map[string]bool) {
	t.Helper()
	got := listSandboxE2EAPIKeyActors(t, c, baseURL, key)
	if got[actor] {
		t.Fatalf("scoped credential survived cleanup: %s", actor)
	}
	if len(got) != len(baseline) {
		t.Fatalf("API key actor set changed: got=%v want=%v", got, baseline)
	}
}
