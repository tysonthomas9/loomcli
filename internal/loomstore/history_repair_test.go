package loomstore

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// repairFixture is a store whose legacy rows tests write before upgrade
// reopens it at the release before the history repair.
type repairFixture struct {
	t    *testing.T
	path string
	s    *Store
}

func newRepairFixture(t *testing.T) *repairFixture {
	path := filepath.Join(t.TempDir(), "loom.db")
	return &repairFixture{t: t, path: path, s: openAt(t, path)}
}

// add inserts a with its saved state transitions (to states, from creating).
func (f *repairFixture) add(a Agent, to ...string) {
	f.t.Helper()
	ctx := context.Background()
	state := a.State
	a.State = "creating"
	if err := f.s.InsertAgent(ctx, a); err != nil {
		f.t.Fatal(err)
	}
	from := "creating"
	for i, s := range to {
		p, _ := json.Marshal(map[string]string{"agentId": a.AgentID, "type": "agent.state_changed", "from": from, "to": s})
		if _, err := f.s.AppendEvent(ctx, Event{AgentID: a.AgentID, EventID: fmt.Sprintf("legacy:%s:%d", a.AgentID, i+1),
			Kind: "agent.state_changed", Payload: p}); err != nil {
			f.t.Fatal(err)
		}
		from = s
	}
	f.exec(`UPDATE agents SET state = ?, attempt = ?, outcome = ? WHERE agent_id = ?`, state, a.Attempt, a.Outcome, a.AgentID)
}

func (f *repairFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.s.db.Exec(q, args...); err != nil {
		f.t.Fatal(q, err)
	}
}

// upgrade reopens the store as the release before the repair left it, so the
// repair runs as the upgrade does.
func (f *repairFixture) upgrade() {
	f.t.Helper()
	f.exec(`DROP TABLE IF EXISTS agent_history_unrepaired`)
	f.exec(fmt.Sprintf("PRAGMA user_version = %d", len(migrations)-1))
	f.s.Close()
	f.s = openAt(f.t, f.path)
}

// rerun runs the repair again on the upgraded store.
func (f *repairFixture) rerun() {
	f.t.Helper()
	f.exec(migrations[len(migrations)-1])
}

func (f *repairFixture) events(agentID string) []Event {
	f.t.Helper()
	page, err := f.s.ListEvents(context.Background(), EventQuery{AgentID: agentID, Limit: 1000})
	if err != nil {
		f.t.Fatal(err)
	}
	return page.Events
}

func (f *repairFixture) repairs(agentID string) []Event {
	var out []Event
	for _, e := range f.events(agentID) {
		if e.Kind == "history.repaired" {
			out = append(out, e)
		}
	}
	return out
}

func (f *repairFixture) unrepaired() []string {
	f.t.Helper()
	rows, err := f.s.db.Query(`SELECT agent_id, gap, detail FROM agent_history_unrepaired ORDER BY agent_id, detail`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, g, d string
		if err := rows.Scan(&a, &g, &d); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, a+" "+g+" "+d)
	}
	return out
}

func withState(a Agent, state string) Agent { a.State = state; return a }

