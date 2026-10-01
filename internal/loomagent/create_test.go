package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// openRec records every OpenSpec passed to the wrapped harness.
type openRec struct {
	loomharness.Harness
	specs []loomharness.OpenSpec
}

func (o *openRec) Open(ctx context.Context, spec loomharness.OpenSpec) (loomharness.NativeRef, error) {
	o.specs = append(o.specs, spec)
	return o.Harness.Open(ctx, spec)
}

// createEnv is one store and one harness that outlive service restarts.
type createEnv struct {
	st *loomstore.Store
	h  *openRec
	ws *fakeWorkspace
}

func newCreateEnv(t *testing.T) *createEnv {
	t.Helper()
	st, err := loomstore.Open(context.Background(), filepath.Join(t.TempDir(), "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &createEnv{st: st, h: &openRec{Harness: fake.New()}, ws: &fakeWorkspace{}}
}

// service starts a service on e, as after a loom serve (re)start.
func (e *createEnv) service(cfg ServiceConfig) *Service {
	cfg.Store, cfg.Events, cfg.Workspace, cfg.WorkspaceID = e.st, NewEventLog(e.st), e.ws, "ws"
	cfg.Harnesses = map[string]loomharness.Harness{"opencode": e.h}
	if cfg.Launch == nil {
		cfg.Launch = func(context.Context, loomstore.Agent, string) (loomharness.Launch, error) {
			return loomharness.Launch{Root: "/root/opencode"}, nil
		}
	}
	return New(cfg)
}

func (e *createEnv) events(t *testing.T, id, kind string) int {
	t.Helper()
	page, err := e.st.ListEvents(context.Background(), loomstore.EventQuery{AgentID: id})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range page.Events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

func leadReq(id string) CreateRequest {
	return CreateRequest{Envelope: Envelope{RequestID: id}, Preset: "lead", Name: "alpha", Repo: "/repo",
		Overrides: Overrides{Harness: "opencode"}}
}

// crashAt makes the next Create crash at point; the returned func runs a
// Create and reports whether it crashed there.
func crashAt(t *testing.T, point string) func(func()) bool {
	t.Helper()
	type crash struct{}
	createCrash = func(p string) {
		if p == point {
			panic(crash{})
		}
	}
	t.Cleanup(func() { createCrash = func(string) {} })
	return func(f func()) (crashed bool) {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(crash); !ok {
					panic(r)
				}
				crashed = true
				createCrash = func(string) {}
			}
		}()
		f()
		return false
	}
}

func TestCreateSpecJSONConfigRoundTrip(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	budget := 2.5
	req := CreateRequest{Envelope: Envelope{RequestID: "r1"}, Preset: "lead@1", Name: "alpha", Repo: "/repo",
		Overrides: Overrides{Harness: "opencode", Model: "fake-model", Effort: "high", MaxBudgetUSD: &budget},
		Persona:   &Persona{Text: "custom persona"}}
	got, err := s.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Resolve(mustPreset(t, "lead"), req, "opencode", []string{"fake-model"})
	if err != nil {
		t.Fatal(err)
	}
	check := func(id string) {
		t.Helper()
		row, err := e.st.GetAgent(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var cfg Config
		if err := json.Unmarshal([]byte(row.SpecJSON), &cfg); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg, want) {
			t.Fatalf("spec_json decodes to\n%+v\nwant\n%+v", cfg, want)
		}
		if cfg.Open.Persona != "custom persona" || cfg.Model != "fake-model" || *cfg.MaxBudgetUSD != 2.5 || cfg.Effort != "high" {
			t.Fatalf("resolved config lost a field: %+v", cfg)
		}
	}
	check(got.AgentID)
	again, err := s.Create(ctx, req)
	if err != nil || again.AgentID != got.AgentID {
		t.Fatalf("replay = %v, %v; want %s", again.AgentID, err, got.AgentID)
	}
	check(got.AgentID)
	if spec := e.h.specs[0]; !reflect.DeepEqual(spec.Rules, want.Rules) || spec.Preset.Persona != "custom persona" {
		t.Fatalf("Open got %+v", spec)
	}
}

func TestCreateReplayReturnsOneAgent(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	a, err := s.Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, leadReq("r1"))
	if err != nil || b.AgentID != a.AgentID {
		t.Fatalf("RequestID replay = %s, %v; want %s", b.AgentID, err, a.AgentID)
	}

	rev := CreateRequest{Envelope: Envelope{RequestID: "w1"}, Preset: "pr-review-webhook", Name: "rev",
		Repo: "/repo", ExternalKey: "pr-review:o/r#7@bbb", Subject: Subject{Type: "pr", ID: "o/r#7", Version: "bbb"},
		BaseRef: "bbb", FirstMessage: "review it", Overrides: Overrides{Harness: "opencode"}}
	r1, err := s.Create(ctx, rev)
	if err != nil {
		t.Fatal(err)
	}
	rev.RequestID, rev.Name = "w2", "other-name" // a second webhook delivery
	r2, err := s.Create(ctx, rev)
	if err != nil || r2.AgentID != r1.AgentID {
		t.Fatalf("ExternalKey replay = %s, %v; want %s", r2.AgentID, err, r1.AgentID)
	}
	if len(e.h.specs) != 2 || e.events(t, r1.AgentID, KindAgentCreated) != 1 {
		t.Fatalf("opens = %d, agent.created = %d", len(e.h.specs), e.events(t, r1.AgentID, KindAgentCreated))
	}
	if r1.Branch != nil || e.ws.ensured[1].Detached != true || e.ws.ensured[1].BaseRef != "bbb" {
		t.Fatalf("reviewer working copy = %+v", e.ws.ensured[1])
	}
	slots, _ := e.st.Slots(ctx, r1.AgentID)
	if len(slots) != 1 || slots[0].Body != "review it" || slots[0].State != loomstore.SlotWaiting {
		t.Fatalf("first message slots = %+v", slots)
	}

	rev.RequestID, rev.Repo = "w3", "/other"
	if _, err := s.Create(ctx, rev); !isCode(err, CodeExternalKeyConflict) {
		t.Fatalf("different spec = %v, want external_key_conflict", err)
	}
}

