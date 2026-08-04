package connectorgrants

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

type bindingQueriesFake struct {
	binding *automation.Binding
	gets    int
}

func (fake *bindingQueriesFake) GetBinding(context.Context, string, string) (*automation.Binding, error) {
	fake.gets++
	if fake.binding == nil {
		return nil, automation.ErrNotFound
	}
	copy := *fake.binding
	return &copy, nil
}

type connectorManagementFake struct {
	connector *connectors.Connector
	grants    []*connectors.ConnectorGrant
	gets      int
	creates   int
	revokes   int
}

func (fake *connectorManagementFake) GetConnector(
	context.Context,
	connectors.GetConnectorQuery,
) (*connectors.Connector, error) {
	fake.gets++
	if fake.connector == nil {
		return nil, connectors.ErrNotFound
	}
	copy := *fake.connector
	return &copy, nil
}

func (fake *connectorManagementFake) CreateGrant(
	_ context.Context,
	command connectors.CreateGrantCommand,
) (*connectors.ConnectorGrant, error) {
	fake.creates++
	for _, grant := range fake.grants {
		if grant.GrantID == command.GrantID && grant.RevokedAt == nil {
			return nil, connectors.ErrAlreadyExists
		}
	}
	created := &connectors.ConnectorGrant{
		WorkspaceKey: command.WorkspaceKey, GrantID: command.GrantID, ConnectorID: command.ConnectorID,
		BindingID: command.BindingID, Action: command.Action, ResourcePattern: command.ResourcePattern,
		CreatedAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
	}
	fake.grants = append(fake.grants, created)
	copy := *created
	return &copy, nil
}

func (fake *connectorManagementFake) RevokeGrant(
	_ context.Context,
	command connectors.RevokeGrantCommand,
) error {
	for _, grant := range fake.grants {
		if grant.GrantID == command.GrantID && grant.RevokedAt == nil {
			now := time.Date(2026, 8, 4, 12, 1, 0, 0, time.UTC)
			grant.RevokedAt = &now
			fake.revokes++
			return nil
		}
	}
	return connectors.ErrGrantRevoked
}

func (fake *connectorManagementFake) ListGrants(
	_ context.Context,
	query connectors.ListGrantsQuery,
) ([]*connectors.ConnectorGrant, error) {
	result := make([]*connectors.ConnectorGrant, 0, len(fake.grants))
	for _, grant := range fake.grants {
		if grant.RevokedAt != nil || grant.WorkspaceKey != query.WorkspaceKey {
			continue
		}
		if query.BindingID != "" && grant.BindingID != query.BindingID {
			continue
		}
		copy := *grant
		result = append(result, &copy)
	}
	return result, nil
}

func newWorkflowHarness(t *testing.T) (*Workflow, *bindingQueriesFake, *connectorManagementFake, ReplaceGrantSetRequest) {
	t.Helper()
	created := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	updated := created.Add(time.Minute)
	bindings := &bindingQueriesFake{binding: &automation.Binding{
		WorkspaceKey: "WS", BindingID: "binding-1", Enabled: false, CreatedAt: created, UpdatedAt: updated,
	}}
	management := &connectorManagementFake{connector: &connectors.Connector{
		WorkspaceKey: "WS", ConnectorID: "github-main", SourceKind: connectors.ConnectorSourceGitHub,
		Status: connectors.ConnectorStatusActive,
	}}
	workflow, err := New(bindings, management)
	if err != nil {
		t.Fatal(err)
	}
	request := ReplaceGrantSetRequest{
		WorkspaceKey: "WS", ConnectorID: "github-main", BindingID: "binding-1",
		ExpectedBindingCreatedAt: created, ExpectedBindingUpdatedAt: updated,
		Grants: []DesiredGrant{{Action: "github.review.post", ResourcePattern: "repo:acme/loom"}},
	}
	return workflow, bindings, management, request
}

func TestReplaceConvergesAndExactReplayPreservesGrant(t *testing.T) {
	workflow, bindings, management, request := newWorkflowHarness(t)
	first, err := workflow.Replace(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Grants) != 1 || first.GrantsRevoked != 0 || management.creates != 1 {
		t.Fatalf("first replacement = %+v creates=%d", first, management.creates)
	}
	firstID := first.Grants[0].GrantID
	if !strings.Contains(firstID, "-g") || !strings.Contains(firstID, "-v") || !strings.Contains(firstID, "-s") {
		t.Fatalf("grant id %q lacks generation, revision, or set fence", firstID)
	}

	replayed, err := workflow.Replace(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed.Grants) != 1 || replayed.Grants[0].GrantID != firstID || replayed.GrantsRevoked != 0 || management.creates != 1 {
		t.Fatalf("replayed replacement = %+v creates=%d", replayed, management.creates)
	}
	if bindings.gets < 4 {
		t.Fatalf("binding snapshot was not rechecked around side effects: gets=%d", bindings.gets)
	}
}

func TestReplaceValidatesCompleteDesiredSetBeforeReadsOrWrites(t *testing.T) {
	workflow, bindings, management, request := newWorkflowHarness(t)
	request.Grants = append(request.Grants, DesiredGrant{Action: "INVALID ACTION", ResourcePattern: "repo:acme/loom"})
	_, err := workflow.Replace(t.Context(), request)
	if !errors.Is(err, connectors.ErrInvalid) {
		t.Fatalf("Replace error = %v, want connectors.ErrInvalid", err)
	}
	if bindings.gets != 0 || management.gets != 0 || management.creates != 0 || management.revokes != 0 {
		t.Fatalf("invalid complete set caused side effects: bindings=%d connector=%d creates=%d revokes=%d",
			bindings.gets, management.gets, management.creates, management.revokes)
	}
}

func TestReplaceRejectsEnabledOrStaleBindingBeforeConnectorMutation(t *testing.T) {
	workflow, bindings, management, request := newWorkflowHarness(t)
	bindings.binding.Enabled = true
	_, err := workflow.Replace(t.Context(), request)
	if !errors.Is(err, automation.ErrConflict) {
		t.Fatalf("enabled binding error = %v, want automation.ErrConflict", err)
	}
	if management.gets != 0 || management.creates != 0 || management.revokes != 0 {
		t.Fatalf("enabled binding reached Connectors: gets=%d creates=%d revokes=%d",
			management.gets, management.creates, management.revokes)
	}

	bindings.binding.Enabled = false
	request.ExpectedBindingUpdatedAt = request.ExpectedBindingUpdatedAt.Add(time.Second)
	_, err = workflow.Replace(t.Context(), request)
	if !errors.Is(err, automation.ErrConflict) {
		t.Fatalf("stale binding error = %v, want automation.ErrConflict", err)
	}
}

func TestNewAndNilWorkflowFailClosed(t *testing.T) {
	workflow, bindings, management, request := newWorkflowHarness(t)
	if _, err := New(nil, management); !errors.Is(err, ErrGrantSetUnavailable) {
		t.Fatalf("New(nil, management) = %v", err)
	}
	if _, err := New(bindings, nil); !errors.Is(err, ErrGrantSetUnavailable) {
		t.Fatalf("New(bindings, nil) = %v", err)
	}
	if _, err := workflow.Replace(nil, request); !errors.Is(err, connectors.ErrInvalid) {
		t.Fatalf("nil context Replace = %v", err)
	}
	var missing *Workflow
	if _, err := missing.Replace(t.Context(), request); !errors.Is(err, ErrGrantSetUnavailable) {
		t.Fatalf("nil workflow Replace = %v", err)
	}
}
