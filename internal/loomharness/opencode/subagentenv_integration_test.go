package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// secretNames are the server-auth and GitHub/publish aliases no tool process
// may see. Tests assert names only and never print values.
var secretNames = []string{"OPENCODE_SERVER_PASSWORD", "OPENCODE_PASSWORD", "GITHUB_TOKEN", "GH_TOKEN",
	"GH_ENTERPRISE_TOKEN", "GITHUB_TOKEN_FILE", "LOOM_PR_GIT_PASSWORD"}

// TestContractSubagentEnv proves no tool process sees the server password or
// a GitHub/publish alias: not the session's first shell, not a subagent's,
// not a subagent-of-a-subagent's, before and after a server restart. Driven
// by the AFT fake-model fixture (2.0d) with synthetic credentials; only names
// are asserted or printed. The control runs plain `opencode serve` without
// Loom's filters and shows the leak the adapter's --stdio launch prevents.
func TestContractSubagentEnv(t *testing.T) {
	if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
		t.Skip("set LOOM_REAL_OPENCODE=1 to run against the real OpenCode build")
	}
	bin := os.Getenv("LOOM_OPENCODE_BIN")
	if bin == "" {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode")
	}
	fixture := startFakeModelFixture(t)
	sbx, err := os.MkdirTemp("/tmp", "loom-opencode-subagent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sbx) })
	repo := filepath.Join(sbx, "repo")
	for _, d := range []string{filepath.Join(sbx, "home"), filepath.Join(sbx, "tmp"), filepath.Join(sbx, "config/opencode"), repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf(`{"provider":{"aft":{"name":"AFT fake","npm":"@ai-sdk/openai-compatible",
		"options":{"baseURL":%q,"apiKey":"x"},
		"models":{"m":{"name":"M","limit":{"context":100000,"output":4000}}}}},"model":"aft/m",
		"experimental":{"subagent_depth":2}}`, fixture+"/v1")
	if err := os.WriteFile(filepath.Join(sbx, "config/opencode/opencode.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// The parent (loom serve) environment: scrubbed, plus every synthetic
	// fixture credential and the marker.
	parentEnv := contractEnv(sbx, "LOOM_NESTED_MARKER=kept")
	for _, k := range secretNames {
		parentEnv = append(parentEnv, k+"=fixture-"+strings.ToLower(k))
	}
	spec := loomharness.OpenSpec{Launch: loomharness.Launch{Root: sbx}, Dir: repo, Model: "aft/m",
		Rules: []loomharness.PermissionRule{{Action: "*", Resource: "*", Effect: "allow"}}}
	check := func(t *testing.T, envs map[string]map[string]bool, leaked func(name string) bool) {
		t.Helper()
		for role, env := range envs {
			for _, k := range secretNames {
				if saw := env[k]; saw != leaked(k) {
					t.Errorf("%s shell saw %s = %v; want %v", role, k, saw, leaked(k))
				}
			}
		}
	}

	t.Run("Adapter", func(t *testing.T) {
		a := New(Config{Bin: bin, Env: parentEnv})
		var owned []loomharness.NativeRef
		t.Cleanup(func() {
			if err := a.Purge(context.Background(), owned); err != nil {
				t.Errorf("cleanup purge: %v", err)
			}
			a.Stop()
		})
		waitFor(t, "aft/m in Models", func() bool { models, err := a.Models(ctx); return err == nil && hasModel(models, "aft/m") })
		spec.Key = "subagent-1"
		ref, err := a.Open(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		owned = append(owned, ref)
		for i, restart := range []bool{false, true} {
			if restart {
				if err := a.Restart(ctx); err != nil {
					t.Fatal(err)
				}
			}
			envs := subagentShells(t, ctx, a.Client, ref, fixture, filepath.Join(sbx, fmt.Sprintf("adapter-%d", i)))
			check(t, envs, func(string) bool { return false })
		}
	})

	t.Run("PlainServeControl", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		base := "http://" + l.Addr().String()
		_ = l.Close()
		cmd := exec.Command(bin, "serve", "--hostname", "127.0.0.1", "--port", strings.TrimPrefix(base, "http://127.0.0.1:"))
		cmd.Env = parentEnv // every synthetic credential, unfiltered
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); _ = cmd.Wait() })
		c := NewClient(base, "fixture-opencode_password") // OPENCODE_PASSWORD wins over OPENCODE_SERVER_PASSWORD
		waitFor(t, "plain serve", func() bool { return answers(ctx, base, "fixture-opencode_password", cmd.Process.Pid) })
		spec.Key = "subagent-control"
		ref, err := c.Open(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Purge(context.Background(), []loomharness.NativeRef{ref}) })
		envs := subagentShells(t, ctx, c, ref, fixture, filepath.Join(sbx, "control"))
		check(t, envs, func(string) bool { return true })
	})
}