func TestCreateValidatesBeforeSideEffects(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	if _, err := s.Create(ctx, leadReq("r0")); err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]CreateRequest{
		"preset":     {Envelope: Envelope{RequestID: "r1"}, Preset: "nope", Name: "b", Repo: "/repo"},
		"name taken": leadReq("r2"),
		"no repo":    {Envelope: Envelope{RequestID: "r3"}, Preset: "lead", Name: "c"},
		"parent":     {Envelope: Envelope{RequestID: "r4"}, Preset: "task", Name: "d", Repo: "/repo", Parent: "agt_missing"},
		"harness":    {Envelope: Envelope{RequestID: "r5"}, Preset: "lead", Name: "e", Repo: "/repo", Overrides: Overrides{Harness: "gemini"}},
		"no request": {Preset: "lead", Name: "f", Repo: "/repo"},
	} {
		if _, err := s.Create(ctx, req); err == nil {
			t.Fatalf("%s: Create succeeded", name)
		}
	}
	rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
	if len(rows) != 1 || len(e.ws.ensured) != 1 || len(e.h.specs) != 1 {
		t.Fatalf("side effects after refused Creates: rows %d, ensures %d, opens %d", len(rows), len(e.ws.ensured), len(e.h.specs))
	}
}

func TestCreateCrashAtEachStepConverges(t *testing.T) {
	for _, point := range []string{"row", "worktree", "open", "recorded", "created"} {
		t.Run(point, func(t *testing.T) {
			ctx := context.Background()
			e := newCreateEnv(t)
			run := crashAt(t, point)
			req := leadReq("r1")
			req.FirstMessage = "hello"
			if !run(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, req) }) {
				t.Fatal("did not crash")
			}
			a, err := e.service(ServiceConfig{}).Create(ctx, req) // after restart
			if err != nil {
				t.Fatal(err)
			}
			row, _ := e.st.GetAgent(ctx, a.AgentID)
			owned, _ := e.st.NativeSessions(ctx, a.AgentID)
			slots, _ := e.st.Slots(ctx, a.AgentID)
			if row.State != StateIdle || row.CreateStep != stepDone || len(owned) != 1 ||
				*row.HarnessSessionID != owned[0].NativeID || len(slots) != 1 ||
				e.events(t, a.AgentID, KindAgentCreated) != 1 {
				t.Fatalf("after replay: row %+v owned %+v slots %d", row, owned, len(slots))
			}
			if *row.WorktreePath != "/wt/"+a.AgentID {
				t.Fatalf("worktree = %v", *row.WorktreePath)
			}
		})
	}
}

