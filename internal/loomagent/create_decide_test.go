package loomagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestDecideCreateTable is Create's check-and-row table: from the request
// and the facts the shell read (a prior Create, the preset, the name's
// owner, the repo, the parent, the base check), decideCreate returns a
// replay of the prior Create, a refusal, or the row to insert. No fakes.
func TestDecideCreateTable(t *testing.T) {
	lead := Preset{Name: "lead", Version: 2, Mode: "persistent", RoleKind: "interactive", OwnerKind: "user"}
	task := Preset{Name: "task", Version: 1, Mode: "single_task", RoleKind: "worker", OwnerKind: "parent"}
	req := func(p, name, base, parent string) CreateRequest {
		return CreateRequest{Envelope: Envelope{RequestID: "r1"}, Preset: p, Name: name, Repo: "/repo", BaseRef: base,
			Parent: parent, Actor: user}
	}
	leadBranch, root := "loom/agent/agt_lead", "agt_root"
	parent := svcAgent("agt_lead", "persistent", StateIdle)
	parent.Branch, parent.RootAgentID = &leadBranch, &root
	prior := svcAgent("agt_0", "persistent", StateIdle)
	prior.Preset, prior.Repo, prior.OwnerKind, prior.OwnerID = "lead", "/repo", "user", "u"
	xkey := req("lead", "alpha", "main", "")
	xkey.ExternalKey = "k1"
	conflict := xkey
	conflict.Repo = "/other"
	in := func(r CreateRequest, p Preset) createInput {
		return createInput{Req: r, Preset: p, WorkspaceID: "ws", ID: "agt_1"}
	}
	for _, c := range []struct {
		name string
		in   createInput
		want string // the decision, or the refusal's code
	}{
		{"duplicate create_request_id", func() createInput { i := in(req("lead", "alpha", "main", ""), Preset{}); i.Prior = &prior; return i }(),
			"replay agt_0"},
		{"duplicate external key, same spec", func() createInput { i := in(xkey, lead); i.Prior = &prior; return i }(), "replay agt_0"},
		{"duplicate external key, other spec", func() createInput { i := in(conflict, lead); i.Prior = &prior; return i }(),
			"external_key_conflict: k1 exists with a different spec"},
		{"name taken", func() createInput { i := in(req("lead", "alpha", "main", ""), lead); i.Taken = true; return i }(),
			`agent_name_taken: an agent named "alpha" already exists`},
		{"no repo", in(CreateRequest{Envelope: Envelope{RequestID: "r1"}, Preset: "task", Name: "t", Parent: "agt_lead"}, task),
			"preset_invalid: Create needs a Name and a Repo"},
		{"no name", in(CreateRequest{Envelope: Envelope{RequestID: "r1"}, Preset: "task", Repo: "/repo", Parent: "agt_lead"}, task),
			"preset_invalid: Create needs a Name and a Repo"},
		{"task without a parent", in(req("task", "t", "main", ""), task), "preset_invalid: task needs a Parent"},
		{"missing base_ref, no parent branch", in(req("lead", "alpha", "", ""), lead),
			"preset_invalid: Create needs a base_ref, the branch or commit the agent starts from"},
		{"unknown base_ref", func() createInput {
			i := in(req("lead", "alpha", "nope", ""), lead)
			i.BaseErr = errors.New("unknown revision")
			return i
		}(), `preset_invalid: base_ref "nope" is not a branch or commit in /repo: unknown revision`},
		{"dead parent", func() createInput {
			i := in(req("task", "t", "", "agt_lead"), task)
			i.ParentErr = &Error{Code: CodeAgentNotFound, Message: "agt_lead is deleted"}
			return i
		}(), "agent_not_found: agt_lead is deleted"},
		{"valid lead", in(req("lead", "alpha", "main", ""), lead),
			"row agt_1 ws alpha lead@2 persistent interactive interactive owner=user:u by=user:u parent= root= base=main branch=loom/agent/agt_1 creating profile=alpha req=r1 repo=/repo subject=// xkey= spec_v=1 step=0 harness= model= spec="},
		{"lead named from the environment", func() createInput { i := in(req("lead", "", "main", ""), lead); i.EnvName = "boss"; return i }(),
			"row agt_1 ws boss lead@2 persistent interactive interactive owner=user:u by=user:u parent= root= base=main branch=loom/agent/agt_1 creating profile=boss req=r1 repo=/repo subject=// xkey= spec_v=1 step=0 harness= model= spec="},
		{"valid task from its lead's branch", func() createInput { i := in(req("task", "t", "", "agt_lead"), task); i.Parent = parent; return i }(),
			"row agt_1 ws t task@1 single_task worker background owner=agent:agt_lead by=user:u parent=agt_lead root=agt_root base=loom/agent/agt_lead branch=loom/agent/agt_1 creating profile=t req=r1 repo=/repo subject=// xkey= spec_v=1 step=0 harness= model= spec="},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := createDecisionString(decideCreate(c.in)); got != c.want {
				t.Fatalf("decideCreate = %q; want %q", got, c.want)
			}
		})
	}
}

