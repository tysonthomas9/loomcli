package svcimpl

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestAgentServiceStoreCRUDAndLifecycle(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	svc := NewAgentService(&fakeGitOps{}, nil, nil, st)

	created, err := svc.CreateAgent(ctx, service.AgentCreateInput{
		WorkspaceKey: "WS",
		Name:         "lead-1",
		RoleName:     " Lead ",
		Auto:         true,
		Backend:      "codex",
		DesiredState: domain.AgentDesiredRunning,
	})
	if err != nil {
		t.Fatalf("CreateAgent lead: %v", err)
	}
	if created.RoleName != "lead" || !created.Auto {
		t.Fatalf("created agent = %+v", created)
	}
	if _, err := st.Roles().Get(ctx, "WS", "lead"); err != nil {
		t.Fatalf("lead role was not ensured: %v", err)
	}

	agents, err := svc.ListAgents(ctx, "WS")
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 1 || agents[0].Name != "lead-1" {
		t.Fatalf("agents = %+v", agents)
	}

	role := "task"
	auto := false
	updated, err := svc.UpdateAgent(ctx, "WS", "lead-1", service.AgentUpdateInput{
		RoleName: &role,
		Auto:     &auto,
	})
	if err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	if updated.RoleName != "task" || updated.Auto {
		t.Fatalf("updated = %+v", updated)
	}

	lifecycle, err := svc.RequestAgentLifecycle(ctx, "WS", "lead-1", service.AgentLifecycleInput{
		State:        domain.AgentStateStopped,
		DesiredState: domain.AgentDesiredStopped,
		CommandType:  "stop",
		Payload:      map[string]string{"reason": "test"},
	})
	if err != nil {
		t.Fatalf("RequestAgentLifecycle: %v", err)
	}
	if lifecycle.State != domain.AgentStateStopped || lifecycle.DesiredState != domain.AgentDesiredStopped {
		t.Fatalf("lifecycle agent = %+v", lifecycle)
	}
	commands, err := st.AgentCommands().List(ctx, "WS", store.AgentCommandFilter{
		TargetAgentID: "lead-1",
		Status:        domain.AgentCommandQueued,
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(commands) != 1 || commands[0].Type != "stop" {
		t.Fatalf("commands = %+v", commands)
	}

	if err := svc.DeleteAgent(ctx, "WS", "lead-1"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if _, err := st.Agents().Get(ctx, "WS", "lead-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("agent still exists or unexpected error: %v", err)
	}
}

func TestAgentServiceStoreValidationAndUnavailableBranches(t *testing.T) {
	ctx := context.Background()
	noStore := NewAgentService(&fakeGitOps{}, nil, nil, nil)
	if _, err := noStore.ListAgents(ctx, "WS"); err == nil {
		t.Fatal("ListAgents without store succeeded")
	}
	if _, err := noStore.CreateAgent(ctx, service.AgentCreateInput{}); err == nil {
		t.Fatal("CreateAgent without store succeeded")
	}
	if _, err := noStore.UpdateAgent(ctx, "WS", "agent", service.AgentUpdateInput{}); err == nil {
		t.Fatal("UpdateAgent without store succeeded")
	}
	if _, err := noStore.RequestAgentLifecycle(ctx, "WS", "agent", service.AgentLifecycleInput{CommandType: "stop"}); err == nil {
		t.Fatal("RequestAgentLifecycle without store succeeded")
	}
	if err := noStore.DeleteAgent(ctx, "WS", "agent"); err == nil {
		t.Fatal("DeleteAgent without store succeeded")
	}

	st := memstore.New()
	svc := NewAgentService(&fakeGitOps{}, nil, nil, st)
	if _, err := svc.ListAgents(ctx, ""); err == nil {
		t.Fatal("ListAgents empty workspace succeeded")
	}
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{WorkspaceKey: "WS", RoleName: "task"}); err == nil {
		t.Fatal("CreateAgent missing name succeeded")
	}
	if _, err := svc.CreateAgent(ctx, service.AgentCreateInput{WorkspaceKey: "WS", Name: "agent"}); err == nil {
		t.Fatal("CreateAgent missing role succeeded")
	}
	if _, err := svc.RequestAgentLifecycle(ctx, "WS", "../bad", service.AgentLifecycleInput{CommandType: "stop"}); err == nil {
		t.Fatal("RequestAgentLifecycle invalid name succeeded")
	}
	if _, err := svc.RequestAgentLifecycle(ctx, "WS", "agent", service.AgentLifecycleInput{CommandType: "dance"}); err == nil {
		t.Fatal("RequestAgentLifecycle invalid command succeeded")
	}
	if err := svc.DeleteAgent(ctx, "WS", "../bad"); err == nil {
		t.Fatal("DeleteAgent invalid name succeeded")
	}
}