func TestCreateWorkspacePortReplay(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	run := crashAt(t, "open") // after Ensure, before harness Open
	if !run(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	a, err := e.service(ServiceConfig{}).Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.ws.ensured) != 1 || e.ws.ensured[0].Key != a.AgentID || e.ws.ensured[0].Branch != "loom/agent/"+a.AgentID {
		t.Fatalf("ensures = %+v", e.ws.ensured)
	}
	if len(e.h.specs) != 1 || e.h.specs[0].Dir != "/wt/"+a.AgentID {
		t.Fatalf("Open used %+v, want the recorded binding", e.h.specs)
	}
}

func TestCreateRecordsReturnedNativeRef(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{Launch: func(context.Context, loomstore.Agent, string) (loomharness.Launch, error) {
		return loomharness.Launch{Root: "/profiles/alpha/opencode"}, nil
	}})
	a, err := s.Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	owned, err := e.st.NativeSessions(ctx, a.AgentID)
	if err != nil || len(owned) != 1 {
		t.Fatalf("owned = %+v, %v", owned, err)
	}
	n := owned[0]
	row, _ := e.st.GetAgent(ctx, a.AgentID)
	if n.AgentID != a.AgentID || n.Harness != "opencode" || n.NativeRoot != "/profiles/alpha/opencode" ||
		n.NativeID != *row.HarnessSessionID {
		t.Fatalf("recorded %+v, session %s", n, *row.HarnessSessionID)
	}
}

func TestCreateCrashReplayRecordsNativeRef(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	run := crashAt(t, "recorded") // Open returned, the ownership write never ran
	if !run(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	a, err := e.service(ServiceConfig{}).Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	owned, _ := e.st.NativeSessions(ctx, a.AgentID)
	if len(owned) != 1 || len(e.h.specs) != 2 || e.h.specs[0].Key != e.h.specs[1].Key {
		t.Fatalf("owned %+v after %d opens", owned, len(e.h.specs))
	}
	again, _ := e.h.Open(ctx, loomharness.OpenSpec{Key: a.AgentID})
	if again.NativeID != owned[0].NativeID || e.events(t, a.AgentID, KindAgentCreated) != 1 {
		t.Fatalf("adopted %s, recorded %s", again.NativeID, owned[0].NativeID)
	}
}

func TestCreateRecordsProfileKey(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	a, err := s.Create(ctx, leadReq("r1"))
	if err != nil || a.ProfileKey != "alpha" || a.Name != "alpha" {
		t.Fatalf("Create = %+v, %v", a, err)
	}
	if _, err := s.Update(ctx, UpdateRequest{AgentID: a.AgentID, Name: "beta"}); err != nil {
		t.Fatal(err)
	}
	row, _ := e.st.GetAgent(ctx, a.AgentID)
	if row.Name != "beta" || row.ProfileKey != "alpha" {
		t.Fatalf("after rename: name %s, profile_key %s", row.Name, row.ProfileKey)
	}
}

func TestLeadProfileKeyDefault(t *testing.T) {
	ctx := context.Background()
	for env, want := range map[string]string{"tyson-lead": "tyson-lead", "": "lead"} {
		t.Setenv("LOOM_AGENT_NAME", env)
		e := newCreateEnv(t)
		req := leadReq("r1")
		req.Name = ""
		a, err := e.service(ServiceConfig{}).Create(ctx, req)
		if err != nil || a.Name != want || a.ProfileKey != want {
			t.Fatalf("LOOM_AGENT_NAME=%q: %s/%s, %v; want %s", env, a.Name, a.ProfileKey, err, want)
		}
	}
}

func TestCreateUsesWorkspaceDefaultBackend(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{DefaultBackend: func(context.Context) (Backend, error) {
		return Backend{Harness: "opencode", Model: "fake-model"}, nil
	}})
	req := leadReq("r1")
	req.Overrides = Overrides{}
	a, err := s.Create(ctx, req)
	if err != nil || a.Harness != "opencode" || deref(a.Model) != "fake-model" || e.h.specs[0].Model != "fake-model" {
		t.Fatalf("omitted = %s/%s, %v", a.Harness, deref(a.Model), err)
	}
	// Explicit values win: a model the default does not name is refused by
	// the catalog check rather than replaced.
	req = leadReq("r2")
	req.Name, req.Overrides = "b", Overrides{Harness: "opencode", Model: "other"}
	if _, err := s.Create(ctx, req); !isCode(err, CodePresetInvalid) {
		t.Fatalf("explicit model = %v, want the catalog check", err)
	}
	// An explicit harness other than the default does not take its model.
	other := e.service(ServiceConfig{DefaultBackend: func(context.Context) (Backend, error) {
		return Backend{Harness: "codex", Model: "gpt-x"}, nil
	}})
	req = leadReq("r3")
	req.Name = "c"
	c, err := other.Create(ctx, req)
	if err != nil || c.Harness != "opencode" || c.Model != nil {
		t.Fatalf("explicit harness = %s/%v, %v", c.Harness, c.Model, err)
	}
}

