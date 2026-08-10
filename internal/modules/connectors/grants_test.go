package connectors

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type grantStoreFake struct {
	lists       [][]*ConnectorGrant
	listErr     error
	create      *ConnectorGrant
	createErr   error
	listCalls   int
	createCalls int
	workspace   string
	bindingID   string
	mutation    CreateGrantMutation
}

func (fake *grantStoreFake) CreateGrant(
	_ context.Context,
	mutation CreateGrantMutation,
) (*ConnectorGrant, error) {
	fake.createCalls++
	fake.mutation = mutation
	return cloneConnectorGrant(fake.create), fake.createErr
}

func (fake *grantStoreFake) ListGrantsByBinding(
	_ context.Context,
	workspace,
	bindingID string,
) ([]*ConnectorGrant, error) {
	fake.listCalls++
	fake.workspace, fake.bindingID = workspace, bindingID
	if fake.listErr != nil {
		return nil, fake.listErr
	}
	index := fake.listCalls - 1
	if index >= len(fake.lists) {
		return []*ConnectorGrant{}, nil
	}
	out := make([]*ConnectorGrant, len(fake.lists[index]))
	for i, value := range fake.lists[index] {
		out[i] = cloneConnectorGrant(value)
	}
	return out, nil
}

func TestEnsureGrantCreatesOneExactBindingScopedGrant(t *testing.T) {
	command := validEnsureGrantCommand()
	created := grantFor(command)
	store := &grantStoreFake{
		lists:  [][]*ConnectorGrant{{}},
		create: created,
	}
	issuer, service := newGrantService(t, store)

	got, err := service.EnsureGrant(
		t.Context(),
		issueEnsureGrantAuthority(t, issuer, command.WorkspaceKey),
		command,
	)
	if err != nil {
		t.Fatalf("EnsureGrant: %v", err)
	}
	if got == created {
		t.Fatal("EnsureGrant leaked the persistence pointer")
	}
	if !grantMatchesCommand(got, command) || got.CreatedAt != created.CreatedAt {
		t.Fatalf("EnsureGrant result = %#v", got)
	}
	if store.listCalls != 1 || store.createCalls != 1 ||
		store.workspace != command.WorkspaceKey || store.bindingID != command.BindingID ||
		store.mutation != CreateGrantMutation(command) {
		t.Fatalf(
			"store calls list=%d create=%d workspace=%q binding=%q mutation=%#v",
			store.listCalls,
			store.createCalls,
			store.workspace,
			store.bindingID,
			store.mutation,
		)
	}
}

func TestEnsureGrantReusesOnlyAnExactActiveGrant(t *testing.T) {
	command := validEnsureGrantCommand()
	existing := grantFor(command)
	store := &grantStoreFake{lists: [][]*ConnectorGrant{{existing}}}
	issuer, service := newGrantService(t, store)

	got, err := service.EnsureGrant(
		t.Context(),
		issueEnsureGrantAuthority(t, issuer, command.WorkspaceKey),
		command,
	)
	if err != nil {
		t.Fatalf("EnsureGrant replay: %v", err)
	}
	if !grantMatchesCommand(got, command) || store.createCalls != 0 || store.listCalls != 1 {
		t.Fatalf("replay result=%#v list=%d create=%d", got, store.listCalls, store.createCalls)
	}

	divergent := grantFor(command)
	divergent.ResourcePattern = "repo:acme/other"
	store = &grantStoreFake{lists: [][]*ConnectorGrant{{divergent}}}
	issuer, service = newGrantService(t, store)
	_, err = service.EnsureGrant(
		t.Context(),
		issueEnsureGrantAuthority(t, issuer, command.WorkspaceKey),
		command,
	)
	if !errors.Is(err, ErrGrantConflict) || store.createCalls != 0 {
		t.Fatalf("divergent replay error=%v create=%d", err, store.createCalls)
	}
}

