package loomagent

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
)

// keyedOpens records the key of every Open.
type keyedOpens struct {
	*fake.Harness
	mu   sync.Mutex
	keys []string
}

func (h *keyedOpens) Open(ctx context.Context, spec loomharness.OpenSpec) (loomharness.NativeRef, error) {
	h.mu.Lock()
	h.keys = append(h.keys, spec.Key)
	h.mu.Unlock()
	return h.Harness.Open(ctx, spec)
}

// openCounted makes e's fb record its Opens.
func openCounted(e *specEnv) *keyedOpens {
	h := &keyedOpens{Harness: e.harnesses["fb"].(*fake.Harness)}
	e.harnesses["fb"] = h
	return h
}

// switchedOnce fails unless a1 switched to fb under r1 once: every Open used
// key a1@2 and one fb session is owned.
func switchedOnce(t *testing.T, e *specEnv, h *keyedOpens, opens int) {
	t.Helper()
	specAgrees(t, e, 2, "a1", "fb", "fake-model", "r1", KindHarnessChanged)
	if want := slices.Repeat([]string{"a1@2"}, opens); !slices.Equal(h.keys, want) {
		t.Fatalf("Open keys %v; want %v", h.keys, want)
	}
	owned, err := e.st.NativeSessions(context.Background(), "a1")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, o := range owned {
		if o.Harness == "fb" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("owned %+v; want one fb session", owned)
	}
}

func rename(id, name string) UpdateRequest {
	return UpdateRequest{Envelope: Envelope{RequestID: id}, AgentID: "a1", Name: name}
}

// replays fails unless req returns the stored result of its first run, at
// spec version ver with name and harness h, and changes nothing.
func replays(t *testing.T, e *specEnv, req UpdateRequest, ver int64, name, h string) {
	t.Helper()
	ids := history(t, e.createEnv, "a1")
	got, err := e.start().Update(context.Background(), req)
	if err != nil || got.SpecVersion != ver || got.Name != name || got.Harness != h {
		t.Fatalf("retry of %s = v%d %s %s, %v; want v%d %s %s", req.RequestID, got.SpecVersion, got.Name, got.Harness, err, ver, name, h)
	}
	if again := history(t, e.createEnv, "a1"); !slices.Equal(again, ids) {
		t.Fatalf("retry changed history %v -> %v", ids, again)
	}
}

// TestUpdateRetryOlderRequestAfterNewer: a retry of r1 after r2 returns
// r1's stored result and does not apply r1 again.
func TestUpdateRetryOlderRequestAfterNewer(t *testing.T) {
	e := newSpecEnv(t)
	if err := update(t, e, rename("r1", "n1")); err != nil {
		t.Fatal(err)
	}
	if err := update(t, e, rename("r2", "n2")); err != nil {
		t.Fatal(err)
	}
	replays(t, e, rename("r1", "n1"), 2, "n1", "fa")
	specAgrees(t, e, 3, "n2", "fa", "fake-model", "r2")
	_, err := e.start().Update(context.Background(), rename("r1", "other"))
	wantCode(t, err, CodeConflict)
}

// TestUpdateReceiptCrashAfterCommit: Update crashes after its commit; its
// receipt was saved with it, so a retry after a newer Update replays it.
func TestUpdateReceiptCrashAfterCommit(t *testing.T) {
	e := newSpecEnv(t)
	crashCommit(t, 1)
	if !panics(func() { _ = update(t, e, rename("r1", "n1")) }) {
		t.Fatal("Update did not crash")
	}
	if err := update(t, e, rename("r2", "n2")); err != nil {
		t.Fatal(err)
	}
	replays(t, e, rename("r1", "n1"), 2, "n1", "fa")
}

// TestHarnessSwitchRetrySameRequest: a retry of a done switch after a newer
// Update replays the switch's result.
func TestHarnessSwitchRetrySameRequest(t *testing.T) {
	e := newSpecEnv(t)
	h := openCounted(e)
	if err := update(t, e, switchReq("r1", 1, "fb")); err != nil {
		t.Fatal(err)
	}
	switchedOnce(t, e, h, 1)
	if err := update(t, e, rename("r2", "n2")); err != nil {
		t.Fatal(err)
	}
	replays(t, e, switchReq("r1", 1, "fb"), 2, "a1", "fb")
	if len(h.keys) != 1 {
		t.Fatalf("replay opened %v", h.keys)
	}
}

