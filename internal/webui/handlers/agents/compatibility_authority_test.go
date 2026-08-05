package agents

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/agentscompat"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/agentscompatstore"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/svcimpl"
)

type authorizedTestAgentService struct {
	service.AgentService
	t      *testing.T
	issuer *authority.Issuer
}

func newAuthorizedTestAgentService(
	t *testing.T,
	st store.Store,
	runtime ...svcimpl.InteractiveRuntimeController,
) service.AgentService {
	t.Helper()
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(agentsmodule.OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	compatibilityPersistence, err := agentscompatstore.New(
		st.Roles(),
		st.AgentServices(),
		st.Agents(),
	)
	if err != nil {
		t.Fatal(err)
	}
	compatibility, err := agentscompat.NewAPI(compatibilityPersistence, admission)
	if err != nil {
		t.Fatal(err)
	}
	retirements, err := agentscompat.NewManagedRetirements(compatibility, issuer)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := agentscompat.NewManagedCommandsWithIssuer(compatibility, issuer)
	if err != nil {
		t.Fatal(err)
	}
	var interactive svcimpl.InteractiveRuntimeController
	if len(runtime) > 0 {
		interactive = runtime[0]
	}
	return &authorizedTestAgentService{
		AgentService: svcimpl.NewAgentServiceWithCompatibility(
			nil, nil, nil, st, interactive, compatibility, managed, retirements,
		),
		t: t, issuer: issuer,
	}
}

func (serviceWithAuth *authorizedTestAgentService) authorize(
	ctx context.Context,
	workspace string,
	action authority.Action,
) context.Context {
	serviceWithAuth.t.Helper()
	principal, err := serviceWithAuth.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "test-operator", Class: authority.ClassOperator,
		Workspace: workspace, Actions: []authority.Action{action},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		serviceWithAuth.t.Fatal(err)
	}
	auth, err := serviceWithAuth.issuer.IssueOperator(principal, workspace, action)
	if err != nil {
		serviceWithAuth.t.Fatal(err)
	}
	return service.WithAgentOperatorAuthority(ctx, auth)
}

func (serviceWithAuth *authorizedTestAgentService) CreateAgent(
	ctx context.Context,
	input service.AgentCreateInput,
) (*domain.Agent, error) {
	return serviceWithAuth.AgentService.CreateAgent(
		serviceWithAuth.authorize(ctx, input.WorkspaceKey, agentsmodule.ActionCreateSupervisedAssignment),
		input,
	)
}

func (serviceWithAuth *authorizedTestAgentService) UpdateAgent(
	ctx context.Context,
	workspace, name string,
	patch service.AgentUpdateInput,
) (*domain.Agent, error) {
	return serviceWithAuth.AgentService.UpdateAgent(
		serviceWithAuth.authorize(ctx, workspace, agentsmodule.ActionUpdateSupervisedAssignmentIntent),
		workspace,
		name,
		patch,
	)
}

func (serviceWithAuth *authorizedTestAgentService) RequestAgentLifecycle(
	ctx context.Context,
	workspace, name string,
	input service.AgentLifecycleInput,
) (*service.AgentLifecycleResult, error) {
	return serviceWithAuth.AgentService.RequestAgentLifecycle(
		serviceWithAuth.authorize(ctx, workspace, agentsmodule.ActionUpdateSupervisedAssignmentIntent),
		workspace,
		name,
		input,
	)
}

func (serviceWithAuth *authorizedTestAgentService) DeleteAgent(
	ctx context.Context,
	workspace, name string,
) error {
	return serviceWithAuth.AgentService.DeleteAgent(
		serviceWithAuth.authorize(ctx, workspace, agentsmodule.ActionRetireSupervisedAssignment),
		workspace,
		name,
	)
}
