package loomagent

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// crashCommit makes the nth commit through the event lane from now on panic
// after its COMMIT, before its publication.
func crashCommit(t *testing.T, n int) {
	t.Helper()
	commitStateCrash = func() {
		if n--; n == 0 {
			commitStateCrash = func() {}
			panic(struct{}{})
		}
	}
	t.Cleanup(func() { commitStateCrash = func() {} })
}

// panics reports whether f panicked.
func panics(f func()) (p bool) {
	defer func() { p = recover() != nil }()
	f()
	return false
}

// history is the event IDs id has saved.
func history(t *testing.T, e *createEnv, id string) []string {
	t.Helper()
	page, err := e.st.ListEvents(context.Background(), loomstore.EventQuery{AgentID: id})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ev := range page.Events {
		out = append(out, ev.EventID)
	}
	return out
}

// archivedOnce fails unless id is archived with its R29 clock started and
// exactly one agent.archived, named by the revision that archived it, and
// one settled event.
func archivedOnce(t *testing.T, e *createEnv, id string) {
	t.Helper()
	row, err := e.st.GetAgent(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	ids := history(t, e, id)
	want := fmt.Sprintf("%s:%d:%s", id, row.Revision, EventArchived)
	if row.State != StateArchived || row.ArchivedAt == nil || deref(row.ArchiveReason) != ArchiveDone ||
		e.events(t, id, EventArchived) != 1 || !slices.Contains(ids, want) || e.events(t, id, EventSettled) != 1 {
		t.Fatalf("%s at %s, clock %v, reason %q, events %v; want archived, clocked, one %s", id, row.State,
			row.ArchivedAt, deref(row.ArchiveReason), ids, want)
	}
}

// TestArchiveCrashBeforeCommit: the move to archived fails before its
// commit; the row stays stopping with no clock and no agent.archived, and
// after a restart it is archived with each event once.
func TestArchiveCrashBeforeCommit(t *testing.T) {
	ctx, e := context.Background(), newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := newLead(t, e, s, "lead")
	lift := failSaving(t, e, EventArchived)
	if err := s.Archive(ctx, ArchiveRequest{AgentID: lead.AgentID}); err == nil {
		t.Fatal("Archive did not fail")
	}
	row := s.get(t, lead.AgentID)
	if row.State != StateStopping || row.ArchivedAt != nil || deref(row.ArchiveReason) != ArchiveDone ||
		e.events(t, lead.AgentID, EventArchived) != 0 {
		t.Fatalf("after the failed commit: %s, clock %v, reason %q, %d archived events", row.State, row.ArchivedAt,
			deref(row.ArchiveReason), e.events(t, lead.AgentID, EventArchived))
	}
	lift()
	restart(t, e)
	archivedOnce(t, e, lead.AgentID)
}

// TestArchiveCrashAfterCommit: Archive crashes after the archived commit,
// before publishing; the row, clock and agent.archived are all saved, and a
// repeat after a restart adds nothing.
func TestArchiveCrashAfterCommit(t *testing.T) {
	ctx, e := context.Background(), newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := newLead(t, e, s, "lead")
	crashCommit(t, 2) // the move to stopping, then the move to archived
	if !panics(func() { _ = s.Archive(ctx, ArchiveRequest{AgentID: lead.AgentID}) }) {
		t.Fatal("Archive did not crash")
	}
	archivedOnce(t, e, lead.AgentID)
	before := history(t, e, lead.AgentID)
	s = restart(t, e)
	if err := s.Archive(ctx, ArchiveRequest{AgentID: lead.AgentID}); err != nil {
		t.Fatal(err)
	}
	archivedOnce(t, e, lead.AgentID)
	if after := history(t, e, lead.AgentID); !slices.Equal(after, before) {
		t.Fatalf("history after the repeat %v, want %v", after, before)
	}
}

// TestArchiveRepeatedNoDuplicateEvents: repeating Archive on an archived
// agent saves no event and keeps the clock.
func TestArchiveRepeatedNoDuplicateEvents(t *testing.T) {
	ctx, e := context.Background(), newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := newLead(t, e, s, "lead")
	if err := s.Archive(ctx, ArchiveRequest{AgentID: lead.AgentID}); err != nil {
		t.Fatal(err)
	}
	archivedOnce(t, e, lead.AgentID)
	first, clock := history(t, e, lead.AgentID), *s.get(t, lead.AgentID).ArchivedAt
	for range 2 {
		if err := s.Archive(ctx, ArchiveRequest{AgentID: lead.AgentID}); err != nil {
			t.Fatal(err)
		}
	}
	archivedOnce(t, e, lead.AgentID)
	if again := history(t, e, lead.AgentID); !slices.Equal(again, first) || *s.get(t, lead.AgentID).ArchivedAt != clock {
		t.Fatalf("repeat changed history %v -> %v or clock %s", first, again, clock)
	}
}

// deletedClean fails unless id is tombstoned with its history purged and no
// event saved.
func deletedClean(t *testing.T, e *createEnv, id string) {
	t.Helper()
	row, err := e.st.GetAgent(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if row.DeletedAt == nil || row.HistoryPurgedAt == nil || len(history(t, e, id)) != 0 {
		t.Fatalf("deleted %v purged %v history %v", row.DeletedAt, row.HistoryPurgedAt, history(t, e, id))
	}
}

// TestDeleteCrashBeforeCommit: the purge fails inside Delete's tombstone
// transaction; the row is not deleted and keeps its history, and the restart
// finishes the Delete with nothing left.
func TestDeleteCrashBeforeCommit(t *testing.T) {
	ctx, e := context.Background(), newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := newLead(t, e, s, "lead")
	lift := failOn(t, e, `DELETE ON agent_events`)
	if err := s.Delete(ctx, DeleteRequest{AgentID: lead.AgentID}); err == nil {
		t.Fatal("Delete did not fail")
	}
	row := s.get(t, lead.AgentID)
	if row.DeletedAt != nil || row.HistoryPurgedAt != nil || !row.DeleteRequested ||
		e.events(t, lead.AgentID, KindAgentCreated) != 1 {
		t.Fatalf("after the failed commit: deleted %v purged %v requested %v history %v", row.DeletedAt,
			row.HistoryPurgedAt, row.DeleteRequested, history(t, e, lead.AgentID))
	}
	lift()
	restart(t, e)
	deletedClean(t, e, lead.AgentID)
}

// TestDeleteCrashAfterCommit: Delete crashes after its tombstone commit,
// before publishing; the row is deleted with no history, and a restart and a
// repeat keep it so.
func TestDeleteCrashAfterCommit(t *testing.T) {
	ctx, e := context.Background(), newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := newLead(t, e, s, "lead")
	sub := s.Bus.Subscribe(lead.AgentID)
	crashCommit(t, 2) // the move to stopping, then the tombstone
	if !panics(func() { _ = s.Delete(ctx, DeleteRequest{AgentID: lead.AgentID}) }) {
		t.Fatal("Delete did not crash")
	}
	if evs := types(drain(sub)); slices.Contains(evs, EventDeleted) {
		t.Fatalf("published before the crash: %v", evs)
	}
	deletedClean(t, e, lead.AgentID)
	s = restart(t, e)
	if err := s.Delete(ctx, DeleteRequest{AgentID: lead.AgentID}); err != nil {
		t.Fatal(err)
	}
	deletedClean(t, e, lead.AgentID)
}

// TestArchiveCancelledStoppingKeepsReason: Archive as cancelled commits its
// reason with the move to stopping, so a failed move to archived leaves a
// stopping row that says why.
func TestArchiveCancelledStoppingKeepsReason(t *testing.T) {
	ctx, e := context.Background(), newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := newLead(t, e, s, "lead")
	failSaving(t, e, EventArchived)
	if err := s.Archive(ctx, ArchiveRequest{AgentID: lead.AgentID, Reason: ArchiveCancelled}); err == nil {
		t.Fatal("Archive did not fail")
	}
	if row := s.get(t, lead.AgentID); row.State != StateStopping || deref(row.ArchiveReason) != ArchiveCancelled {
		t.Fatalf("after the failed commit: %s, reason %q; want stopping, cancelled", row.State, deref(row.ArchiveReason))
	}
}

// TestDeleteCascadeOneLockAtATime: a cascading Delete never holds the
// parent's lock while it deletes a child.
func TestDeleteCascadeOneLockAtATime(t *testing.T) {
	child := svcAgent("c1", "single_task", StateFinished)
	child.ParentAgentID = sp("p1")
	var s *Service
	both := false
	s = newService(t, ServiceConfig{Purge: func(_ context.Context, a loomstore.Agent, _ []loomstore.NativeSession) error {
		if m := s.agentLock("p1"); a.AgentID == "c1" && !m.TryLock() {
			both = true
		} else if a.AgentID == "c1" {
			m.Unlock()
		}
		return nil
	}}, svcAgent("p1", "persistent", StateIdle), child)
	if err := s.Delete(context.Background(), DeleteRequest{AgentID: "p1", Cascade: true}); err != nil {
		t.Fatal(err)
	}
	if both || s.get(t, "c1").DeletedAt == nil || s.get(t, "p1").DeletedAt == nil {
		t.Fatalf("held both locks %v; c1 deleted %v, p1 deleted %v", both, s.get(t, "c1").DeletedAt, s.get(t, "p1").DeletedAt)
	}
}

// TestDeletePublishesLiveOnly: Delete publishes settled and agent.deleted
// after its commit, with fixed IDs and no saved row.
func TestDeletePublishesLiveOnly(t *testing.T) {
	ctx, e := context.Background(), newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := newLead(t, e, s, "lead")
	sub := s.Bus.Subscribe(lead.AgentID)
	if err := s.Delete(ctx, DeleteRequest{AgentID: lead.AgentID}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, ev := range drain(sub) {
		if ev.Type == EventDeleted || ev.Type == EventSettled {
			ids = append(ids, ev.EventID)
		}
	}
	want := []string{lead.AgentID + ":deleted:" + EventSettled, lead.AgentID + ":deleted:" + EventDeleted}
	if !slices.Equal(ids, want) {
		t.Fatalf("published %v, want %v", ids, want)
	}
	deletedClean(t, e, lead.AgentID)
}

// TestArchiveBusyStoppingRollsBack: Archive of an active agent records its
// reason only with the move to stopping; a failed move leaves neither.
func TestArchiveBusyStoppingRollsBack(t *testing.T) {
	ctx, e := context.Background(), newCreateEnv(t)
	if err := e.st.InsertAgent(ctx, svcAgent("a1", "persistent", StateActive)); err != nil {
		t.Fatal(err)
	}
	s := e.service(ServiceConfig{})
	failSaving(t, e, EventStateChanged)
	if err := s.Archive(ctx, ArchiveRequest{AgentID: "a1"}); err == nil {
		t.Fatal("Archive did not fail")
	}
	if row := s.get(t, "a1"); row.State != StateActive || row.ArchiveReason != nil || len(history(t, e, "a1")) != 0 {
		t.Fatalf("after the failed commit: %s, reason %q, history %v", row.State, deref(row.ArchiveReason), history(t, e, "a1"))
	}
}
