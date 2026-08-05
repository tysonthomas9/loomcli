package agents

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type compatibilityStoreFake struct {
	createCalls int
	updateCalls int
	retireCalls int
	bindCalls   int
	repairCalls int

	createCommand CreateSupervisedAssignmentCommand
	updateCommand UpdateSupervisedAssignmentIntentCommand
	bindCommand   BindSupervisedAssignmentParentCommand
}

func (*compatibilityStoreFake) EnsureRole(
	context.Context,
	EnsureRoleCommand,
) (*Role, bool, error) {
	return &Role{}, false, nil
}

func (*compatibilityStoreFake) EnsureAgent(
	context.Context,
	EnsureAgentCommand,
) (*Agent, bool, error) {
	return &Agent{}, false, nil
}

func (store *compatibilityStoreFake) CreateSupervisedAssignment(
	_ context.Context,
	command CreateSupervisedAssignmentCommand,
) (*SupervisedAssignment, error) {
	store.createCalls++
	store.createCommand = command
	return &SupervisedAssignment{WorkspaceKey: command.WorkspaceKey, Name: command.AgentName}, nil
}

func (store *compatibilityStoreFake) UpdateSupervisedAssignmentIntent(
	_ context.Context,
	command UpdateSupervisedAssignmentIntentCommand,
) (*SupervisedAssignment, error) {
	store.updateCalls++
	store.updateCommand = command
	return &SupervisedAssignment{WorkspaceKey: command.WorkspaceKey, Name: command.AgentName}, nil
}

func (store *compatibilityStoreFake) RetireSupervisedAssignment(
	context.Context,
	RetireSupervisedAssignmentCommand,
) error {
	store.retireCalls++
	return nil
}

func (store *compatibilityStoreFake) BindSupervisedAssignmentParent(
	_ context.Context,
	command BindSupervisedAssignmentParentCommand,
) (*SupervisedAssignment, error) {
	store.bindCalls++
	store.bindCommand = command
	return &SupervisedAssignment{
		WorkspaceKey: command.WorkspaceKey,
		Name:         command.AgentName,
		Parent:       command.Parent,
	}, nil
}

func (store *compatibilityStoreFake) RepairManagedRolePromptFile(
	_ context.Context,
	command RepairManagedRolePromptFileCommand,
) (*Role, bool, error) {
	store.repairCalls++
	return &Role{
		WorkspaceKey: command.WorkspaceKey,
		Name:         command.RoleName,
		PromptFile:   command.PromptFile,
	}, true, nil
}

func newCompatibilityTestService(
	t *testing.T,
	store CompatibilityStore,
) (*CompatibilityService, *authority.Issuer) {
	t.Helper()
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewCompatibilityService(store, admission)
	if err != nil {
		t.Fatal(err)
	}
	return service, issuer
}

func TestCompatibilityCommandsRequireExactOperatorAuthority(t *testing.T) {
	store := &compatibilityStoreFake{}
	service, issuer := newCompatibilityTestService(t, store)
	command := CreateSupervisedAssignmentCommand{
		WorkspaceKey: "WS",
		AgentName:    "docs",
		RoleName:     "docs-role",
	}

	if _, err := service.CreateSupervisedAssignment(
		t.Context(),
		authority.OperatorAuthority{},
		command,
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("zero authority error = %v, want admission denied", err)
	}
	wrongAction := issueOperator(t, issuer, "WS", "operator-a", ActionUpdateSupervisedAssignmentIntent)
	if _, err := service.CreateSupervisedAssignment(
		t.Context(),
		wrongAction,
		command,
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-action authority error = %v, want admission denied", err)
	}
	wrongWorkspace := issueOperator(t, issuer, "OTHER", "operator-a", ActionCreateSupervisedAssignment)
	if _, err := service.CreateSupervisedAssignment(
		t.Context(),
		wrongWorkspace,
		command,
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-workspace authority error = %v, want admission denied", err)
	}
	if store.createCalls != 0 {
		t.Fatalf("denied commands reached compatibility persistence %d times", store.createCalls)
	}

	auth := issueOperator(t, issuer, "WS", "operator-a", ActionCreateSupervisedAssignment)
	created, err := service.CreateSupervisedAssignment(t.Context(), auth, command)
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != command.AgentName || store.createCalls != 1 ||
		!reflect.DeepEqual(store.createCommand, command) {
		t.Fatalf("created=%+v calls=%d command=%+v", created, store.createCalls, store.createCommand)
	}
}

