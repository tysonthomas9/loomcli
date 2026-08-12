package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type workerNodePortStub struct {
	registerCalls  int
	heartbeatCalls int
	drainCalls     int
	register       RegisterWorkerNodeCommand
	heartbeat      HeartbeatWorkerNodeCommand
	drain          SetWorkerNodeDrainCommand
}

func (stub *workerNodePortStub) RegisterWorkerNode(_ context.Context, command RegisterWorkerNodeCommand) (*WorkerNode, error) {
	stub.registerCalls++
	stub.register = command
	return &WorkerNode{
		WorkspaceKey: command.WorkspaceKey, NodeID: command.NodeID, OwnerActor: command.OwnerActor,
		RuntimeProvider: command.RuntimeProvider, DrainState: WorkerNodeActive,
	}, nil
}

func (stub *workerNodePortStub) HeartbeatWorkerNode(_ context.Context, command HeartbeatWorkerNodeCommand) (*WorkerNode, error) {
	stub.heartbeatCalls++
	stub.heartbeat = command
	return &WorkerNode{
		WorkspaceKey: command.WorkspaceKey, NodeID: command.NodeID, DrainState: WorkerNodeActive,
		LastHeartbeat: command.HeartbeatAt, ExpiresAt: command.HeartbeatAt.Add(command.TTL),
	}, nil
}

func (stub *workerNodePortStub) SetWorkerNodeDrain(_ context.Context, command SetWorkerNodeDrainCommand) (*WorkerNode, error) {
	stub.drainCalls++
	stub.drain = command
	return &WorkerNode{WorkspaceKey: command.WorkspaceKey, NodeID: command.NodeID, DrainState: command.DrainState}, nil
}

func TestWorkerNodeMutationsRequireExactSystemScopeAndConvergeOnRetry(t *testing.T) {
	port := &workerNodePortStub{}
	service, issuer := newTestService(t, Dependencies{Workers: WorkerDependencies{
		Registration: port, Heartbeats: port, Drain: port,
	}})
	now := time.Now().UTC()
	register := RegisterWorkerNodeCommand{
		WorkspaceKey: "TEST", RequestID: "register-node-1", NodeID: "node-1",
		OwnerActor: "loom", RuntimeProvider: "local", TTL: time.Minute, RegisteredAt: now,
	}
	for range 2 {
		if _, err := service.RegisterWorkerNode(context.Background(), issueSystem(t, issuer, ActionRegisterWorkerNode), register); err != nil {
			t.Fatal(err)
		}
	}
	heartbeat := HeartbeatWorkerNodeCommand{
		WorkspaceKey: "TEST", RequestID: "heartbeat-node-1", NodeID: "node-1",
		TTL: time.Minute, HeartbeatAt: now,
	}
	for range 2 {
		if _, err := service.HeartbeatWorkerNode(context.Background(), issueSystem(t, issuer, ActionHeartbeatWorkerNode), heartbeat); err != nil {
			t.Fatal(err)
		}
	}
	drain := SetWorkerNodeDrainCommand{
		WorkspaceKey: "TEST", RequestID: "drain-node-1", NodeID: "node-1",
		DrainState: WorkerNodeDraining, ChangedAt: now,
	}
	for range 2 {
		if _, err := service.SetWorkerNodeDrain(context.Background(), issueSystem(t, issuer, ActionSetWorkerNodeDrain), drain); err != nil {
			t.Fatal(err)
		}
	}
	if port.registerCalls != 2 || port.heartbeatCalls != 2 || port.drainCalls != 2 {
		t.Fatalf("calls register=%d heartbeat=%d drain=%d", port.registerCalls, port.heartbeatCalls, port.drainCalls)
	}

	if _, err := service.RegisterWorkerNode(context.Background(), issueSystem(t, issuer, ActionHeartbeatWorkerNode), register); err == nil {
		t.Fatal("wrong-action SystemAuthority was accepted")
	}
	if port.registerCalls != 2 {
		t.Fatal("wrong-action request reached the mutation port")
	}
	invalid := register
	invalid.RequestID = ""
	if _, err := service.RegisterWorkerNode(context.Background(), issueSystem(t, issuer, ActionRegisterWorkerNode), invalid); err == nil {
		t.Fatal("invalid registration was accepted")
	}
	if port.registerCalls != 2 {
		t.Fatal("invalid registration reached the mutation port")
	}
}