// createDecisionString is a readable form of decideCreate's outcome.
func createDecisionString(d createDecision, err error) string {
	if e, ok := err.(*Error); ok {
		return string(e.Code) + ": " + e.Message
	} else if err != nil {
		return err.Error()
	}
	a := d.Row
	if d.Replay {
		return "replay " + a.AgentID
	}
	return fmt.Sprintf("row %s %s %s %s@%s %s %s %s owner=%s:%s by=%s:%s parent=%s root=%s base=%s branch=%s %s "+
		"profile=%s req=%s repo=%s subject=%s/%s/%s xkey=%s spec_v=%d step=%d harness=%s model=%s spec=%s",
		a.AgentID, a.WorkspaceID, a.Name, a.Preset, a.PresetVersion, a.Mode, a.RoleKind, a.InteractionMode,
		a.OwnerKind, a.OwnerID, a.CreatedByKind, a.CreatedByID, deref(a.ParentAgentID), deref(a.RootAgentID),
		deref(a.BaseRef), deref(a.Branch), a.State, a.ProfileKey, a.CreateRequestID, a.Repo, deref(a.SubjectType),
		deref(a.SubjectID), deref(a.SubjectVersion), deref(a.ExternalKey), a.SpecVersion, a.CreateStep, a.Harness, deref(a.Model), a.SpecJSON)
}

// unknownBase is a workspace whose CheckBase knows no ref.
type unknownBase struct{ *fakeWorkspace }

func (unknownBase) CheckBase(context.Context, string, string) error {
	return errors.New("unknown revision")
}

// TestCreateUnknownBaseBeforeInsert: the shell's base check reports an
// unknown base_ref, so Create fails invalid and inserts no row, ensures no
// worktree and opens no session.
func TestCreateUnknownBaseBeforeInsert(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	s.workspace = unknownBase{e.ws}
	wantCode(t, mustFail(s.Create(ctx, leadReq("r1"))), CodePresetInvalid)
	rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
	if len(rows) != 0 || len(e.ws.ensured) != 0 || len(e.h.specs) != 0 {
		t.Fatalf("side effects: rows %d, ensures %d, opens %d", len(rows), len(e.ws.ensured), len(e.h.specs))
	}
}

// TestCreateCrashAtInsertCommit: a Create crashes just after its row and
// first message commit (step 1, before the worktree); after a restart the
// same Create finishes that one agent, with its first message handed over
// once and agent.created saved once.
func TestCreateCrashAtInsertCommit(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	req := leadReq("r1")
	req.FirstMessage = "hello"
	if !crashAt(t, "inserted")(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, req) }) {
		t.Fatal("Create did not crash")
	}
	rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
	if len(rows) != 1 || rows[0].CreateStep != stepRow || len(e.ws.ensured) != 0 {
		t.Fatalf("at the crash: rows %d, ensures %d; want one row at step 1 and no worktree", len(rows), len(e.ws.ensured))
	}
	s := e.service(ServiceConfig{}) // restart
	runDispatcher(t, s)
	a, err := s.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	settled(t, s)
	rows, _, _ = e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
	slots, _ := e.st.Slots(ctx, a.AgentID)
	if len(rows) != 1 || rows[0].AgentID != a.AgentID || rows[0].CreateStep != stepDone || len(slots) != 1 ||
		slots[0].State != loomstore.SlotHanded || e.events(t, a.AgentID, KindAgentCreated) != 1 {
		t.Fatalf("after restart: rows %d step %d slots %+v created %d; want the one agent finished, its message handed once",
			len(rows), rows[0].CreateStep, slots, e.events(t, a.AgentID, KindAgentCreated))
	}
}