func TestCreateBridgeCapsFromHostOnly(t *testing.T) {
	ctx := context.Background()
	var forged CreateRequest
	if err := json.Unmarshal([]byte(`{"Preset":"lead","Bridge":{"HasGitHubRead":true,"HasPublish":true}}`), &forged); err != nil {
		t.Fatal(err)
	}
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	req := leadReq("r1")
	req.Bridge = BridgeCaps{HasGitHubRead: true, HasPublish: true} // a forged in-process value
	if forged.Bridge != (BridgeCaps{}) {
		t.Fatalf("request body set Bridge: %+v", forged.Bridge)
	}
	a, err := s.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if cfg := specOf(t, e, a.AgentID); cfg.Bridge != (BridgeCaps{}) || hasPublishDenies(cfg.Rules) {
		t.Fatalf("no host registration, yet config = %+v", cfg)
	}

	host := e.service(ServiceConfig{Bridge: func(_ context.Context, p Preset) BridgeCaps {
		return BridgeCaps{HasGitHubRead: p.Name == "lead", HasPublish: p.Name == "lead"}
	}})
	req = leadReq("r2")
	req.Name = "b"
	b, err := host.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	cfg := specOf(t, e, b.AgentID)
	if cfg.Bridge != (BridgeCaps{HasGitHubRead: true, HasPublish: true}) || !hasPublishDenies(cfg.Rules) ||
		!hasPublishDenies(e.h.specs[1].Rules) {
		t.Fatalf("host registration not applied: %+v", cfg)
	}
}

func TestResumeRecompilesWithStoredBridgeCaps(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	run := crashAt(t, "open")
	both := func(context.Context, Preset) BridgeCaps { return BridgeCaps{HasGitHubRead: true, HasPublish: true} }
	if !run(func() { _, _ = e.service(ServiceConfig{Bridge: both}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	// After the restart the host has not registered the bridge yet; recovery
	// uses the capabilities stored with the agent.
	a, err := e.service(ServiceConfig{}).Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := specOf(t, e, a.AgentID)
	if cfg.Bridge != (BridgeCaps{HasGitHubRead: true, HasPublish: true}) || len(e.h.specs) != 1 ||
		!hasPublishDenies(e.h.specs[0].Rules) {
		t.Fatalf("recovery lost the stored caps: %+v, opens %+v", cfg, e.h.specs)
	}
	// The stored caps recompile to the same rules.
	p := mustPreset(t, "lead")
	re, err := Resolve(p, CreateRequest{Overrides: Overrides{Harness: "opencode"}, Bridge: cfg.Bridge}, "opencode", nil)
	if err != nil || !reflect.DeepEqual(re.Rules, cfg.Rules) {
		t.Fatalf("recompiled rules %+v, stored %+v, %v", re.Rules, cfg.Rules, err)
	}
}

func specOf(t *testing.T, e *createEnv, id string) Config {
	t.Helper()
	row, err := e.st.GetAgent(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := json.Unmarshal([]byte(row.SpecJSON), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func hasPublishDenies(rules []loomharness.PermissionRule) bool {
	return len(rules) >= 2 && slices.Equal(rules[len(rules)-2:], publishDenies)
}

func isCode(err error, c Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == c
}
