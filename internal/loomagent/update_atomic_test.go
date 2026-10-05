package loomagent

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// specEnv is lead a1 on harness "fa" with harness "fb" beside it, in a
// store and harnesses that outlive service restarts.
type specEnv struct {
	*createEnv
	harnesses map[string]loomharness.Harness
}

func newSpecEnv(t *testing.T) *specEnv {
	t.Helper()
	ctx := context.Background()
	e := &specEnv{createEnv: newCreateEnv(t), harnesses: map[string]loomharness.Harness{"fa": fake.New(), "fb": fake.New()}}
	if _, err := e.harnesses["fb"].Open(ctx, loomharness.OpenSpec{Key: "other"}); err != nil { // fb ids differ from fa's
		t.Fatal(err)
	}
	old, err := e.harnesses["fa"].Open(ctx, loomharness.OpenSpec{Key: "a1", Launch: loomharness.Launch{Root: "/root/fa"}})
	if err != nil {
		t.Fatal(err)
	}
	a := svcAgent("a1", "persistent", StateIdle)
	a.Harness, a.HarnessSessionID, a.HarnessSessionRoot, a.Model = "fa", &old.NativeID, &old.Root, sp("fake-model")
	if err := e.st.InsertAgent(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := e.st.RecordNativeSession(ctx, loomstore.NativeSession{AgentID: "a1", Harness: "fa",
		NativeRoot: old.Root, NativeID: old.NativeID}); err != nil {
		t.Fatal(err)
	}
	return e
}

// start starts a service on e, as after a loom serve (re)start.
func (e *specEnv) start() *Service {
	s := New(ServiceConfig{Store: e.st, Events: NewEventLog(e.st), WorkspaceID: "ws", Harnesses: e.harnesses,
		Launch: func(_ context.Context, _ loomstore.Agent, h string) (loomharness.Launch, error) {
			return loomharness.Launch{Root: "/root/" + h}, nil
		}})
	useTestClock(s)
	return s
}

// specAgrees fails unless a1 is at spec version ver on harness h with
// last request req, and has exactly one of each of kinds, each named by the
// revision that saved it; it returns a1's history.
func specAgrees(t *testing.T, e *specEnv, ver int64, h, req string, kinds ...string) []string {
	t.Helper()
	row, err := e.st.GetAgent(context.Background(), "a1")
	if err != nil {
		t.Fatal(err)
	}
	ids := history(t, e.createEnv, "a1")
	if row.SpecVersion != ver || row.Harness != h || deref(row.LastRequestID) != req {
		t.Fatalf("row at v%d on %s req %q, history %v; want v%d on %s req %q", row.SpecVersion, row.Harness,
			deref(row.LastRequestID), ids, ver, h, req)
	}
	for _, k := range kinds {
		if want := fmt.Sprintf("a1:%d:%s", row.Revision, k); e.events(t, "a1", k) != 1 || !slices.Contains(ids, want) {
			t.Fatalf("history %v; want one %s", ids, want)
		}
	}
	return ids
}

// update starts a service on e, as after a restart, and runs req.
func update(t *testing.T, e *specEnv, req UpdateRequest) error {
	t.Helper()
	_, err := e.start().Update(context.Background(), req)
	return err
}

// retryAddsNothing retries req after a restart; a1's history must stay ids.
func retryAddsNothing(t *testing.T, e *specEnv, req UpdateRequest, ids []string) {
	t.Helper()
	if err := update(t, e, req); err != nil {
		t.Fatal(err)
	}
	if again := history(t, e.createEnv, "a1"); !slices.Equal(again, ids) {
		t.Fatalf("retry changed history %v -> %v", ids, again)
	}
}

func renameReq() UpdateRequest {
	return UpdateRequest{Envelope: Envelope{RequestID: "r1"}, AgentID: "a1", Name: "renamed"}
}

// TestUpdateCrashBeforeCommit: agent.updated fails inside Update's
// transaction; the spec is unchanged, and a retry after the restart commits
// both once.
func TestUpdateCrashBeforeCommit(t *testing.T) {
	e := newSpecEnv(t)
	lift := failSaving(t, e.createEnv, KindAgentUpdated)
	if err := update(t, e, renameReq()); err == nil {
		t.Fatal("Update did not fail")
	}
	specAgrees(t, e, 1, "fa", "")
	if row, _ := e.st.GetAgent(context.Background(), "a1"); row.Name != "a1" {
		t.Fatalf("name %q saved without agent.updated", row.Name)
	}
	lift()
	if err := update(t, e, renameReq()); err != nil {
		t.Fatal(err)
	}
	retryAddsNothing(t, e, renameReq(), specAgrees(t, e, 2, "fa", "r1", KindAgentUpdated))
}

// TestUpdateCrashAfterCommit: Update crashes after its commit, before
// publishing; the spec and agent.updated are both saved, once.
func TestUpdateCrashAfterCommit(t *testing.T) {
	e := newSpecEnv(t)
	crashCommit(t, 1)
	if !panics(func() { _ = update(t, e, renameReq()) }) {
		t.Fatal("Update did not crash")
	}
	retryAddsNothing(t, e, renameReq(), specAgrees(t, e, 2, "fa", "r1", KindAgentUpdated))
}

// TestHarnessSwitchCommitCrashBeforeCommit: harness.changed fails inside
// the switch's transaction; the row stays on the old harness, and a retry
// after the restart commits both once.
func TestHarnessSwitchCommitCrashBeforeCommit(t *testing.T) {
	e := newSpecEnv(t)
	lift := failSaving(t, e.createEnv, KindHarnessChanged)
	if err := update(t, e, switchReq("r1", 1, "fb")); err == nil {
		t.Fatal("switch did not fail")
	}
	specAgrees(t, e, 1, "fa", "")
	lift()
	if err := update(t, e, switchReq("r1", 1, "fb")); err != nil {
		t.Fatal(err)
	}
	retryAddsNothing(t, e, switchReq("r1", 1, "fb"), specAgrees(t, e, 2, "fb", "r1", KindHarnessChanged))
}

// TestHarnessSwitchCommitCrashAfterCommit: the switch crashes after its
// commit, before publishing; the row is on the new harness with one
// harness.changed.
func TestHarnessSwitchCommitCrashAfterCommit(t *testing.T) {
	e := newSpecEnv(t)
	crashCommit(t, 1)
	if !panics(func() { _ = update(t, e, switchReq("r1", 1, "fb")) }) {
		t.Fatal("switch did not crash")
	}
	retryAddsNothing(t, e, switchReq("r1", 1, "fb"), specAgrees(t, e, 2, "fb", "r1", KindHarnessChanged))
}

// TestUpdateUnverifiedModelCrashAfterCommitBeforeFanout: an Update to an
// unlisted model crashes after its commit, before the fanout; the spec,
// agent.updated and model.unverified are each saved once, and a
// subscriber reconnecting from before the Update gets both events.
func TestUpdateUnverifiedModelCrashAfterCommitBeforeFanout(t *testing.T) {
	ctx, e := context.Background(), newSpecEnv(t)
	req := UpdateRequest{Envelope: Envelope{RequestID: "r1"}, AgentID: "a1", Model: "openai/other"}
	crashCommit(t, 1)
	if !panics(func() { _ = update(t, e, req) }) {
		t.Fatal("Update did not crash")
	}
	ids := specAgrees(t, e, 2, "fa", "r1", KindAgentUpdated, KindModelUnverified)
	s := e.start()
	sub, err := s.events.Subscribe(ctx, map[string]int64{"a1": 0})
	if err != nil {
		t.Fatal(err)
	}
	defer s.events.drop(sub)
	if got := recv(t, sub, 2); !slices.Equal(kindsOf(got), []string{KindAgentUpdated, KindModelUnverified}) {
		t.Fatalf("reconnect got %v; want agent.updated then model.unverified", kindsOf(got))
	}
	retryAddsNothing(t, e, req, ids)
}

// kindsOf is the kinds of es.
func kindsOf(es []loomstore.Event) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Kind)
	}
	return out
}
