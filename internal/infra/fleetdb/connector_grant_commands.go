package fleetdb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrConnectorGrantNotFound    = errors.New("fleetdb: connector grant not found")
	ErrConnectorGrantInvalid     = errors.New("fleetdb: connector grant invalid request")
	ErrConnectorGrantConflict    = errors.New("fleetdb: connector grant conflict")
	ErrConnectorGrantUnavailable = errors.New("fleetdb: connector grant unavailable")
)

// ConnectorGrantRecord is the neutral FleetDB wire result exposed to
// capability composition. It intentionally does not import the legacy domain
// model or expose the composite Store.
type ConnectorGrantRecord struct {
	WorkspaceKey    string
	GrantID         string
	ConnectorID     string
	BindingID       string
	Action          string
	ResourcePattern string
	CreatedAt       time.Time
	RevokedAt       *time.Time
}

type ConnectorGrantCreateCommand struct {
	WorkspaceKey    string
	GrantID         string
	ConnectorID     string
	BindingID       string
	Action          string
	ResourcePattern string
}

// ConnectorGrantTransport is the narrow create/list surface required by the
// Connectors owner. Revoke and provider-secret operations are deliberately
// absent from this grant-provisioning boundary.
type ConnectorGrantTransport interface {
	CreateConnectorGrant(context.Context, ConnectorGrantCreateCommand) (*ConnectorGrantRecord, error)
	ListConnectorGrantsByBinding(context.Context, string, string) ([]*ConnectorGrantRecord, error)
}

type connectorGrantTransport struct{ client *Client }

var _ ConnectorGrantTransport = (*connectorGrantTransport)(nil)

func (c *Client) ConnectorGrantCommands() ConnectorGrantTransport {
	if c == nil {
		return nil
	}
	return &connectorGrantTransport{client: c}
}

func (transport *connectorGrantTransport) CreateConnectorGrant(
	ctx context.Context,
	command ConnectorGrantCreateCommand,
) (*ConnectorGrantRecord, error) {
	if err := validateConnectorGrantCreate(command); err != nil {
		return nil, err
	}
	body := map[string]any{
		"grant_id": command.GrantID, "connector_id": command.ConnectorID,
		"binding_id": command.BindingID, "action": command.Action,
		"resource_pattern": command.ResourcePattern,
	}
	var wire connectorGrantWire
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/connector-grants"
	if err := transport.client.do(ctx, http.MethodPost, path, body, &wire); err != nil {
		return nil, mapConnectorGrantTransportError("create", err)
	}
	result := connectorGrantRecordFromWire(&wire)
	if err := validateConnectorGrantRecord(result, command.WorkspaceKey, command.BindingID); err != nil {
		return nil, err
	}
	if result.GrantID != command.GrantID || result.ConnectorID != command.ConnectorID ||
		result.Action != command.Action || result.ResourcePattern != command.ResourcePattern {
		return nil, fmt.Errorf("connector grant create returned divergent state: %w", ErrConnectorGrantUnavailable)
	}
	return result, nil
}

func (transport *connectorGrantTransport) ListConnectorGrantsByBinding(
	ctx context.Context,
	workspace,
	bindingID string,
) ([]*ConnectorGrantRecord, error) {
	if err := validateConnectorGrantCoordinate("workspace", workspace); err != nil {
		return nil, err
	}
	if err := validateConnectorGrantCoordinate("binding id", bindingID); err != nil {
		return nil, err
	}
	query := url.Values{}
	query.Set("binding_id", bindingID)
	path := withQuery("/api/v1/"+pathEscape(workspace)+"/connector-grants", query)
	var response struct {
		ConnectorGrants []*connectorGrantWire `json:"connector_grants"`
	}
	if err := transport.client.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, mapConnectorGrantTransportError("list by binding", err)
	}
	result := make([]*ConnectorGrantRecord, len(response.ConnectorGrants))
	for index, wire := range response.ConnectorGrants {
		result[index] = connectorGrantRecordFromWire(wire)
		if err := validateConnectorGrantRecord(result[index], workspace, bindingID); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func validateConnectorGrantCreate(command ConnectorGrantCreateCommand) error {
	coordinates := []struct{ label, value string }{
		{"workspace", command.WorkspaceKey}, {"grant id", command.GrantID},
		{"connector id", command.ConnectorID}, {"binding id", command.BindingID},
		{"action", command.Action}, {"resource pattern", command.ResourcePattern},
	}
	for _, coordinate := range coordinates {
		if err := validateConnectorGrantCoordinate(coordinate.label, coordinate.value); err != nil {
			return err
		}
	}
	return nil
}

func validateConnectorGrantCoordinate(label, value string) error {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("connector grant %s must be canonical: %w", label, ErrConnectorGrantInvalid)
	}
	return nil
}

func validateConnectorGrantRecord(value *ConnectorGrantRecord, workspace, bindingID string) error {
	if value == nil || value.WorkspaceKey != workspace || value.BindingID != bindingID ||
		strings.TrimSpace(value.GrantID) == "" || strings.TrimSpace(value.ConnectorID) == "" ||
		strings.TrimSpace(value.Action) == "" || strings.TrimSpace(value.ResourcePattern) == "" ||
		value.CreatedAt.IsZero() {
		return fmt.Errorf("connector grant response escaped requested scope: %w", ErrConnectorGrantUnavailable)
	}
	return nil
}

func connectorGrantRecordFromWire(value *connectorGrantWire) *ConnectorGrantRecord {
	if value == nil {
		return nil
	}
	return &ConnectorGrantRecord{
		WorkspaceKey: value.WorkspaceKey, GrantID: value.GrantID,
		ConnectorID: value.ConnectorID, BindingID: value.BindingID,
		Action: value.Action, ResourcePattern: value.ResourcePattern,
		CreatedAt: value.CreatedAt, RevokedAt: cloneConnectorGrantTime(value.RevokedAt),
	}
}

func cloneConnectorGrantTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
