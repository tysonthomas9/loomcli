package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/executionmanagement"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type canonicalWorkspaceWorkerProfiles struct {
	create execution.CreateWorkerProfileCommand
}

func (stub *canonicalWorkspaceWorkerProfiles) CreateWorkerProfile(
	_ context.Context,
	_ authority.OperatorAuthority,
	command execution.CreateWorkerProfileCommand,
) (*execution.WorkerProfile, error) {
	stub.create = command
	return &execution.WorkerProfile{
		WorkspaceKey: command.WorkspaceKey,
		ProfileID:    command.ProfileID,
		Role:         command.Role,
	}, nil
}

func (*canonicalWorkspaceWorkerProfiles) UpdateWorkerProfile(
	context.Context,
	authority.OperatorAuthority,
	execution.UpdateWorkerProfileCommand,
) (*execution.WorkerProfile, error) {
	panic("unexpected worker-profile update")
}

func (*canonicalWorkspaceWorkerProfiles) DeleteWorkerProfile(
	context.Context,
	authority.OperatorAuthority,
	execution.DeleteWorkerProfileCommand,
) (execution.DeleteWorkerProfileResult, error) {
	panic("unexpected worker-profile delete")
}

type canonicalWorkspaceOperatorResolver struct {
	workspace        string
	contextWorkspace string
	action           authority.Action
}

func (stub *canonicalWorkspaceOperatorResolver) ResolveOperatorAuthority(
	request *http.Request,
	workspace string,
	action authority.Action,
) (authority.OperatorAuthority, error) {
	stub.workspace = workspace
	stub.contextWorkspace = middleware.WorkspaceFromContext(request.Context())
	stub.action = action
	return authority.OperatorAuthority{}, nil
}

func TestRegisterWorkspaceRoutes_Phase5ManagementUsesCanonicalWorkspace(t *testing.T) {
	profiles := &canonicalWorkspaceWorkerProfiles{}
	resolver := &canonicalWorkspaceOperatorResolver{}
	server := &Server{
		mux: http.NewServeMux(),
		wsResolveFn: func(_ context.Context, requestedID string) (middleware.WorkspaceRef, bool) {
			if requestedID != "workspace-alias" {
				t.Fatalf("requested workspace = %q, want workspace-alias", requestedID)
			}
			return middleware.WorkspaceRef{
				RequestedID: requestedID,
				CanonicalID: "workspace-canonical",
			}, true
		},
		wsModules: []wsModule{
			executionmanagement.New(executionmanagement.Config{
				WorkerProfiles: profiles,
				Authority:      resolver,
			}),
		},
	}
	server.registerWorkspaceRoutes()

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/workspaces/workspace-alias/execution/worker-profiles",
		strings.NewReader(`{"profile_id":"reviewer","role":"task"}`),
	)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
	}
	if resolver.workspace != "workspace-canonical" ||
		resolver.contextWorkspace != "workspace-canonical" ||
		resolver.action != execution.ActionCreateWorkerProfile {
		t.Fatalf(
			"authority workspace/context/action = %q/%q/%q",
			resolver.workspace,
			resolver.contextWorkspace,
			resolver.action,
		)
	}
	if profiles.create.WorkspaceKey != "workspace-canonical" ||
		profiles.create.ProfileID != "reviewer" {
		t.Fatalf("create command = %+v", profiles.create)
	}
}
