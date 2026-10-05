package loomagent

import (
	"context"
	"errors"
	"fmt"
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
			string(CodeExternalKeyConflict)},
		{"name taken", func() createInput { i := in(req("lead", "alpha", "main", ""), lead); i.Taken = true; return i }(),
			string(CodeAgentNameTaken)},
		{"no name or repo", in(CreateRequest{Envelope: Envelope{RequestID: "r1"}, Preset: "task", Name: "t"}, task),
			string(CodePresetInvalid)},
		{"task without a parent", in(req("task", "t", "main", ""), task), string(CodePresetInvalid)},
		{"missing base_ref, no parent branch", in(req("lead", "alpha", "", ""), lead), string(CodePresetInvalid)},
		{"unknown base_ref", func() createInput {
			i := in(req("lead", "alpha", "nope", ""), lead)
			i.BaseErr = errors.New("unknown revision")
			return i
		}(), string(CodePresetInvalid)},
		{"dead parent", func() createInput {
			i := in(req("task", "t", "", "agt_lead"), task)
			i.ParentErr = &Error{Code: CodeAgentNotFound, Message: "agt_lead is deleted"}
			return i
		}(), string(CodeAgentNotFound)},
		{"valid lead", in(req("lead", "alpha", "main", ""), lead),
			"row agt_1 ws alpha lead@2 persistent interactive interactive owner=user:u by=user:u parent= root= base=main branch=loom/agent/agt_1 creating"},
		{"lead named from the environment", func() createInput { i := in(req("lead", "", "main", ""), lead); i.EnvName = "boss"; return i }(),
			"row agt_1 ws boss lead@2 persistent interactive interactive owner=user:u by=user:u parent= root= base=main branch=loom/agent/agt_1 creating"},
		{"valid task from its lead's branch", func() createInput { i := in(req("task", "t", "", "agt_lead"), task); i.Parent = parent; return i }(),
			"row agt_1 ws t task@1 single_task worker background owner=agent:agt_lead by=user:u parent=agt_lead root=agt_root base=loom/agent/agt_lead branch=loom/agent/agt_1 creating"},
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
		return string(e.Code)
	} else if err != nil {
		return err.Error()
	}
	a := d.Row
	if d.Replay {
		return "replay " + a.AgentID
	}
	return fmt.Sprintf("row %s %s %s %s@%s %s %s %s owner=%s:%s by=%s:%s parent=%s root=%s base=%s branch=%s %s",
		a.AgentID, a.WorkspaceID, a.Name, a.Preset, a.PresetVersion, a.Mode, a.RoleKind, a.InteractionMode,
		a.OwnerKind, a.OwnerID, a.CreatedByKind, a.CreatedByID, deref(a.ParentAgentID), deref(a.RootAgentID),
		deref(a.BaseRef), deref(a.Branch), a.State)
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

// TestCreateCrashAtInsertCommit: a Create crashes at its row's commit; after
// a restart the same Create finishes one agent, with its first message
// handed over once and agent.created saved once.
func TestCreateCrashAtInsertCommit(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	req := leadReq("r1")
	req.FirstMessage = "hello"
	crashCommit(t, 1)
	if !panics(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, req) }) {
		t.Fatal("Create did not crash")
	}
	s := e.service(ServiceConfig{}) // restart
	runDispatcher(t, s)
	a, err := s.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	settled(t, s)
	rows, _, _ := e.st.ListAgents(ctx, loomstore.AgentFilter{IncludeArchived: true})
	slots, _ := e.st.Slots(ctx, a.AgentID)
	if len(rows) != 1 || rows[0].CreateStep != stepDone || len(slots) != 1 || slots[0].State != loomstore.SlotHanded ||
		e.events(t, a.AgentID, KindAgentCreated) != 1 {
		t.Fatalf("after restart: rows %d step %d slots %+v created %d; want one finished agent, its message handed once",
			len(rows), rows[0].CreateStep, slots, e.events(t, a.AgentID, KindAgentCreated))
	}
}