func TestEnsureGrantResolvesConcurrentCreateByRelisting(t *testing.T) {
	command := validEnsureGrantCommand()
	store := &grantStoreFake{
		lists:     [][]*ConnectorGrant{{}, {grantFor(command)}},
		createErr: ErrGrantConflict,
	}
	issuer, service := newGrantService(t, store)
	got, err := service.EnsureGrant(
		t.Context(),
		issueEnsureGrantAuthority(t, issuer, command.WorkspaceKey),
		command,
	)
	if err != nil || !grantMatchesCommand(got, command) ||
		store.listCalls != 2 || store.createCalls != 1 {
		t.Fatalf(
			"raced EnsureGrant result=%#v error=%v list=%d create=%d",
			got,
			err,
			store.listCalls,
			store.createCalls,
		)
	}

	store = &grantStoreFake{lists: [][]*ConnectorGrant{{}, {}}, createErr: ErrGrantConflict}
	issuer, service = newGrantService(t, store)
	_, err = service.EnsureGrant(
		t.Context(),
		issueEnsureGrantAuthority(t, issuer, command.WorkspaceKey),
		command,
	)
	if !errors.Is(err, ErrGrantConflict) {
		t.Fatalf("unresolved race error = %v, want ErrGrantConflict", err)
	}
}

func TestEnsureGrantRequiresFreshExactSystemAuthorityBeforeStorage(t *testing.T) {
	command := validEnsureGrantCommand()
	store := &grantStoreFake{}
	issuer, service := newGrantService(t, store)

	tests := []struct {
		name string
		auth authority.SystemAuthority
	}{
		{name: "zero authority"},
		{
			name: "wrong workspace",
			auth: issueEnsureGrantAuthority(t, issuer, "OTHER"),
		},
		{
			name: "foreign issuer",
			auth: issueEnsureGrantAuthority(t, authority.NewIssuer(), command.WorkspaceKey),
		},
		{
			name: "wrong action",
			auth: issueSystemAuthority(
				t,
				issuer,
				command.WorkspaceKey,
				ActionExecuteGitRead,
			),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.EnsureGrant(t.Context(), test.auth, command)
			if !errors.Is(err, authority.ErrAdmissionDenied) {
				t.Fatalf("EnsureGrant error = %v, want admission denied", err)
			}
		})
	}
	if store.listCalls != 0 || store.createCalls != 0 {
		t.Fatalf("storage calls list=%d create=%d, want zero", store.listCalls, store.createCalls)
	}
}

func TestEnsureGrantRejectsMalformedCommandsBeforeStorage(t *testing.T) {
	valid := validEnsureGrantCommand()
	store := &grantStoreFake{}
	issuer, service := newGrantService(t, store)
	auth := issueEnsureGrantAuthority(t, issuer, valid.WorkspaceKey)

	tests := []struct {
		name   string
		mutate func(*EnsureGrantCommand)
	}{
		{name: "workspace", mutate: func(value *EnsureGrantCommand) { value.WorkspaceKey = "" }},
		{name: "grant id", mutate: func(value *EnsureGrantCommand) { value.GrantID = " grant-1" }},
		{name: "connector id", mutate: func(value *EnsureGrantCommand) { value.ConnectorID = "" }},
		{name: "binding id", mutate: func(value *EnsureGrantCommand) { value.BindingID = "" }},
		{name: "short action", mutate: func(value *EnsureGrantCommand) { value.Action = "read" }},
		{name: "empty action segment", mutate: func(value *EnsureGrantCommand) { value.Action = "github..read" }},
		{name: "uppercase action", mutate: func(value *EnsureGrantCommand) { value.Action = "GitHub.read" }},
		{name: "resource", mutate: func(value *EnsureGrantCommand) { value.ResourcePattern = " " }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := valid
			test.mutate(&command)
			_, err := service.EnsureGrant(t.Context(), auth, command)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("EnsureGrant error = %v, want ErrInvalid", err)
			}
		})
	}
	if store.listCalls != 0 || store.createCalls != 0 {
		t.Fatalf("storage calls list=%d create=%d, want zero", store.listCalls, store.createCalls)
	}
}