// subagentShells scripts a session whose first tool records which secret
// names its environment has, then spawns a subagent that records and spawns
// another that records. It returns
// the names set in each by role, after checking that two nested subagent sessions ran.
func subagentShells(t *testing.T, ctx context.Context, c *Client, ref loomharness.NativeRef, fixture, dir string) map[string]map[string]bool {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dump := func(role string) map[string]any { return presenceStep(filepath.Join(dir, role)) }
	spawn := map[string]any{"tool_calls": []map[string]any{{"name": "subagent",
		"arguments": map[string]string{"agent": "general", "description": "env check", "prompt": "check env"}}}}
	// One FIFO queue serves every session in turn.
	b, _ := json.Marshal(map[string]any{"steps": []any{dump("parent"), spawn, dump("child"), spawn, dump("grandchild"),
		map[string]any{"text": "grandchild done"}, map[string]any{"text": "child done"}, map[string]any{"text": "parent done"}}})
	resp, err := http.Post(fixture+"/__script", "application/json", strings.NewReader(string(b)))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("script: %v %v", resp, err)
	}
	_ = resp.Body.Close()
	before := len(childSessions(t, ctx, c, ref.NativeID))
	if err := c.Session(ref).Prompt(ctx, loomharness.Input{Key: PromptID(ref.NativeID, dir), Text: "spawn"}); err != nil {
		t.Fatal(err)
	}
	envs := map[string]map[string]bool{}
	for _, role := range []string{"parent", "child", "grandchild"} {
		envs[role] = readPresence(t, filepath.Join(dir, role))
	}
	kids := childSessions(t, ctx, c, ref.NativeID)
	if len(kids) != before+1 || len(childSessions(t, ctx, c, kids[len(kids)-1])) != 1 {
		t.Fatalf("subagents did not run: %d children (was %d)", len(kids), before)
	}
	return envs
}

// childSessions lists the ids of sessions whose parent is id, oldest first.
func childSessions(t *testing.T, ctx context.Context, c *Client, id string) []string {
	t.Helper()
	var page struct {
		Data []struct {
			ID       string `json:"id"`
			ParentID string `json:"parentID"`
		} `json:"data"`
	}
	if err := c.call(ctx, "GET", "/api/session", nil, &page); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range page.Data {
		if s.ParentID == id {
			ids = append(ids, s.ID)
		}
	}
	slices.Sort(ids)
	return ids
}

// presenceStep is a scripted shell step that writes NAME=present or
// NAME=absent for each of secretNames and LOOM_NESTED_MARKER. It checks only
// those names and never writes a value or the environment. The file is
// written aside, then renamed.
func presenceStep(file string) map[string]any {
	names := strings.Join(append(slices.Clone(secretNames), "LOOM_NESTED_MARKER"), " ")
	return map[string]any{"bash": fmt.Sprintf(
		`for k in %s; do if printenv "$k" >/dev/null; then echo "$k=present"; else echo "$k=absent"; fi; done > %s.part; mv %s.part %s`,
		names, file, file, file)}
}

// readPresence waits for a presenceStep file whose shell had the marker and
// returns the names it recorded as present.
func readPresence(t *testing.T, file string) map[string]bool {
	t.Helper()
	set := map[string]bool{}
	waitFor(t, filepath.Base(file)+" presence record", func() bool {
		out, err := os.ReadFile(file)
		if err != nil {
			return false
		}
		clear(set)
		for _, line := range strings.Fields(string(out)) {
			if k, ok := strings.CutSuffix(line, "=present"); ok {
				set[k] = true
			}
		}
		return set["LOOM_NESTED_MARKER"]
	})
	return set
}
