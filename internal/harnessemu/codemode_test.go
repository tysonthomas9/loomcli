package harnessemu_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tysonthomas9/loomcli/internal/harnessemu"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/opencode"
)

// TestMain lets the test binary serve as a stdio MCP server (the bridge's
// stand-in) when the emulator launches it with EMU_TEST_MCP set.
func TestMain(m *testing.M) {
	if who := os.Getenv("EMU_TEST_MCP"); who != "" {
		s := mcp.NewServer(&mcp.Implementation{Name: "fake-bridge", Version: "1"}, nil)
		type out struct {
			Agents []string `json:"agents"`
		}
		mcp.AddTool(s, &mcp.Tool{Name: "agent_list"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, out, error) {
			return nil, out{Agents: []string{who}}, nil
		})
		_ = s.Run(context.Background(), &mcp.StdioTransport{})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// A model call of OpenCode's execute tool with tools.loom.agent_list({})
// runs the tool on the location's registered "loom" server with its
// environment, shows as a completed tool item, and the model gets the
// result before it replies.
func TestEmulatorCodeModeRunsBridgeTool(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var bodies []string
	replies := []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"execute","arguments":"{\"code\":\"return await tools.loom.agent_list({})\"}"}}]}}]}`,
		`{"choices":[{"delta":{"content":"listed"}}]}`,
	}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		bodies = append(bodies, string(b))
		_, _ = io.WriteString(w, "data: "+replies[0]+"\n\ndata: [DONE]\n\n")
		replies = replies[1:]
	}))
	defer model.Close()
	s, err := harnessemu.New(filepath.Join(t.TempDir(), "state.json"), "", "pw")
	if err != nil {
		t.Fatal(err)
	}
	s.Model = model.URL
	srv := httptest.NewServer(s.Handler())
	defer func() { s.Close(); srv.Close() }()
	url, c := srv.URL, opencode.NewClient(srv.URL, "pw")
	dir := t.TempDir()
	cfg := `{"config":{"type":"local","command":["` + os.Args[0] + `"],"environment":{"EMU_TEST_MCP":"child-from-env"}}}`
	if code, b := send(t, "PUT", url+"/api/experimental/mcp/loom?location[directory]="+dir, cfg); code != 204 {
		t.Fatalf("PUT = %d %s", code, b)
	}
	f, err := c.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ref, err := c.Open(ctx, loomharness.OpenSpec{Key: "lead", Dir: dir, Metadata: map[string]string{"agent_id": "lead"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Session(ref).Prompt(ctx, loomharness.Input{Key: opencode.PromptID("lead", "r1"), Text: "list"}); err != nil {
		t.Fatal(err)
	}
	var tool, text string
	for _, e := range until(t, f, func(e loomharness.Event) bool { return completed(e) && e.Session.NativeID == ref.NativeID }) {
		if e.ItemKind == "tool" && e.Type == loomharness.EventItemCompleted {
			tool = e.ItemID
		}
		if e.Type == loomharness.EventDelta {
			text += e.Text
		}
	}
	if !strings.HasSuffix(tool, "/tool/call_1") || text != "listed" {
		t.Fatalf("tool item %q, text %q", tool, text)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || !strings.Contains(bodies[1], `"role":"tool"`) || !strings.Contains(bodies[1], `{\"agents\":[\"child-from-env\"]}`) {
		t.Fatalf("model requests = %v", bodies)
	}
}