// TestHistoryRepairLegacyFixture: a legacy row whose current state has no
// saved transition to it gets one history.repaired event with its current
// state and the state its history last shows; no transition is invented, and
// rows whose history matches get nothing.
func TestHistoryRepairLegacyFixture(t *testing.T) {
	f := newRepairFixture(t)
	gap := withState(agent("gap", "interactive"), "finished")
	gap.Outcome = ptr("end_turn")
	f.add(gap, "idle", "active") // active -> finished was lost
	f.add(withState(agent("never", "interactive"), "idle"))
	f.add(withState(agent("ok", "interactive"), "idle"), "idle")
	f.add(withState(agent("new", "interactive"), "creating"))
	f.upgrade()

	got := f.repairs("gap")
	if len(got) != 1 {
		t.Fatalf("gap repairs = %+v; want one", got)
	}
	var p map[string]any
	if err := json.Unmarshal(got[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p["state"] != "finished" || p["outcome"] != "end_turn" || p["after"] != "active" || got[0].Seq != 3 ||
		got[0].EventID != "history.repaired:gap:0" {
		t.Fatalf("gap repair = %s %s seq %d", got[0].EventID, got[0].Payload, got[0].Seq)
	}
	for _, e := range f.events("gap") {
		if e.Kind == "agent.state_changed" && e.Seq > 2 {
			t.Fatalf("a transition was invented: %s", e.Payload)
		}
	}
	if got := f.repairs("never"); len(got) != 1 || !jsonHas(got[0].Payload, "after", "creating") {
		t.Fatalf("never repairs = %+v; want one after creating", got)
	}
	if n, m := len(f.repairs("ok")), len(f.repairs("new")); n+m != 0 {
		t.Fatalf("matching histories repaired: ok %d, new %d", n, m)
	}
	if got := f.unrepaired(); len(got) != 0 {
		t.Fatalf("unrepaired = %v", got)
	}
}

func jsonHas(p json.RawMessage, k, v string) bool {
	var m map[string]any
	return json.Unmarshal(p, &m) == nil && m[k] == v
}

// TestHistoryRepairIdempotent: running the repair again, or reopening, adds
// no event and no report.
func TestHistoryRepairIdempotent(t *testing.T) {
	f := newRepairFixture(t)
	f.add(withState(agent("gap", "interactive"), "idle"), "idle", "active")
	f.add(withState(agent("L", "interactive"), "idle"), "idle")
	c := withState(agent("c", "background"), "active")
	c.Mode, c.ParentAgentID, c.Attempt = "single_task", ptr("L"), 1
	f.add(c, "idle", "active", "finished", "active")
	f.upgrade()
	before, report := len(f.events("gap"))+len(f.events("L")), f.unrepaired()
	f.rerun()
	f.rerun()
	f.s.Close()
	f.s = openAt(t, f.path)
	if n := len(f.events("gap")) + len(f.events("L")); n != before || len(f.repairs("gap")) != 1 {
		t.Fatalf("events %d -> %d, repairs %d; want unchanged and one", before, n, len(f.repairs("gap")))
	}
	if got := f.unrepaired(); fmt.Sprint(got) != fmt.Sprint(report) || len(got) != 1 {
		t.Fatalf("unrepaired %v -> %v; want one, unchanged", report, got)
	}
}

// TestHistoryRepairConcurrentWrites: state changes saved with their events
// (after OR3) while the repair runs are never repaired, and neither are
// changes saved after a repaired one.
func TestHistoryRepairConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	f := newRepairFixture(t)
	f.add(withState(agent("gap", "interactive"), "active"), "idle")
	f.add(withState(agent("live", "interactive"), "idle"), "idle")
	f.upgrade()
	commit := func(id, to string) {
		a, err := f.s.GetAgent(ctx, id)
		if err != nil {
			t.Error(err)
			return
		}
		next := a.StateOf()
		next.State = to
		p, _ := json.Marshal(map[string]string{"type": "agent.state_changed", "from": a.State, "to": to})
		if _, err := f.s.CommitState(ctx, id, a.StateOf(), next, a.Revision,
			[]Event{{AgentID: id, Kind: "agent.state_changed", Payload: p}}); err != nil {
			t.Error(err)
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 40 {
			commit("live", []string{"active", "idle"}[i%2])
			commit("gap", []string{"idle", "active"}[i%2])
		}
	}()
	go func() {
		defer wg.Done()
		for range 40 {
			if _, err := f.s.db.Exec(migrations[len(migrations)-1]); err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Wait()
	if n, m := len(f.repairs("live")), len(f.repairs("gap")); n != 0 || m != 1 {
		t.Fatalf("repairs live %d, gap %d; want 0 and the upgrade's 1", n, m)
	}
	if n := len(f.events("live")); n != 41 {
		t.Fatalf("live events = %d; want its 41 saved transitions", n)
	}
}

// TestHistoryRepairSkipsPurged: an agent whose history was purged (R29) and
// a deleted agent have empty histories on purpose: neither is repaired or
// reported, nor is a lost record owed to them.
func TestHistoryRepairSkipsPurged(t *testing.T) {
	ctx := context.Background()
	f := newRepairFixture(t)
	f.add(withState(agent("purged", "interactive"), "archived"), "idle", "archived")
	f.add(withState(agent("deleted", "interactive"), "idle"), "idle")
	for _, parent := range []string{"purged", "deleted"} {
		c := withState(agent("c-"+parent, "background"), "active")
		c.Mode, c.ParentAgentID, c.Attempt = "single_task", ptr(parent), 1
		f.add(c, "idle", "active", "finished", "active")
	}
	f.exec(`UPDATE agents SET archived_at = ? WHERE agent_id = 'purged'`, Stamp(time.Now()))
	if err := f.s.MarkHistoryPurged(ctx, "purged", time.Now().Add(HistoryRetention+time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.s.Tombstone(ctx, "deleted", time.Now()); err != nil {
		t.Fatal(err)
	}
	f.upgrade()
	for _, id := range []string{"purged", "deleted"} {
		if got := f.events(id); len(got) != 0 {
			t.Fatalf("%s history = %+v; want none", id, got)
		}
	}
	if got := f.unrepaired(); len(got) != 0 {
		t.Fatalf("unrepaired = %v; want none", got)
	}
}

// TestHistoryRepairRecordsIrreparable: a child attempt that ended (the child
// was reopened since) with no task_completed on its parent and none owed
// can't be rebuilt, as its outcome, head and summary are gone: it is
// recorded, and nothing is appended for it. An attempt whose record is saved
// or still owed by a marker is not.
func TestHistoryRepairRecordsIrreparable(t *testing.T) {
	ctx := context.Background()
	f := newRepairFixture(t)
	f.add(withState(agent("L", "interactive"), "idle"), "idle")
	c := withState(agent("c", "background"), "active")
	c.Mode, c.ParentAgentID, c.Attempt = "single_task", ptr("L"), 2
	f.add(c, "idle", "active", "finished", "active", "finished", "active")
	o := withState(agent("o", "background"), "active")
	o.Mode, o.ParentAgentID, o.Attempt = "single_task", ptr("L"), 1
	f.add(o, "idle", "active", "finished", "active")
	if _, err := f.s.AppendEvent(ctx, Event{AgentID: "L", EventID: "task_completed:c:0", Kind: "task_completed",
		Payload: json.RawMessage(`{"child":"c","attempt":0}`)}); err != nil {
		t.Fatal(err)
	}
	f.exec(`INSERT INTO agent_completion_markers (child_agent_id, attempt, parent_agent_id, outcome, branch, created_at)
		VALUES ('o', 0, 'L', 'end_turn', '', ?)`, Stamp(time.Now()))
	before := len(f.events("L"))
	f.upgrade()
	if got := f.unrepaired(); fmt.Sprint(got) != "[L task_completed c:1]" {
		t.Fatalf("unrepaired = %v; want [L task_completed c:1]", got)
	}
	if n := len(f.events("L")); n != before {
		t.Fatalf("parent events %d -> %d; want nothing appended", before, n)
	}
}
