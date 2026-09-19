package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestE2E_SandboxOneshotFleetDBRBAC is deliberately opt-in: it starts a real,
// auth-enabled fleet-db and runs an uploaded bootstrap using a host-built Loom
// binary. OpenShell is the only fake; its exec subcommand runs the uploaded
// bootstrap in a per-sandbox temporary directory.
func TestE2E_SandboxOneshotFleetDBRBAC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sandbox RBAC E2E needs POSIX sh and git")
	}
	fleetDBBin := strings.TrimSpace(os.Getenv("FLEET_DB_BIN"))
	if fleetDBBin == "" {
		t.Skip("sandbox RBAC E2E requires FLEET_DB_BIN: set it to an auth-capable fleet-db binary")
	}
	if info, err := os.Stat(fleetDBBin); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("sandbox RBAC E2E requires an executable FLEET_DB_BIN, got %q: %v", fleetDBBin, err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("sandbox RBAC E2E requires git on PATH")
	}
	if _, err := exec.LookPath("redis-cli"); err != nil {
		t.Fatal("sandbox RBAC E2E requires redis-cli to verify dedicated Redis db 9 is empty")
	}

	// This E2E owns db 9 only if it begins empty. It never FLUSHDBs: teardown
	// deletes the keys created in the otherwise empty, dedicated database.
	if n := redisDBSize(t, 9); n != 0 {
		t.Skipf("sandbox RBAC E2E requires empty Redis db 9; DBSIZE=%d (will not touch existing keys)", n)
	}
	t.Cleanup(func() { deleteRedisDBKeys(t, 9) })

	root := t.TempDir()
	loomBin := buildSandboxE2ELoom(t, root)
	probeBin := buildSandboxE2EProbe(t, root)
	fleetURL, adminKey := startSandboxE2EFleetDB(t, fleetDBBin, root)

	const workspace = "WS-E2E"
	adminHTTP := &http.Client{Timeout: 10 * time.Second}
	postJSONStatus(t, adminHTTP, fleetURL+"/api/v1/admin/workspaces", adminKey,
		map[string]any{"key": workspace, "name": "Sandbox RBAC E2E"}, http.StatusCreated)
	baselineKeyActors := listSandboxE2EAPIKeyActors(t, adminHTTP, fleetURL, adminKey)

	// The host one-shot uses the bootstrap admin credential to mint each
	// sandbox's short-lived, developer-scoped key. The sandbox URL stays on
	// loopback because fake OpenShell runs the bootstrap on the host.
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(root, "loom-config"))
	t.Setenv("LOOM_FLEET_DB_URL", fleetURL)
	t.Setenv("LOOM_SANDBOX_FLEETDB_URL", fleetURL)
	t.Setenv("LOOM_FLEET_DB_API_KEY", adminKey)
	t.Setenv("LOOM_FLEET_DB_ACTOR", "sandbox-e2e-admin")
	t.Setenv("LOOM_WORKSPACE", workspace)
	t.Setenv("LOOM_SANDBOX_HOST_GATEWAY", "127.0.0.1")
	t.Setenv("LOOM_SANDBOX_LOOM_BIN", loomBin)
	t.Setenv("LOOM_ISSUE_BACKEND", "fleetdb")
	// Pin the in-sandbox agent backend to the deterministic probe. Without this
	// the bootstrap's `loom task` falls back to the host-default AI backend —
	// real spend, and exactly what this E2E exists to avoid.
	t.Setenv("LOOM_SANDBOX_BACKEND", "probe")

	binDir := filepath.Join(root, "bin")
	mustMkdir(t, binDir)
	// The external backend is discovered by the real Loom process from PATH.
	if err := os.Link(probeBin, filepath.Join(binDir, "loom-backend-probe")); err != nil {
		t.Fatalf("link probe backend: %v", err)
	}
	containers := filepath.Join(root, "containers")
	mustMkdir(t, containers)
	openshellLog := filepath.Join(root, "openshell.log")
	mustWriteExec(t, filepath.Join(binDir, "openshell"), realBootstrapFakeOpenshell)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPENSHELL_LOG", openshellLog)
	t.Setenv("FAKE_OPENSHELL_ROOT", containers)

	proj := setupSandboxE2EGitRepo(t, root)
	remoteServer := serveGitHTTP(t, root)
	runGit(t, proj, "remote", "add", "origin", remoteServer.URL+"/remote.git")

	// Success leg: the probe uses the real uploaded Loom binary to claim and
	// close this task, then writes a marker which the bootstrap commits/pushes.
	successTask := seedSandboxE2ETask(t, adminHTTP, fleetURL, adminKey, workspace, "success")
	t.Setenv("LOOM_PROBE_TASK_ID", successTask)
	t.Setenv("LOOM_PROBE_EXIT", "")
	exitCode, err := runSandboxOneshot(SandboxOneshotConfig{
		AgentType: "task", AgentName: "falcon", WorktreePath: proj,
	})
	if err != nil {
		t.Fatalf("success runSandboxOneshot: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("success exit code = %d, want 0", exitCode)
	}
	marker, err := os.ReadFile(filepath.Join(proj, "SANDBOX_PROBE_MARKER"))
	if err != nil {
		t.Fatalf("success marker was not fast-forwarded into host worktree: %v", err)
	}
	if string(marker) != "claimed-and-closed; rbac=200,403,401\n" {
		t.Fatalf("unexpected marker/RBAC battery result: %q", marker)
	}
	assertSandboxE2ETaskClosed(t, adminHTTP, fleetURL, adminKey, workspace, successTask)
	assertSandboxE2EKeyActors(t, adminHTTP, fleetURL, adminKey, baselineKeyActors)

	// Failure leg: the probe exits 17 before touching the clone, so set -e
	// prevents the bootstrap commit/push. Cleanup must still revoke the newly
	// provisioned credential and delete its sandbox.
	failureTask := seedSandboxE2ETask(t, adminHTTP, fleetURL, adminKey, workspace, "failure")
	t.Setenv("LOOM_PROBE_TASK_ID", failureTask)
	t.Setenv("LOOM_PROBE_EXIT", "17")
	headBeforeFailure := sandboxE2EGitOutput(t, proj, "rev-parse", "HEAD")
	exitCode, err = runSandboxOneshot(SandboxOneshotConfig{
		AgentType: "task", AgentName: "falcon", WorktreePath: proj,
	})
	if err != nil {
		t.Fatalf("failure runSandboxOneshot: %v", err)
	}
	if exitCode != 17 {
		t.Fatalf("failure exit code = %d, want 17", exitCode)
	}
	if _, err := os.Stat(filepath.Join(proj, "SANDBOX_PROBE_FAILURE_MARKER")); !os.IsNotExist(err) {
		t.Fatalf("failure marker unexpectedly merged into host worktree: %v", err)
	}
	if headAfterFailure := sandboxE2EGitOutput(t, proj, "rev-parse", "HEAD"); headAfterFailure != headBeforeFailure {
		t.Fatalf("failure leg changed host HEAD: before=%s after=%s", headBeforeFailure, headAfterFailure)
	}
	assertSandboxE2EKeyActors(t, adminHTTP, fleetURL, adminKey, baselineKeyActors)

	logBytes, err := os.ReadFile(openshellLog)
	if err != nil {
		t.Fatalf("read fake openshell log: %v", err)
	}
	if got := strings.Count(string(logBytes), "sandbox delete "); got != 2 {
		t.Fatalf("sandbox delete calls = %d, want 2 (success + failure); log:\n%s", got, logBytes)
	}
	if entries, err := os.ReadDir(containers); err != nil || len(entries) != 0 {
		t.Fatalf("fake sandbox roots survived cleanup: entries=%v err=%v", entries, err)
	}
	t.Logf("E2E verified real auth/authz FleetDB, scoped task claim/close, and cleanup after success + exit 17")
}