func TestEnsureGrantRejectsInvalidFleetResponses(t *testing.T) {
	command := validEnsureGrantCommand()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	revokedAt := now.Add(time.Minute)

	tests := []struct {
		name   string
		values []*ConnectorGrant
		create *ConnectorGrant
	}{
		{name: "nil listed grant", values: []*ConnectorGrant{nil}},
		{
			name: "cross workspace listing",
			values: []*ConnectorGrant{func() *ConnectorGrant {
				value := grantFor(command)
				value.WorkspaceKey = "OTHER"
				return value
			}()},
		},
		{
			name: "cross binding listing",
			values: []*ConnectorGrant{func() *ConnectorGrant {
				value := grantFor(command)
				value.BindingID = "binding-other"
				return value
			}()},
		},
		{
			name: "revoked active listing",
			values: []*ConnectorGrant{func() *ConnectorGrant {
				value := grantFor(command)
				value.RevokedAt = &revokedAt
				return value
			}()},
		},
		{
			name:   "duplicate active id",
			values: []*ConnectorGrant{grantFor(command), grantFor(command)},
		},
		{name: "nil create", values: []*ConnectorGrant{}, create: nil},
		{
			name:   "mismatched create",
			values: []*ConnectorGrant{},
			create: func() *ConnectorGrant {
				value := grantFor(command)
				value.ConnectorID = "connector-other"
				return value
			}(),
		},
		{
			name:   "zero create timestamp",
			values: []*ConnectorGrant{},
			create: func() *ConnectorGrant {
				value := grantFor(command)
				value.CreatedAt = time.Time{}
				return value
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &grantStoreFake{lists: [][]*ConnectorGrant{test.values}, create: test.create}
			issuer, service := newGrantService(t, store)
			_, err := service.EnsureGrant(
				t.Context(),
				issueEnsureGrantAuthority(t, issuer, command.WorkspaceKey),
				command,
			)
			if !errors.Is(err, ErrInvalidPersistedState) {
				t.Fatalf("EnsureGrant error = %v, want invalid persisted state", err)
			}
		})
	}
}

func TestNewRejectsMissingGrantStore(t *testing.T) {
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	executor := gitReadExecutorFunc(func(context.Context, GitReadCommand) error { return nil })
	if _, err := New(executor, nil, admission); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New nil grant store error = %v", err)
	}
}

func newGrantService(t *testing.T, store ConnectorGrantStore) (*authority.Issuer, *Service) {
	t.Helper()
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(
		gitReadExecutorFunc(func(context.Context, GitReadCommand) error { return nil }),
		store,
		admission,
	)
	if err != nil {
		t.Fatal(err)
	}
	return issuer, service
}

func issueEnsureGrantAuthority(
	t *testing.T,
	issuer *authority.Issuer,
	workspace string,
) authority.SystemAuthority {
	t.Helper()
	return issueSystemAuthority(t, issuer, workspace, ActionEnsureGrant)
}

func issueSystemAuthority(
	t *testing.T,
	issuer *authority.Issuer,
	workspace string,
	action authority.Action,
) authority.SystemAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject:   "serve-agent-provisioning-recovery",
		Class:     authority.ClassSystem,
		Workspace: workspace,
		Actions:   []authority.Action{action},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := issuer.IssueSystem(principal, workspace, action, "connector grant test")
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func validEnsureGrantCommand() EnsureGrantCommand {
	return EnsureGrantCommand{
		WorkspaceKey:    "WS",
		GrantID:         "grant-read",
		ConnectorID:     "github-main",
		BindingID:       "binding-docs",
		Action:          "pull_request.read",
		ResourcePattern: "repo:acme/docs",
	}
}

func grantFor(command EnsureGrantCommand) *ConnectorGrant {
	return &ConnectorGrant{
		WorkspaceKey: command.WorkspaceKey, GrantID: command.GrantID,
		ConnectorID: command.ConnectorID, BindingID: command.BindingID,
		Action: command.Action, ResourcePattern: command.ResourcePattern,
		CreatedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
	}
}
