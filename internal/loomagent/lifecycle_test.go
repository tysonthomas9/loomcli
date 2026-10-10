package loomagent

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

func queue(t *testing.T, s *Service, agentID, sender, body string) {
	t.Helper()
	_, _, err := s.store.Send(context.Background(), loomstore.SlotSend{AgentID: agentID, Sender: sender,
		RequestID: sender + "-" + body, Body: body, Source: "user", Result: func(bool) (string, error) { return "{}", nil }})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGetReturnsWaitingMessagesNotSessionID(t *testing.T) {
	ctx := context.Background()
	a := svcAgent("a1", "persistent", StateActive)
	a.HarnessSessionID = sp("ses_secret")
	s := newService(t, ServiceConfig{}, a)
	queue(t, s, "a1", "user", "hello")
	got, err := s.Get(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if got.HarnessSessionID != nil || got.Compute != "local" || got.Host != "local" {
		t.Fatalf("Get = session %v compute %q host %q", got.HarnessSessionID, got.Compute, got.Host)
	}
	if len(got.WaitingMessages) != 1 || got.WaitingMessages[0].Sender != "user" ||
		got.WaitingMessages[0].Text != "hello" || got.WaitingMessages[0].Since == "" {
		t.Fatalf("waiting = %+v", got.WaitingMessages)
	}
	_, err = s.Get(ctx, "nope")
	wantCode(t, err, CodeAgentNotFound)
}

func TestListFiltersAndPages(t *testing.T) {
	ctx := context.Background()
	lead := svcAgent("a1", "persistent", StateIdle)
	child := svcAgent("a2", "single_task", StateActive)
	child.Preset, child.ExternalKey = "task", sp("task:7")
	child.ParentAgentID = sp("a1")
	arch := svcAgent("a3", "persistent", StateArchived)
	gone := svcAgent("a4", "persistent", StateIdle)
	s := newService(t, ServiceConfig{}, lead, child, arch, gone)
	if err := s.store.Tombstone(ctx, "a4", time.Now()); err != nil {
		t.Fatal(err)
	}
	ids := func(f loomstore.AgentFilter) []string {
		t.Helper()
		got, _, err := s.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, a := range got {
			out = append(out, a.AgentID)
		}
		return out
	}
	for name, c := range map[string]struct {
		f    loomstore.AgentFilter
		want []string
	}{
		"default":    {loomstore.AgentFilter{WorkspaceID: "ws"}, []string{"a1", "a2"}},
		"archived":   {loomstore.AgentFilter{IncludeArchived: true}, []string{"a1", "a2", "a3"}},
		"deleted":    {loomstore.AgentFilter{IncludeArchived: true, IncludeDeleted: true}, []string{"a1", "a2", "a3", "a4"}},
		"parent":     {loomstore.AgentFilter{Parent: "a1"}, []string{"a2"}},
		"preset":     {loomstore.AgentFilter{Preset: "task", Mode: "single_task"}, []string{"a2"}},
		"xkeyPrefix": {loomstore.AgentFilter{ExternalKeyPrefix: "task:"}, []string{"a2"}},
		"state":      {loomstore.AgentFilter{State: StateArchived}, []string{"a3"}},
		"name":       {loomstore.AgentFilter{Name: "a1"}, []string{"a1"}},
	} {
		if got := ids(c.f); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
	page, next, err := s.List(ctx, loomstore.AgentFilter{IncludeArchived: true, Limit: 2})
	if err != nil || len(page) != 2 || next != "a2" {
		t.Fatalf("page 1 = %d agents, next %q, %v", len(page), next, err)
	}
	page, next, err = s.List(ctx, loomstore.AgentFilter{IncludeArchived: true, Limit: 2, After: next})
	if err != nil || len(page) != 1 || page[0].AgentID != "a3" || next != "" {
		t.Fatalf("page 2 = %+v, next %q, %v", page, next, err)
	}
}

func TestArchiveCancelledInterruptsAndWithdraws(t *testing.T) {
	ctx := context.Background()
	a := svcAgent("a1", "single_task", StateActive)
	a.RunningTurnID = sp("t1")
	var interrupted []string
	s := newService(t, ServiceConfig{Interrupt: func(_ context.Context, a loomstore.Agent) error {
		interrupted = append(interrupted, deref(a.RunningTurnID))
		return nil
	}}, a)
	queue(t, s, "a1", "lead", "more")
	sub := s.Bus.Subscribe("a1")
	if err := s.Archive(ctx, ArchiveRequest{AgentID: "a1", Reason: ArchiveCancelled}); err != nil {
		t.Fatal(err)
	}
	got := s.get(t, "a1")
	if got.State != StateArchived || deref(got.Outcome) != ArchiveCancelled || got.ArchivedAt == nil ||
		deref(got.ArchiveReason) != ArchiveCancelled || !slices.Equal(interrupted, []string{"t1"}) {
		t.Fatalf("after cancel: state %s outcome %v archived %v interrupted %v", got.State, deref(got.Outcome), got.ArchivedAt, interrupted)
	}
	evs := types(drain(sub))
	for _, want := range []string{EventWithdrawn, EventArchived, EventSettled} {
		if !slices.Contains(evs, want) {
			t.Fatalf("events %v missing %s", evs, want)
		}
	}
	info, _ := s.Get(ctx, "a1")
	if len(info.WaitingMessages) != 0 {
		t.Fatalf("waiting after cancel = %+v", info.WaitingMessages)
	}
}

func TestArchiveDoneRulesAndRetry(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, svcAgent("task", "single_task", StateActive),
		svcAgent("busy", "persistent", StateActive), svcAgent("idle", "persistent", StateIdle))
	wantCode(t, s.Archive(ctx, ArchiveRequest{AgentID: "task"}), CodeAgentBusy)
	if err := s.Archive(ctx, ArchiveRequest{AgentID: "busy"}); err != nil {
		t.Fatal(err)
	}
	if b := s.get(t, "busy"); b.State != StateStopping || deref(b.ArchiveReason) != ArchiveDone || b.ArchivedAt != nil {
		t.Fatalf("busy = %s reason %v archived %v", b.State, deref(b.ArchiveReason), b.ArchivedAt)
	}
	if err := s.Archive(ctx, ArchiveRequest{AgentID: "idle"}); err != nil {
		t.Fatal(err)
	}
	first := s.get(t, "idle") // stamps are in ns: a retry that set the clock again would differ
	if err := s.Archive(ctx, ArchiveRequest{AgentID: "idle"}); err != nil {
		t.Fatal(err)
	}
	again := s.get(t, "idle")
	if first.State != StateArchived || first.ArchivedAt == nil || *again.ArchivedAt != *first.ArchivedAt {
		t.Fatalf("retry moved the clock: %v -> %v", first.ArchivedAt, again.ArchivedAt)
	}
}

func TestUnarchiveCancelsHistoryClock(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, svcAgent("a1", "persistent", StateIdle), svcAgent("t1", "single_task", StateFinished))
	for _, id := range []string{"a1", "t1"} {
		if err := s.Archive(ctx, ArchiveRequest{AgentID: id}); err != nil {
			t.Fatal(err)
		}
	}
	later := time.Now().Add(loomstore.HistoryRetention + time.Hour)
	if due, _ := s.store.RetentionDue(ctx, later); !slices.Equal(due, []string{"a1", "t1"}) {
		t.Fatalf("due = %v", due)
	}
	for i := 0; i < 2; i++ { // the retry is a no-op
		if err := s.Unarchive(ctx, ArchiveRequest{AgentID: "a1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Unarchive(ctx, ArchiveRequest{AgentID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if a, tk := s.get(t, "a1"), s.get(t, "t1"); a.State != StateIdle || a.ArchivedAt != nil || tk.State != StateFinished {
		t.Fatalf("a1 %s %v, t1 %s", a.State, a.ArchivedAt, tk.State)
	}
	if due, _ := s.store.RetentionDue(ctx, later); len(due) != 0 {
		t.Fatalf("still due after unarchive: %v", due)
	}
}

// deleteWorkspace is a Workspace port with a set Status that records Removes.
type deleteWorkspace struct {
	Workspace
	status  WorkspaceStatus
	removed []WorkspaceSpec
}

func (f *deleteWorkspace) Status(context.Context, WorkspaceSpec) (WorkspaceStatus, error) {
	return f.status, nil
}

func (f *deleteWorkspace) DropCheckpoints(context.Context, string, string) error { return nil }

func (f *deleteWorkspace) Remove(_ context.Context, s WorkspaceSpec) error {
	f.removed = append(f.removed, s)
	return nil
}

func TestDeleteWorkspacePortFingerprint(t *testing.T) {
	ctx := context.Background()
	a := svcAgent("a1", "persistent", StateIdle)
	a.WorktreePath, a.Branch, a.BaseRef = sp("/wt/a1"), sp("loom/agent/a1"), sp("main")
	ws := &deleteWorkspace{status: WorkspaceStatus{Uncommitted: []string{"x.go"}, Fingerprint: "f1"}}
	var purged []string
	s := newService(t, ServiceConfig{Workspace: ws, Purge: func(_ context.Context, _ loomstore.Agent, owned []loomstore.NativeSession) error {
		for _, n := range owned {
			purged = append(purged, n.NativeID)
		}
		return nil
	}}, a)
	if err := s.store.RecordNativeSession(ctx, loomstore.NativeSession{AgentID: "a1", Harness: "fake", NativeRoot: "/r", NativeID: "ses_1"}); err != nil {
		t.Fatal(err)
	}
	e := wantCode(t, s.Delete(ctx, DeleteRequest{AgentID: "a1"}), CodeUnsavedWork)
	if !slices.Equal(e.Paths, []string{"x.go"}) || e.Fingerprint != "f1" {
		t.Fatalf("unsaved_work = %+v", e)
	}
	ws.status = WorkspaceStatus{Uncommitted: []string{"x.go", "y.go"}, Fingerprint: "f2"} // edited since
	e = wantCode(t, s.Delete(ctx, DeleteRequest{AgentID: "a1", Fingerprint: "f1"}), CodeUnsavedWork)
	if e.Fingerprint != "f2" || len(ws.removed) != 0 || s.get(t, "a1").State != StateIdle {
		t.Fatalf("stale fingerprint went ahead: %+v removed %v", e, ws.removed)
	}
	sub := s.Bus.Subscribe("a1")
	for i := 0; i < 2; i++ { // the retry is a no-op
		if err := s.Delete(ctx, DeleteRequest{AgentID: "a1", Fingerprint: "f2"}); err != nil {
			t.Fatal(err)
		}
	}
	want := WorkspaceSpec{Key: "a1", Repo: "/repo", BaseRef: "main", Branch: "loom/agent/a1", Confirm: "f2"}
	if !slices.Equal(ws.removed, []WorkspaceSpec{want}) || !slices.Equal(purged, []string{"ses_1"}) {
		t.Fatalf("removed %+v purged %v", ws.removed, purged)
	}
	got := s.get(t, "a1")
	if got.DeletedAt == nil || !got.DeleteRequested || got.State != StateStopping {
		t.Fatalf("row = deleted %v requested %v state %s", got.DeletedAt, got.DeleteRequested, got.State)
	}
	if evs := types(drain(sub)); !slices.Contains(evs, EventDeleted) || !slices.Contains(evs, EventSettled) {
		t.Fatalf("events = %v", evs)
	}
}

func TestDeleteChildrenAndPurgeFailure(t *testing.T) {
	ctx := context.Background()
	child := svcAgent("c1", "single_task", StateActive)
	child.ParentAgentID = sp("p1")
	purgeErr := errors.New("purge failed")
	fail := true
	s := newService(t, ServiceConfig{Purge: func(context.Context, loomstore.Agent, []loomstore.NativeSession) error {
		if fail {
			return purgeErr
		}
		return nil
	}}, svcAgent("p1", "persistent", StateIdle), child)
	wantCode(t, s.Delete(ctx, DeleteRequest{AgentID: "p1"}), CodeChildrenLive)
	if err := s.Delete(ctx, DeleteRequest{AgentID: "p1", Cascade: true}); !errors.Is(err, purgeErr) {
		t.Fatalf("err = %v, want purge failure", err)
	}
	if c := s.get(t, "c1"); c.DeletedAt != nil || !c.DeleteRequested || c.State != StateStopping {
		t.Fatalf("child after failed purge: deleted %v requested %v state %s", c.DeletedAt, c.DeleteRequested, c.State)
	}
	fail = false
	if err := s.Delete(ctx, DeleteRequest{AgentID: "p1", Cascade: true}); err != nil {
		t.Fatal(err)
	}
	if p, c := s.get(t, "p1"), s.get(t, "c1"); p.DeletedAt == nil || c.DeletedAt == nil {
		t.Fatalf("not tombstoned: p1 %v c1 %v", p.DeletedAt, c.DeletedAt)
	}
}

// TestRetireRunsOnArchiveAndDelete: the Retire hook runs once an agent is
// archived (not while it is still stopping) and once it is deleted; a
// failed Retire fails the Archive, and repeating the Archive runs it again.
func TestRetireRunsOnArchiveAndDelete(t *testing.T) {
	ctx := context.Background()
	var retired []string
	fail := true
	s := newService(t, ServiceConfig{Workspace: &deleteWorkspace{}, Retire: func(_ context.Context, a loomstore.Agent) error {
		if a.AgentID == "flaky" && fail {
			fail = false
			return errors.New("boom")
		}
		retired = append(retired, a.AgentID)
		return nil
	}}, svcAgent("idle", "persistent", StateIdle), svcAgent("busy", "persistent", StateActive),
		svcAgent("flaky", "persistent", StateIdle), svcAgent("gone", "persistent", StateIdle))
	for _, id := range []string{"idle", "busy"} {
		if err := s.Archive(ctx, ArchiveRequest{AgentID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(retired, []string{"idle"}) {
		t.Fatalf("retired after archive = %v; want only the archived agent", retired)
	}
	if err := s.Archive(ctx, ArchiveRequest{AgentID: "flaky"}); err == nil {
		t.Fatal("Archive succeeded although Retire failed")
	}
	if err := s.Archive(ctx, ArchiveRequest{AgentID: "flaky"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Unarchive(ctx, ArchiveRequest{AgentID: "idle"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, DeleteRequest{AgentID: "gone"}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(retired, []string{"idle", "flaky", "gone"}) {
		t.Fatalf("retired = %v; want idle, flaky (on the retry), gone", retired)
	}
}
