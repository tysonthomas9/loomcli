package loomagent

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// markerService is newService on the database file path.
func markerService(t *testing.T, cfg ServiceConfig, path string, agents ...loomstore.Agent) *Service {
	t.Helper()
	st, err := loomstore.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, a := range agents {
		if err := st.InsertAgent(context.Background(), a); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Store, cfg.Events, cfg.WorkspaceID = st, NewEventLog(st), "ws"
	s := New(cfg)
	useTestClock(s)
	return s
}

// markers is how many completion markers the database at path holds.
func markers(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_completion_markers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// failRecords makes every task_completed append fail in its transaction,
// as a crash before it commits; the returned func lifts it.
func failRecords(t *testing.T, path string) func() {
	t.Helper()
	return failOn(t, &createEnv{path: path}, `INSERT ON agent_events WHEN NEW.kind = 'task_completed'`)
}

// published is the task_completed events the Bus sent sub so far.
func published(sub *BusSubscription) []Event {
	var out []Event
	for {
		select {
		case e, ok := <-sub.C:
			if !ok {
				return out
			}
			if e.Type == KindTaskCompleted {
				out = append(out, e)
			}
		default:
			return out
		}
	}
}

func dbPath(t *testing.T) string { return filepath.Join(t.TempDir(), "loom.db") }

// TestCompletionCrashAfterChildCommit: the child's attempt ends and Loom
// crashes before the parent's record is appended; the lead sends the child
// again before the restart. After the restart the sweep saves attempt 1's
// record exactly once, and nothing stays owed.
func TestCompletionCrashAfterChildCommit(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	s := markerService(t, ServiceConfig{}, path, busy("L", "persistent", StateActive), childOf("c1", "L"))
	lift := failRecords(t, path)
	endAttempt(t, s, "c1", "completed")
	if got := completions(t, s, "L"); len(got) != 0 {
		t.Fatalf("setup: records before the crash = %+v", got)
	}
	nextAttempt(t, s, "c1")
	lift()
	s2 := markerService(t, ServiceConfig{}, path) // restart
	s2.recordCompletions(ctx)
	s2.recordCompletions(ctx)
	got := completions(t, s2, "L")
	if len(got) != 1 || got[0].Attempt != 1 || got[0].Outcome != "completed" {
		t.Fatalf("records after restart = %+v; want attempt 1's, once", got)
	}
	if n := markers(t, path); n != 0 {
		t.Fatalf("markers left = %d", n)
	}
}

// TestCompletionAppendAndMarkerDeleteAtomic: the child's own commit appends
// nothing on its parent; a crash inside the delivery's transaction keeps
// the marker and saves no record, so delivery runs again after the restart;
// once it commits, nothing is owed and a repeat saves nothing more.
func TestCompletionAppendAndMarkerDeleteAtomic(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	s := markerService(t, ServiceConfig{}, path, busy("L", "persistent", StateActive), childOf("c1", "L"))
	finishTurn(t, s, "c1", "completed")
	if got := completions(t, s, "L"); len(got) != 0 {
		t.Fatalf("the child's commit saved the parent's record: %+v", got)
	}
	lift := failRecords(t, path)
	s.recordCompletions(ctx)
	if got, n := completions(t, s, "L"), markers(t, path); len(got) != 0 || n != 1 {
		t.Fatalf("after a crashed delivery: records %+v, markers %d; want none and 1", got, n)
	}
	lift()
	s2 := markerService(t, ServiceConfig{}, path) // restart
	s2.recordCompletions(ctx)
	s2.recordCompletions(ctx)
	if got, n := completions(t, s2, "L"), markers(t, path); len(got) != 1 || got[0].Attempt != 1 || n != 0 {
		t.Fatalf("after delivery: records %+v, markers %d; want one and none", got, n)
	}
}

// TestCompletionReattemptGetsNewEvent: two attempts end while no record can
// be saved; each gets its own record, with its own event ID.
func TestCompletionReattemptGetsNewEvent(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	s := markerService(t, ServiceConfig{}, path, busy("L", "persistent", StateActive), childOf("c1", "L"))
	lift := failRecords(t, path)
	endAttempt(t, s, "c1", "failed")
	nextAttempt(t, s, "c1")
	endAttempt(t, s, "c1", "completed")
	lift()
	s.recordCompletions(ctx)
	s.recordCompletions(ctx)
	var ids []string
	for _, e := range kinds(rows(t, s, "L", 0), KindTaskCompleted) {
		ids = append(ids, e.EventID)
	}
	got := completions(t, s, "L")
	if len(got) != 2 || got[0].Outcome != "failed" || got[1].Outcome != "completed" ||
		strings.Join(ids, ",") != completionKey("c1", 1)+","+completionKey("c1", 2) {
		t.Fatalf("records = %+v, ids %v; want attempt 1 failed and attempt 2 completed", got, ids)
	}
	if n := markers(t, path); n != 0 {
		t.Fatalf("markers left = %d", n)
	}
}

// hookStatus is a Workspace port that runs hook during each Status.
type hookStatus struct {
	headWorkspace
	hook func()
}

func (w *hookStatus) Status(ctx context.Context, s WorkspaceSpec) (WorkspaceStatus, error) {
	if w.hook != nil {
		w.hook()
	}
	return w.headWorkspace.Status(ctx, s)
}

// parentGoneRace runs the three orders of a child's completion and its
// parent going (gone: a Delete or a history purge): the marker before,
// during delivery (after its head read) and after. In each, no marker is
// left, and the parent gets no saved record and no published one.
func parentGoneRace(t *testing.T, lead loomstore.Agent, gone func(*testing.T, *Service)) {
	for _, order := range []string{"marker first", "during delivery", "parent first"} {
		t.Run(order, func(t *testing.T) {
			ctx := context.Background()
			path := dbPath(t)
			ws := &hookStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc"}}
			c := childOf("c1", "L")
			c.WorktreePath, c.Branch = sp("/wt/c1"), sp("loom/agent/c1")
			s := markerService(t, ServiceConfig{Workspace: ws}, path, lead, c)
			sub := s.Bus.Subscribe("L")
			defer s.Bus.Unsubscribe(sub)
			switch order {
			case "marker first":
				lift := failRecords(t, path)
				endAttempt(t, s, "c1", "completed")
				lift()
				gone(t, s)
			case "during delivery":
				var once sync.Once
				ws.hook = func() { once.Do(func() { gone(t, s) }) }
				endAttempt(t, s, "c1", "completed")
			case "parent first":
				gone(t, s)
				endAttempt(t, s, "c1", "completed")
			}
			s.recordCompletions(ctx) // delivery, and its retry
			s.recordCompletions(ctx)
			if got := published(sub); len(got) != 0 {
				t.Fatalf("published to the gone parent: %+v", got)
			}
			if got := completions(t, s, "L"); len(got) != 0 {
				t.Fatalf("records on the gone parent = %+v", got)
			}
			if n := markers(t, path); n != 0 {
				t.Fatalf("markers left = %d", n)
			}
		})
	}
}

// TestCompletionParentDeleteRace: a parent deleted before, during or after
// its child's completion gets no record, and no marker is left.
func TestCompletionParentDeleteRace(t *testing.T) {
	parentGoneRace(t, busy("L", "persistent", StateActive), func(t *testing.T, s *Service) {
		if err := s.store.Tombstone(context.Background(), "L", time.Now()); err != nil {
			t.Fatal(err)
		}
	})
}

// TestCompletionParentPurgeRace: a parent whose history is purged (R29)
// before, during or after its child's completion gets no record, and no
// marker is left.
func TestCompletionParentPurgeRace(t *testing.T) {
	lead := busy("L", "persistent", StateActive)
	lead.ArchivedAt = sp(loomstore.Stamp(time.Now()))
	parentGoneRace(t, lead, func(t *testing.T, s *Service) {
		if err := s.store.MarkHistoryPurged(context.Background(), "L", time.Now().Add(loomstore.HistoryRetention+time.Hour)); err != nil {
			t.Fatal(err)
		}
	})
}

// TestCompletionChildDeletedBeforeDelivery: the child is deleted after its
// attempt ended and before its record was saved; the record is saved once,
// with the outcome, branch and summary from the marker, no head, and
// child_deleted.
func TestCompletionChildDeletedBeforeDelivery(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	c := childOf("c1", "L")
	c.WorktreePath, c.Branch = sp("/wt/c1"), sp("loom/agent/c1")
	ws := &headWorkspace{branch: "loom/agent/c1", head: "abc123"}
	s := markerService(t, ServiceConfig{Workspace: ws}, path, busy("L", "persistent", StateActive), c)
	if err := s.appendEvent(ctx, "c1", "item.completed", "item:done", map[string]string{"itemKind": "message", "text": "done; PR ready"}); err != nil {
		t.Fatal(err)
	}
	lift := failRecords(t, path)
	endAttempt(t, s, "c1", "completed")
	if err := s.store.Tombstone(ctx, "c1", time.Now()); err != nil {
		t.Fatal(err)
	}
	lift()
	s.recordCompletions(ctx)
	s.recordCompletions(ctx)
	evs := kinds(rows(t, s, "L", 0), KindTaskCompleted)
	got := completions(t, s, "L")
	if len(got) != 1 || got[0].Outcome != "completed" || got[0].Branch != "loom/agent/c1" ||
		got[0].Summary != "done; PR ready" || got[0].Head != "" || !strings.Contains(string(evs[0].Payload), `"child_deleted":true`) {
		t.Fatalf("records = %+v; want one, from the marker, with no head and child_deleted", got)
	}
	if n := markers(t, path); n != 0 {
		t.Fatalf("markers left = %d", n)
	}
}

// lockCheck is a Workspace port whose Status, which delivery calls,
// counts, while on, each call made without the parent's lock held or with
// the child's lock held.
type lockCheck struct {
	headWorkspace
	s      *Service
	parent string
	on     atomic.Bool
	bad    atomic.Int64
}

func (w *lockCheck) Status(ctx context.Context, spec WorkspaceSpec) (WorkspaceStatus, error) {
	if !w.on.Load() {
		return w.headWorkspace.Status(ctx, spec)
	}
	if l := w.s.agentLock(w.parent); l.TryLock() {
		l.Unlock()
		w.bad.Add(1)
	}
	if l := w.s.agentLock(spec.Key); l.TryLock() {
		l.Unlock()
	} else {
		w.bad.Add(1)
	}
	return w.headWorkspace.Status(ctx, spec)
}

// TestChildParentConcurrentNoDeadlock: a child's change, made under its own
// lock, never takes its parent's, and delivery runs under the parent's lock
// alone. Children finishing and reopening race the lead's dispatches,
// sweeps and the dispatcher; nothing deadlocks and each attempt gets
// exactly one record.
func TestChildParentConcurrentNoDeadlock(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	ws := &lockCheck{headWorkspace: headWorkspace{branch: "b", head: "h"}, parent: "L"}
	kids := []string{"c1", "c2", "c3"}
	agents := []loomstore.Agent{busy("L", "persistent", StateActive)}
	for _, id := range kids {
		c := childOf(id, "L")
		c.WorktreePath, c.Branch = sp("/wt/"+id), sp("loom/agent/"+id)
		agents = append(agents, c)
	}
	s := markerService(t, ServiceConfig{Workspace: ws}, path, agents...)
	ws.s = s
	held := func(id string, f func()) {
		defer s.lock(id)()
		f()
	}
	ws.on.Store(true)
	held("c1", func() { finishTurn(t, s, "c1", "completed") }) // the child's change under its lock alone
	s.recordCompletions(ctx)
	if n := ws.bad.Load(); n != 0 {
		t.Fatalf("delivery ran %d time(s) without the parent's lock or with the child's", n)
	}
	ws.on.Store(false) // below, another goroutine may hold a child's lock while delivery runs
	held("c1", func() { nextAttempt(t, s, "c1") })

	runDispatcher(t, s)
	const rounds = 4
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for _, id := range kids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range rounds {
				held(id, func() { finishTurn(t, s, id, "completed") })
				if i < rounds-1 {
					held(id, func() { nextAttempt(t, s, id) })
				}
			}
		}()
	}
	var bg sync.WaitGroup
	for _, f := range []func(){func() { s.recordCompletions(ctx) }, func() { _ = s.Dispatch(ctx, "L") }} {
		bg.Add(1)
		go func() {
			defer bg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					f()
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	bg.Wait()
	settled(t, s)
	s.recordCompletions(ctx)
	want := map[string]int{}
	for _, id := range kids {
		last := int64(rounds)
		if id == "c1" {
			last++
		}
		for a := int64(1); a <= last; a++ {
			want[completionKey(id, a)] = 1
		}
	}
	got := map[string]int{}
	for _, e := range kinds(rows(t, s, "L", 0), KindTaskCompleted) {
		got[e.EventID]++
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("records = %v; want %v", got, want)
	}
	if n := markers(t, path); n != 0 {
		t.Fatalf("markers left %d", n)
	}
}

// TestCompletionOwedAtUpgrade: an attempt ended before the upgrade with its
// record unsaved; the upgrade saves its marker, and the sweep saves the
// record once, with the summary read from the child's history.
func TestCompletionOwedAtUpgrade(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	s := markerService(t, ServiceConfig{}, path, busy("L", "persistent", StateActive), childOf("c1", "L"))
	if err := s.appendEvent(ctx, "c1", "item.completed", "item:done", map[string]string{"itemKind": "message", "text": "all done"}); err != nil {
		t.Fatal(err)
	}
	finishTurn(t, s, "c1", "completed")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"DROP TABLE agent_completion_markers", fmt.Sprintf("PRAGMA user_version = %d", v-1)} {
		if _, err := db.Exec(q); err != nil { // the release before the markers
			t.Fatal(q, err)
		}
	}
	s2 := markerService(t, ServiceConfig{}, path) // the upgrade
	s2.recordCompletions(ctx)
	s2.recordCompletions(ctx)
	got := completions(t, s2, "L")
	if len(got) != 1 || got[0].Attempt != 1 || got[0].Outcome != "completed" || got[0].Summary != "all done" {
		t.Fatalf("records = %+v; want attempt 1's, once, with its summary", got)
	}
	if n := markers(t, path); n != 0 {
		t.Fatalf("markers left = %d", n)
	}
}

// TestCompletionArchiveAfterDelivery: archiving a finished child whose
// record was delivered (finished, stopping, archived) saves and publishes
// no second record.
func TestCompletionArchiveAfterDelivery(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	s := markerService(t, ServiceConfig{}, path, busy("L", "persistent", StateActive), childOf("c1", "L"))
	endAttempt(t, s, "c1", "completed")
	sub := s.Bus.Subscribe("L")
	defer s.Bus.Unsubscribe(sub)
	if err := s.Archive(ctx, ArchiveRequest{AgentID: "c1", Reason: "done"}); err != nil {
		t.Fatal(err)
	}
	s.recordCompletions(ctx)
	if got := published(sub); len(got) != 0 {
		t.Fatalf("archive republished the record: %+v", got)
	}
	if got, n := completions(t, s, "L"), markers(t, path); len(got) != 1 || n != 0 {
		t.Fatalf("records %+v, markers %d; want one and none", got, n)
	}
}

// TestCompletionGoneParentFailingStatus: a marker owed to a deleted parent
// is dropped even while the child's Workspace.Status keeps failing.
func TestCompletionGoneParentFailingStatus(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	ws := &flakyStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc"}}
	ws.fail.Store(true)
	c := childOf("c1", "L")
	c.WorktreePath, c.Branch = sp("/wt/c1"), sp("loom/agent/c1")
	s := markerService(t, ServiceConfig{Workspace: ws}, path, busy("L", "persistent", StateActive), c)
	if err := s.store.Tombstone(ctx, "L", time.Now()); err != nil {
		t.Fatal(err)
	}
	finishTurn(t, s, "c1", "completed") // its marker is owed to the deleted parent
	s.recordCompletions(ctx)
	if n := markers(t, path); n != 0 {
		t.Fatalf("markers left = %d", n)
	}
}

// TestCompletionChildDeletedDuringDelivery: the child is deleted after
// delivery read its head and before delivery commits; the record has no
// head and child_deleted.
func TestCompletionChildDeletedDuringDelivery(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	c := childOf("c1", "L")
	c.WorktreePath, c.Branch = sp("/wt/c1"), sp("loom/agent/c1")
	ws := &hookStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc"}}
	s := markerService(t, ServiceConfig{Workspace: ws}, path, busy("L", "persistent", StateActive), c)
	ws.hook = func() {
		if err := s.store.Tombstone(ctx, "c1", time.Now()); err != nil {
			t.Error(err)
		}
	}
	endAttempt(t, s, "c1", "completed")
	evs := kinds(rows(t, s, "L", 0), KindTaskCompleted)
	got := completions(t, s, "L")
	if len(got) != 1 || got[0].Head != "" || !strings.Contains(string(evs[0].Payload), `"child_deleted":true`) {
		t.Fatalf("records = %+v; want one with no head and child_deleted", got)
	}
}

// TestCompletionKeepsMarkerBranch: a Workspace.Status that reports no
// branch (the working copy is gone) keeps the branch the marker saved.
func TestCompletionKeepsMarkerBranch(t *testing.T) {
	c := childOf("c1", "L")
	c.WorktreePath, c.Branch = sp("/wt/c1"), sp("loom/agent/c1")
	s := markerService(t, ServiceConfig{Workspace: &headWorkspace{}}, dbPath(t), busy("L", "persistent", StateActive), c)
	endAttempt(t, s, "c1", "completed")
	if got := completions(t, s, "L"); len(got) != 1 || got[0].Branch != "loom/agent/c1" {
		t.Fatalf("records = %+v; want the marker's branch", got)
	}
}

// TestCompletionBackfillAfterReopen: a marker the upgrade saved is
// delivered after its child began its next attempt; the record quotes no
// reply of that later attempt.
func TestCompletionBackfillAfterReopen(t *testing.T) {
	ctx := context.Background()
	path := dbPath(t)
	s := markerService(t, ServiceConfig{}, path, busy("L", "persistent", StateActive), childOf("c1", "L"))
	finishTurn(t, s, "c1", "completed")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE agent_completion_markers SET summary = NULL, result = NULL`); err != nil { // as the upgrade saves it
		t.Fatal(err)
	}
	nextAttempt(t, s, "c1")
	if err := s.appendEvent(ctx, "c1", "item.completed", "item:new", map[string]string{"itemKind": "message", "text": "attempt 2 reply"}); err != nil {
		t.Fatal(err)
	}
	s.recordCompletions(ctx)
	if got := completions(t, s, "L"); len(got) != 1 || got[0].Attempt != 1 || got[0].Summary != "" || got[0].Result != "" {
		t.Fatalf("records = %+v; want attempt 1's with no later reply", got)
	}
}
