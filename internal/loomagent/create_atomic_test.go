package loomagent

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// failSaving makes every save of a kind event fail inside the transaction
// writing it, as a crash before COMMIT would; the returned func lifts it.
func failSaving(t *testing.T, e *createEnv, kind string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+e.path)
	if err != nil {
		t.Fatal(err)
	}
	trigger := `CREATE TRIGGER fail_saving BEFORE INSERT ON agent_events WHEN NEW.kind = '` + kind + //nolint:gosec // G202: DDL takes no parameters; kind is a test constant.
		`' BEGIN SELECT RAISE(ABORT, 'crash'); END`
	if _, err := db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	lift := func() {
		if _, err := db.Exec(`DROP TRIGGER IF EXISTS fail_saving`); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP TRIGGER IF EXISTS fail_saving`); db.Close() })
	return lift
}

// childReq is a delegated child of parent whose model ("other") is not in
// the catalog, so its Create also saves model.unverified.
func childReq(parent string) CreateRequest {
	return CreateRequest{Envelope: Envelope{RequestID: "c"}, Preset: "task", Name: "c", Parent: parent, Repo: "/repo",
		Overrides: Overrides{Harness: "opencode", Model: "other"}, FirstMessage: "do it"}
}

// childID is the one child of parent.
func childID(t *testing.T, e *createEnv, parent string) string {
	t.Helper()
	kids, _, err := e.st.ListAgents(context.Background(), loomstore.AgentFilter{Parent: parent, IncludeArchived: true})
	if err != nil || len(kids) != 1 {
		t.Fatalf("children of %s = %d, %v", parent, len(kids), err)
	}
	return kids[0].AgentID
}

// createAtomic fails unless child's Create is all or nothing: still creating
// below done with none of its created events, or past creating at done with
// each exactly once. It reports whether the Create is done.
func createAtomic(t *testing.T, e *createEnv, child, parent string) bool {
	t.Helper()
	row, err := e.st.GetAgent(context.Background(), child)
	if err != nil {
		t.Fatal(err)
	}
	n := []int{e.events(t, child, KindAgentCreated), e.events(t, child, KindModelUnverified),
		e.events(t, parent, KindChildCreated)}
	if row.State == StateCreating {
		if row.CreateStep >= stepDone || n[0]+n[1]+n[2] != 0 {
			t.Fatalf("creating row at step %d with created events %v", row.CreateStep, n)
		}
		return false
	}
	if row.CreateStep != stepDone || n[0] != 1 || n[1] != 1 || n[2] != 1 {
		t.Fatalf("%s row at step %d with created events %v; want done, one of each", row.State, row.CreateStep, n)
	}
	return true
}

// restart starts a new service on e, reconciles and runs its dispatcher
// until it has nothing left to do.
func restart(t *testing.T, e *createEnv) *Service {
	t.Helper()
	s := e.service(ServiceConfig{})
	reconcile(t, s)
	runDispatcher(t, s)
	settled(t, s)
	return s
}

// handedOnce fails unless child's first message was handed over in exactly one turn.
func handedOnce(t *testing.T, e *createEnv, child string) {
	t.Helper()
	row, _ := e.st.GetAgent(context.Background(), child)
	slots, _ := e.st.Slots(context.Background(), child)
	ref := loomharness.NativeRef{Root: *row.HarnessSessionRoot, NativeID: *row.HarnessSessionID}
	if len(slots) != 1 || slots[0].State == loomstore.SlotWaiting || turnsRun(e, ref) != 1 {
		t.Fatalf("first message: slots %+v, turns %d", slots, turnsRun(e, ref))
	}
}

// crashChildCreate runs a child Create of a new lead that crashes (panics) at
// createCrash point, or at "published" (after the commit, before its
// fanout), or, for "fail:<kind>", fails saving that kind's event; it returns
// the lead and the child.
func crashChildCreate(t *testing.T, e *createEnv, point string) (string, string) {
	t.Helper()
	ctx := context.Background()
	s := e.service(ServiceConfig{})
	lead, _ := newLead(t, e, s, "lead")
	if kind, ok := strings.CutPrefix(point, "fail:"); ok {
		lift := failSaving(t, e, kind)
		if _, err := s.Create(ctx, childReq(lead.AgentID)); err == nil {
			t.Fatal("Create did not fail")
		}
		lift()
		return lead.AgentID, childID(t, e, lead.AgentID)
	}
	run := crashAt(t, point)
	if point == "published" {
		commitStateCrash = func() { commitStateCrash = func() {}; panic(struct{}{}) }
		t.Cleanup(func() { commitStateCrash = func() {} })
	}
	crashed := func() (c bool) {
		defer func() {
			if r := recover(); r != nil {
				c = true
			}
		}()
		return run(func() { _, _ = s.Create(ctx, childReq(lead.AgentID)) })
	}()
	if !crashed {
		t.Fatalf("did not crash at %s", point)
	}
	return lead.AgentID, childID(t, e, lead.AgentID)
}

// TestCreateDoneCrashBeforeCommit: Create's final step fails before its
// commit; the row stays creating below done with no created events, and
// reconcile finishes it with exactly one of each.
func TestCreateDoneCrashBeforeCommit(t *testing.T) {
	e := newCreateEnv(t)
	lead, child := crashChildCreate(t, e, "fail:"+KindChildCreated)
	if createAtomic(t, e, child, lead) {
		t.Fatal("a failed final step left the Create done")
	}
	restart(t, e)
	if !createAtomic(t, e, child, lead) {
		t.Fatal("reconcile did not finish the Create")
	}
	handedOnce(t, e, child)
}

// TestCreateDoneCrashAfterCommitBeforeWake: a crash after the commit,
// before the first message's wake: the row is done with each created event
// once, and after a restart the first message is handed over once.
func TestCreateDoneCrashAfterCommitBeforeWake(t *testing.T) {
	for _, point := range []string{"published", "done"} {
		t.Run(point, func(t *testing.T) {
			e := newCreateEnv(t)
			lead, child := crashChildCreate(t, e, point)
			if !createAtomic(t, e, child, lead) {
				t.Fatal("a crash after the commit left the Create undone")
			}
			restart(t, e)
			createAtomic(t, e, child, lead)
			handedOnce(t, e, child)
		})
	}
}

// TestCreateNeverIdleBelowDone: a crash at every point of the final step
// never leaves a row past creating below done, and a restart converges.
func TestCreateNeverIdleBelowDone(t *testing.T) {
	for _, point := range []string{"created", "fail:" + EventStateChanged, "fail:" + KindAgentCreated,
		"fail:" + KindModelUnverified, "fail:" + KindChildCreated, "published", "done"} {
		t.Run(point, func(t *testing.T) {
			e := newCreateEnv(t)
			lead, child := crashChildCreate(t, e, point)
			createAtomic(t, e, child, lead)
			restart(t, e)
			if !createAtomic(t, e, child, lead) {
				t.Fatal("restart did not finish the Create")
			}
			handedOnce(t, e, child)
		})
	}
}

// TestCreateModelUnverifiedAtomic: model.unverified commits with
// agent.created, or neither exists.
func TestCreateModelUnverifiedAtomic(t *testing.T) {
	e := newCreateEnv(t)
	lead, child := crashChildCreate(t, e, "fail:"+KindModelUnverified)
	if e.events(t, child, KindAgentCreated) != 0 || createAtomic(t, e, child, lead) {
		t.Fatal("agent.created saved without model.unverified")
	}
	restart(t, e)
	createAtomic(t, e, child, lead)
}

// TestChildCreatedParentDeletedRace: the parent is deleted during the
// child's Create; the child commits and the deleted parent gets no event.
func TestChildCreatedParentDeletedRace(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := newLead(t, e, s, "lead")
	createCrash = func(p string) {
		if p == "created" {
			if err := e.st.Tombstone(ctx, lead.AgentID, time.Now()); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { createCrash = func(string) {} })
	if _, err := s.Create(ctx, childReq(lead.AgentID)); err != nil {
		t.Fatal(err)
	}
	child := childID(t, e, lead.AgentID)
	row := s.get(t, child)
	if row.State == StateCreating || row.CreateStep != stepDone || e.events(t, child, KindAgentCreated) != 1 {
		t.Fatalf("child = %s at step %d", row.State, row.CreateStep)
	}
	if n := e.events(t, lead.AgentID, KindChildCreated); n != 0 {
		t.Fatalf("deleted parent got %d child.created", n)
	}
}

// TestChildCreateParentBusyNoDeadlock: the parent holds its own lock for the
// whole of the child's Create, which still finishes.
func TestChildCreateParentBusyNoDeadlock(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := newLead(t, e, s, "lead")
	unlock := s.lock(lead.AgentID)
	defer unlock()
	if _, err := s.Create(ctx, childReq(lead.AgentID)); err != nil {
		t.Fatal(err)
	}
	if !createAtomic(t, e, childID(t, e, lead.AgentID), lead.AgentID) {
		t.Fatal("child Create not done")
	}
}