func TestWorkerNodeMutationsCanonicalizeScopeBeforeCallingPorts(t *testing.T) {
	port := &workerNodePortStub{}
	service, issuer := newTestService(t, Dependencies{Workers: WorkerDependencies{
		Registration: port, Heartbeats: port, Drain: port,
	}})
	now := time.Now().UTC()
	if _, err := service.RegisterWorkerNode(context.Background(), issueSystem(t, issuer, ActionRegisterWorkerNode), RegisterWorkerNodeCommand{
		WorkspaceKey: " TEST ", RequestID: " register-1 ", NodeID: " node-1 ", OwnerActor: " loom ",
		RuntimeProvider: " local ", Version: " v1 ", TTL: time.Minute, RegisteredAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if port.register.WorkspaceKey != "TEST" || port.register.RequestID != "register-1" || port.register.NodeID != "node-1" ||
		port.register.OwnerActor != "loom" || port.register.RuntimeProvider != "local" || port.register.Version != "v1" {
		t.Fatalf("register command was not canonicalized: %+v", port.register)
	}
	if _, err := service.HeartbeatWorkerNode(context.Background(), issueSystem(t, issuer, ActionHeartbeatWorkerNode), HeartbeatWorkerNodeCommand{
		WorkspaceKey: " TEST ", RequestID: " heartbeat-1 ", NodeID: " node-1 ", TTL: time.Minute, HeartbeatAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if port.heartbeat.WorkspaceKey != "TEST" || port.heartbeat.RequestID != "heartbeat-1" || port.heartbeat.NodeID != "node-1" {
		t.Fatalf("heartbeat command was not canonicalized: %+v", port.heartbeat)
	}
	if _, err := service.SetWorkerNodeDrain(context.Background(), issueSystem(t, issuer, ActionSetWorkerNodeDrain), SetWorkerNodeDrainCommand{
		WorkspaceKey: " TEST ", RequestID: " drain-1 ", NodeID: " node-1 ", DrainState: WorkerNodeDraining, ChangedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if port.drain.WorkspaceKey != "TEST" || port.drain.RequestID != "drain-1" || port.drain.NodeID != "node-1" {
		t.Fatalf("drain command was not canonicalized: %+v", port.drain)
	}
}

func TestWorkerNodeMutationsRejectWrongWorkspaceAndActionBeforePorts(t *testing.T) {
	port := &workerNodePortStub{}
	service, issuer := newTestService(t, Dependencies{Workers: WorkerDependencies{
		Registration: port, Heartbeats: port, Drain: port,
	}})
	now := time.Now().UTC()
	register := RegisterWorkerNodeCommand{WorkspaceKey: "OTHER", RequestID: "register-1", NodeID: "node-1", OwnerActor: "loom", RuntimeProvider: "local", TTL: time.Minute, RegisteredAt: now}
	if _, err := service.RegisterWorkerNode(context.Background(), issueSystem(t, issuer, ActionRegisterWorkerNode), register); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-workspace registration error = %v, want admission denied", err)
	}
	heartbeat := HeartbeatWorkerNodeCommand{WorkspaceKey: "TEST", RequestID: "heartbeat-1", NodeID: "node-1", TTL: time.Minute, HeartbeatAt: now}
	if _, err := service.HeartbeatWorkerNode(context.Background(), issueSystem(t, issuer, ActionSetWorkerNodeDrain), heartbeat); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-action heartbeat error = %v, want admission denied", err)
	}
	drain := SetWorkerNodeDrainCommand{WorkspaceKey: "TEST", RequestID: "drain-1", NodeID: "node-1", DrainState: WorkerNodeDraining, ChangedAt: now}
	if _, err := service.SetWorkerNodeDrain(context.Background(), issueSystem(t, issuer, ActionRegisterWorkerNode), drain); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-action drain error = %v, want admission denied", err)
	}
	if port.registerCalls != 0 || port.heartbeatCalls != 0 || port.drainCalls != 0 {
		t.Fatalf("denied mutations reached ports: register=%d heartbeat=%d drain=%d", port.registerCalls, port.heartbeatCalls, port.drainCalls)
	}
}

func TestWorkerNodeMutationRejectsExpiredSystemAuthority(t *testing.T) {
	now := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	clock := now
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	port := &workerNodePortStub{}
	service, err := New(Dependencies{Workers: WorkerDependencies{Registration: port}}, admission)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "runtime-test", Class: authority.ClassSystem, Workspace: "TEST",
		Actions: []authority.Action{ActionRegisterWorkerNode}, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := issuer.IssueSystem(principal, "TEST", ActionRegisterWorkerNode, "worker node expiry test")
	if err != nil {
		t.Fatal(err)
	}
	clock = now.Add(2 * time.Minute)
	_, err = service.RegisterWorkerNode(context.Background(), auth, RegisterWorkerNodeCommand{
		WorkspaceKey: "TEST", RequestID: "register-1", NodeID: "node-1", OwnerActor: "loom",
		RuntimeProvider: "local", TTL: time.Minute, RegisteredAt: now,
	})
	if !errors.Is(err, authority.ErrAdmissionDenied) || port.registerCalls != 0 {
		t.Fatalf("expired authority error/calls = %v/%d", err, port.registerCalls)
	}
}
