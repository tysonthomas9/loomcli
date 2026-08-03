package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type workerProfilePortStub struct {
	gets       int
	lists      int
	creates    int
	updates    int
	deletes    int
	lastUpdate UpdateWorkerProfileCommand
}

func (stub *workerProfilePortStub) GetWorkerProfile(_ context.Context, workspace, profileID string) (*WorkerProfile, error) {
	stub.gets++
	now := time.Now().UTC()
	return &WorkerProfile{WorkspaceKey: workspace, ProfileID: profileID, Name: profileID, Role: "lead", Enabled: true, CreatedAt: now, UpdatedAt: now}, nil
}

func (stub *workerProfilePortStub) ListWorkerProfiles(_ context.Context, workspace string, _ WorkerProfileFilter) ([]*WorkerProfile, error) {
	stub.lists++
	now := time.Now().UTC()
	return []*WorkerProfile{{WorkspaceKey: workspace, ProfileID: "lead-profile", Name: "lead-profile", Role: "lead", Enabled: true, CreatedAt: now, UpdatedAt: now}}, nil
}

func TestWorkerProfileQueriesValidateAndClone(t *testing.T) {
	port := &workerProfilePortStub{}
	service, _ := newTestService(t, Dependencies{Workers: WorkerDependencies{Profiles: port}})
	profile, err := service.GetWorkerProfile(context.Background(), " WS ", "lead-profile")
	if err != nil || profile.WorkspaceKey != "WS" || port.gets != 1 {
		t.Fatalf("profile/calls/error = %+v/%d/%v", profile, port.gets, err)
	}
	profiles, err := service.ListWorkerProfiles(context.Background(), "WS", WorkerProfileFilter{Role: " lead ", Limit: 1})
	if err != nil || len(profiles) != 1 || port.lists != 1 {
		t.Fatalf("profiles/calls/error = %+v/%d/%v", profiles, port.lists, err)
	}
	if _, err := service.GetWorkerProfile(context.Background(), "", "lead-profile"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty workspace error = %v, want ErrInvalid", err)
	}
	if _, err := service.ListWorkerProfiles(context.Background(), "WS", WorkerProfileFilter{Limit: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative limit error = %v, want ErrInvalid", err)
	}
}

func (stub *workerProfilePortStub) CreateWorkerProfile(_ context.Context, command CreateWorkerProfileCommand) (*WorkerProfile, error) {
	stub.creates++
	now := time.Now().UTC()
	enabled := true
	if command.Enabled != nil {
		enabled = *command.Enabled
	}
	return &WorkerProfile{
		WorkspaceKey: command.WorkspaceKey, ProfileID: command.ProfileID, Name: command.Name, Role: command.Role,
		Enabled: enabled, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (stub *workerProfilePortStub) UpdateWorkerProfile(_ context.Context, command UpdateWorkerProfileCommand) (*WorkerProfile, error) {
	stub.updates++
	stub.lastUpdate = command
	now := time.Now().UTC()
	role := "task"
	if command.Patch.Role != nil {
		role = *command.Patch.Role
	}
	profile := &WorkerProfile{
		WorkspaceKey: command.WorkspaceKey, ProfileID: command.ProfileID, Name: command.ProfileID,
		Role: role, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if command.Patch.ParentEpic != nil {
		profile.ParentEpic = *command.Patch.ParentEpic
	}
	return profile, nil
}

func (stub *workerProfilePortStub) DeleteWorkerProfile(_ context.Context, _ DeleteWorkerProfileCommand) error {
	stub.deletes++
	return nil
}

func TestWorkerProfileMutationsRequireExactOperatorScope(t *testing.T) {
	port := &workerProfilePortStub{}
	service, issuer := newTestService(t, Dependencies{Workers: WorkerDependencies{Profiles: port}})
	auth := issueWorkerProfileOperator(t, issuer, "WS", ActionCreateWorkerProfile)
	enabled := true
	profile, err := service.CreateWorkerProfile(context.Background(), auth, CreateWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "create-1", ProfileID: "falcon", Role: "task", Enabled: &enabled,
	})
	if err != nil || profile.ProfileID != "falcon" || port.creates != 1 {
		t.Fatalf("profile/calls/error = %+v/%d/%v", profile, port.creates, err)
	}

	_, err = service.CreateWorkerProfile(context.Background(), auth, CreateWorkerProfileCommand{
		WorkspaceKey: "OTHER", RequestID: "create-2", ProfileID: "foreign", Role: "task",
	})
	if !errors.Is(err, authority.ErrAdmissionDenied) || port.creates != 1 {
		t.Fatalf("wrong-workspace error/calls = %v/%d", err, port.creates)
	}
	_, err = service.CreateWorkerProfile(context.Background(), authority.OperatorAuthority{}, CreateWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "create-3", ProfileID: "denied", Role: "task",
	})
	if !errors.Is(err, authority.ErrAdmissionDenied) || port.creates != 1 {
		t.Fatalf("zero-authority error/calls = %v/%d", err, port.creates)
	}
}

func TestWorkerProfileUpdateAndDeleteValidateBeforePort(t *testing.T) {
	port := &workerProfilePortStub{}
	service, issuer := newTestService(t, Dependencies{Workers: WorkerDependencies{Profiles: port}})
	updateAuth := issueWorkerProfileOperator(t, issuer, "WS", ActionUpdateWorkerProfile)
	if _, err := service.UpdateWorkerProfile(context.Background(), updateAuth, UpdateWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "update-1", ProfileID: "falcon",
	}); !errors.Is(err, ErrInvalid) || port.updates != 0 {
		t.Fatalf("empty update error/calls = %v/%d", err, port.updates)
	}
	role := "lead"
	updated, err := service.UpdateWorkerProfile(context.Background(), updateAuth, UpdateWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "update-2", ProfileID: "falcon", Patch: WorkerProfilePatch{Role: &role},
	})
	if err != nil || updated.Role != "lead" || port.updates != 1 {
		t.Fatalf("updated/calls/error = %+v/%d/%v", updated, port.updates, err)
	}

	deleteAuth := issueWorkerProfileOperator(t, issuer, "WS", ActionDeleteWorkerProfile)
	result, err := service.DeleteWorkerProfile(context.Background(), deleteAuth, DeleteWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "delete-1", ProfileID: "falcon",
	})
	if err != nil || result.ProfileID != "falcon" || port.deletes != 1 {
		t.Fatalf("result/calls/error = %+v/%d/%v", result, port.deletes, err)
	}
}

