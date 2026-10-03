package cleanup

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

type e2eWorkspace struct{}

func (e2eWorkspace) Ensure(_ context.Context, s loomagent.WorkspaceSpec) (loomagent.WorkingCopy, error) {
	return loomagent.WorkingCopy{Path: "/wt/" + s.Key, Branch: s.Branch, HEAD: "abc"}, nil
}

func (e2eWorkspace) Status(_ context.Context, s loomagent.WorkspaceSpec) (loomagent.WorkspaceStatus, error) {
	return loomagent.WorkspaceStatus{Branch: s.Branch, HEAD: "abc"}, nil
}

func (e2eWorkspace) Remove(context.Context, loomagent.WorkspaceSpec) error { return nil }

func (e2eWorkspace) Publish(context.Context, loomagent.PublishRequest) (loomagent.PublishResult, error) {
	return loomagent.PublishResult{}, nil
}

// TestUsageShowsNewAgentTokens is end to end on the fake harness: a new
// Agent API agent runs a turn with two usage steps through the real
// loomagent service, and `loom usage --format json` (its command path, on
// that agents.db as the Loom data dir) shows the steps' summed tokens and cost.
func TestUsageShowsNewAgentTokens(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", dir) // agents.db is read from the Loom data dir
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", dir)
	st, err := loomstore.Open(ctx, filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	fh := fake.New()
	svc := loomagent.New(loomagent.ServiceConfig{Store: st, Workspace: e2eWorkspace{}, WorkspaceID: "ws",
		Harnesses: map[string]loomharness.Harness{"opencode": fh},
		Bridge: func(context.Context, loomagent.Preset) (loomagent.BridgeCaps, error) {
			return loomagent.BridgeCaps{}, nil
		},
		Launch: func(context.Context, loomstore.Agent, string) (loomharness.Launch, error) {
			return loomharness.Launch{Root: "/root/opencode"}, nil
		}})
	done := make(chan struct{})
	go func() { defer close(done); svc.RunFeed(ctx, "opencode") }()
	defer func() { cancel(); <-done }()

	info, err := svc.Create(ctx, loomagent.CreateRequest{Envelope: loomagent.Envelope{RequestID: "c1"}, Preset: "lead",
		Name: "nova", Repo: "/repo", BaseRef: "main", Overrides: loomagent.Overrides{Harness: "opencode"}})
	if err != nil {
		t.Fatal(err)
	}
	fh.Script(info.AgentID, fake.Turn{Steps: []fake.Step{
		{Usage: &loomharness.Usage{InputTokens: 100, OutputTokens: 10, CacheReadTokens: 5, CacheWriteTokens: 1, CostUSD: 0.25}},
		{Delta: "hi"},
		{Usage: &loomharness.Usage{InputTokens: 50, OutputTokens: 5, CostUSD: 0.5}},
	}})
	if _, err := svc.Send(ctx, loomagent.SendRequest{Envelope: loomagent.Envelope{RequestID: "s1"}, AgentID: info.AgentID,
		Text: "go", Source: "user_chat", Actor: loomagent.ActorRef{Kind: "user", ID: "u"}}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		p, err := st.ListEvents(ctx, loomstore.EventQuery{AgentID: info.AgentID, Kinds: []string{"usage"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Events) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the turn's 2 usage rows were never saved: %d", len(p.Events))
		}
	}

	var out struct {
		Input      int64   `json:"total_input_tokens"`
		Output     int64   `json:"total_output_tokens"`
		CacheRead  int64   `json:"total_cache_read_tokens"`
		CacheWrite int64   `json:"total_cache_write_tokens"`
		Cost       float64 `json:"total_cost"`
		Sessions   []struct {
			AgentName string `json:"agent_name"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(runUsageJSON(t), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sessions) != 1 || out.Sessions[0].AgentName != "nova" || out.Input != 150 || out.Output != 15 ||
		out.CacheRead != 5 || out.CacheWrite != 1 || out.Cost != 0.75 {
		t.Fatalf("loom usage --format json = %+v", out)
	}
}

// runUsageJSON runs `loom usage --format json` and returns what it printed.
func runUsageJSON(t *testing.T) []byte {
	t.Helper()
	if err := usageCmd.Flags().Set("format", "json"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = usageCmd.Flags().Set("format", "table") })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = stdout }()
	got := make(chan []byte)
	go func() { b, _ := io.ReadAll(r); got <- b }()
	usageCmd.Run(usageCmd, nil)
	_ = w.Close()
	return <-got
}