const realBootstrapFakeOpenshell = `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$OPENSHELL_LOG"
case "$1 $2" in
"sandbox create") exit 0 ;;
"sandbox upload")
  name=$3 src=$4 dst=$5
  root="$FAKE_OPENSHELL_ROOT/$name"
  target="$root$dst"
  mkdir -p "$(dirname "$target")"
  cp "$src" "$target"
  ;;
"sandbox exec")
  name=$4
  root="$FAKE_OPENSHELL_ROOT/$name"
  script="$root/sandbox/bootstrap.sh"
  translated="$root/bootstrap.host.sh"
  sed "s#/sandbox#$root/sandbox#g" "$script" > "$translated"
  set +e
  PATH="$root/sandbox:$PATH" sh "$translated"
  code=$?
  set -e
  # loom's command layer normalizes backend failures to 1. Fake OpenShell
  # represents the remote process boundary, so preserve the deterministic
  # probe's selected remote exit after proving the real bootstrap failed.
  if [ -n "${LOOM_PROBE_EXIT:-}" ]; then
    [ "$code" -ne 0 ] || exit 98
    exit "$LOOM_PROBE_EXIT"
  fi
  exit "$code"
  ;;
"sandbox delete") rm -rf "$FAKE_OPENSHELL_ROOT/$3" ;;
*) echo "unexpected openshell invocation: $*" >&2; exit 2 ;;
esac
`

