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

// TestContractNestedEnv proves GitHub tokens in loom serve's own environment,
// and the server's per-boot password, never reach a grandchild: a shell
// command the model runs inside a session, driven by the AFT fake-model
// fixture (2.0d).
func TestContractNestedEnv(t *testing.T) {
	if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
		t.Skip("set LOOM_REAL_OPENCODE=1 to run against the real OpenCode build")
	}
	bin := os.Getenv("LOOM_OPENCODE_BIN")
	if bin == "" {
		home, _ := os.UserHomeDir()
		bin = filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode")
	}
	fixture := startFakeModelFixture(t)
	sbx, err := os.MkdirTemp("/tmp", "loom-opencode-nested-")
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
		"models":{"m":{"name":"M","limit":{"context":100000,"output":4000}}}}},"model":"aft/m"}`, fixture+"/v1")
	if err := os.WriteFile(filepath.Join(sbx, "config/opencode/opencode.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	// The parent (loom serve) environment, as the adapter inherits it.
	tokens := map[string]string{"GITHUB_TOKEN": "ghp_nested", "GH_TOKEN": "gho_nested",
		"GH_ENTERPRISE_TOKEN": "ghe_nested", "GITHUB_TOKEN_FILE": filepath.Join(sbx, "token"),
		"LOOM_PR_GIT_PASSWORD": "pr_nested"}
	for k, v := range tokens {
		t.Setenv(k, v)
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
		Key: "nested-1", Launch: loomharness.Launch{Root: sbx}, Dir: repo, Model: "aft/m",
		Rules: []loomharness.PermissionRule{{Action: "*", Resource: "*", Effect: "allow"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	owned = append(owned, ref)

	// OpenCode keeps a session's environment in memory, so the second dump
	// runs after a server restart with no Resume: Prompt must set it again.
	for i, restart := range []bool{false, true} {
		if restart {
			if err := a.Restart(ctx); err != nil {
				t.Fatal(err)
			}
		}
		dump := filepath.Join(sbx, fmt.Sprintf("nested-env-%d.txt", i))
		b, _ := json.Marshal(map[string]any{"steps": []map[string]any{{"bash": "env > " + dump}, {"text": "done"}}})
		resp, err := http.Post(fixture+"/__script", "application/json", strings.NewReader(string(b)))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("script: %v %v", resp, err)
		}
		_ = resp.Body.Close()
		if err := a.Session(ref).Prompt(ctx, loomharness.Input{Key: PromptID("nested-1", dump), Text: "dump env"}); err != nil {
			t.Fatal(err)
		}
		var env string
		waitFor(t, "grandchild env dump", func() bool {
			out, err := os.ReadFile(dump)
			env = string(out)
			return err == nil && strings.Contains(env, "LOOM_NESTED_MARKER=kept")
		})
		for k, v := range tokens {
			if strings.Contains(env, k+"=") || strings.Contains(env, v) {
				t.Errorf("restart=%v: grandchild shell saw %s", restart, k)
			}
		}
		// OpenCode hands shell commands the server's own environment unless
		// the session has one; the adapter sets it without the password.
		if _, pw := a.endpoint(); strings.Contains(env, "OPENCODE_SERVER_PASSWORD=") || strings.Contains(env, pw) {
			t.Errorf("restart=%v: grandchild shell saw the server password", restart)
		}
	}
}
