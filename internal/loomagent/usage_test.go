package loomagent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestUsageCostFromSessionTotal: a harness that reports its session's running
// cost (Claude's total_cost_usd, which a resumed process continues) saves each
// usage row's cost as the rise over the session's last saved total. A new
// session counts its first total whole; a turn after a resumed relaunch
// counts only its rise; so does the first turn after a serve restart, whose
// baseline is read back from the saved rows.
func TestUsageCostFromSessionTotal(t *testing.T) {
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	stop := startFeed(s, e)
	a, _ := newLead(t, e, s, "alpha")
	turn := func(s *Service, req string, steps ...fake.Step) {
		t.Helper()
		fh.Script(a.AgentID, fake.Turn{Steps: steps})
		n := len(kinds(rows(t, s, a.AgentID, 0), EventIdle))
		mustSendMsg(t, s, sendReq(a.AgentID, req, "go", user))
		eventually(t, "idle", func() bool { return len(kinds(rows(t, s, a.AgentID, 0), EventIdle)) == n+1 })
	}
	total := func(v float64) fake.Step {
		return fake.Step{Usage: &loomharness.Usage{OutputTokens: 1, CostTotalUSD: v}}
	}
	costs := func(s *Service) []float64 {
		var out []float64
		for _, r := range kinds(rows(t, s, a.AgentID, 0), string(loomharness.EventUsage)) {
			var u struct {
				Cost float64 `json:"costUsd"`
			}
			_ = json.Unmarshal(r.Payload, &u)
			out = append(out, u.Cost)
		}
		return out
	}

	turn(s, "u1", total(0.25)) // a new session: its first total whole
	// A cut-off turn's partial usage has no total; then a resumed process
	// continues the session's total.
	turn(s, "u2", fake.Step{Usage: &loomharness.Usage{InputTokens: 3}}, total(0.75))
	if got, want := costs(s), []float64{0.25, 0, 0.5}; !slices.Equal(got, want) {
		t.Fatalf("costs %v, want %v", got, want)
	}

	stop() // a serve restart: a new service on the same store
	s2 := e.service(ServiceConfig{})
	defer startFeed(s2, e)()
	turn(s2, "u3", total(1))
	turn(s2, "u4", total(0.125)) // a total below the last: the session started again
	if got, want := costs(s2), []float64{0.25, 0, 0.5, 0.25, 0.125}; !slices.Equal(got, want) {
		t.Fatalf("costs after a restart %v, want %v", got, want)
	}
}

// TestLastCostTotal: the baseline lookup returns the newest saved total of
// the asked session, skipping other sessions and rows without a total, and 0
// for a session with none.
func TestLastCostTotal(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	a, _ := newLead(t, e, s, "alpha")
	ctx := context.Background()
	for i, p := range []string{
		`{"session":"s1","costTotalUsd":0.25}`, `{"session":"s1","costTotalUsd":0.5}`,
		`{"session":"s2","costTotalUsd":9}`, `{"session":"s1","inputTokens":3}`,
	} {
		if _, err := s.store.AppendEvent(ctx, loomstore.Event{AgentID: a.AgentID, Kind: "usage",
			EventID: fmt.Sprintf("usage:%d", i), Payload: json.RawMessage(p)}); err != nil {
			t.Fatal(err)
		}
	}
	for session, want := range map[string]float64{"s1": 0.5, "s2": 9, "s3": 0} {
		if got, err := s.store.LastCostTotal(ctx, a.AgentID, session); err != nil || got != want {
			t.Fatalf("LastCostTotal(%s) = %v, %v; want %v", session, got, err, want)
		}
	}
}