const sandboxE2EProbeSource = `package main

import (
 "fmt"
 "io"
 "net/http"
 "os"
 "os/exec"
 "path/filepath"
)

func main() {
 if len(os.Args) >= 2 && os.Args[1] == "meta" { fmt.Print("{}\n"); return }
 if len(os.Args) >= 2 && os.Args[1] == "health" { fmt.Print("{\"installed\":true,\"healthy\":true}\n"); return }
 // Failure leg: drop a marker in the clone BEFORE exiting non-zero. The
 // bootstrap's set -e must abort before commit/push, so this file reaching
 // the host worktree would prove failed work leaked through the merge-back.
 if os.Getenv("LOOM_PROBE_EXIT") != "" {
  _ = os.WriteFile(filepath.Join(mustGetwd(), "SANDBOX_PROBE_FAILURE_MARKER"), []byte("failure-leg\n"), 0644)
  os.Exit(17)
 }
 id := os.Getenv("LOOM_PROBE_TASK_ID")
 if id == "" { fmt.Fprintln(os.Stderr, "LOOM_PROBE_TASK_ID is required"); os.Exit(2) }
 base, key, ws := os.Getenv("LOOM_FLEET_DB_URL"), os.Getenv("LOOM_FLEET_DB_API_KEY"), os.Getenv("LOOM_WORKSPACE")
 scoped := status(base+"/api/v1/"+ws+"/workspace", key)
 admin := status(base+"/api/v1/admin/workspaces", key)
 anonymous := status(base+"/api/v1/"+ws+"/workspace", "")
 if scoped != 200 || admin != 403 || anonymous != 401 { fmt.Fprintf(os.Stderr, "RBAC battery = %d,%d,%d\n", scoped, admin, anonymous); os.Exit(3) }
 for _, args := range [][]string{{"data", "claim", id}, {"data", "close", id, "--reason", "sandbox probe"}} {
  cmd := exec.Command("loom", args...)
  cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
  if err := cmd.Run(); err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
 }
 if err := os.WriteFile(filepath.Join(mustGetwd(), "SANDBOX_PROBE_MARKER"), []byte("claimed-and-closed; rbac=200,403,401\n"), 0644); err != nil { panic(err) }
}

func status(url, key string) int { req, err := http.NewRequest(http.MethodGet, url, nil); if err != nil { panic(err) }; if key != "" { req.Header.Set("X-API-Key", key) }; resp, err := http.DefaultClient.Do(req); if err != nil { panic(err) }; _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close(); return resp.StatusCode }
func mustGetwd() string { d, err := os.Getwd(); if err != nil { panic(err) }; return d }
`

