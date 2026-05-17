//go:build playground

// Package playground hosts end-to-end tests that drive the deterministic
// mock harness in this directory through `loom serve` + `loom daemon`.
//
// Run with:
//
//	go test -tags=playground -v ./test/playground/...
//	# or via Makefile:
//	make test-playground
//
// Prereqs (same as smoke_test.sh):
//   - `loom serve` running at http://localhost:8080
//   - `loom` on PATH
//   - bash, git available
package playground_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	workspaceKey      = "PLAYGROUND"
	expectedTaskCount = 3
)

func serveBaseURL() string {
	if v := strings.TrimRight(os.Getenv("LOOM_BASE_URL"), "/"); v != "" {
		return v
	}
	return "http://localhost:8080"
}

// hereDir returns this directory (test/playground/) at test runtime.
func hereDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return wd
}

// requireServe fails fast if loom serve isn't reachable.
func requireServe(t *testing.T) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	baseURL := serveBaseURL()
	resp, err := client.Get(baseURL + "/health")
	if err != nil {
		if os.Getenv("LOOM_PLAYGROUND_REQUIRE_SERVE") != "" {
			t.Fatalf("loom serve not reachable at %s (%v)", baseURL, err)
		}
		t.Skipf("loom serve not reachable at %s (%v) — start it before running -tags=playground tests", baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if os.Getenv("LOOM_PLAYGROUND_REQUIRE_SERVE") != "" {
			t.Fatalf("loom serve /health = %d at %s", resp.StatusCode, baseURL)
		}
		t.Skipf("loom serve /health = %d at %s — start a fresh server first", resp.StatusCode, baseURL)
	}
}

// runScript runs a script in the test/playground/ dir, surfacing output on failure.
func runScript(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(hereDir(t), name))
	cmd.Stdout = nil
	cmd.Stderr = nil
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s failed: %v\n--- stderr ---\n%s", name, err, stderr.String())
	}
}

// envFromRuntime parses .runtime/env (two `export KEY="value"` lines).
func envFromRuntime(t *testing.T) map[string]string {
	t.Helper()
	envPath := filepath.Join(hereDir(t), ".runtime", "env")
	b, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read %s: %v", envPath, err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "export ")
		if line == "" {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		k := line[:eq]
		v := strings.Trim(line[eq+1:], `"`)
		v = strings.ReplaceAll(v, "$PATH", os.Getenv("PATH"))
		out[k] = v
	}
	return out
}

// startDaemon launches `loom daemon` as a subprocess with the playground env applied.
// Returns the process so the test can stop it during cleanup.
func startDaemon(t *testing.T) *exec.Cmd {
	t.Helper()
	env := envFromRuntime(t)
	cmd := exec.Command("loom", "daemon")
	cmd.Dir = hereDir(t)
	cmd.Env = append(os.Environ(),
		"PATH="+env["PATH"],
		"LOOM_WORKSPACE="+env["LOOM_WORKSPACE"],
	)
	logFile, err := os.Create(filepath.Join(hereDir(t), ".runtime", "daemon.log"))
	if err != nil {
		t.Fatalf("create daemon.log: %v", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	return cmd
}

// stopDaemon SIGTERMs the daemon and waits, ignoring errors.
func stopDaemon(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
	}
}

// listTasks fetches the playground task list via FleetDB HTTP API.
type issue struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Design string `json:"design"`
}

// envelope mirrors the wire format used by /api/workspaces/<ws>/issues.
type issueEnvelope struct {
	Data  []issue `json:"data"`
	Items []issue `json:"items"`
}

func listTasks(t *testing.T) []issue {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	url := fmt.Sprintf("%s/api/workspaces/%s/issues", serveBaseURL(), workspaceKey)
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: HTTP %d body=%s", url, resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// Try envelope first, then raw array.
	var env issueEnvelope
	if err := json.Unmarshal(body, &env); err == nil {
		if len(env.Data) > 0 {
			return env.Data
		}
		if len(env.Items) > 0 {
			return env.Items
		}
	}
	var arr []issue
	if err := json.Unmarshal(body, &arr); err == nil {
		return arr
	}
	t.Fatalf("could not parse issues response: %s", string(body))
	return nil
}

// waitForAllClosed polls until expectedTaskCount tasks are closed or deadline expires.
func waitForAllClosed(t *testing.T, deadline time.Time) int {
	t.Helper()
	for time.Now().Before(deadline) {
		closed := 0
		for _, i := range listTasks(t) {
			if strings.EqualFold(i.Status, "closed") {
				closed++
			}
		}
		if closed >= expectedTaskCount {
			return closed
		}
		time.Sleep(2 * time.Second)
	}
	return -1
}

func TestPlaygroundHappyPath(t *testing.T) {
	requireServe(t)

	// Pre-clean any stale state from earlier runs.
	_ = exec.Command("bash", filepath.Join(hereDir(t), "teardown.sh")).Run()
	_ = os.RemoveAll(filepath.Join(hereDir(t), ".loom"))

	t.Log("running setup.sh")
	runScript(t, "setup.sh")

	t.Log("starting loom daemon")
	daemon := startDaemon(t)
	t.Cleanup(func() {
		stopDaemon(t, daemon)
		_ = exec.Command("bash", filepath.Join(hereDir(t), "teardown.sh")).Run()
		_ = os.RemoveAll(filepath.Join(hereDir(t), ".loom"))
	})

	timeout := 90 * time.Second
	t.Logf("waiting up to %s for all tasks to close", timeout)
	closed := waitForAllClosed(t, time.Now().Add(timeout))
	if closed < expectedTaskCount {
		dlog, _ := os.ReadFile(filepath.Join(hereDir(t), ".runtime", "daemon.log"))
		t.Fatalf("only %d/%d tasks closed within %s\n--- daemon.log tail ---\n%s",
			closed, expectedTaskCount, timeout, tail(string(dlog), 50))
	}

	// Assertions on side effects.
	loomDir := os.Getenv("LOOM_CONFIG_DIR")
	if loomDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir: %v", err)
		}
		loomDir = filepath.Join(home, ".loom")
	}
	coderRepo := filepath.Join(loomDir, "workspaces", "playground", "worktrees", "repo", "playground-coder")

	logOut, err := exec.Command("git", "-C", coderRepo, "log", "--oneline").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	implCount := strings.Count(string(logOut), "Playground implementation (PLAYGROUND-")
	if implCount != expectedTaskCount {
		t.Errorf("expected %d implementation commits, found %d:\n%s", expectedTaskCount, implCount, logOut)
	}

	pf, err := os.ReadFile(filepath.Join(coderRepo, "playground.txt"))
	if err != nil {
		t.Fatalf("read playground.txt: %v", err)
	}
	blocks := strings.Count(string(pf), "Result: playground deterministic backend")
	if blocks != expectedTaskCount {
		t.Errorf("expected %d result blocks in playground.txt, found %d", expectedTaskCount, blocks)
	}

	// Spot-check one task has the playground design.
	for _, i := range listTasks(t) {
		if i.ID == "PLAYGROUND-1" {
			if !strings.Contains(i.Design, "playground planner verified") {
				t.Errorf("PLAYGROUND-1 design missing playground marker: %q", i.Design)
			}
			break
		}
	}

	t.Log("playground happy path OK")
}

func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
