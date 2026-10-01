package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// secretNames are the server-auth and GitHub/publish aliases no tool process
// may see. Tests assert names only and never print values.
var secretNames = []string{"OPENCODE_SERVER_PASSWORD", "OPENCODE_PASSWORD", "GITHUB_TOKEN", "GH_TOKEN",
	"GH_ENTERPRISE_TOKEN", "GITHUB_TOKEN_FILE", "LOOM_PR_GIT_PASSWORD"}

// TestContractSubagentEnv checks what a shell sees inside a subagent child
// session and inside a subagent of a subagent, before and after a server
// restart, driven by the AFT fake-model fixture (2.0d).
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
	// Synthetic fixture credentials in the parent (loom serve) environment.
	for _, k := range secretNames {
		t.Setenv(k, "fixture-"+strings.ToLower(k))
	}
	t.Setenv("LOOM_NESTED_MARKER", "kept")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a := New(Config{Bin: bin, Env: contractEnv(sbx)})
	var owned []loomharness.NativeRef
	t.Cleanup(func() {
		if err := a.Purge(context.Background(), owned); err != nil {
			t.Errorf("cleanup purge: %v", err)
		}
		a.Stop()
	})
	waitFor(t, "aft/m in Models", func() bool { models, err := a.Models(ctx); return err == nil && hasModel(models, "aft/m") })
	ref, err := a.Open(ctx, loomharness.OpenSpec{
		Key: "subagent-1", Launch: loomharness.Launch{Root: sbx}, Dir: repo, Model: "aft/m",
		Rules: []loomharness.PermissionRule{{Action: "*", Resource: "*", Effect: "allow"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	owned = append(owned, ref)

	spawn := map[string]any{"tool_calls": []map[string]any{{"name": "subagent",
		"arguments": map[string]string{"agent": "general", "description": "env check", "prompt": "check env"}}}}
	for i, restart := range []bool{false, true} {
		if restart {
			if err := a.Restart(ctx); err != nil {
				t.Fatal(err)
			}
		}
		child := filepath.Join(sbx, fmt.Sprintf("child-%d.txt", i))
		grandchild := filepath.Join(sbx, fmt.Sprintf("grandchild-%d.txt", i))
		// One FIFO queue serves every session in order: parent spawns, child
		// dumps and spawns, grandchild dumps, then each finishes.
		b, _ := json.Marshal(map[string]any{"steps": []any{
			spawn, map[string]any{"bash": "env > " + child}, spawn, map[string]any{"bash": "env > " + grandchild},
			map[string]any{"text": "grandchild done"}, map[string]any{"text": "child done"}, map[string]any{"text": "parent done"},
		}})
		resp, err := http.Post(fixture+"/__script", "application/json", strings.NewReader(string(b)))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("script: %v %v", resp, err)
		}
		_ = resp.Body.Close()
		if err := a.Session(ref).Prompt(ctx, loomharness.Input{Key: PromptID("subagent-1", child), Text: "spawn"}); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{child, grandchild} {
			var env string
			waitFor(t, filepath.Base(f), func() bool {
				out, err := os.ReadFile(f)
				env = string(out)
				return err == nil && strings.Contains(env, "LOOM_NESTED_MARKER=kept")
			})
			// Pinned finding: OpenCode creates subagent sessions without the
			// parent's environment, so their shells get the server's own,
			// per-boot password included. GitHub/publish aliases never
			// reach the server, so they stay absent.
			for _, k := range secretNames {
				saw := strings.Contains(env, "\n"+k+"=") || strings.HasPrefix(env, k+"=")
				if want := k == "OPENCODE_SERVER_PASSWORD"; saw != want {
					t.Errorf("restart=%v: %s shell saw %s = %v; pinned %v", restart, strings.TrimSuffix(filepath.Base(f), ".txt"), k, saw, want)
				}
			}
		}
	}
}
