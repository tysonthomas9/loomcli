package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestConnectorGrantCommandsUseNarrowRoutesAndValidateScope(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	step := 0
	httpClient := newWorkspaceHTTPClient(func(response http.ResponseWriter, request *http.Request) {
		step++
		grant := connectorGrantWire{
			WorkspaceKey: "WS", GrantID: "grant-1", ConnectorID: "github-main",
			BindingID: "binding-1", Action: "github.read",
			ResourcePattern: "repo:owner/name", CreatedAt: now,
		}
		switch step {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != "/api/v1/WS/connector-grants" {
				t.Fatalf("create request = %s %s", request.Method, request.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["grant_id"] != grant.GrantID || body["connector_id"] != grant.ConnectorID ||
				body["binding_id"] != grant.BindingID || body["action"] != grant.Action ||
				body["resource_pattern"] != grant.ResourcePattern {
				t.Fatalf("create body = %+v", body)
			}
			writeJSON(t, response, grant)
		case 2:
			if request.Method != http.MethodGet || request.URL.Path != "/api/v1/WS/connector-grants" ||
				request.URL.Query().Get("binding_id") != grant.BindingID {
				t.Fatalf("list request = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
			}
			writeJSON(t, response, map[string]any{"connector_grants": []connectorGrantWire{grant}})
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
	})
	client, err := New(Config{BaseURL: "http://fleet.test", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	transport := client.ConnectorGrantCommands()
	created, err := transport.CreateConnectorGrant(t.Context(), ConnectorGrantCreateCommand{
		WorkspaceKey: "WS", GrantID: "grant-1", ConnectorID: "github-main",
		BindingID: "binding-1", Action: "github.read", ResourcePattern: "repo:owner/name",
	})
	if err != nil || created.GrantID != "grant-1" {
		t.Fatalf("CreateConnectorGrant = %#v, %v", created, err)
	}
	listed, err := transport.ListConnectorGrantsByBinding(t.Context(), "WS", "binding-1")
	if err != nil || len(listed) != 1 || listed[0].GrantID != "grant-1" {
		t.Fatalf("ListConnectorGrantsByBinding = %#v, %v", listed, err)
	}
}

func TestConnectorGrantCommandsRejectCrossBindingResponse(t *testing.T) {
	httpClient := newWorkspaceHTTPClient(func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(t, response, map[string]any{"connector_grants": []connectorGrantWire{{
			WorkspaceKey: "WS", GrantID: "grant-1", ConnectorID: "github-main",
			BindingID: "other-binding", Action: "github.read",
			ResourcePattern: "repo:owner/name", CreatedAt: time.Now(),
		}}})
	})
	client, err := New(Config{BaseURL: "http://fleet.test", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ConnectorGrantCommands().ListConnectorGrantsByBinding(t.Context(), "WS", "binding-1")
	if !errors.Is(err, ErrConnectorGrantUnavailable) {
		t.Fatalf("cross-binding response error = %v, want ErrConnectorGrantUnavailable", err)
	}
}

func TestConnectorGrantCommandsMapCreateConflict(t *testing.T) {
	httpClient := newWorkspaceHTTPClient(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusConflict)
		writeJSON(t, response, map[string]string{"code": "already_exists", "error": "grant exists"})
	})
	client, err := New(Config{BaseURL: "http://fleet.test", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ConnectorGrantCommands().CreateConnectorGrant(t.Context(), ConnectorGrantCreateCommand{
		WorkspaceKey: "WS", GrantID: "grant-1", ConnectorID: "github-main",
		BindingID: "binding-1", Action: "github.read", ResourcePattern: "repo:owner/name",
	})
	if !errors.Is(err, ErrConnectorGrantConflict) {
		t.Fatalf("create conflict error = %v, want ErrConnectorGrantConflict", err)
	}
}
