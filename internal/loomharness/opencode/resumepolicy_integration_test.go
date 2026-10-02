package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestContractResumePolicy proves on the pinned OpenCode build that Resume
// and an Open repeat install the current rules as the session's whole policy
// before a real shell tool runs, that the installed policy survives a server
// restart, and that a failed install changes nothing and runs nothing. The
// scripted model runs `printf 'POL%sRAN-<n>' ICY-`, whose output
// POLICY-RAN-<n> never appears in any command text, so that output reaching
// the model proves turn n's tool ran.
func TestContractResumePolicy(t *testing.T) {
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
	sbx := newSandbox(t, "loom-opencode-policy-", config)
	repo := filepath.Join(sbx, "repo")
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	a := New(Config{Bin: bin, Env: contractEnv(sbx)})
	var owned []loomharness.NativeRef
	t.Cleanup(func() {
		if err := a.Purge(context.Background(), owned); err != nil {
			t.Errorf("cleanup purge: %v", err)
		}
		a.Stop()
	})
	if h, err := a.Health(ctx); err != nil || !h.OK {
		t.Fatalf("Health = %+v, %v", h, err)
	}
	waitFor(t, "aft/m in Models", func() bool { models, err := a.Models(ctx); return err == nil && hasModel(models, "aft/m") })
	feed, err := a.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close()
	events := collect(feed)

	launch := loomharness.Launch{Root: sbx}
	allow := []loomharness.PermissionRule{{Action: "read", Resource: "*", Effect: "allow"},
		{Action: "edit", Resource: "*", Effect: "allow"}, {Action: "bash", Resource: "*", Effect: "allow"}}
	deny := append(slices.Clone(allow), loomharness.PermissionRule{Action: "bash", Resource: "printf *", Effect: "deny"})

	turns := 0
	// ran prompts one turn on ref whose model calls the shell tool, waits for
	// it to finish, and reports whether the tool's output reached the model.
	ran := func(t *testing.T, ref loomharness.NativeRef) bool {
		t.Helper()
		turns++
		mark := fmt.Sprintf("POLICY-RAN-%d", turns)
		script(t, fixture, map[string]any{"bash": fmt.Sprintf("printf 'POL%%sRAN-%d' ICY-", turns)}, map[string]any{"text": "after"})
		completed := func() int {
			events.mu.Lock()
			defer events.mu.Unlock()
			n := 0
			for _, e := range events.events {
				if e.Session.NativeID == ref.NativeID && e.Type == loomharness.EventTurnCompleted {
					n++
				}
			}
			return n
		}
		done, before := completed(), len(fixtureRequests(t, fixture))
		if err := a.Session(ref).Prompt(ctx, loomharness.Input{Key: PromptID(ref.NativeID, fmt.Sprint(turns)), Text: "go"}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "turn completed", func() bool { return completed() > done })
		for _, r := range fixtureRequests(t, fixture)[before:] {
			if strings.Contains(r, mark) {
				return true
			}
		}
		return false
	}
	open := func(t *testing.T, key string, rules []loomharness.PermissionRule) loomharness.NativeRef {
		t.Helper()
		ref, err := a.Open(ctx, loomharness.OpenSpec{Key: key, Launch: launch, Dir: repo, Model: "aft/m", Rules: rules})
		if err != nil {
			t.Fatal(err)
		}
		owned = append(owned, ref)
		return ref
	}

	t.Run("ResumeReplacesStalePolicy", func(t *testing.T) {
		ref := open(t, "policy-resume", allow)
		if !ran(t, ref) {
			t.Fatal("control: the tool did not run under the allow policy")
		}
		if _, err := a.Session(ref).Resume(ctx, launch, deny); err != nil {
			t.Fatal(err)
		}
		if ran(t, ref) {
			t.Fatal("the tool ran after Resume installed a deny")
		}
		if _, err := a.Session(ref).Resume(ctx, launch, allow); err != nil { // a removed deny goes too
			t.Fatal(err)
		}
		if !ran(t, ref) {
			t.Fatal("Resume did not replace the policy: the removed deny still applied")
		}
	})

	t.Run("OpenRepeatInstalls", func(t *testing.T) {
		ref := open(t, "policy-open", allow)
		if again := open(t, "policy-open", deny); again != ref {
			t.Fatalf("Open repeat = %v; want %v", again, ref)
		}
		if ran(t, ref) {
			t.Fatal("the tool ran after an Open repeat installed a deny")
		}
	})

	t.Run("RestartKeepsInstalledPolicy", func(t *testing.T) {
		ref := open(t, "policy-restart", allow)
		if _, err := a.Session(ref).Resume(ctx, launch, deny); err != nil {
			t.Fatal(err)
		}
		if err := a.Restart(ctx); err != nil {
			t.Fatal(err)
		}
		// No Resume after the restart: the deny is on OpenCode's session row.
		if ran(t, ref) {
			t.Fatal("the tool ran after a restart; the installed deny was lost")
		}
	})

	t.Run("FailedInstallChangesNothing", func(t *testing.T) {
		ref := open(t, "policy-fail", deny)
		n := len(fixtureRequests(t, fixture))
		bad := []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "sometimes"}} // OpenCode rejects the effect
		if _, err := a.Session(ref).Resume(ctx, launch, bad); err == nil || !strings.Contains(err.Error(), "install session permissions") {
			t.Fatalf("Resume with a rejected policy = %v", err)
		}
		if got := len(fixtureRequests(t, fixture)); got != n {
			t.Fatalf("a failed install ran %d model requests", got-n)
		}
		if ran(t, ref) {
			t.Fatal("a failed install replaced the deny")
		}
	})
}

func script(t *testing.T, fixture string, steps ...map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"steps": steps})
	resp, err := http.Post(fixture+"/__script", "application/json", strings.NewReader(string(b)))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("script: %v %v", resp, err)
	}
	_ = resp.Body.Close()
}

// fixtureRequests returns every agent-turn request body the fixture got.
func fixtureRequests(t *testing.T, fixture string) []string {
	t.Helper()
	resp, err := http.Get(fixture + "/__requests")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var log struct {
		Requests []json.RawMessage `json:"requests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&log); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range log.Requests {
		if !strings.Contains(string(r), "You are a title generator") {
			out = append(out, string(r))
		}
	}
	return out
}
