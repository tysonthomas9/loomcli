package operationalview_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	agentsowner "github.com/tysonthomas9/loomcli/internal/modules/agents"
)

type agentRecords struct {
	agents []*agentsowner.Agent
	roles  []*agentsowner.Role
	err    error
}

func (records *agentRecords) ListAgents(
	context.Context,
	string,
	agentsowner.AgentFilter,
) ([]*agentsowner.Agent, error) {
	return records.agents, records.err
}

func (records *agentRecords) ListRoles(context.Context, string) ([]*agentsowner.Role, error) {
	return records.roles, records.err
}

func TestWorkspaceRosterQueryProjectsOwnerRecordsDefensively(t *testing.T) {
	metadata, err := agentsowner.WithRuntimeMetadata(nil, agentsowner.RuntimeMetadata{
		RoleKind: "interactive", Backend: "codex", Repos: []string{"loom"},
		RepoGroups: []string{"core"}, CrossRepo: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	records := &agentRecords{
		agents: []*agentsowner.Agent{
			{WorkspaceKey: "ALPHA", AgentID: "zeta", Behavior: agentsowner.BehaviorReference{RoleName: "review"}, Metadata: metadata},
			{WorkspaceKey: "ALPHA", AgentID: "alpha", Behavior: agentsowner.BehaviorReference{RoleName: "review"}},
		},
		roles: []*agentsowner.Role{{WorkspaceKey: "ALPHA", Name: "review", Kind: "reviewer", Backend: "claude"}},
	}
	view := &operationalview.Workspace{ID: "ALPHA"}
	if err := operationalview.NewWorkspaceRosterQuery(records).Project(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	if got := []string{view.Agents[0].Name, view.Agents[1].Name}; !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("Agent order = %v", got)
	}
	if got := view.Agents[1]; got.Kind != "interactive" || got.Backend != "codex" ||
		!reflect.DeepEqual(got.Repos, []string{"loom"}) || !reflect.DeepEqual(got.RepoGroups, []string{"core"}) || !got.CrossRepo {
		t.Fatalf("projected Agent = %#v", got)
	}
	metadata[agentsowner.MetadataRepos] = `["mutated"]`
	if !reflect.DeepEqual(view.Agents[1].Repos, []string{"loom"}) {
		t.Fatalf("projected Repos retained mutable owner state: %v", view.Agents[1].Repos)
	}
}

func TestWorkspaceRosterQueryRejectsInvalidOwnerState(t *testing.T) {
	view := &operationalview.Workspace{ID: "ALPHA"}
	records := &agentRecords{agents: []*agentsowner.Agent{{
		WorkspaceKey: "ALPHA", AgentID: "orphan", Behavior: agentsowner.BehaviorReference{RoleName: "missing"},
	}}}
	err := operationalview.NewWorkspaceRosterQuery(records).Project(context.Background(), view)
	if !errors.Is(err, agentsowner.ErrInvalidPersistedState) {
		t.Fatalf("Project error = %v, want Agents invalid persisted state", err)
	}
}

func TestWorkspaceRosterQueryFailsClosedToEmptyWhenUnavailable(t *testing.T) {
	view := &operationalview.Workspace{ID: "ALPHA", Agents: []operationalview.Agent{{Name: "stale"}}}
	if err := operationalview.NewWorkspaceRosterQuery(nil).Project(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	if len(view.Agents) != 0 || view.Agents == nil {
		t.Fatalf("unavailable roster = %#v, want explicit empty slice", view.Agents)
	}
}
