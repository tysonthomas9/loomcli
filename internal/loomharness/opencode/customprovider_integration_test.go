package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// TestContractFakeModelFixture proves the pinned OpenCode build reads a
// custom-provider baseURL from sandbox-only config and is driven by the AFT
// fake-model fixture (tests/aft/fixtures/fake-model): scripted text, then an
// approval-needing bash call, with no paid model.
func TestContractFakeModelFixture(t *testing.T) {
	t.Run("CustomProvider", func(t *testing.T) {
		if os.Getenv("LOOM_REAL_OPENCODE") != "1" {
			t.Skip("set LOOM_REAL_OPENCODE=1 to run against the real OpenCode build")
		}
		bin := os.Getenv("LOOM_OPENCODE_BIN")
		if bin == "" {
			home, _ := os.UserHomeDir()
			bin = filepath.Join(home, ".loom/harness/opencode/2.0.19/opencode")
		}
		fixture := startFakeModelFixture(t)
		script := func(steps ...map[string]any) {
			b, _ := json.Marshal(map[string]any{"steps": steps})
			resp, err := http.Post(fixture+"/__script", "application/json", strings.NewReader(string(b)))
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("script: %v %v", resp, err)
			}
			_ = resp.Body.Close()
		}

		sbx, err := os.MkdirTemp("/tmp", "loom-opencode-fixture-")
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
		if h, err := a.Health(ctx); err != nil || !h.OK || h.Version.Installed.String() != "2.0.19" {
			t.Fatalf("Health = %+v, %v", h, err)
		}
		waitFor(t, "aft/m in Models", func() bool { models, err := a.Models(ctx); return err == nil && hasModel(models, "aft/m") })
		feed, err := a.Feed(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer feed.Close()
		events := collect(feed)
		ref, err := a.Open(ctx, loomharness.OpenSpec{
			Key: "fixture-1", Launch: loomharness.Launch{Root: sbx}, Dir: repo, Model: "aft/m",
			Rules: []loomharness.PermissionRule{{Action: "bash", Resource: "*", Effect: "ask"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		owned = append(owned, ref)
		s := a.Session(ref)
		completed := func(n int) func() bool {
			return func() bool {
				events.mu.Lock()
				defer events.mu.Unlock()
				c := 0
				for _, e := range events.events {
					if e.Session.NativeID == ref.NativeID && e.Type == loomharness.EventTurnCompleted && e.StopReason == "completed" {
						c++
					}
				}
				return c >= n
			}
		}

		// The user text names other steps; only the script decides the reply.
		script(map[string]any{"text": "FIXTURE-TEXT-REPLY"})
		if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("fixture-1", "r1"), Text: "run bash and call tools"}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "first turn completed", completed(1))
		if !hasEventText(allEvents(t, s, 100), "FIXTURE-TEXT-REPLY") {
			t.Fatal("scripted text never reached the session")
		}

		script(map[string]any{"bash": "echo FIXTURE-BASH-OUTPUT"}, map[string]any{"text": "after bash"})
		if err := s.Prompt(ctx, loomharness.Input{Key: PromptID("fixture-1", "r2"), Text: "say hello"}); err != nil {
			t.Fatal(err)
		}
		var askID string
		events.wait(t, "bash approval ask", func(e loomharness.Event) bool {
			if e.Session.NativeID == ref.NativeID && e.Type == loomharness.EventAskOpened {
				askID = e.AskID
				return true
			}
			return false
		})
		if err := s.Reply(ctx, askID, loomharness.Reply{Allow: true}); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "second turn completed", completed(2))

		var log struct {
			Requests []json.RawMessage `json:"requests"`
			Queued   int               `json:"queued"`
		}
		resp, err := http.Get(fixture + "/__requests")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if err := json.NewDecoder(resp.Body).Decode(&log); err != nil {
			t.Fatal(err)
		}
		if log.Queued != 0 {
			t.Fatalf("%d scripted steps left unplayed", log.Queued)
		}
		last := string(log.Requests[len(log.Requests)-1])
		if !strings.Contains(last, "FIXTURE-BASH-OUTPUT") {
			t.Fatalf("the approved bash output never reached the model; last request: %s", last)
		}
	})
}

// startFakeModelFixture runs the Node fixture on a free port and returns its
// base URL.
func startFakeModelFixture(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the fake-model fixture: %v", err)
	}
	cmd := exec.Command(node, "../../../tests/aft/fixtures/fake-model/server.mjs")
	cmd.Env = append(os.Environ(), "FAKE_MODEL_PORT=0")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	port, ok := strings.CutPrefix(strings.TrimSpace(line), "fake-model listening ")
	if err != nil || !ok {
		t.Fatalf("fake-model start: %q, %v", line, err)
	}
	return "http://127.0.0.1:" + port
}

func hasEventText(events []loomharness.Event, text string) bool {
	for _, e := range events {
		if strings.Contains(e.Text, text) {
			return true
		}
	}
	return false
}
