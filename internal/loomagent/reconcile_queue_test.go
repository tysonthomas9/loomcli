package loomagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// OR4a: create, delete and purge markers are finished through one
// reconcile queue, retried with backoff until they succeed; only a
// permanent Create failure is terminal.

// onlyRow is e's one agent row.
func onlyRow(t *testing.T, e *createEnv) loomstore.Agent {
	t.Helper()
	rows, _, err := e.st.ListAgents(context.Background(), loomstore.AgentFilter{IncludeArchived: true})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d, %v; want one", len(rows), err)
	}
	return rows[0]
}

// execSQL runs q on e's store file, as a legacy writer would have.
func execSQL(t *testing.T, e *createEnv, q string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+e.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

// retrying fails unless a shows a non-terminal Attention while its Create is retried.
func retrying(t *testing.T, a loomstore.Agent, when string) {
	t.Helper()
	if r := deref(a.AttentionReason); r == "" || r == AttentionCreateIncomplete || a.CreateStep >= stepDone {
		t.Fatalf("%s: step %d Attention %q; want a non-terminal Attention while it retries", when, a.CreateStep, r)
	}
}

// finished fails unless id finished its Create once, idle or running its
// first turn, with no Attention left.
func finished(t *testing.T, e *createEnv, id string) loomstore.Agent {
	t.Helper()
	a, err := e.st.GetAgent(context.Background(), id)
	if err != nil || a.CreateStep != stepDone || a.State == StateCreating || a.AttentionReason != nil ||
		e.events(t, id, KindAgentCreated) != 1 {
		t.Fatalf("%s: %v state %s step %d Attention %q created %d; want one finished Create",
			id, err, a.State, a.CreateStep, deref(a.AttentionReason), e.events(t, id, KindAgentCreated))
	}
	return a
}

// backoff is the reconcile backoff after n failures: 100 ms doubling to 30 s.
func backoff(n int) []time.Duration {
	out, d := []time.Duration{}, 100*time.Millisecond
	for range n {
		out = append(out, d)
		d = min(2*d, 30*time.Second)
	}
	return out
}

// TestReconcileUnknownErrorKeepsRetrying: an unknown failure is never
// terminal. Ten failures, each retried after a backoff that doubles from
// 100 ms and caps at 30 s, then the Create finishes and clears its Attention.
func TestReconcileUnknownErrorKeepsRetrying(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	var fails atomic.Int32
	fails.Store(10)
	s := e.service(ServiceConfig{Launch: func(context.Context, loomstore.Agent, string) (loomharness.Launch, error) {
		if fails.Add(-1) >= 0 {
			return loomharness.Launch{}, errors.New("something unknown broke")
		}
		return loomharness.Launch{Root: "/root/opencode"}, nil
	}})
	c := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s) // its start-up sweep is done before the Create
	if _, err := s.Create(ctx, leadReq("r1")); err == nil {
		t.Fatal("Create succeeded")
	}
	for i := 1; fails.Load() >= 0; i++ {
		retrying(t, onlyRow(t, e), fmt.Sprintf("after %d failures", i))
		if n := c.fire(); n != 1 {
			t.Fatalf("after %d failures: %d retries pending; want 1", i, n)
		}
		settled(t, s)
	}
	finished(t, e, onlyRow(t, e).AgentID)
	if got := c.backoffs(); !slices.Equal(got, backoff(10)) {
		t.Fatalf("backoffs = %v; want %v", got, backoff(10))
	}
	if c.fire() != 0 {
		t.Fatal("a retry is still pending after the Create finished")
	}
}

// TestCreateTransientFailureNotTerminal: Create's own finishCreate fails
// with the harness unavailable. The row shows a non-terminal Attention, is
// queued for reconcile, and finishes once the harness is back.
func TestCreateTransientFailureNotTerminal(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	c := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s) // its start-up sweep is done before the Create
	fh.FailOpen(fmt.Errorf("opencode is down: %w", loomharness.ErrUnavailable), false)
	if _, err := s.Create(ctx, leadReq("r1")); err == nil {
		t.Fatal("Create succeeded with the harness unavailable")
	}
	retrying(t, onlyRow(t, e), "harness unavailable")
	fh.FailOpen(nil, false)
	if c.fire() != 1 {
		t.Fatal("the failed Create was not queued for reconcile")
	}
	settled(t, s)
	finished(t, e, onlyRow(t, e).AgentID)
}

