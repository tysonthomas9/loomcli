package loomagent

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// purgeRec records the refs each Purge is given.
type purgeRec struct {
	*fake.Harness
	purged []loomharness.NativeRef
}

func (p *purgeRec) Purge(ctx context.Context, owned []loomharness.NativeRef) error {
	p.purged = append(p.purged, owned...)
	return p.Harness.Purge(ctx, owned)
}

var day0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

const day = 24 * time.Hour

// expiring returns an interactive agent archived, or a background task
// finished, at day0.
func expiring(id string, background bool) loomstore.Agent {
	if background {
		a := svcAgent(id, "single_task", StateFinished)
		a.InteractionMode, a.FinishedAt = "background", sp(loomstore.Stamp(day0))
		return a
	}
	a := svcAgent(id, "persistent", StateArchived)
	a.ArchivedAt = sp(loomstore.Stamp(day0))
	return a
}

// own opens a fake session under root and records it as owned by agentID.
func own(t *testing.T, s *Service, h *fake.Harness, agentID, root string) loomharness.NativeRef {
	t.Helper()
	ctx := context.Background()
	ref, err := h.Open(ctx, loomharness.OpenSpec{Key: agentID + root, Launch: loomharness.Launch{Root: root}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.RecordNativeSession(ctx, loomstore.NativeSession{AgentID: agentID, Harness: "fake",
		NativeRoot: ref.Root, NativeID: ref.NativeID}); err != nil {
		t.Fatal(err)
	}
	return ref
}

// TestHistoryPurgeRetentionDays: at day 29 nothing is purged; at exactly day
// 30 and at day 31 the interactive archive and the background finish are
// purged with their owned native sessions and saved events.
func TestHistoryPurgeRetentionDays(t *testing.T) {
	ctx := context.Background()
	for _, d := range []time.Duration{29, 30, 31} {
		h := fake.New()
		s := newService(t, ServiceConfig{Harnesses: map[string]loomharness.Harness{"fake": h}},
			expiring("i1", false), expiring("b1", true))
		refs := []loomharness.NativeRef{own(t, s, h, "i1", "/r"), own(t, s, h, "b1", "/r")}
		if err := s.emit(ctx, Event{AgentID: "i1", Type: EventArchived, Time: day0}); err != nil {
			t.Fatal(err)
		}
		s.RetentionSweep(ctx, day0.Add(d*day))
		for i, id := range []string{"i1", "b1"} {
			a := s.get(t, id)
			if purged := a.HistoryPurgedAt != nil; purged != (d >= 30) || exists(h, refs[i]) == purged {
				t.Fatalf("day %d %s: purged %v, native left %v", d, id, purged, exists(h, refs[i]))
			}
		}
		page, err := s.store.ListEvents(ctx, loomstore.EventQuery{AgentID: "i1"})
		if err != nil || (len(page.Events) == 0) != (d >= 30) {
			t.Fatalf("day %d: %d events left, %v", d, len(page.Events), err)
		}
	}
}

// TestHistoryPurgeOnlyOwnedSessions: the sweep deletes exactly the due
// agent's recorded sessions; another agent's session on the same shared
// root and an unrecorded session there (a fork) survive.
func TestHistoryPurgeOnlyOwnedSessions(t *testing.T) {
	ctx := context.Background()
	h := fake.New()
	s := newService(t, ServiceConfig{Harnesses: map[string]loomharness.Harness{"fake": h}},
		expiring("i1", false), svcAgent("a2", "persistent", StateIdle))
	mine, theirs := own(t, s, h, "i1", "/shared"), own(t, s, h, "a2", "/shared")
	fork, err := h.Open(ctx, loomharness.OpenSpec{Key: "fork", Launch: loomharness.Launch{Root: "/shared"}})
	if err != nil {
		t.Fatal(err)
	}
	s.RetentionSweep(ctx, day0.Add(30*day))
	if exists(h, mine) || !exists(h, theirs) || !exists(h, fork) {
		t.Fatalf("mine left %v, theirs left %v, fork left %v", exists(h, mine), exists(h, theirs), exists(h, fork))
	}
	if s.get(t, "a2").HistoryPurgedAt != nil {
		t.Fatal("the other agent's history was purged")
	}
}

// TestHistoryPurgeRetriesFailedDelete: a failed native delete leaves the
// history unpurged (an incomplete expiry); after a restart the sweep
// retries it.
func TestHistoryPurgeRetriesFailedDelete(t *testing.T) {
	ctx := context.Background()
	h := fake.New()
	cfg := ServiceConfig{Harnesses: map[string]loomharness.Harness{"fake": h}}
	s := newService(t, cfg, expiring("i1", false))
	ref := own(t, s, h, "i1", "/r")
	h.FailPurge(errors.New("delete failed"))
	s.RetentionSweep(ctx, day0.Add(30*day))
	if a := s.get(t, "i1"); a.HistoryPurgedAt != nil || a.HistoryPurgeFailedAt == nil || !exists(h, ref) {
		t.Fatal("a failed native delete did not leave an incomplete expiry")
	}
	h.FailPurge(nil)
	cfg.Store, cfg.WorkspaceID = s.store, "ws"
	New(cfg).RetentionSweep(ctx, day0.Add(31*day)) // restart
	if a := s.get(t, "i1"); a.HistoryPurgedAt == nil || a.HistoryPurgeFailedAt != nil || exists(h, ref) {
		t.Fatal("the retry did not purge and clear the incomplete expiry")
	}
}

// TestHistoryPurgeUnknownOwnerBlocks: a recorded session on a harness that
// is not wired blocks every native delete and the purge mark.
func TestHistoryPurgeUnknownOwnerBlocks(t *testing.T) {
	ctx := context.Background()
	h := fake.New()
	s := newService(t, ServiceConfig{Harnesses: map[string]loomharness.Harness{"fake": h}}, expiring("i1", false))
	ref := own(t, s, h, "i1", "/r")
	if err := s.store.RecordNativeSession(ctx, loomstore.NativeSession{AgentID: "i1", Harness: "gone",
		NativeRoot: "/g", NativeID: "g1"}); err != nil {
		t.Fatal(err)
	}
	s.RetentionSweep(ctx, day0.Add(31*day))
	if s.get(t, "i1").HistoryPurgedAt != nil || !exists(h, ref) {
		t.Fatal("an unprovable session did not block the purge")
	}
}

// TestHistoryPurgeRacesUnarchiveAndSend: an agent listed as due that is
// unarchived, or sent a new background attempt, before the sweep takes its
// lock keeps its history and native session.
func TestHistoryPurgeRacesUnarchiveAndSend(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	daemon := ActorRef{Kind: "system", ID: "daemon"}
	lead, err := s.Create(ctx, leadReq("l1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Archive(ctx, ArchiveRequest{AgentID: lead.AgentID}); err != nil {
		t.Fatal(err)
	}
	task, err := s.Create(ctx, CreateRequest{Envelope: Envelope{RequestID: "t1"}, Preset: "daemon-worker", Name: "t1",
		Repo: "/repo", Overrides: Overrides{Harness: "opencode"}})
	if err != nil {
		t.Fatal(err)
	}
	mustSendMsg(t, s, sendReq(task.AgentID, "go", "fix it", daemon))
	a := s.get(t, task.AgentID)
	if err := s.HarnessEvent(ctx, a.AgentID, loomharness.Event{Type: loomharness.EventTurnCompleted,
		Session: loomharness.NativeRef{Root: *a.HarnessSessionRoot, NativeID: *a.HarnessSessionID},
		TurnID:  *a.RunningTurnID, StopReason: "failed"}); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(loomstore.HistoryRetention + time.Hour)
	if due, _ := s.store.RetentionDue(ctx, later); len(due) != 2 {
		t.Fatalf("due = %v", due)
	}
	if err := s.Unarchive(ctx, ArchiveRequest{AgentID: lead.AgentID}); err != nil {
		t.Fatal(err)
	}
	mustSendMsg(t, s, sendReq(task.AgentID, "again", "try again", daemon))
	fh := e.h.Harness.(*fake.Harness)
	for _, id := range []string{lead.AgentID, task.AgentID} {
		if err := s.expire(ctx, id, later); err != nil {
			t.Fatal(err)
		}
		a := s.get(t, id)
		if a.HistoryPurgedAt != nil || !exists(fh, loomharness.NativeRef{Root: *a.HarnessSessionRoot, NativeID: *a.HarnessSessionID}) {
			t.Fatalf("%s purged after it was reused", id)
		}
	}
}

// TestPurgeUsesRecordedNativeRoot: session N opened under the inherited
// root is purged from that root after the agent moves to a per-agent
// profile root; unrelated content under the new root survives.
func TestPurgeUsesRecordedNativeRoot(t *testing.T) {
	ctx := context.Background()
	rec := &purgeRec{Harness: fake.New()}
	s := newService(t, ServiceConfig{Harnesses: map[string]loomharness.Harness{"fake": rec}}, expiring("i1", false))
	n := own(t, s, rec.Harness, "i1", "/inherited")
	if err := s.store.SetCreateStep(ctx, "i1", 0, nil, sp("other"), sp("/profile/i1")); err != nil {
		t.Fatal(err) // the agent's current root is now its own profile
	}
	unrelated, err := rec.Open(ctx, loomharness.OpenSpec{Key: "unrelated", Launch: loomharness.Launch{Root: "/profile/i1"}})
	if err != nil {
		t.Fatal(err)
	}
	s.RetentionSweep(ctx, day0.Add(30*day))
	if !slices.Equal(rec.purged, []loomharness.NativeRef{n}) || exists(rec.Harness, n) || !exists(rec.Harness, unrelated) {
		t.Fatalf("purged %v; unrelated left %v", rec.purged, exists(rec.Harness, unrelated))
	}
}

// TestWorkspaceRetentionPort: the sweep removes a clean working copy through
// the Workspace port at day 30 and 31, never at day 29, and never a dirty or
// reused (unarchived) one; a dirty copy is kept, its history still purged,
// and removed by a later sweep once clean.
func TestWorkspaceRetentionPort(t *testing.T) {
	ctx := context.Background()
	withCopy := func(a loomstore.Agent) loomstore.Agent {
		a.WorktreePath, a.Branch, a.BaseRef = sp("/wt/"+a.AgentID), sp("loom/agent/"+a.AgentID), sp("main")
		return a
	}
	for _, d := range []time.Duration{29, 30, 31} {
		ws := &deleteWorkspace{}
		s := newService(t, ServiceConfig{Workspace: ws}, withCopy(expiring("i1", false)), withCopy(expiring("b1", true)),
			withCopy(expiring("reused", false)))
		if err := s.Unarchive(ctx, ArchiveRequest{AgentID: "reused"}); err != nil {
			t.Fatal(err)
		}
		s.RetentionSweep(ctx, day0.Add(d*day))
		var keys []string
		for _, r := range ws.removed {
			keys = append(keys, r.Key)
		}
		if want := []string{"b1", "i1"}; (d >= 30) != slices.Equal(keys, want) || (d < 30 && len(keys) > 0) {
			t.Fatalf("day %d removed %v", d, keys)
		}
	}
	ws := &deleteWorkspace{status: WorkspaceStatus{Uncommitted: []string{"x.go"}, Fingerprint: "f"}}
	s := newService(t, ServiceConfig{Workspace: ws}, withCopy(expiring("dirty", false)))
	s.RetentionSweep(ctx, day0.Add(31*day))
	if len(ws.removed) != 0 || s.get(t, "dirty").HistoryPurgedAt == nil {
		t.Fatalf("dirty copy removed %v; history purged %v", ws.removed, s.get(t, "dirty").HistoryPurgedAt)
	}
	ws.status = WorkspaceStatus{} // committed since: a later sweep removes it once
	for i := 0; i < 2; i++ {
		s.RetentionSweep(ctx, day0.Add(32*day))
	}
	if len(ws.removed) != 1 || s.get(t, "dirty").WorktreePath != nil {
		t.Fatalf("after cleaning: removed %v, worktree %v", ws.removed, s.get(t, "dirty").WorktreePath)
	}
}

// TestDeletePurgesHistoryImmediately: Delete purges the recorded native
// sessions and the Loom history at once; a failed native purge leaves the
// Delete pending with the history kept, and a retry finishes it.
func TestDeletePurgesHistoryImmediately(t *testing.T) {
	ctx := context.Background()
	h := fake.New()
	s := newService(t, ServiceConfig{Harnesses: map[string]loomharness.Harness{"fake": h}}, svcAgent("a1", "persistent", StateIdle))
	ref := own(t, s, h, "a1", "/r")
	if err := s.emit(ctx, Event{AgentID: "a1", Type: EventWaiting, Time: day0}); err != nil {
		t.Fatal(err)
	}
	h.FailPurge(errors.New("delete failed"))
	if err := s.Delete(ctx, DeleteRequest{AgentID: "a1"}); err == nil {
		t.Fatal("Delete succeeded with a failing native purge")
	}
	page, _ := s.store.ListEvents(ctx, loomstore.EventQuery{AgentID: "a1"})
	if a := s.get(t, "a1"); a.DeletedAt != nil || !a.DeleteRequested || a.HistoryPurgedAt != nil || len(page.Events) == 0 || !exists(h, ref) {
		t.Fatalf("after a failed purge: deleted %v requested %v purged %v events %d", a.DeletedAt, a.DeleteRequested, a.HistoryPurgedAt, len(page.Events))
	}
	h.FailPurge(nil)
	if err := s.Delete(ctx, DeleteRequest{AgentID: "a1"}); err != nil {
		t.Fatal(err)
	}
	page, _ = s.store.ListEvents(ctx, loomstore.EventQuery{AgentID: "a1"})
	if a := s.get(t, "a1"); a.DeletedAt == nil || a.HistoryPurgedAt == nil || len(page.Events) != 0 || exists(h, ref) {
		t.Fatalf("after Delete: deleted %v purged %v events %d", a.DeletedAt, a.HistoryPurgedAt, len(page.Events))
	}
}
