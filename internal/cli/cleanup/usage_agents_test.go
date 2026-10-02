package cleanup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

func seedAgent(t *testing.T, st *loomstore.Store, id, ws, harness string, subject ...string) {
	t.Helper()
	a := loomstore.Agent{AgentID: id, WorkspaceID: ws, Name: id, ProfileKey: id, Preset: "lead",
		PresetVersion: "1", Mode: "persistent", InteractionMode: "interactive", RoleKind: "interactive",
		SpecJSON: "{}", SpecVersion: 1, OwnerKind: "user", OwnerID: "u", CreatedByKind: "user",
		CreatedByID: "u", CreateRequestID: "req-" + id, Repo: "/repo", Harness: harness, State: "idle"}
	if len(subject) == 2 {
		a.SubjectType, a.SubjectID = &subject[0], &subject[1]
	}
	if err := st.InsertAgent(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}

func seedEvent(t *testing.T, st *loomstore.Store, agentID, kind, id string, payload any) {
	t.Helper()
	b, _ := json.Marshal(payload)
	if _, err := st.AppendEvent(context.Background(), loomstore.Event{AgentID: agentID, Kind: kind,
		EventID: kind + ":" + id, TurnID: "t1", Payload: b}); err != nil {
		t.Fatal(err)
	}
}

// seedUsageDB writes an agents.db with one agent per harness in workspace
// "ws" and one elsewhere: opencode has two usage steps, codex one, Claude
// a turn whose usage row has no token counts, and "idle" never ran.
func seedUsageDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := loomstore.Open(context.Background(), filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	seedAgent(t, st, "oc", "ws", "opencode", "task", "T-1")
	seedAgent(t, st, "cx", "ws", "codex", "epic", "E-1")
	seedAgent(t, st, "cl", "ws", "claude")
	seedAgent(t, st, "idle", "ws", "claude")
	seedAgent(t, st, "far", "other", "opencode")
	tok := map[string]any{"session": "s", "inputTokens": 100, "outputTokens": 10,
		"cacheReadTokens": 5, "cacheWriteTokens": 1, "costUsd": 0.25}
	for _, id := range []string{"oc", "cx", "cl", "far"} {
		seedEvent(t, st, id, "turn.started", id, map[string]any{"session": "s"})
	}
	seedEvent(t, st, "oc", "usage", "oc-1", tok)
	seedEvent(t, st, "oc", "usage", "oc-2", tok)
	seedEvent(t, st, "oc", "usage", "oc-2", tok) // a replayed step is one row
	seedEvent(t, st, "cx", "usage", "cx-1", tok)
	seedEvent(t, st, "cl", "usage", "cl-1", map[string]any{"session": "s"})
	seedEvent(t, st, "far", "usage", "far-1", tok)
	for _, id := range []string{"oc", "cx", "cl", "far"} {
		seedEvent(t, st, id, "turn.completed", id, map[string]any{"session": "s", "stopReason": "completed"})
	}
	return dir
}

func byName(recs []usage.SessionUsage) map[string]usage.SessionUsage {
	m := map[string]usage.SessionUsage{}
	for _, r := range recs {
		m[r.AgentName] = r
	}
	return m
}

func TestReadAgentUsageAcrossHarnesses(t *testing.T) {
	dir := seedUsageDB(t)
	recs, err := readAgentUsage(context.Background(), dir, "ws", usage.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	got := byName(recs)
	if len(recs) != 4 || got["far"].SessionID != "" {
		t.Fatalf("want the 4 agents of ws, got %+v", recs)
	}
	oc := got["oc"]
	if oc.Backend != "opencode" || oc.TaskID != "T-1" || oc.SessionID != "oc" ||
		oc.InputTokens != 200 || oc.OutputTokens != 20 || oc.CacheReadTokens != 10 ||
		oc.CacheWriteTokens != 2 || oc.EstimatedCostUSD != 0.5 {
		t.Errorf("opencode = %+v", oc)
	}
	if cx := got["cx"]; cx.Backend != "codex" || cx.EpicID != "E-1" || cx.InputTokens != 100 || cx.EstimatedCostUSD != 0.25 {
		t.Errorf("codex = %+v", cx)
	}
	for _, name := range []string{"cl", "idle"} {
		r := got[name]
		if r.Backend != "claude" || r.InputTokens != 0 || r.OutputTokens != 0 || r.StartedAt.IsZero() {
			t.Errorf("%s should be listed with zero usage, got %+v", name, r)
		}
	}
	if oc.EndedAt.Before(oc.StartedAt) {
		t.Errorf("opencode window %v..%v", oc.StartedAt, oc.EndedAt)
	}

	all, err := readAgentUsage(context.Background(), dir, "", usage.Filter{})
	if err != nil || len(all) != 5 {
		t.Fatalf("no workspace reads every agent: %d %v", len(all), err)
	}
}

func TestReadAgentUsageFilters(t *testing.T) {
	dir := seedUsageDB(t)
	read := func(f usage.Filter) map[string]usage.SessionUsage {
		t.Helper()
		recs, err := readAgentUsage(context.Background(), dir, "ws", f)
		if err != nil {
			t.Fatal(err)
		}
		return byName(recs)
	}
	if got := read(usage.Filter{AgentName: "cx"}); len(got) != 1 || got["cx"].AgentName != "cx" {
		t.Errorf("--agent: %+v", got)
	}
	if got := read(usage.Filter{Backend: "claude"}); len(got) != 2 {
		t.Errorf("--backend claude: %+v", got)
	}
	if got := read(usage.Filter{EpicID: "E-1"}); len(got) != 1 || got["cx"].EpicID != "E-1" {
		t.Errorf("--epic: %+v", got)
	}
	today := time.Now().Add(-time.Hour)
	if got := read(usage.Filter{Since: today}); len(got) != 4 {
		t.Errorf("--today: %+v", got)
	}
	if got := read(usage.Filter{Since: time.Now().Add(time.Hour)}); len(got) != 0 {
		t.Errorf("a range after every event lists nothing: %+v", got)
	}
	if got := read(usage.Filter{Until: time.Now().Add(-time.Hour)}); len(got) != 0 {
		t.Errorf("a range before every event lists nothing: %+v", got)
	}
}

func TestReadAgentUsageNoDatabase(t *testing.T) {
	dir := t.TempDir()
	recs, err := readAgentUsage(context.Background(), dir, "ws", usage.Filter{})
	if err != nil || recs != nil {
		t.Fatalf("got %v, %v", recs, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agents.db")); !os.IsNotExist(err) {
		t.Errorf("reading usage must not create agents.db")
	}
}

func TestJoinUsageCountsEachSessionOnce(t *testing.T) {
	dir := seedUsageDB(t)
	agents, err := readAgentUsage(context.Background(), dir, "ws", usage.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	v5 := []usage.SessionUsage{
		{AgentName: "nova", Backend: "claude", InputTokens: 1000, SessionID: "v5-1"},
		{AgentName: "oc", Backend: "opencode", InputTokens: 200, SessionID: "oc"}, // also in agents.db
		{AgentName: "old", Backend: "codex", InputTokens: 7},
	}
	joined := joinUsage(v5, agents)
	agg := aggregateUsage(joined)
	if agg.SessionCount != 6 || agg.TotalInput != 1000+200+7+100 {
		t.Fatalf("sessions %d input %d: %+v", agg.SessionCount, agg.TotalInput, joined)
	}
	backends := map[string]int{}
	for _, b := range agg.ByBackend {
		backends[b.Name] = b.Sessions
	}
	if backends["claude"] != 3 || backends["opencode"] != 1 || backends["codex"] != 2 {
		t.Errorf("by backend %+v", backends)
	}
}