// TestReconcileBaseRefDisappearsThenReturns: the base branch goes away
// after the row is written, so the worktree step fails; it is retried with
// backoff under a non-terminal Attention, and once the branch is back the
// next retry finishes the agent and clears the Attention.
func TestReconcileBaseRefDisappearsThenReturns(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	c := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s) // its start-up sweep is done before the Create
	e.ws.setEnsureErr(errors.New("fatal: invalid reference: main"))
	if _, err := s.Create(ctx, leadReq("r1")); err == nil {
		t.Fatal("Create succeeded without its base branch")
	}
	retrying(t, onlyRow(t, e), "base gone")
	if c.fire() != 1 {
		t.Fatal("no retry pending")
	}
	settled(t, s)
	retrying(t, onlyRow(t, e), "base still gone")
	e.ws.setEnsureErr(nil)
	if c.fire() != 1 {
		t.Fatal("no second retry pending")
	}
	settled(t, s)
	finished(t, e, onlyRow(t, e).AgentID)
	if got := c.backoffs(); !slices.Equal(got, backoff(2)) {
		t.Fatalf("backoffs = %v; want %v", got, backoff(2))
	}
}

// TestReconcileCreateCancelledAfterRow: the request is cancelled after the
// row is written. The row shows a non-terminal Attention (not
// create_incomplete), and reconcile finishes it, first message included.
func TestReconcileCreateCancelledAfterRow(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	c := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s) // its start-up sweep is done before the Create
	ctx, cancel := context.WithCancel(context.Background())
	createCrash = func(p string) {
		if p == "worktree" {
			cancel()
		}
	}
	t.Cleanup(func() { createCrash = func(string) {} })
	req := leadReq("r1")
	req.FirstMessage = "hello"
	if _, err := s.Create(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create = %v; want context.Canceled", err)
	}
	createCrash = func(string) {}
	retrying(t, onlyRow(t, e), "cancelled")
	if c.fire() != 1 {
		t.Fatal("the cancelled Create was not queued for reconcile")
	}
	settled(t, s)
	handedOnce(t, e, finished(t, e, onlyRow(t, e).AgentID).AgentID)
}

// TestReconcileQueueFirstFailure: the first message cannot be stored, so
// the row is not stored either; the retried Create makes the agent with the
// message queued once.
func TestReconcileQueueFirstFailure(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	req := leadReq("r1")
	req.FirstMessage = "hello"
	lift := failOn(t, e, `INSERT ON agent_send_receipts`)
	if _, err := s.Create(ctx, req); err == nil {
		t.Fatal("Create succeeded without its first message")
	}
	if rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true}); len(rows) != 0 {
		t.Fatalf("rows = %d, first at step %d; want none without the first message", len(rows), rows[0].CreateStep)
	}
	lift()
	a, err := s.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	handedOnce(t, e, finished(t, e, a.AgentID).AgentID)
}

// TestCreateInsertAndFirstMessageAtomic: a crash before the insert commits
// leaves no row and no slot message; one after it leaves the row at stepRow
// with the message queued once, which a restart hands over once.
func TestCreateInsertAndFirstMessageAtomic(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	req := leadReq("r1")
	req.FirstMessage = "hello"
	lift := failOn(t, e, `INSERT ON agent_send_receipts`) // the insert's transaction fails before its COMMIT
	_, _ = e.service(ServiceConfig{}).Create(ctx, req)
	lift()
	if rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true}); len(rows) != 0 {
		slots, _ := e.st.Slots(ctx, rows[0].AgentID)
		t.Fatalf("before commit: rows %d at step %d, slots %d; want none", len(rows), rows[0].CreateStep, len(slots))
	}
	run := crashAt(t, "worktree") // the first point after the commit
	if !run(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, req) }) {
		t.Fatal("did not crash")
	}
	row := onlyRow(t, e)
	slots, _ := e.st.Slots(ctx, row.AgentID)
	if row.CreateStep != stepRow || len(slots) != 1 || slots[0].State != loomstore.SlotWaiting {
		t.Fatalf("after commit: step %d slots %+v; want stepRow with the message waiting once", row.CreateStep, slots)
	}
	restart(t, e)
	handedOnce(t, e, finished(t, e, row.AgentID).AgentID)
}

