package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// openRec records every OpenSpec passed to the wrapped harness. A non-empty
// root replaces the root Open returns, as a changed launch root would.
type openRec struct {
	loomharness.Harness
	specs []loomharness.OpenSpec
	root  string
}

func (o *openRec) Open(ctx context.Context, spec loomharness.OpenSpec) (loomharness.NativeRef, error) {
	o.specs = append(o.specs, spec)
	ref, err := o.Harness.Open(ctx, spec)
	if o.root != "" {
		ref.Root = o.root
	}
	return ref, err
}

// createEnv is one store and one harness that outlive service restarts.
type createEnv struct {
	st   *loomstore.Store
	path string // the store's file
	h    *openRec
	ws   *fakeWorkspace
}

func newCreateEnv(t *testing.T) *createEnv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "loom.db")
	st, err := loomstore.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &createEnv{st: st, path: path, h: &openRec{Harness: fake.New()}, ws: &fakeWorkspace{}}
}

// service starts a service on e, as after a loom serve (re)start.
func (e *createEnv) service(cfg ServiceConfig) *Service {
	cfg.Store, cfg.Events, cfg.Workspace, cfg.WorkspaceID = e.st, NewEventLog(e.st), e.ws, "ws"
	cfg.Harnesses = map[string]loomharness.Harness{"opencode": e.h}
	if cfg.Bridge == nil { // a host bridge that registers no capabilities
		cfg.Bridge = func(context.Context, Preset) (BridgeCaps, error) { return BridgeCaps{}, nil }
	}
	if cfg.Launch == nil {
		cfg.Launch = func(context.Context, loomstore.Agent, string) (loomharness.Launch, error) {
			return loomharness.Launch{Root: "/root/opencode"}, nil
		}
	}
	s := New(cfg)
	useTestClock(s) // nothing in a test waits on real time
	return s
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
	return CreateRequest{Envelope: Envelope{RequestID: id}, Preset: "lead", Name: "alpha", Repo: "/repo", BaseRef: "main",
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
	req := CreateRequest{Envelope: Envelope{RequestID: "r1"}, Preset: "lead@1", Name: "alpha", Repo: "/repo", BaseRef: "main",
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
	if spec := e.h.specs[0]; !reflect.DeepEqual(spec.Rules, append(slices.Clone(want.Rules), subagentDeny)) || spec.Preset.Persona != "custom persona" {
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
	// The dispatcher handed the first message over once, at the end of Create.
	if len(slots) != 1 || slots[0].Body != "review it" || slots[0].State != loomstore.SlotHanded {
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
	for _, point := range []string{"row", "worktree", "open", "recorded", "session", "created"} {
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
			// The first message is handed over exactly once (one turn ran).
			_, turns := e.h.Harness.(*fake.Harness).Rules(loomharness.NativeRef{Root: *row.HarnessSessionRoot, NativeID: *row.HarnessSessionID})
			if row.State != StateActive || row.CreateStep != stepDone || len(owned) != 1 ||
				*row.HarnessSessionID != owned[0].NativeID || len(slots) != 1 || slots[0].State != loomstore.SlotHanded ||
				len(turns) != 1 || e.events(t, a.AgentID, KindAgentCreated) != 1 {
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
	// Explicit values win: a model the default does not name is kept, not
	// replaced (unverified when the catalog lacks it, MCS1).
	req = leadReq("r2")
	req.Name, req.Overrides = "b", Overrides{Harness: "opencode", Model: "other"}
	if b, err := s.Create(ctx, req); err != nil || deref(b.Model) != "other" || !b.ModelUnverified {
		t.Fatalf("explicit model = %s unverified=%v, %v; want other kept", deref(b.Model), b.ModelUnverified, err)
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
	both := BridgeCaps{HasGitHubRead: true, HasPublish: true}
	out, err := json.Marshal(Config{Bridge: both})
	if err != nil || strings.Contains(string(out), "Bridge") || strings.Contains(string(out), "HasPublish") {
		t.Fatalf("Config JSON carries bridge caps: %s, %v", out, err)
	}
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	req := leadReq("r1")
	req.Bridge = both // a caller-set value
	a, err := s.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := e.st.GetAgent(ctx, a.AgentID)
	if strings.Contains(row.SpecJSON, "Bridge") || hasPublishDenies(e.h.specs[0].Rules) {
		t.Fatalf("caller caps reached the agent: spec %s, rules %+v", row.SpecJSON, e.h.specs[0].Rules)
	}
	var legacy Config // a spec_json written when caps were still stored
	if err := json.Unmarshal([]byte(`{"Preset":{"Name":"lead"},"Bridge":{"HasGitHubRead":true,"HasPublish":true}}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if rules, err := s.policy(ctx, legacy); err != nil || legacy.Bridge != (BridgeCaps{}) || hasPublishDenies(rules) {
		t.Fatalf("legacy stored caps enabled: %+v, %v", rules, err)
	}

	// Absent, partial and full wiring keep each role's own restrictions;
	// only full wiring adds the gh and git push denies.
	ro := Overrides{Harness: "opencode", ReadOnly: true, DeniedTools: []string{"webfetch"}}
	for i, caps := range []BridgeCaps{{}, {HasGitHubRead: true}, {HasPublish: true}, both} {
		e := newCreateEnv(t)
		s := e.service(ServiceConfig{Bridge: func(context.Context, Preset) (BridgeCaps, error) { return caps, nil }})
		for j, preset := range []string{"daemon-worker", "pr-review-webhook"} {
			req := CreateRequest{Envelope: Envelope{RequestID: preset}, Preset: preset, Name: preset, Repo: "/repo", BaseRef: "main",
				Overrides: Overrides{Harness: "opencode"}}
			if preset == "daemon-worker" {
				req.Overrides = ro
			}
			if _, err := s.Create(ctx, req); err != nil {
				t.Fatal(err)
			}
			base, _ := Resolve(mustPreset(t, preset), req, "opencode", nil)
			got := e.h.specs[j].Rules
			if !slices.Equal(got[:len(base.Rules)], base.Rules) || hasPublishDenies(got) != (caps == both) {
				t.Fatalf("wiring %d, %s: rules %+v", i, preset, got)
			}
		}
	}
}

func TestBridgeRegistrationRequiredOnRestart(t *testing.T) {
	ctx := context.Background()
	both := BridgeCaps{HasGitHubRead: true, HasPublish: true}
	errAbsent := errors.New("lead needs the github_read and publish bridge, which is not wired")
	errDown := errors.New("bridge unavailable")
	e := newCreateEnv(t)
	launches := 0
	start := func(bridge func(context.Context, Preset) (BridgeCaps, error)) *Service {
		return e.service(ServiceConfig{Bridge: bridge,
			Launch: func(context.Context, loomstore.Agent, string) (loomharness.Launch, error) {
				launches++ // the only place launch credentials come from
				return loomharness.Launch{Root: "/root/opencode"}, nil
			}})
	}
	run := crashAt(t, "open")
	if !run(func() {
		_, _ = start(func(context.Context, Preset) (BridgeCaps, error) { return both, nil }).Create(ctx, leadReq("r1"))
	}) {
		t.Fatal("did not crash")
	}
	// Restart without the registration, then during an outage: recovery
	// stops before launching and loosens nothing.
	for _, cause := range []error{errAbsent, errDown} {
		s := start(func(context.Context, Preset) (BridgeCaps, error) { return BridgeCaps{}, cause })
		if _, err := s.Create(ctx, leadReq("r1")); !errors.Is(err, cause) {
			t.Fatalf("recovery = %v, want %v", err, cause)
		}
	}
	if launches != 0 || len(e.h.specs) != 0 {
		t.Fatalf("launched %d times, opened %d, without the bridge", launches, len(e.h.specs))
	}
	// Restored registration compiles the current rules; nothing was stored.
	s := start(func(context.Context, Preset) (BridgeCaps, error) { return both, nil })
	a, err := s.Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	if row, _ := e.st.GetAgent(ctx, a.AgentID); strings.Contains(row.SpecJSON, "Bridge") ||
		hasPublishDenies(specOf(t, e, a.AgentID).Rules) || !hasPublishDenies(e.h.specs[0].Rules) {
		t.Fatalf("restored registration: spec %s, opened %+v", row.SpecJSON, e.h.specs[0].Rules)
	}
	// Resume and harness switch also stop before launching without it.
	launches = 0
	s = start(func(context.Context, Preset) (BridgeCaps, error) { return BridgeCaps{}, errAbsent })
	if _, err := s.resume(ctx, s.get(t, a.AgentID)); !errors.Is(err, errAbsent) {
		t.Fatalf("resume = %v", err)
	}
	fb := &openRec{Harness: fake.New()}
	s.harnesses["codex"] = fb
	v := int64(1)
	if _, err := s.Update(ctx, UpdateRequest{Envelope: Envelope{RequestID: "u1", Expect: &Expect{SpecVersion: &v}},
		AgentID: a.AgentID, Harness: "codex"}); !errors.Is(err, errAbsent) {
		t.Fatalf("switch = %v", err)
	}
	if launches != 0 || len(fb.specs) != 0 {
		t.Fatalf("launched %d, opened %d without the bridge", launches, len(fb.specs))
	}
}

func TestCreateReplayChangedRootSameID(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	run := crashAt(t, "session") // ownership recorded, checkpoint not written
	if !run(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	e.h.root = "/root/replaced" // the replay's Open returns the same id under another root
	s := e.service(ServiceConfig{})
	a, err := s.Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	owned, _ := e.st.NativeSessions(ctx, a.AgentID)
	_, ref, err := s.current(ctx, s.get(t, a.AgentID))
	if err != nil || len(owned) != 2 || ref != (loomharness.NativeRef{Root: "/root/replaced", NativeID: owned[0].NativeID}) {
		t.Fatalf("current = %+v, %v; owned %+v", ref, err, owned)
	}
}

func TestCurrentAmbiguousLegacyRootFailsClosed(t *testing.T) {
	ctx := context.Background()
	a := svcAgent("a1", "persistent", StateIdle)
	a.HarnessSessionID = sp("s1") // a legacy row: no saved root
	s := newService(t, ServiceConfig{Harnesses: map[string]loomharness.Harness{"fake": fake.New()}}, a)
	record := func(root string) {
		if err := s.store.RecordNativeSession(ctx, loomstore.NativeSession{AgentID: "a1", Harness: "fake",
			NativeRoot: root, NativeID: "s1"}); err != nil {
			t.Fatal(err)
		}
	}
	record("/r1")
	if _, ref, err := s.current(ctx, s.get(t, "a1")); err != nil || ref.Root != "/r1" {
		t.Fatalf("single root = %+v, %v", ref, err)
	}
	record("/r2")
	if sess, _, err := s.current(ctx, s.get(t, "a1")); err == nil || sess != nil ||
		!strings.Contains(err.Error(), "2 roots") {
		t.Fatalf("ambiguous roots = %v, %v; want a clear error", sess, err)
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

// hasPublishDenies reports whether rules end with the gh and git push denies,
// before any subagent deny (which policy appends last).
func hasPublishDenies(rules []loomharness.PermissionRule) bool {
	if n := len(rules); n > 0 && rules[n-1] == subagentDeny {
		rules = rules[:n-1]
	}
	return len(rules) >= 2 && slices.Equal(rules[len(rules)-2:], publishDenies)
}

// leadWithDenies serves a lead preset that names the gh and git push denies
// itself, exactly as the bridge would.
type leadWithDenies struct{ BuiltinPresets }

func (leadWithDenies) Get(ctx context.Context, ref string) (Preset, error) {
	p, err := BuiltinPresets{}.Get(ctx, ref)
	if p.Name == "lead" {
		p.Rules = slices.Concat(p.Rules, publishDenies)
	}
	return p, err
}

func countDenies(rules []loomharness.PermissionRule) int {
	n := 0
	for _, r := range rules {
		if slices.Contains(publishDenies, r) {
			n++
		}
	}
	return n
}

func TestCreateKeepsExplicitGHDenies(t *testing.T) {
	ctx := context.Background()
	both := BridgeCaps{HasGitHubRead: true, HasPublish: true}
	errAbsent := errors.New("bridge wiring absent")
	t.Cleanup(func() { delete(Enforcement, "codex") })
	Enforcement["codex"] = Enforces{Rules: true} // a switch destination that enforces rules
	for _, now := range []struct {
		name string
		hook func(context.Context, Preset) (BridgeCaps, error)
	}{
		{"caps removed", func(context.Context, Preset) (BridgeCaps, error) { return BridgeCaps{}, nil }},
		{"caps changed", func(context.Context, Preset) (BridgeCaps, error) { return BridgeCaps{HasGitHubRead: true}, nil }},
		{"no hook", nil},
	} {
		t.Run(now.name, func(t *testing.T) {
			e := newCreateEnv(t)
			run := crashAt(t, "open")
			first := e.service(ServiceConfig{Presets: leadWithDenies{},
				Bridge: func(context.Context, Preset) (BridgeCaps, error) { return both, nil }})
			if !run(func() { _, _ = first.Create(ctx, leadReq("r1")) }) {
				t.Fatal("did not crash")
			}
			s := e.service(ServiceConfig{Presets: leadWithDenies{}, Bridge: now.hook})
			a, err := s.Create(ctx, leadReq("r1")) // Create recovery
			if err != nil {
				t.Fatal(err)
			}
			row := s.get(t, a.AgentID)
			cfg, err := loadConfig(row)
			if err != nil || countDenies(cfg.Rules) != 2 || countDenies(e.h.specs[0].Rules) != 2 {
				t.Fatalf("load %+v, recovery opened %+v", cfg.Rules, e.h.specs[0].Rules)
			}
			if _, err := s.resume(ctx, row); err != nil { // Resume
				t.Fatal(err)
			}
			if rules, err := s.policy(ctx, cfg); err != nil || countDenies(rules) != 2 {
				t.Fatalf("resume policy %+v, %v", rules, err)
			}
			fb := &openRec{Harness: fake.New()}
			s.harnesses["codex"] = fb
			v := int64(1)
			if _, err := s.Update(ctx, UpdateRequest{Envelope: Envelope{RequestID: "u1", Expect: &Expect{SpecVersion: &v}},
				AgentID: a.AgentID, Harness: "codex"}); err != nil { // harness switch
				t.Fatal(err)
			}
			if cfg := specOf(t, e, a.AgentID); countDenies(fb.specs[0].Rules) != 2 || countDenies(cfg.Rules) != 2 {
				t.Fatalf("switch opened %+v, stored %+v", fb.specs[0].Rules, cfg.Rules)
			}
		})
	}
	// A kept deny does not stand in for the bridge: without required wiring
	// recovery still stops before any launch.
	e := newCreateEnv(t)
	run := crashAt(t, "open")
	if !run(func() { _, _ = e.service(ServiceConfig{Presets: leadWithDenies{}}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	launches := 0
	s := e.service(ServiceConfig{Presets: leadWithDenies{},
		Bridge: func(context.Context, Preset) (BridgeCaps, error) { return BridgeCaps{}, errAbsent },
		Launch: func(context.Context, loomstore.Agent, string) (loomharness.Launch, error) {
			launches++
			return loomharness.Launch{}, nil
		}})
	if _, err := s.Create(ctx, leadReq("r1")); !errors.Is(err, errAbsent) || launches != 0 || len(e.h.specs) != 0 {
		t.Fatalf("recovery = %v after %d launches", err, launches)
	}
}

func TestCreateLegacySavedBridgeDeniesKept(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	run := crashAt(t, "open")
	if !run(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
		t.Fatal("did not crash")
	}
	row, err := e.st.FindCreated(ctx, "ws", "", "r1")
	if err != nil {
		t.Fatal(err)
	}
	cfg := specOf(t, e, row.AgentID) // write the shape builds before R-G saved
	cfg.Rules = slices.Concat(cfg.Rules, publishDenies)
	b, _ := json.Marshal(cfg)
	to := row.SpecOf()
	to.SpecJSON = string(b)
	if err := e.st.CompareAndSetSpec(ctx, row.AgentID, row.SpecVersion, to); err != nil {
		t.Fatal(err)
	}
	if _, err := e.service(ServiceConfig{}).Create(ctx, leadReq("r1")); err != nil { // no caps now
		t.Fatal(err)
	}
	if n := countDenies(e.h.specs[0].Rules); n != 2 {
		t.Fatalf("legacy saved denies: opened with %d, want them kept", n)
	}
}

func TestHarnessResumeInstallsCurrentPolicy(t *testing.T) {
	ctx := context.Background()
	both := BridgeCaps{HasGitHubRead: true, HasPublish: true}
	caps := BridgeCaps{}
	hook := func(context.Context, Preset) (BridgeCaps, error) { return caps, nil }
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{Bridge: hook})
	req := CreateRequest{Envelope: Envelope{RequestID: "r1"}, Preset: "daemon-worker", Name: "w", Repo: "/repo", BaseRef: "main",
		Overrides: Overrides{Harness: "opencode", ReadOnly: true, DeniedTools: []string{"edit"}}}
	a, err := s.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	row := s.get(t, a.AgentID)
	ref := loomharness.NativeRef{Root: *row.HarnessSessionRoot, NativeID: *row.HarnessSessionID}
	user := specOf(t, e, a.AgentID).Rules // preset rules plus the stricter user denies
	if !slices.Contains(user, loomharness.PermissionRule{Action: "edit", Resource: "*", Effect: "deny"}) {
		t.Fatalf("user deny missing from %+v", user)
	}

	// A turn is cut off; the dispatcher's next message waits in a slot.
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Delta: "a"}, {Crash: true}, {Delta: "b"}}, ResumeContinues: true})
	if err := fh.Session(ref).Prompt(ctx, loomharness.Input{Key: "k1", Text: "go"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.st.Send(ctx, loomstore.SlotSend{AgentID: a.AgentID, Sender: "user:local", RequestID: "s1",
		Body: "next", Source: "user_chat", Result: func(bool) (string, error) { return `{}`, nil }}); err != nil {
		t.Fatal(err)
	}
	_ = fh.Restart(ctx)

	// Install failure: an explicit error, nothing runs, nothing is handed over.
	caps = both
	fh.FailInstall(errors.New("permissions not installed"))
	if _, err := s.resume(ctx, s.get(t, a.AgentID)); !isCode(err, CodeHarnessError) || !strings.Contains(err.Error(), "install permissions") {
		t.Fatalf("resume with a failed install = %v", err)
	}
	if _, turns := fh.Rules(ref); len(turns) != 1 {
		t.Fatalf("the cut-off turn ran %d times after a failed install", len(turns))
	}
	if slots, _ := e.st.Slots(ctx, a.AgentID); len(slots) != 1 || slots[0].State != loomstore.SlotWaiting {
		t.Fatalf("slots after a failed install = %+v", slots)
	}

	// Installed: the current policy (bridge denies now registered) replaces
	// the stale one before the resumed turn runs; the user denies stay.
	fh.FailInstall(nil)
	got, err := s.resume(ctx, s.get(t, a.AgentID))
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Concat(user, publishDenies)
	installed, turns := fh.Rules(ref)
	if !slices.Equal(installed, want) || len(turns) != 2 || !slices.Equal(turns[1], want) {
		t.Fatalf("installed %+v, runs %+v; want %+v", installed, turns, want)
	}
	// Caps removed: the bridge denies go, the user's stay.
	caps = BridgeCaps{}
	if _, err := s.resume(ctx, got); err != nil {
		t.Fatal(err)
	}
	if installed, _ := fh.Rules(ref); !slices.Equal(installed, user) {
		t.Fatalf("after caps removal installed %+v, want %+v", installed, user)
	}
	// Identity and ownership are unchanged: same session, root and spec version.
	after := s.get(t, a.AgentID)
	owned, _ := e.st.NativeSessions(ctx, a.AgentID)
	if *after.HarnessSessionID != ref.NativeID || *after.HarnessSessionRoot != ref.Root || after.SpecVersion != row.SpecVersion || len(owned) != 1 {
		t.Fatalf("resume changed identity: %+v, owned %+v", after, owned)
	}
}

// TestCreateRefusesSharedBridgeFolder: OpenCode runs one MCP bridge per
// folder, so a worktree that a live agent already has is refused with
// worktree_taken when either agent has bridge tools, before any session
// opens; agents without tools may share, and a deleted agent's folder is
// free.
func TestCreateRefusesSharedBridgeFolder(t *testing.T) {
	ctx := context.Background()
	worker := func(id string) CreateRequest { // a preset with no bridge tools
		return CreateRequest{Envelope: Envelope{RequestID: id}, Preset: "daemon-worker", Name: id, Repo: "/repo", BaseRef: "main",
			Overrides: Overrides{Harness: "opencode"}}
	}
	for name, c := range map[string]struct {
		first, second CreateRequest
		deleteFirst   bool
		want          Code
	}{
		"lead then lead":         {leadReq("l1"), CreateRequest{Envelope: Envelope{RequestID: "l2"}, Preset: "lead", Name: "beta", Repo: "/repo", BaseRef: "main", Overrides: Overrides{Harness: "opencode"}}, false, CodeWorktreeTaken},
		"lead then worker":       {leadReq("l1"), worker("w1"), false, CodeWorktreeTaken},
		"worker then lead":       {worker("w1"), leadReq("l1"), false, CodeWorktreeTaken},
		"worker then worker":     {worker("w1"), worker("w2"), false, ""},
		"deleted lead then lead": {leadReq("l1"), CreateRequest{Envelope: Envelope{RequestID: "l2"}, Preset: "lead", Name: "beta", Repo: "/repo", BaseRef: "main", Overrides: Overrides{Harness: "opencode"}}, true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			e := newCreateEnv(t)
			e.ws.path = "/wt/shared"
			s := e.service(ServiceConfig{})
			first, err := s.Create(ctx, c.first)
			if err != nil {
				t.Fatal(err)
			}
			if c.deleteFirst {
				if err := s.store.Tombstone(ctx, first.AgentID, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			opened := len(e.h.specs)
			_, err = s.Create(ctx, c.second)
			if c.want == "" {
				if err != nil {
					t.Fatalf("second Create: %v", err)
				}
				return
			}
			wantCode(t, err, c.want)
			if len(e.h.specs) != opened {
				t.Fatal("a session opened in a folder that belongs to another agent")
			}
		})
	}
}

// TestCreateUnwiredHarnessLeavesNoAgent: a harness this server does not run
// is refused before the row is saved, with a message naming the ones it runs,
// so a retry on a wired harness can reuse the name.
func TestCreateUnwiredHarnessLeavesNoAgent(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	req := leadReq("r1")
	req.Overrides.Harness = "codex"
	_, err := s.Create(ctx, req)
	if got := wantCode(t, err, CodeHarnessUnavailable); got.Message != "codex is not available on this server; use opencode" {
		t.Fatalf("message = %q", got.Message)
	}
	if _, err := s.Create(ctx, leadReq("r2")); err != nil {
		t.Fatalf("retry on opencode = %v", err)
	}
	_, err = s.Create(ctx, leadReq("r3"))
	if got := wantCode(t, err, CodeAgentNameTaken); got.Message != `an agent named "alpha" already exists` {
		t.Fatalf("message = %q", got.Message)
	}
}

// CR1: a lead with no base_ref and no parent branch to start from is refused
// before any side effect, not after its row is written.
func TestCreateNeedsBaseRefBeforeRow(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	req := leadReq("r1")
	req.BaseRef = ""
	got := wantCode(t, mustFail(s.Create(ctx, req)), CodePresetInvalid)
	if !strings.Contains(got.Message, "base_ref") {
		t.Fatalf("message = %q", got.Message)
	}
	rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
	if len(rows) != 0 || len(e.ws.ensured) != 0 || len(e.h.specs) != 0 {
		t.Fatalf("side effects: rows %d, ensures %d, opens %d", len(rows), len(e.ws.ensured), len(e.h.specs))
	}
}

// CR1: a Create that fails after its row is written shows create_incomplete
// at once, rather than sitting in creating with nothing to say why.
func TestCreateFailureAfterRowShowsAttention(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{Launch: func(context.Context, loomstore.Agent, string) (loomharness.Launch, error) {
		return loomharness.Launch{}, errors.New("launch broke")
	}})
	if _, err := s.Create(ctx, leadReq("r1")); err == nil {
		t.Fatal("Create succeeded")
	}
	rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
	if len(rows) != 1 || deref(rows[0].AttentionReason) != AttentionCreateIncomplete {
		t.Fatalf("rows = %+v", rows)
	}
	a, err := e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) // a retry finishes it and clears the Attention
	if err != nil {
		t.Fatal(err)
	}
	row, _ := e.st.GetAgent(ctx, a.AgentID)
	if row.State != StateIdle || row.AttentionReason != nil {
		t.Fatalf("after retry: state %s attention %v", row.State, deref(row.AttentionReason))
	}
}

func mustFail(_ AgentInfo, err error) error { return err }

// A reconcile that saw a Create in flight (its row not yet past step 1) and
// failed to finish it must not flag the agent once that Create has finished:
// create_incomplete is raised only on an agent still creating.
func TestLateCreateIncompleteSkipsFinishedAgent(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	a, err := s.Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	s.failed(ctx, a.AgentID, AttentionCreateIncomplete, errors.New("loomagent: was not fully inserted; retry its Create"))
	if row, _ := e.st.GetAgent(ctx, a.AgentID); row.AttentionReason != nil {
		t.Fatalf("Attention = %q on a created agent", *row.AttentionReason)
	}
}

// The other order: a reconcile flags create_incomplete while a Create is in
// flight (row inserted, step 1 not yet recorded); the Create then finishes
// and clears it, leaving no stale Attention on the idle agent.
func TestCreateClearsCreateIncompleteRaisedInFlight(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	createCrash = func(p string) {
		if p != "row" {
			return
		}
		rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
		_, err := s.finishCreate(ctx, rows[0].AgentID) // what Reconcile does with a creating row
		s.failed(ctx, rows[0].AgentID, AttentionCreateIncomplete, err)
		if r, _ := e.st.GetAgent(ctx, rows[0].AgentID); deref(r.AttentionReason) != AttentionCreateIncomplete {
			t.Errorf("in flight: Attention = %v; want create_incomplete raised", deref(r.AttentionReason))
		}
	}
	t.Cleanup(func() { createCrash = func(string) {} })
	a, err := s.Create(ctx, leadReq("r1"))
	if err != nil {
		t.Fatal(err)
	}
	row, _ := e.st.GetAgent(ctx, a.AgentID)
	if row.State != StateIdle || row.AttentionReason != nil {
		t.Fatalf("after Create: state %s Attention %v; want idle with none", row.State, deref(row.AttentionReason))
	}
}

// A Create whose first message cannot be queued (step 1, right after the row
// is written) shows create_incomplete too, not a bare creating row.
func TestCreateQueueFirstFailureShowsAttention(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	createCrash = func(p string) {
		if p != "row" {
			return
		}
		rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
		if _, _, err := e.st.Send(ctx, loomstore.SlotSend{AgentID: rows[0].AgentID, Sender: "user:local", RequestID: "busy",
			Body: "x", Hand: true, NativeKey: "k", Result: func(bool) (string, error) { return `{}`, nil }}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { createCrash = func(string) {} })
	req := leadReq("r1")
	req.FirstMessage = "hello"
	if _, err := s.Create(ctx, req); !errors.Is(err, loomstore.ErrSlotBusy) {
		t.Fatalf("Create = %v; want the queueFirst failure", err)
	}
	rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
	if len(rows) != 1 || rows[0].State != StateCreating || deref(rows[0].AttentionReason) != AttentionCreateIncomplete {
		t.Fatalf("rows = %d, state %s, Attention %v; want one creating row with create_incomplete",
			len(rows), rows[0].State, deref(rows[0].AttentionReason))
	}
}

// A Create whose request is cancelled after its row is written still saves
// create_incomplete: the mark does not use the cancelled request context.
func TestCreateCancelAfterRowShowsAttention(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	createCrash = func(p string) {
		if p == "row" {
			cancel()
		}
	}
	t.Cleanup(func() { createCrash = func(string) {} })
	req := leadReq("r1")
	req.FirstMessage = "hello"
	if _, err := s.Create(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create = %v; want context.Canceled", err)
	}
	rows, _, _ := e.st.ListAgents(context.Background(), loomstore.AgentFilter{IncludeArchived: true})
	if len(rows) != 1 || rows[0].State != StateCreating || deref(rows[0].AttentionReason) != AttentionCreateIncomplete {
		t.Fatalf("rows = %d, state %s, Attention %v; want one creating row with create_incomplete",
			len(rows), rows[0].State, deref(rows[0].AttentionReason))
	}
}