func buildSandboxE2ELoom(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(root, "loom")
	buildSandboxE2EBinary(t, bin, "./cmd/loom")
	return bin
}

func buildSandboxE2EProbe(t *testing.T, root string) string {
	t.Helper()
	src := filepath.Join(root, "loom-backend-probe.go")
	mustWrite(t, src, sandboxE2EProbeSource)
	bin := filepath.Join(root, "loom-backend-probe")
	buildSandboxE2EBinary(t, bin, src)
	return bin
}

func buildSandboxE2EBinary(t *testing.T, out, target string) {
	t.Helper()
	root := sandboxE2ERepoRoot(t)
	cmd := exec.Command("go", "build", "-o", out, target) //nolint:gosec // E2E builds fixed local targets
	cmd.Dir = root
	if log, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", target, err, log)
	}
}

func sandboxE2ERepoRoot(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--show-toplevel") //nolint:gosec // fixed git query
	if out, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "../../.."))
}

func startSandboxE2EFleetDB(t *testing.T, bin, root string) (string, string) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve fleet-db port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	const adminKey = "sandbox-e2e-admin-key"
	cmd := exec.Command(bin,
		"--addr", addr,
		"--redis-addr", "127.0.0.1:6379",
		"--redis-db", "9",
		"--rpc-enabled=false",
		"--auth-enabled",
		"--authz-enabled",
		"--auth-bootstrap-admin-actor", "sandbox-e2e-admin",
		"--auth-bootstrap-admin-key", adminKey,
	) //nolint:gosec // FLEET_DB_BIN is the explicit E2E gate
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fleet-db: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil && cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	url := "http://" + addr
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/readyz") //nolint:gosec // test-owned loopback server
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return url, adminKey
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("fleet-db did not become ready at %s:\n%s", url, log.String())
	return "", ""
}

func setupSandboxE2EGitRepo(t *testing.T, root string) string {
	t.Helper()
	remote := filepath.Join(root, "remote.git")
	proj := filepath.Join(root, "proj")
	const branch = "sandbox-e2e"
	runGit(t, root, "init", "--bare", remote)
	runGit(t, remote, "config", "http.receivepack", "true")
	mustMkdir(t, proj)
	runGit(t, proj, "init")
	runGit(t, proj, "config", "user.email", "e2e@local")
	runGit(t, proj, "config", "user.name", "Sandbox E2E")
	mustMkdir(t, filepath.Join(proj, ".loom"))
	mustWrite(t, filepath.Join(proj, "README.md"), "seed\n")
	runGit(t, proj, "checkout", "-b", branch)
	runGit(t, proj, "add", "-A")
	runGit(t, proj, "commit", "-m", "seed")
	return proj
}

func seedSandboxE2ETask(t *testing.T, c *http.Client, baseURL, key, workspace, title string) string {
	t.Helper()
	body := postJSONStatus(t, c, baseURL+"/api/v1/"+workspace+"/issues", key,
		map[string]any{"title": "sandbox " + title, "type": "task", "priority": 1, "design": "Run deterministic probe."}, http.StatusCreated)
	var issue struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &issue); err != nil || issue.ID == "" {
		t.Fatalf("decode seeded task: id=%q err=%v body=%s", issue.ID, err, body)
	}
	return issue.ID
}