func TestBindWorkerProfileParentRequiresExactDriverRunOwnerAndCAS(t *testing.T) {
	port := &workerProfilePortStub{}
	service, issuer := newTestService(t, Dependencies{Workers: WorkerDependencies{Profiles: port}})
	owner := Owner{
		ResourceKind: ResourceDriverRun,
		ResourceID:   "run-1",
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		LeaseToken:   "lease-token",
		FencingToken: 7,
	}
	auth := issueExecution(t, issuer, ActionBindWorkerProfileParent, owner)
	profile, err := service.BindWorkerProfileParent(context.Background(), auth, BindWorkerProfileParentCommand{
		WorkspaceKey:   "TEST",
		RequestID:      "bind-1",
		ProfileID:      "lead-profile",
		ExpectedParent: "",
		Parent:         "EPIC-1",
		Owner:          owner,
	})
	if err != nil || profile.ParentEpic != "EPIC-1" || port.updates != 1 {
		t.Fatalf("profile/calls/error = %+v/%d/%v", profile, port.updates, err)
	}
	if port.lastUpdate.ExpectedParentEpic == nil || *port.lastUpdate.ExpectedParentEpic != "" {
		t.Fatalf("expected parent guard = %#v, want empty", port.lastUpdate.ExpectedParentEpic)
	}
	wrongOwner := owner
	wrongOwner.FencingToken++
	if _, err := service.BindWorkerProfileParent(context.Background(), auth, BindWorkerProfileParentCommand{
		WorkspaceKey: "TEST", RequestID: "bind-2", ProfileID: "lead-profile",
		Parent: "EPIC-2", Owner: wrongOwner,
	}); !errors.Is(err, ErrFenceConflict) || port.updates != 1 {
		t.Fatalf("wrong owner error/calls = %v/%d, want fence conflict/1", err, port.updates)
	}
}

func TestWorkerProfileMutationsRejectWrongActionAndWorkspaceBeforePort(t *testing.T) {
	port := &workerProfilePortStub{}
	service, issuer := newTestService(t, Dependencies{Workers: WorkerDependencies{Profiles: port}})
	enabled := true
	create := CreateWorkerProfileCommand{WorkspaceKey: "WS", RequestID: "create-1", ProfileID: "falcon", Role: "task", Enabled: &enabled}
	if _, err := service.CreateWorkerProfile(context.Background(), issueWorkerProfileOperator(t, issuer, "WS", ActionUpdateWorkerProfile), create); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-action create error = %v, want admission denied", err)
	}
	role := "lead"
	update := UpdateWorkerProfileCommand{WorkspaceKey: "OTHER", RequestID: "update-1", ProfileID: "falcon", Patch: WorkerProfilePatch{Role: &role}}
	if _, err := service.UpdateWorkerProfile(context.Background(), issueWorkerProfileOperator(t, issuer, "WS", ActionUpdateWorkerProfile), update); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-workspace update error = %v, want admission denied", err)
	}
	remove := DeleteWorkerProfileCommand{WorkspaceKey: "WS", RequestID: "delete-1", ProfileID: "falcon"}
	if _, err := service.DeleteWorkerProfile(context.Background(), issueWorkerProfileOperator(t, issuer, "WS", ActionCreateWorkerProfile), remove); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-action delete error = %v, want admission denied", err)
	}
	if port.creates != 0 || port.updates != 0 || port.deletes != 0 {
		t.Fatalf("denied profile mutations reached port: creates=%d updates=%d deletes=%d", port.creates, port.updates, port.deletes)
	}
}

func TestWorkerProfileMutationRejectsExpiredOperator(t *testing.T) {
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
	port := &workerProfilePortStub{}
	service, err := New(Dependencies{Workers: WorkerDependencies{Profiles: port}}, admission)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "worker-profile-test", Class: authority.ClassOperator, Workspace: "WS",
		Actions: []authority.Action{ActionCreateWorkerProfile}, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := issuer.IssueOperator(principal, "WS", ActionCreateWorkerProfile)
	if err != nil {
		t.Fatal(err)
	}
	clock = now.Add(2 * time.Minute)
	_, err = service.CreateWorkerProfile(context.Background(), auth, CreateWorkerProfileCommand{
		WorkspaceKey: "WS", RequestID: "create-1", ProfileID: "falcon", Role: "task",
	})
	if !errors.Is(err, authority.ErrAdmissionDenied) || port.creates != 0 {
		t.Fatalf("expired operator error/calls = %v/%d", err, port.creates)
	}
}

func issueWorkerProfileOperator(t *testing.T, issuer *authority.Issuer, workspace string, action authority.Action) authority.OperatorAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "worker-profile-test", Class: authority.ClassOperator, Workspace: workspace,
		Actions: []authority.Action{action}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := issuer.IssueOperator(principal, workspace, action)
	if err != nil {
		t.Fatal(err)
	}
	return auth
}
