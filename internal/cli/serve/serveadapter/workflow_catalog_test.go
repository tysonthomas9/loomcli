package serveadapter

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	appworkflowauthoring "github.com/tysonthomas9/loomcli/internal/app/workflowauthoring"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type targetPreparationCatalogStub struct {
	workflowcatalog.API
	driver *workflowcatalog.Driver
}

func (stub targetPreparationCatalogStub) GetDriver(
	_ context.Context,
	_, ref string,
) (*workflowcatalog.Driver, error) {
	if stub.driver == nil || (ref != stub.driver.DriverID && ref != stub.driver.Name) {
		return nil, workflowcatalog.ErrNotFound
	}
	return stub.driver, nil
}

type targetPreparationAuthoringStub struct {
	appworkflowauthoring.CatalogCommands
}

func (targetPreparationAuthorityStub) AuthorityForVersionAvailability(
	context.Context, string, string,
) (authority.SystemAuthority, error) {
	return authority.SystemAuthority{}, nil
}

func (targetPreparationAuthorityStub) AuthorityForManagedVersionLifecycle(
	context.Context, string, authority.Action, string,
) (authority.SystemAuthority, error) {
	return authority.SystemAuthority{}, nil
}

type targetPreparationAuthorityStub struct{}

func (targetPreparationAuthorityStub) AuthorityForManagedBuiltin(
	context.Context,
	string,
	string,
) (authority.SystemAuthority, error) {
	return authority.SystemAuthority{}, nil
}

func TestExternalOperatorResolverFactoryUsesVerifiedMiddlewareIdentity(t *testing.T) {
	unauthenticated := errors.New("unauthenticated")
	factory := newExternalOperatorResolverFactory(func(_ context.Context, workspace string, identity middleware.UserIdentity) (string, error) {
		if workspace == "TEST" && identity.UserID == "maintainer-1" {
			return "maintainer", nil
		}
		return "developer", nil
	})
	if factory == nil {
		t.Fatal("external operator resolver factory is nil")
	}
	resolver := factory(authority.NewIssuer(), unauthenticated)
	if _, err := resolver.ResolveOperatorAuthority(nil, "TEST", workflowcatalog.ActionApproveVersion); !errors.Is(err, unauthenticated) {
		t.Fatalf("nil request error = %v, want %v", err, unauthenticated)
	}
	request := httptest.NewRequest("POST", "/api/workspaces/TEST/workflows/demo/versions/v1/approve", nil)
	if _, err := resolver.ResolveOperatorAuthority(request, "TEST", workflowcatalog.ActionApproveVersion); !errors.Is(err, unauthenticated) {
		t.Fatalf("unverified request error = %v, want %v", err, unauthenticated)
	}
	request = request.WithContext(middleware.WithUserIdentity(request.Context(), middleware.UserIdentity{UserID: "developer-1"}))
	if _, err := resolver.ResolveOperatorAuthority(request, "TEST", workflowcatalog.ActionApproveVersion); !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("developer error = %v, want %v", err, authority.ErrAdmissionDenied)
	}
	request = request.WithContext(middleware.WithUserIdentity(request.Context(), middleware.UserIdentity{UserID: "maintainer-1"}))
	granted, err := resolver.ResolveOperatorAuthority(request, "TEST", workflowcatalog.ActionApproveVersion)
	if err != nil {
		t.Fatalf("ResolveOperatorAuthority: %v", err)
	}
	if granted.Workspace() != "TEST" || granted.Action() != workflowcatalog.ActionApproveVersion {
		t.Fatalf("granted authority = workspace:%q action:%q", granted.Workspace(), granted.Action())
	}
	if newExternalOperatorResolverFactory(nil) != nil {
		t.Fatal("nil workspace role resolver constructed a factory")
	}
}

func TestWorkflowCatalogTargetPreparationProjectsActivatedTarget(t *testing.T) {
	catalog := targetPreparationCatalogStub{driver: &workflowcatalog.Driver{
		WorkspaceKey: "TEST", DriverID: "driver-1", Name: "custom-workflow",
		OwnerType: workflowcatalog.DriverOwnerSystem, Status: workflowcatalog.DriverStatusActive,
		ActiveVersionID: "version-2",
	}}
	factory := newWorkflowCatalogTargetPreparation(func() workflowTargetAuthoringPorts {
		return workflowTargetAuthoringPorts{
			catalog: catalog, authoring: &targetPreparationAuthoringStub{},
			authorities: targetPreparationAuthorityStub{},
		}
	})
	if factory == nil {
		t.Fatal("Workflow Catalog preparation factory is nil")
	}
	prepare := factory(errors.New("unavailable"))
	target, err := prepare(t.Context(), "TEST", "custom-workflow")
	if err != nil {
		t.Fatalf("prepare workflow target: %v", err)
	}
	if target.DriverID != "driver-1" || target.DriverVersionID != "version-2" {
		t.Fatalf("target = %+v", target)
	}
	if _, err := prepare(t.Context(), "TEST", "missing"); !errors.Is(err, workflowcatalog.ErrNotFound) {
		t.Fatalf("missing workflow error = %v, want %v", err, workflowcatalog.ErrNotFound)
	}
	if newWorkflowCatalogTargetPreparation(nil) != nil {
		t.Fatal("nil catalog ports resolver constructed a preparation factory")
	}
}
