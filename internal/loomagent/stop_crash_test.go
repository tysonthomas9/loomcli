package loomagent

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestStopCrashBeforeInterrupt: Loom crashes after Archive(cancelled) or
// Delete saved the agent stopping but before it interrupted the running
// turn. The restart interrupts the turn (a Delete purges its session) and
// finishes the archive or delete, on every harness.
func TestStopCrashBeforeInterrupt(t *testing.T) {
	for _, name := range Harnesses {
		for _, op := range []string{"archive", "delete"} {
			t.Run(name+"/"+op, func(t *testing.T) {
				ctx := context.Background()
				e := newCreateEnv(t)
				e.name = name
				fh := e.h.Harness.(*fake.Harness)
				req := leadReq("r1")
				req.Overrides.Harness = name
				s := e.service(ServiceConfig{Interrupt: func(context.Context, loomstore.Agent) error { panic("crash") }})
				info, err := s.Create(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				id, ref := info.AgentID, sessionOf(s.get(t, info.AgentID))
				fh.Script(id, fake.Turn{Steps: []fake.Step{{Ask: "t1"}}})
				mustSendMsg(t, s, sendReq(id, "u1", "go", user))
				if !panics(func() {
					if op == "archive" {
						_ = s.Archive(ctx, ArchiveRequest{AgentID: id, Reason: ArchiveCancelled})
					} else {
						_ = s.Delete(ctx, DeleteRequest{AgentID: id})
					}
				}) {
					t.Fatal("did not crash before the interrupt")
				}
				if row := s.get(t, id); row.State != StateStopping || row.RunningTurnID == nil {
					t.Fatalf("at the crash: %s, running %v; want stopping with the turn running", row.State, row.RunningTurnID)
				}
				s = e.service(ServiceConfig{}) // Loom restarts
				if err := s.Reconcile(ctx, name); err != nil {
					t.Fatal(err)
				}
				runDispatcher(t, s)
				settled(t, s)
				st, err := fh.Session(ref).Status(ctx)
				if gone := op == "delete" && errors.Is(err, loomharness.ErrSessionNotFound); !gone && (err != nil || st.Running) {
					t.Fatalf("after the restart the turn still runs (%v, %v); want it interrupted", st, err)
				}
				row, err := e.st.GetAgent(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if op == "delete" {
					deletedClean(t, e, id)
					return
				}
				if row.State != StateArchived || deref(row.ArchiveReason) != ArchiveCancelled || row.ArchivedAt == nil ||
					e.events(t, id, EventArchived) != 1 {
					t.Fatalf("after the restart: %s, reason %q, clock %v, %d archived; want archived as cancelled once",
						row.State, deref(row.ArchiveReason), row.ArchivedAt, e.events(t, id, EventArchived))
				}
			})
		}
	}
}