// TestReconcileRestartAtEveryCreateStep: a serve that stopped at stepRow,
// stepWorktree or stepSession finishes the Create at its next start, with
// no request retry: the dispatcher's start reconciles every marker.
func TestReconcileRestartAtEveryCreateStep(t *testing.T) {
	for point, step := range map[string]int64{"worktree": stepRow, "open": stepWorktree, "created": stepSession} {
		t.Run(point, func(t *testing.T) {
			ctx := context.Background()
			e := newCreateEnv(t)
			req := leadReq("r1")
			req.FirstMessage = "hello"
			if !crashAt(t, point)(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, req) }) {
				t.Fatal("did not crash")
			}
			if got := onlyRow(t, e).CreateStep; got != step {
				t.Fatalf("crashed at step %d; want %d", got, step)
			}
			s := e.service(ServiceConfig{})
			runDispatcher(t, s)
			settled(t, s)
			a := finished(t, e, onlyRow(t, e).AgentID)
			handedOnce(t, e, a.AgentID)
			if len(e.h.specs) != 1 {
				t.Fatalf("opens = %d; want 1", len(e.h.specs))
			}
		})
	}
}

// TestReconcileDuringInsertSeesNothing: a reconcile while Create runs never
// sees the row below stepRow: before the insert commits there is no row,
// and after it the row is at stepRow, where reconcile, taking the agent
// lock first, finishes it. Create then finds it done; create_incomplete is
// never written.
func TestReconcileDuringInsertSeesNothing(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	hooked := false
	createCrash = func(p string) {
		rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
		switch p {
		case "row":
			if len(rows) != 0 {
				t.Errorf("before the insert committed a reader saw the row at step %d", rows[0].CreateStep)
			}
			reconcile(t, s)
		case "inserted":
			hooked = true
			if len(rows) != 1 || rows[0].CreateStep < stepRow {
				t.Errorf("after the insert: rows %+v; want one at stepRow", rows)
			}
			reconcile(t, s)
		}
	}
	t.Cleanup(func() { createCrash = func(string) {} })
	a, err := s.Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	if !hooked {
		t.Fatal("reconcile never ran between the insert and finishCreate")
	}
	finished(t, e, a.AgentID)
	if n := e.events(t, a.AgentID, EventAttentionRaised); n != 0 {
		t.Fatalf("Attention raised %d time(s) on a Create that only raced reconcile", n)
	}
}