// TestHarnessSwitchCrashBeforeOwned: the switch crashes after Open, before
// the session is recorded as owned; the retry opens again with the same
// key, gets the same session, records it once and commits.
func TestHarnessSwitchCrashBeforeOwned(t *testing.T) {
	e := newSpecEnv(t)
	h := openCounted(e)
	crash := crashDispatchAt(t, "switch_opened")
	if !crash(func() { _ = update(t, e, switchReq("r1", 1, "fb")) }) {
		t.Fatal("switch did not crash after Open")
	}
	specAgrees(t, e, 1, "a1", "fa", "fake-model", "")
	if err := update(t, e, switchReq("r1", 1, "fb")); err != nil {
		t.Fatal(err)
	}
	switchedOnce(t, e, h, 2)
}

// TestHarnessSwitchCrashAfterStopBeforeOpen: the switch crashes after
// stopping the turn, before Open; reconcile finishes it.
func TestHarnessSwitchCrashAfterStopBeforeOpen(t *testing.T) {
	e := newSpecEnv(t)
	h := openCounted(e)
	crash := crashDispatchAt(t, "switch_stopped")
	if !crash(func() { _ = update(t, e, switchReq("r1", 1, "fb")) }) {
		t.Fatal("switch did not crash after the stop")
	}
	if len(h.keys) != 0 {
		t.Fatalf("opened %v before the crash", h.keys)
	}
	if err := e.start().reconcileAgent(context.Background(), "a1"); err != nil {
		t.Fatal(err)
	}
	switchedOnce(t, e, h, 1)
	replays(t, e, switchReq("r1", 1, "fb"), 2, "a1", "fb")
}

// TestHarnessSwitchCrashAfterOwnedBeforeCommit: the switch crashes after
// recording the session, before its commit; reconcile reopens it with the
// same key and commits.
func TestHarnessSwitchCrashAfterOwnedBeforeCommit(t *testing.T) {
	e := newSpecEnv(t)
	h := openCounted(e)
	beforeSwitchCommit = func() { panic("crash") }
	t.Cleanup(func() { beforeSwitchCommit = func() {} })
	if !panics(func() { _ = update(t, e, switchReq("r1", 1, "fb")) }) {
		t.Fatal("switch did not crash")
	}
	beforeSwitchCommit = func() {}
	if err := e.start().reconcileAgent(context.Background(), "a1"); err != nil {
		t.Fatal(err)
	}
	switchedOnce(t, e, h, 2)
}

// TestPendingSwitchThenNewerUpdateThenReconcile: while r1's switch is
// pending, r2 is refused as agent_busy; reconcile settles r1, then r2
// applies and r1 replays.
func TestPendingSwitchThenNewerUpdateThenReconcile(t *testing.T) {
	ctx, e := context.Background(), newSpecEnv(t)
	h := openCounted(e)
	beforeSwitchCommit = func() { panic("crash") }
	t.Cleanup(func() { beforeSwitchCommit = func() {} })
	if !panics(func() { _ = update(t, e, switchReq("r1", 1, "fb")) }) {
		t.Fatal("switch did not crash")
	}
	beforeSwitchCommit = func() {}
	wantCode(t, update(t, e, rename("r2", "n2")), CodeAgentBusy)
	if err := e.start().reconcileAgent(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	switchedOnce(t, e, h, 2)
	if err := update(t, e, rename("r2", "n2")); err != nil {
		t.Fatal(err)
	}
	specAgrees(t, e, 3, "n2", "fb", "fake-model", "r2")
	replays(t, e, switchReq("r1", 1, "fb"), 2, "a1", "fb")
}

// TestHarnessSwitchRetryWhileSwitching: while r1's switch is pending, r1
// with another payload is a conflict and a request without an ID is
// agent_busy; r1 itself finishes the switch.
func TestHarnessSwitchRetryWhileSwitching(t *testing.T) {
	e := newSpecEnv(t)
	h := openCounted(e)
	crash := crashDispatchAt(t, "switch_stopped")
	if !crash(func() { _ = update(t, e, switchReq("r1", 1, "fb")) }) {
		t.Fatal("switch did not crash after the stop")
	}
	other := switchReq("r1", 1, "fb")
	other.Model = "openai/other"
	wantCode(t, update(t, e, other), CodeConflict)
	wantCode(t, update(t, e, rename("", "n2")), CodeAgentBusy)
	if err := update(t, e, switchReq("r1", 1, "fb")); err != nil {
		t.Fatal(err)
	}
	switchedOnce(t, e, h, 1)
}
