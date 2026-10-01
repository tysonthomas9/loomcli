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
	config := fmt.Sprintf(`{"provider":{"aft":{"name":"AFT fake","npm":"@ai-sdk/openai-compatible",
		"options":{"baseURL":%q,"apiKey":"x"},
		"models":{"m":{"name":"M","limit":{"context":100000,"output":4000}}}}},"model":"aft/m"}`, fixture+"/v1")
	sbx := newSandbox(t, "loom-opencode-nested-", config)
	repo := filepath.Join(sbx, "repo")

	// The parent (loom serve) environment: scrubbed, plus synthetic tokens.
	tokens := map[string]string{"GITHUB_TOKEN": "ghp_nested", "GH_TOKEN": "gho_nested",
		"GH_ENTERPRISE_TOKEN": "ghe_nested", "GITHUB_TOKEN_FILE": filepath.Join(sbx, "token"),
		"LOOM_PR_GIT_PASSWORD": "pr_nested"}
	parentEnv := contractEnv(sbx, "LOOM_NESTED_MARKER=kept")
	for k, v := range tokens {
		parentEnv = append(parentEnv, k+"="+v)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a := New(Config{Bin: bin, Env: parentEnv})
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
		b, _ := json.Marshal(map[string]any{"steps": []map[string]any{presenceStep(dump), {"text": "done"}}})
		resp, err := http.Post(fixture+"/__script", "application/json", strings.NewReader(string(b)))
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("script: %v %v", resp, err)
		}
		_ = resp.Body.Close()
		if err := a.Session(ref).Prompt(ctx, loomharness.Input{Key: PromptID("nested-1", dump), Text: "dump env"}); err != nil {
			t.Fatal(err)
		}
		set := readPresence(t, dump)
		for k := range tokens {
			if set[k] {
				t.Errorf("restart=%v: grandchild shell saw %s", restart, k)
			}
		}
		// OpenCode hands shell commands the server's own environment unless
		// the session has one; the adapter sets it without the password.
		if set["OPENCODE_SERVER_PASSWORD"] || set["OPENCODE_PASSWORD"] {
			t.Errorf("restart=%v: grandchild shell saw the server password", restart)
		}
	}
}