func assertSandboxE2ETaskClosed(t *testing.T, c *http.Client, baseURL, key, workspace, id string) {
	t.Helper()
	body := getJSONStatus(t, c, baseURL+"/api/v1/"+workspace+"/issues/"+id, key, http.StatusOK)
	var issue struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &issue); err != nil {
		t.Fatalf("decode task %s: %v", id, err)
	}
	if issue.Status != "closed" {
		t.Fatalf("task %s status = %q, want closed (real in-container Loom claim/close)", id, issue.Status)
	}

	// FleetDB deliberately clears assignee when an issue closes. Prove both
	// lifecycle mutations were performed by the provisioned scoped actor using
	// the durable event feed instead of inspecting the terminal issue record.
	eventsBody := getJSONStatus(t, c,
		baseURL+"/api/v1/"+workspace+"/events/mutations?since=0&limit=1000", key, http.StatusOK)
	var mutations struct {
		Events []struct {
			Actor    string `json:"actor"`
			Action   string `json:"action"`
			EntityID string `json:"entity_id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(eventsBody, &mutations); err != nil {
		t.Fatalf("decode mutation events for task %s: %v", id, err)
	}
	actorPrefix := "sandbox:" + workspace + ":falcon:"
	var claimed, closed bool
	for _, event := range mutations.Events {
		if event.EntityID != id || !strings.HasPrefix(event.Actor, actorPrefix) {
			continue
		}
		switch event.Action {
		case "issue.claim":
			claimed = true
		case "issue.close":
			closed = true
		}
	}
	if !claimed || !closed {
		t.Fatalf("task %s scoped mutation attribution: issue.claim=%t issue.close=%t", id, claimed, closed)
	}
}

func sandboxE2EGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // fixed test repository query
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func assertSandboxE2EKeyActors(t *testing.T, c *http.Client, baseURL, key string, want map[string]bool) {
	t.Helper()
	got := listSandboxE2EAPIKeyActors(t, c, baseURL, key)
	for actor := range got {
		if strings.HasPrefix(actor, "sandbox:WS-E2E:falcon:") {
			t.Fatalf("scoped sandbox credential survived cleanup: %s", actor)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("API key actor set changed after sandbox cleanup: got=%v want=%v", got, want)
	}
	for actor := range want {
		if !got[actor] {
			t.Fatalf("API key actor set changed after sandbox cleanup: got=%v want=%v", got, want)
		}
	}
}

func listSandboxE2EAPIKeyActors(t *testing.T, c *http.Client, baseURL, key string) map[string]bool {
	t.Helper()
	body := getJSONStatus(t, c, baseURL+"/api/v1/admin/apikeys", key, http.StatusOK)
	var response struct {
		Keys []struct {
			ActorID string `json:"actor_id"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode api key list: %v", err)
	}
	actors := make(map[string]bool, len(response.Keys))
	for _, entry := range response.Keys {
		actors[entry.ActorID] = true
	}
	return actors
}

func postJSONStatus(t *testing.T, c *http.Client, url, key string, body any, want int) []byte {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	return doSandboxE2EHTTP(t, c, req, want)
}

func getJSONStatus(t *testing.T, c *http.Client, url, key string, want int) []byte {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	return doSandboxE2EHTTP(t, c, req, want)
}

func doSandboxE2EHTTP(t *testing.T, c *http.Client, req *http.Request, want int) []byte {
	t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", req.Method, req.URL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d: %s", req.Method, req.URL, resp.StatusCode, want, body)
	}
	return body
}

func redisDBSize(t *testing.T, db int) int {
	t.Helper()
	out, err := exec.Command("redis-cli", "-n", strconv.Itoa(db), "DBSIZE").CombinedOutput() //nolint:gosec // fixed Redis admin probe
	if err != nil {
		t.Fatalf("sandbox RBAC E2E cannot query Redis db %d: %v (%s)", db, err, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("sandbox RBAC E2E received invalid Redis DBSIZE for db %d: %q", db, out)
	}
	return n
}

func deleteRedisDBKeys(t *testing.T, db int) {
	t.Helper()
	out, err := exec.Command("redis-cli", "-n", strconv.Itoa(db), "--scan").Output() //nolint:gosec // dedicated E2E Redis db, checked empty before startup
	if err != nil {
		t.Logf("list E2E Redis keys for cleanup: %v", err)
		return
	}
	for _, key := range strings.Fields(string(out)) {
		if del, err := exec.Command("redis-cli", "-n", strconv.Itoa(db), "DEL", key).CombinedOutput(); err != nil { //nolint:gosec // key came from dedicated db scan
			t.Logf("delete E2E Redis key %q: %v (%s)", key, err, del)
		}
	}
}