// TestDecideCreatePrecedence pins Create's refusal order: with every fact
// failing at once, a failed read (a store error, a repo that does not
// resolve, a dead parent, an unknown base) never replaces an earlier
// refusal. Each step clears the refusal before it and gets the next.
func TestDecideCreatePrecedence(t *testing.T) {
	task := Preset{Name: "task", Version: 1, Mode: "single_task", RoleKind: "worker", OwnerKind: "parent"}
	in := createInput{Req: CreateRequest{Envelope: Envelope{RequestID: "r1"}, Preset: "task", Actor: user},
		Preset: task, WorkspaceID: "ws", ID: "agt_1",
		TakenErr: errors.New("store down"), Taken: true, RepoErr: errors.New("repo does not resolve"),
		ParentErr: &Error{Code: CodeAgentNotFound, Message: "agt_lead is deleted"}, BaseErr: errors.New("unknown revision")}
	for _, step := range []struct {
		fix  func(*createInput)
		want string
	}{
		{func(*createInput) {}, "preset_invalid: Create needs a Name and a Repo"},
		{func(i *createInput) { i.Req.Name, i.Req.Repo = "t", "/repo" }, "preset_invalid: task needs a Parent"},
		{func(i *createInput) { i.Req.Parent = "agt_lead" }, "store down"},
		{func(i *createInput) { i.TakenErr = nil }, `agent_name_taken: an agent named "t" already exists`},
		{func(i *createInput) { i.Taken = false }, "repo does not resolve"},
		{func(i *createInput) { i.RepoErr = nil }, "agent_not_found: agt_lead is deleted"},
		{func(i *createInput) { i.Req.BaseRef = "nope" }, "agent_not_found: agt_lead is deleted"},
		{func(i *createInput) { i.ParentErr = nil }, `preset_invalid: base_ref "nope" is not a branch or commit in /repo: unknown revision`},
		{func(i *createInput) { i.Req.BaseRef = "" }, "preset_invalid: Create needs a base_ref, the branch or commit the agent starts from"},
		{func(i *createInput) { i.Req.BaseRef, i.BaseErr = "nope", nil }, "row"},
	} {
		step.fix(&in)
		got := "row"
		if _, err := decideCreate(in); err != nil {
			got = err.Error()
			if e, ok := err.(*Error); ok {
				got = string(e.Code) + ": " + e.Message
			}
		}
		if got != step.want {
			t.Fatalf("decideCreate = %q; want %q", got, step.want)
		}
	}
}

// lookupSpy counts the repo resolutions and base checks a Create does;
// every repo resolves and every base check fails, as for an unknown ref.
type lookupSpy struct {
	*fakeWorkspace
	repos, bases int
}

func (l *lookupSpy) CheckBase(context.Context, string, string) error {
	l.bases++
	return errors.New("unknown revision")
}

func (l *lookupSpy) resolve(context.Context, Target, string) (string, error) {
	l.repos++
	return "/repo", nil
}

// TestCreateLookupsKeepPrecedence: through the shell, a Create with no name
// or no repo touches neither the repo nor git, and is refused for that;
// one whose name is taken is refused agent_name_taken although its base
// check fails too.
func TestCreateLookupsKeepPrecedence(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	spy := &lookupSpy{fakeWorkspace: e.ws}
	if _, err := e.service(ServiceConfig{}).Create(ctx, leadReq("r0")); err != nil { // takes the name alpha
		t.Fatal(err)
	}
	s := e.service(ServiceConfig{ResolveRepo: spy.resolve})
	s.workspace = spy
	for _, c := range []struct {
		name       string
		edit       func(*CreateRequest)
		want       Code
		noLookups  bool // with no name or repo nothing is looked up
		wantPrefix string
	}{
		{"no repo", func(r *CreateRequest) { r.Repo = "" }, CodePresetInvalid, true, "Create needs a Name and a Repo"},
		{"no name", func(r *CreateRequest) { r.Preset, r.Name = "task", "" }, CodePresetInvalid, true, "Create needs a Name and a Repo"},
		{"name taken", func(*CreateRequest) {}, CodeAgentNameTaken, false, "an agent named"},
	} {
		spy.repos, spy.bases = 0, 0
		req := leadReq("r-" + c.name)
		c.edit(&req)
		got := wantCode(t, mustFail(s.Create(ctx, req)), c.want)
		if !strings.HasPrefix(got.Message, c.wantPrefix) || (c.noLookups && spy.repos+spy.bases > 0) {
			t.Fatalf("%s: %q, %d repo and %d base lookups; want %q, no lookups %t", c.name, got.Message, spy.repos, spy.bases,
				c.wantPrefix, c.noLookups)
		}
	}
}
