package cleanup

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/usage"
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
// loomagent service, and loom usage's read of that agents.db shows the
// steps' summed tokens and cost.
func TestUsageShowsNewAgentTokens(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
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
		Name: "nova", Repo: "/repo", Overrides: loomagent.Overrides{Harness: "opencode"}})
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
	var rec usage.SessionUsage
	for deadline := time.Now().Add(10 * time.Second); ; {
		recs, err := readAgentUsage(ctx, dir, "ws", usage.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		if recs = joinUsage(nil, recs); len(recs) == 1 && recs[0].OutputTokens == 15 {
			rec = recs[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("loom usage never showed the turn's tokens: %+v", recs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec.AgentName != "nova" || rec.Backend != "opencode" || rec.InputTokens != 150 || rec.CacheReadTokens != 5 ||
		rec.CacheWriteTokens != 1 || rec.EstimatedCostUSD != 0.75 {
		t.Fatalf("loom usage record = %+v", rec)
	}
}