// TestReconcileAndCreateRaceFinishOnce: in either lock order the steps run
// once and nothing deadlocks: reconcile first (between the insert and
// finishCreate), or Create first (reconcile waits on the lock Create holds).
func TestReconcileAndCreateRaceFinishOnce(t *testing.T) {
	for _, first := range []string{"reconcile", "create"} {
		t.Run(first, func(t *testing.T) {
			ctx := context.Background()
			e := newCreateEnv(t)
			s := e.service(ServiceConfig{})
			done, ran := make(chan error, 1), false
			createCrash = func(p string) {
				switch {
				case first == "reconcile" && p == "inserted":
					ran = true
					done <- s.Reconcile(ctx, "opencode")
				case first == "create" && p == "open" && !ran: // Create holds the agent lock
					ran = true
					go func() { done <- s.Reconcile(ctx, "opencode") }()
				}
			}
			t.Cleanup(func() { createCrash = func(string) {} })
			a, err := s.Create(ctx, leadReq("r1"))
			if err != nil {
				t.Fatal(err)
			}
			if !ran {
				t.Fatal("reconcile never raced the Create")
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			finished(t, e, a.AgentID)
			if len(e.h.specs) != 1 || len(e.ws.ensured) != 1 {
				t.Fatalf("opens %d ensures %d; want each once", len(e.h.specs), len(e.ws.ensured))
			}
		})
	}
}

// TestReconcileScansByCreateStep: a legacy row that is idle but below done
// is finished by reconcile, which scans create_step, not the creating state.
func TestReconcileScansByCreateStep(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	if !crashAt(t, "created")(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	id := onlyRow(t, e).AgentID
	execSQL(t, e, `UPDATE agents SET state = 'idle' WHERE agent_id = ?`, id)
	reconcile(t, e.service(ServiceConfig{}))
	finished(t, e, id)
}

// TestReconcileTerminalConfigUnloadable: a row whose stored config cannot
// load can never finish: reconcile shows one terminal create_incomplete and
// does not retry it, at start-up or on the resync clock.
func TestReconcileTerminalConfigUnloadable(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	if !crashAt(t, "worktree")(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	id := onlyRow(t, e).AgentID
	execSQL(t, e, `UPDATE agents SET spec_json = '{' WHERE agent_id = ?`, id)
	s := e.service(ServiceConfig{})
	c := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	c.tick(t) // the resync clock
	settled(t, s)
	a := onlyRow(t, e)
	if deref(a.AttentionReason) != AttentionCreateIncomplete || e.events(t, id, EventAttentionRaised) != 1 {
		t.Fatalf("Attention %q raised %d time(s); want one create_incomplete", deref(a.AttentionReason),
			e.events(t, id, EventAttentionRaised))
	}
	if got := c.backoffs(); len(got) != 0 || c.fire() != 0 {
		t.Fatalf("backoffs = %v; a permanent failure was retried", got)
	}
}

// TestCreateHarnessBadRequestTerminal: the harness refusing the session as
// a bad request is permanent. A Create stopped before Open is reconciled at
// the next start; Open's bad request shows create_incomplete once, with no
// retry.
func TestCreateHarnessBadRequestTerminal(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	if !crashAt(t, "open")(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	e.h.Harness.(*fake.Harness).FailOpen(fmt.Errorf("opencode: unknown preset: %w", loomharness.ErrBadRequest), false)
	s := e.service(ServiceConfig{})
	c := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	c.tick(t) // the resync clock
	settled(t, s)
	a := onlyRow(t, e)
	if deref(a.AttentionReason) != AttentionCreateIncomplete || e.events(t, a.AgentID, EventAttentionRaised) != 1 {
		t.Fatalf("Attention %q raised %d time(s); want one create_incomplete", deref(a.AttentionReason),
			e.events(t, a.AgentID, EventAttentionRaised))
	}
	if got := c.backoffs(); len(got) != 0 || len(e.h.specs) != 1 {
		t.Fatalf("backoffs = %v, opens %d; a bad request was retried", got, len(e.h.specs))
	}
}

// TestReconcilePurgeRetriesWithBackoff: a leftover session whose purge
// fails stays purge-pending and is retried with backoff until it goes.
func TestReconcilePurgeRetriesWithBackoff(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateIdle)
	c := useTestClock(e.s)
	runDispatcher(t, e.s)
	settled(t, e.s)
	e.fb.FailOpen(errors.New("open failed after creating"), true)
	e.fb.FailPurge(errors.New("purge down"))
	if _, err := e.s.Update(ctx, switchReq("r1", 1, "fb")); err == nil {
		t.Fatal("switch succeeded with a failing Open")
	}
	e.fb.FailOpen(nil, false)
	pending := func() []loomstore.NativeSession { p, _ := e.s.store.PurgePending(ctx, "ws"); return p }
	left := pending()
	if len(left) != 1 {
		t.Fatalf("purge-pending = %v; want the leftover", left)
	}
	if c.fire() != 1 {
		t.Fatal("the failed purge was not queued")
	}
	settled(t, e.s)
	if len(pending()) != 1 {
		t.Fatal("the purge went though it still fails")
	}
	e.fb.FailPurge(nil)
	if c.fire() != 1 {
		t.Fatal("no second retry pending")
	}
	settled(t, e.s)
	if p := pending(); len(p) != 0 || exists(e.fb, loomharness.NativeRef{Root: left[0].NativeRoot, NativeID: left[0].NativeID}) {
		t.Fatalf("purge-pending = %v after the purge recovered", p)
	}
	if got := c.backoffs(); !slices.Equal(got, backoff(2)) {
		t.Fatalf("backoffs = %v; want %v", got, backoff(2))
	}
}

// TestDeleteRetireFailureRetried: Retire fails after Delete purged the
// agent. Retire runs before the tombstone, so the delete marker stays and
// the next start re-runs Retire, then tombstones the agent.
func TestDeleteRetireFailureRetried(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	var retired atomic.Int32
	cfg := ServiceConfig{Retire: func(context.Context, loomstore.Agent) error {
		if retired.Add(1) == 1 {
			return errors.New("bridge removal failed")
		}
		return nil
	}}
	a, err := e.service(cfg).Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.service(cfg).Delete(ctx, DeleteRequest{AgentID: a.AgentID}); err == nil {
		t.Fatal("Delete succeeded although Retire failed")
	}
	s := e.service(cfg) // restart
	runDispatcher(t, s)
	settled(t, s)
	row, _ := e.st.GetAgent(ctx, a.AgentID)
	if retired.Load() != 2 || row.DeletedAt == nil {
		t.Fatalf("Retire ran %d time(s), deleted %v; want it re-run, then the tombstone", retired.Load(), row.DeletedAt != nil)
	}
}

// TestUnarchiveOneTransaction: Unarchive's clock reset and its state change
// are one write. A failed state save leaves the agent archived with its R29
// clock; the retry unarchives it.
func TestUnarchiveOneTransaction(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	a, err := s.Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Archive(ctx, ArchiveRequest{AgentID: a.AgentID}); err != nil {
		t.Fatal(err)
	}
	lift := failSaving(t, e, EventStateChanged)
	if err := s.Unarchive(ctx, ArchiveRequest{AgentID: a.AgentID}); err == nil {
		t.Fatal("Unarchive succeeded although its state change failed")
	}
	lift()
	if row, _ := e.st.GetAgent(ctx, a.AgentID); row.State != StateArchived || row.ArchivedAt == nil || row.ArchiveReason == nil {
		t.Fatalf("after a failed Unarchive: state %s archived_at %v reason %v; want archived with its clock",
			row.State, row.ArchivedAt, row.ArchiveReason)
	}
	if err := s.Unarchive(ctx, ArchiveRequest{AgentID: a.AgentID}); err != nil {
		t.Fatal(err)
	}
	if row, _ := e.st.GetAgent(ctx, a.AgentID); row.State != StateIdle || row.ArchivedAt != nil || row.ArchiveReason != nil {
		t.Fatalf("after Unarchive: state %s archived_at %v", row.State, row.ArchivedAt)
	}
}

// TestReconcileLegacyCreateIncompleteRetriedAtStart: an earlier Loom
// marked any failed Create create_incomplete. The upgrade turns that mark,
// on a row below done, into create_retrying, so reconcile retries it and
// finishes it.
func TestReconcileLegacyCreateIncompleteRetriedAtStart(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	if !crashAt(t, "open")(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	id := onlyRow(t, e).AgentID
	execSQL(t, e, `UPDATE agents SET attention_reason = 'create_incomplete' WHERE agent_id = ?`, id)
	var v int
	db, err := sql.Open("sqlite", "file:"+e.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	db.Close()
	execSQL(t, e, fmt.Sprintf("PRAGMA user_version = %d", min(v, 12)-1)) // the release before OR4a
	st, err := loomstore.Open(ctx, e.path)                               // the upgrade
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.st = st
	s := e.service(ServiceConfig{})
	runDispatcher(t, s)
	settled(t, s)
	finished(t, e, id)
}

// TestReconcileBelowRowTerminal: a row an earlier Loom left below stepRow
// (its first message never stored) only a retried Create request can
// finish, so reconcile shows create_incomplete and does not retry it.
func TestReconcileBelowRowTerminal(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	if !crashAt(t, "worktree")(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	id := onlyRow(t, e).AgentID
	execSQL(t, e, `UPDATE agents SET create_step = 0 WHERE agent_id = ?`, id)
	s := e.service(ServiceConfig{})
	c := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	c.tick(t)
	settled(t, s)
	if a := onlyRow(t, e); deref(a.AttentionReason) != AttentionCreateIncomplete {
		t.Fatalf("Attention = %q; want create_incomplete", deref(a.AttentionReason))
	}
	if got := c.backoffs(); len(got) != 0 {
		t.Fatalf("backoffs = %v; a row only its Create request can finish was retried", got)
	}
	if _, err := s.Create(ctx, leadReq("r1")); err != nil { // the retried request finishes it
		t.Fatal(err)
	}
	finished(t, e, id)
}

// badModel is a harness whose sessions refuse SetModel as a bad request.
type badModel struct{ loomharness.Harness }

func (b badModel) Session(ref loomharness.NativeRef) loomharness.Session {
	return badModelSession{b.Harness.Session(ref)}
}

type badModelSession struct{ loomharness.Session }

func (badModelSession) SetModel(context.Context, string, []loomharness.Option) error {
	return fmt.Errorf("opencode: unknown variant: %w", loomharness.ErrBadRequest)
}

// TestCreateSetModelBadRequestTerminal: the harness refusing the session's
// model options as a bad request is permanent too: create_incomplete, no retry.
func TestCreateSetModelBadRequestTerminal(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	e.h.Harness = badModel{e.h.Harness}
	s := e.service(ServiceConfig{})
	c := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	req := leadReq("r1")
	req.Overrides.Model, req.Overrides.Effort = "fake-model", "high"
	if _, err := s.Create(ctx, req); err == nil {
		t.Fatal("Create succeeded")
	}
	settled(t, s)
	if a := onlyRow(t, e); deref(a.AttentionReason) != AttentionCreateIncomplete {
		t.Fatalf("Attention = %q; want create_incomplete", deref(a.AttentionReason))
	}
	if got := c.backoffs(); len(got) != 0 {
		t.Fatalf("backoffs = %v; a bad request was retried", got)
	}
}

// TestReconcileDeleteUnsavedWorkNotRetried: a Delete the user confirmed
// over unsaved work fails after its mark. A retry without the user's
// fingerprint cannot pass the unsaved-work check, so it shows
// delete_incomplete and is not retried; the user's repeated Delete finishes it.
func TestReconcileDeleteUnsavedWorkNotRetried(t *testing.T) {
	t.Run("created", func(t *testing.T) { deleteUnsavedNotRetried(t, false) })
	t.Run("creating", func(t *testing.T) { deleteUnsavedNotRetried(t, true) }) // its Create is below done too
}

func deleteUnsavedNotRetried(t *testing.T, creating bool) {
	ctx := context.Background()
	e := newCreateEnv(t)
	dws := &countStatus{deleteWorkspace: &deleteWorkspace{Workspace: e.ws,
		status: WorkspaceStatus{Uncommitted: []string{"a.go"}, Fingerprint: "f1"}}}
	var purges atomic.Int32
	cfg := ServiceConfig{Purge: func(context.Context, loomstore.Agent, []loomstore.NativeSession) error {
		if purges.Add(1) == 1 {
			return errors.New("purge down")
		}
		return nil
	}}
	var a AgentInfo
	if creating {
		if !crashAt(t, "open")(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
			t.Fatal("did not crash")
		}
		a.AgentID = onlyRow(t, e).AgentID
	} else {
		var err error
		if a, err = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")); err != nil {
			t.Fatal(err)
		}
	}
	s := e.service(cfg)
	s.workspace = dws // its working copy has uncommitted work
	c := useTestClock(s)
	fp := wantCode(t, s.Delete(ctx, DeleteRequest{AgentID: a.AgentID}), CodeUnsavedWork).Fingerprint
	if err := s.Delete(ctx, DeleteRequest{AgentID: a.AgentID, Fingerprint: fp}); err == nil {
		t.Fatal("Delete succeeded although the purge failed")
	}
	runDispatcher(t, s) // after the Delete, so a creating agent is still below done
	settled(t, s)
	for range 3 {
		c.fire()
		settled(t, s)
		c.tick(t)
		settled(t, s)
	}
	if row, _ := e.st.GetAgent(ctx, a.AgentID); deref(row.AttentionReason) != AttentionDeleteIncomplete || row.DeletedAt != nil {
		t.Fatalf("Attention %q deleted %v; want delete_incomplete, not deleted", deref(row.AttentionReason), row.DeletedAt != nil)
	}
	if got := c.backoffs(); len(got) > 1 || dws.n.Load() != 3 { // the user's two Deletes and one retry
		t.Fatalf("backoffs = %v, unsaved-work checks %d; a Delete that needs the user's fingerprint kept retrying",
			got, dws.n.Load())
	}
	if err := s.Delete(ctx, DeleteRequest{AgentID: a.AgentID, Fingerprint: fp}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, a.AgentID); err != nil || got.State != StateDeleted || got.AttentionReason != nil {
		t.Fatalf("Get = %s, Attention %q, %v; want deleted with no Attention", got.State, deref(got.AttentionReason), err)
	}
}

// countStatus counts the unsaved-work checks of a deleteWorkspace.
type countStatus struct {
	*deleteWorkspace
	n atomic.Int32
}

func (c *countStatus) Status(ctx context.Context, s WorkspaceSpec) (WorkspaceStatus, error) {
	c.n.Add(1)
	return c.deleteWorkspace.Status(ctx, s)
}

// TestReconcileDueCancelledLeavesNothingRunning: a reconcile pass whose
// context ends leaves no queued agent marked running, so the next pass
// (a restarted dispatcher) still runs it.
func TestReconcileDueCancelledLeavesNothingRunning(t *testing.T) {
	s := newService(t, ServiceConfig{})
	s.enqueue("a1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.reconcileDue(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if q := s.queue["a1"]; q == nil || q.running {
		t.Fatalf("queue entry = %+v; want a1 still queued, not running", q)
	}
}

// TestCreateStopsOnceDeleteRequested: a Delete that runs between the
// insert and finishCreate wins. The Create opens nothing for the deleted
// agent, and its failure shows no Create Attention over the Delete's.
func TestCreateStopsOnceDeleteRequested(t *testing.T) {
	for _, purgeFails := range []bool{false, true} {
		t.Run(fmt.Sprint("purgeFails=", purgeFails), func(t *testing.T) {
			ctx := context.Background()
			e := newCreateEnv(t)
			s := e.service(ServiceConfig{Purge: func(context.Context, loomstore.Agent, []loomstore.NativeSession) error {
				if purgeFails {
					return errors.New("purge down")
				}
				return nil
			}})
			createCrash = func(p string) {
				if p == "inserted" {
					_ = s.Delete(ctx, DeleteRequest{AgentID: onlyRow(t, e).AgentID})
				}
			}
			t.Cleanup(func() { createCrash = func(string) {} })
			if _, err := s.Create(ctx, leadReq("r1")); err == nil {
				t.Fatal("Create succeeded for an agent deleted meanwhile")
			}
			row, _ := e.st.GetAgent(ctx, onlyRowAny(t, e).AgentID)
			if len(e.h.specs) != 0 || len(e.ws.ensured) != 0 || createReason(deref(row.AttentionReason)) {
				t.Fatalf("opens %d ensures %d Attention %q; want nothing made, no Create Attention",
					len(e.h.specs), len(e.ws.ensured), deref(row.AttentionReason))
			}
		})
	}
}

// onlyRowAny is e's one agent row, deleted or not.
func onlyRowAny(t *testing.T, e *createEnv) loomstore.Agent {
	t.Helper()
	rows, _, err := e.st.ListAgents(context.Background(), loomstore.AgentFilter{IncludeArchived: true, IncludeDeleted: true})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %d, %v; want one", len(rows), err)
	}
	return rows[0]
}

// TestCreateAfterReconcileAndDeleteFails: reconcile finishes the Create
// between its insert and finishCreate, then a Delete wins; the Create
// reports the agent gone, not created.
func TestCreateAfterReconcileAndDeleteFails(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	createCrash = func(p string) {
		if p == "inserted" {
			reconcile(t, s)
			if err := s.Delete(ctx, DeleteRequest{AgentID: onlyRow(t, e).AgentID}); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { createCrash = func(string) {} })
	if _, err := s.Create(ctx, leadReq("r1")); !isCode(err, CodeAgentNotFound) {
		t.Fatalf("Create = %v; want agent_not_found for the agent deleted meanwhile", err)
	}
}

// TestDeleteCancelledAfterMarkQueued: a Delete whose request is cancelled
// after its mark is still queued for its retry.
func TestDeleteCancelledAfterMarkQueued(t *testing.T) {
	e := newCreateEnv(t)
	a, err := e.service(ServiceConfig{}).Create(context.Background(), leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := e.service(ServiceConfig{Purge: func(context.Context, loomstore.Agent, []loomstore.NativeSession) error {
		cancel()
		return context.Canceled
	}})
	c := useTestClock(s)
	if err := s.Delete(ctx, DeleteRequest{AgentID: a.AgentID}); err == nil {
		t.Fatal("Delete succeeded")
	}
	if c.fire() != 1 {
		t.Fatal("the cancelled Delete was not queued for its retry")
	}
}

// TestBadRequestLeftoverUnrecordedRetried: Open refuses as a bad request
// but leaves a session, and recording it purge-pending fails. Nothing
// would find that session, so the failure is not terminal: the retry
// records it (the fake's re-Open by key hands the same session back, so
// the Create adopts it as its working one).
func TestBadRequestLeftoverUnrecordedRetried(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	c := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	fh.FailOpen(fmt.Errorf("opencode: unknown preset: %w", loomharness.ErrBadRequest), true)
	lift := failOn(t, e, `INSERT ON native_purge_pending`)
	if _, err := s.Create(ctx, leadReq("r1")); err == nil {
		t.Fatal("Create succeeded")
	}
	lift()
	if c.fire() != 1 {
		t.Fatal("the unrecorded leftover's Create was not queued for a retry")
	}
	settled(t, s)
	a := onlyRow(t, e)
	if owned, _ := e.st.NativeSessions(ctx, a.AgentID); len(owned) != 1 {
		t.Fatalf("owned = %v; want the leftover recorded", owned)
	}
}

// TestRefusedDeleteReplacesCreateAttention: a partly created agent shows
// create_retrying when a Delete the user confirmed over unsaved work is
// marked. The reconcile retry, refused for lack of the fingerprint, shows
// delete_incomplete in its place and stops; the resync clock does not
// retry it.
func TestRefusedDeleteReplacesCreateAttention(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	if !crashAt(t, "open")(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	id := onlyRow(t, e).AgentID
	execSQL(t, e, `UPDATE agents SET attention_reason = 'create_retrying', delete_requested = 1 WHERE agent_id = ?`, id)
	dws := &countStatus{deleteWorkspace: &deleteWorkspace{Workspace: e.ws,
		status: WorkspaceStatus{Uncommitted: []string{"a.go"}, Fingerprint: "f1"}}}
	s := e.service(ServiceConfig{})
	s.workspace = dws
	c := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	for range 3 {
		c.tick(t)
		settled(t, s)
	}
	if a := onlyRow(t, e); deref(a.AttentionReason) != AttentionDeleteIncomplete || dws.n.Load() != 1 {
		t.Fatalf("Attention %q, unsaved-work checks %d; want delete_incomplete after one refused retry",
			deref(a.AttentionReason), dws.n.Load())
	}
}

// TestCascadeChildFailureQueued: a cascading Delete whose child fails after
// its mark queues the child for its retry, which deletes it.
func TestCascadeChildFailureQueued(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	var fails atomic.Int32
	fails.Store(1)
	s := e.service(ServiceConfig{Purge: func(_ context.Context, a loomstore.Agent, _ []loomstore.NativeSession) error {
		if a.ParentAgentID != nil && fails.Add(-1) >= 0 {
			return errors.New("purge down")
		}
		return nil
	}})
	c := useTestClock(s)
	lead, _ := newLead(t, e, s, "lead")
	child, err := s.Create(ctx, childReq(lead.AgentID))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, DeleteRequest{AgentID: lead.AgentID, Cascade: true}); err == nil {
		t.Fatal("cascade Delete succeeded although the child's purge failed")
	}
	runDispatcher(t, s)
	if c.fire() == 0 {
		t.Fatal("the child that failed after its mark was not queued")
	}
	settled(t, s)
	if row, _ := e.st.GetAgent(ctx, child.AgentID); row.DeletedAt == nil {
		t.Fatal("the child's retry did not delete it")
	}
}

// TestReconcileCreateHungOpen (HANG1): the harness takes the session Open
// and never answers, as a frozen OpenCode did. The Create gives up within
// openWait and shows create_retrying, rather than sitting in creating with
// no Attention; reconcile finishes it once the harness answers, with one
// session and the first message handed over once. Same on every harness.
func TestReconcileCreateHungOpen(t *testing.T) {
	defer func(d time.Duration) { openWait = d }(openWait)
	openWait = 50 * time.Millisecond
	for _, harness := range []string{"opencode", "codex", "claude"} {
		t.Run(harness, func(t *testing.T) {
			e := newCreateEnv(t)
			e.name = harness
			s := e.service(ServiceConfig{})
			c := useTestClock(s)
			runDispatcher(t, s)
			settled(t, s)
			ctx, cancel := context.WithCancel(context.Background()) // ends a Create still hung at the test's end
			t.Cleanup(cancel)
			e.h.hang.Store(true)
			req := leadReq("r1")
			req.Overrides.Harness, req.FirstMessage = harness, "hello"
			done := make(chan error, 1)
			go func() { _, err := s.Create(ctx, req); done <- err }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "did not open") {
					t.Fatalf("Create = %v; want the hung Open given up", err)
				}
			case <-time.After(drainGuard):
				t.Fatal("Create still hung in the harness Open")
			}
			retrying(t, onlyRow(t, e), "hung open")
			if r := deref(onlyRow(t, e).AttentionReason); r != AttentionCreateRetrying {
				t.Fatalf("Attention = %q; want %s", r, AttentionCreateRetrying)
			}
			e.h.hang.Store(false)
			if c.fire() != 1 {
				t.Fatal("the hung Create was not queued for reconcile")
			}
			settled(t, s)
			a := finished(t, e, onlyRow(t, e).AgentID)
			handedOnce(t, e, a.AgentID)
			if n := len(e.h.specs); n != 2 || e.h.specs[0].Key != e.h.specs[1].Key {
				t.Fatalf("opens = %d; want the hung one and one retry, same key", n)
			}
		})
	}
}