func TestCompatibilityIntentPatchCannotCarryRuntimeOrParentState(t *testing.T) {
	for _, commandType := range []reflect.Type{
		reflect.TypeFor[CreateSupervisedAssignmentCommand](),
		reflect.TypeFor[SupervisedAssignmentIntentPatch](),
	} {
		for _, forbidden := range []string{"State", "Parent", "LiveStatus", "LeaseToken", "FencingToken"} {
			if _, found := commandType.FieldByName(forbidden); found {
				t.Fatalf("%s exposes forbidden runtime field %s", commandType, forbidden)
			}
		}
	}
	api := reflect.TypeFor[CompatibilityAPI]()
	for _, forbidden := range []string{
		"UpdateSupervisedAssignment",
		"RecordSupervisedAssignmentRuntime",
		"SetSupervisedAssignmentState",
	} {
		if _, found := api.MethodByName(forbidden); found {
			t.Fatalf("CompatibilityAPI exposes mixed/runtime command %s", forbidden)
		}
	}
}

func TestCompatibilityParentBindingRequiresExactDriverRunSubjectAndProof(t *testing.T) {
	store := &compatibilityStoreFake{}
	service, issuer := newCompatibilityTestService(t, store)
	command := BindSupervisedAssignmentParentCommand{
		WorkspaceKey:   "WS",
		AgentName:      "docs",
		ExpectedParent: stringPointer(""),
		Parent:         "run-7",
		Proof: ParentBindingProof{
			DriverRunID:  "run-7",
			NodeID:       "node-a",
			LeaseID:      "lease-7",
			FencingToken: 7,
		},
	}

	wrongSubject := issueSystem(
		t,
		issuer,
		"WS",
		"driver-run:run-8",
		ActionBindSupervisedAssignmentParent,
	)
	if _, err := service.BindSupervisedAssignmentParent(
		t.Context(),
		wrongSubject,
		command,
	); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("wrong-subject binding error = %v, want not owner", err)
	}
	wrongAction := issueSystem(t, issuer, "WS", "driver-run:run-7", ActionRetireManagedAssignment)
	if _, err := service.BindSupervisedAssignmentParent(
		t.Context(),
		wrongAction,
		command,
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong-action binding error = %v, want admission denied", err)
	}
	invalidProof := command
	invalidProof.Proof.FencingToken = 0
	correctSubject := issueSystem(
		t,
		issuer,
		"WS",
		"driver-run:run-7",
		ActionBindSupervisedAssignmentParent,
	)
	if _, err := service.BindSupervisedAssignmentParent(
		t.Context(),
		correctSubject,
		invalidProof,
	); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid-proof binding error = %v, want invalid", err)
	}
	if store.bindCalls != 0 {
		t.Fatalf("denied bindings reached compatibility persistence %d times", store.bindCalls)
	}

	bound, err := service.BindSupervisedAssignmentParent(t.Context(), correctSubject, command)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Parent != command.Parent || store.bindCalls != 1 ||
		!reflect.DeepEqual(store.bindCommand, command) {
		t.Fatalf("bound=%+v calls=%d command=%+v", bound, store.bindCalls, store.bindCommand)
	}
}

func TestCompatibilitySystemCommandsRejectOperatorOrWrongSystemAction(t *testing.T) {
	store := &compatibilityStoreFake{}
	service, issuer := newCompatibilityTestService(t, store)
	retire := RetireSupervisedAssignmentCommand{WorkspaceKey: "WS", AgentName: "docs"}

	wrongSystem := issueSystem(t, issuer, "WS", "migration", ActionRepairManagedRolePromptFile)
	if err := service.RetireManagedSupervisedAssignment(
		t.Context(),
		wrongSystem,
		retire,
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong system action error = %v, want admission denied", err)
	}
	rightSystem := issueSystem(t, issuer, "WS", "migration", ActionRetireManagedAssignment)
	if err := service.RetireManagedSupervisedAssignment(t.Context(), rightSystem, retire); err != nil {
		t.Fatal(err)
	}
	if store.retireCalls != 1 {
		t.Fatalf("retire calls = %d, want 1", store.retireCalls)
	}

	wrongRepair := issueSystem(t, issuer, "WS", "migration", ActionRetireManagedAssignment)
	if _, _, err := service.RepairManagedRolePromptFile(
		t.Context(),
		wrongRepair,
		RepairManagedRolePromptFileCommand{
			RequestID: "repair-1", WorkspaceKey: "WS", RoleName: "docs",
			PromptFile: "prompts/docs.md",
		},
	); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("wrong repair action error = %v, want admission denied", err)
	}
	if store.repairCalls != 0 {
		t.Fatalf("denied repair reached compatibility persistence %d times", store.repairCalls)
	}
}

func stringPointer(value string) *string {
	return &value
}
