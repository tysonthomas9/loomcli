package fleetdb

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// connectorTestServer is the fake fleet-db for the connector control-plane
// routes. It asserts snake_case request shapes and responds with snake_case
// bodies the way fleet-db's connector handlers do: Get/List/Create/Rotate
// return REDACTED connectors (no secret fields); only GET .../secrets
// returns the inbound secrets + sealed credential ciphertext.
func connectorTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	validUntil := now.Add(15 * time.Minute)
	redactedConnector := map[string]any{
		"workspace_key":         "WS",
		"connector_id":          "gh-main",
		"source_kind":           "github",
		"display_name":          "GitHub main",
		"inbound_endpoint_path": "/hooks/github/gh-main",
		"status":                "active",
		"created_by":            "tester",
		"created_at":            now,
		"updated_at":            now,
	}
	grantRow := map[string]any{
		"workspace_key":    "WS",
		"grant_id":         "grant-1",
		"connector_id":     "gh-main",
		"binding_id":       "binding-1",
		"action":           "github.merge",
		"resource_pattern": "repo:octocat/hello",
		"created_at":       now,
	}
	callRow := map[string]any{
		"workspace_key":     "WS",
		"call_id":           "run-1#github.merge#1",
		"seq":               1,
		"run_id":            "run-1",
		"binding_id":        "binding-1",
		"connector_id":      "gh-main",
		"source_kind":       "github",
		"action":            "github.merge",
		"resource":          "repo:octocat/hello",
		"decision":          "granted",
		"upstream_status":   200,
		"sanitized_summary": "merged PR 7",
		"occurred_at":       now,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/connectors":
			var req struct {
				ConnectorID              string `json:"connector_id"`
				SourceKind               string `json:"source_kind"`
				DisplayName              string `json:"display_name"`
				InboundEndpointPath      string `json:"inbound_endpoint_path"`
				InboundSecret            string `json:"inbound_secret"`
				OutboundCredentialSealed []byte `json:"outbound_credential_sealed"`
				Status                   string `json:"status"`
				CreatedBy                string `json:"created_by"`
			}
			decodeJSONBody(t, r, &req)
			if req.ConnectorID == "gh-dupe" {
				w.WriteHeader(http.StatusConflict)
				writeJSON(t, w, map[string]any{"error": map[string]string{"code": "already_exists", "message": "connector exists"}})
				return
			}
			if req.ConnectorID != "gh-main" || req.SourceKind != "github" || req.DisplayName != "GitHub main" {
				t.Errorf("create connector body = %+v", req)
			}
			if req.InboundSecret != "whsec-1" || string(req.OutboundCredentialSealed) != "sealed-ciphertext" {
				t.Errorf("create connector secrets = %q / %q", req.InboundSecret, req.OutboundCredentialSealed)
			}
			if req.Status != "" || req.CreatedBy != "tester" {
				t.Errorf("create connector status/created_by = %q / %q (status empty defaults server-side)", req.Status, req.CreatedBy)
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, redactedConnector)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/connectors":
			q := r.URL.Query()
			if q.Get("source_kind") != "github" || q.Get("status") != "active" || q.Get("limit") != "5" {
				t.Errorf("list connectors query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"connectors": []map[string]any{redactedConnector}, "count": 1})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/connectors/gh-main":
			writeJSON(t, w, redactedConnector)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/connectors/missing":
			w.WriteHeader(http.StatusNotFound)
			writeJSON(t, w, map[string]any{"error": map[string]string{"code": "not_found", "message": "connector not found"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/connectors/gh-main/secrets":
			writeJSON(t, w, map[string]any{
				"connector_id":                "gh-main",
				"inbound_secret":              "whsec-2",
				"previous_inbound_secret":     "whsec-1",
				"previous_secret_valid_until": validUntil,
				"outbound_credential_sealed":  []byte("sealed-ciphertext"),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/connectors/missing/secrets":
			w.WriteHeader(http.StatusNotFound)
			writeJSON(t, w, map[string]any{"error": map[string]string{"code": "not_found", "message": "connector not found"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/connectors/gh-main/rotate":
			var req struct {
				NewInboundSecret            string     `json:"new_inbound_secret"`
				PreviousSecretValidUntil    *time.Time `json:"previous_secret_valid_until"`
				NewOutboundCredentialSealed []byte     `json:"new_outbound_credential_sealed"`
			}
			decodeJSONBody(t, r, &req)
			if req.NewInboundSecret != "whsec-2" {
				t.Errorf("rotate new_inbound_secret = %q", req.NewInboundSecret)
			}
			if req.PreviousSecretValidUntil != nil {
				t.Errorf("rotate previous_secret_valid_until = %v, want absent for zero input", req.PreviousSecretValidUntil)
			}
			if req.NewOutboundCredentialSealed != nil {
				t.Errorf("rotate new_outbound_credential_sealed = %q, want absent for nil input", req.NewOutboundCredentialSealed)
			}
			rotated := cloneJSONMap(redactedConnector)
			rotated["rotated_at"] = now
			writeJSON(t, w, rotated)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/connector-grants":
			var req struct {
				GrantID         string `json:"grant_id"`
				ConnectorID     string `json:"connector_id"`
				BindingID       string `json:"binding_id"`
				Action          string `json:"action"`
				ResourcePattern string `json:"resource_pattern"`
			}
			decodeJSONBody(t, r, &req)
			if req.GrantID != "grant-1" || req.ConnectorID != "gh-main" || req.BindingID != "binding-1" ||
				req.Action != "github.merge" || req.ResourcePattern != "repo:octocat/hello" {
				t.Errorf("create grant body = %+v", req)
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, grantRow)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/connector-grants":
			q := r.URL.Query()
			if q.Get("binding_id") == "" && q.Get("connector_id") == "" {
				t.Errorf("list grants query missing binding_id/connector_id: %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"connector_grants": []map[string]any{grantRow}, "count": 1})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/connector-grants/grant-1/revoke":
			revoked := cloneJSONMap(grantRow)
			revoked["revoked_at"] = now
			writeJSON(t, w, revoked)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/connector-grants/grant-revoked/revoke":
			w.WriteHeader(http.StatusConflict)
			writeJSON(t, w, map[string]any{"error": map[string]string{"code": "invalid_transition", "message": "grant already revoked"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/connector-audit":
			var req struct {
				CallID     string `json:"call_id"`
				Seq        int    `json:"seq"`
				RunID      string `json:"run_id"`
				SourceKind string `json:"source_kind"`
				Action     string `json:"action"`
				Decision   string `json:"decision"`
			}
			decodeJSONBody(t, r, &req)
			if req.RunID == "run-dupe" {
				w.WriteHeader(http.StatusConflict)
				writeJSON(t, w, map[string]any{"error": map[string]string{"code": "already_exists", "message": "duplicate call id"}})
				return
			}
			if req.CallID != "run-1#github.merge#1" || req.Seq != 1 || req.SourceKind != "github" ||
				req.Action != "github.merge" || req.Decision != "granted" {
				t.Errorf("append call body = %+v", req)
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, callRow)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/connector-audit":
			q := r.URL.Query()
			if q.Get("run_id") == "" && q.Get("binding_id") == "" {
				t.Errorf("list calls query missing run_id/binding_id: %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"connector_calls": []map[string]any{callRow}, "count": 1})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestConnectorClientConnectorRoutes(t *testing.T) {
	ts := connectorTestServer(t)
	defer ts.Close()
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	created, err := client.Connectors().Create(t.Context(), store.ConnectorCreate{
		WorkspaceKey:             "WS",
		ConnectorID:              "gh-main",
		SourceKind:               domain.ConnectorSourceGitHub,
		DisplayName:              "GitHub main",
		InboundEndpointPath:      "/hooks/github/gh-main",
		InboundSecret:            "whsec-1",
		OutboundCredentialSealed: []byte("sealed-ciphertext"),
		CreatedBy:                "tester",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ConnectorID != "gh-main" || created.SourceKind != domain.ConnectorSourceGitHub || created.Status != domain.ConnectorStatusActive {
		t.Fatalf("Create connector = %+v", created)
	}
	// fleet-db returns redacted connectors from Create/Get/List.
	if created.InboundSecret != "" || created.HasOutboundCredential() {
		t.Fatalf("Create returned unredacted connector: %+v", created)
	}

	got, err := client.Connectors().Get(t.Context(), "WS", "gh-main")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ConnectorID != "gh-main" || !got.CreatedAt.Equal(now) {
		t.Fatalf("Get connector = %+v", got)
	}

	listed, err := client.Connectors().List(t.Context(), "WS", store.ConnectorFilter{
		SourceKind: domain.ConnectorSourceGitHub,
		Status:     domain.ConnectorStatusActive,
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].ConnectorID != "gh-main" {
		t.Fatalf("List = %+v", listed)
	}

	rotated, err := client.Connectors().RotateSecrets(t.Context(), "WS", "gh-main", store.ConnectorSecretRotation{
		NewInboundSecret: "whsec-2",
	})
	if err != nil {
		t.Fatalf("RotateSecrets: %v", err)
	}
	if rotated.RotatedAt == nil || !rotated.RotatedAt.Equal(now) {
		t.Fatalf("RotateSecrets connector = %+v, want rotated_at set", rotated)
	}
}

func TestConnectorClientResolveSecrets(t *testing.T) {
	ts := connectorTestServer(t)
	defer ts.Close()
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	validUntil := time.Date(2026, 6, 12, 10, 15, 0, 0, time.UTC)

	secrets, err := client.Connectors().ResolveInboundSecret(t.Context(), "WS", "gh-main")
	if err != nil {
		t.Fatalf("ResolveInboundSecret: %v", err)
	}
	if secrets.Current != "whsec-2" || secrets.Previous != "whsec-1" || !secrets.PreviousValidUntil.Equal(validUntil) {
		t.Fatalf("ResolveInboundSecret = %+v", secrets)
	}

	sealed, err := client.Connectors().ResolveOutboundCredentialSealed(t.Context(), "WS", "gh-main")
	if err != nil {
		t.Fatalf("ResolveOutboundCredentialSealed: %v", err)
	}
	if string(sealed) != "sealed-ciphertext" {
		t.Fatalf("ResolveOutboundCredentialSealed = %q", sealed)
	}
}

func TestConnectorClientGrantAndAuditRoutes(t *testing.T) {
	ts := connectorTestServer(t)
	defer ts.Close()
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	grant, err := client.ConnectorGrants().Create(t.Context(), store.ConnectorGrantCreate{
		WorkspaceKey:    "WS",
		GrantID:         "grant-1",
		ConnectorID:     "gh-main",
		BindingID:       "binding-1",
		Action:          "github.merge",
		ResourcePattern: "repo:octocat/hello",
	})
	if err != nil {
		t.Fatalf("grant Create: %v", err)
	}
	if grant.GrantID != "grant-1" || grant.Revoked() {
		t.Fatalf("grant Create = %+v", grant)
	}

	byBinding, err := client.ConnectorGrants().ListByBinding(t.Context(), "WS", "binding-1")
	if err != nil {
		t.Fatalf("ListByBinding: %v", err)
	}
	if len(byBinding) != 1 || byBinding[0].BindingID != "binding-1" {
		t.Fatalf("ListByBinding = %+v", byBinding)
	}

	byConnector, err := client.ConnectorGrants().ListByConnector(t.Context(), "WS", "gh-main")
	if err != nil {
		t.Fatalf("ListByConnector: %v", err)
	}
	if len(byConnector) != 1 || byConnector[0].ConnectorID != "gh-main" {
		t.Fatalf("ListByConnector = %+v", byConnector)
	}

	if err := client.ConnectorGrants().Revoke(t.Context(), "WS", "grant-1"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	rec := &domain.ConnectorCallRecord{
		WorkspaceKey:     "WS",
		CallID:           domain.ConnectorCallID("run-1", "github.merge", 1),
		Seq:              1,
		RunID:            "run-1",
		BindingID:        "binding-1",
		ConnectorID:      "gh-main",
		SourceKind:       domain.ConnectorSourceGitHub,
		Action:           "github.merge",
		Resource:         "repo:octocat/hello",
		Decision:         domain.ConnectorCallGranted,
		UpstreamStatus:   200,
		SanitizedSummary: "merged PR 7",
		OccurredAt:       now,
	}
	if err := client.ConnectorCalls().Append(t.Context(), rec); err != nil {
		t.Fatalf("audit Append: %v", err)
	}

	byRun, err := client.ConnectorCalls().ListByRun(t.Context(), "WS", "run-1", store.ConnectorCallFilter{
		Decision: domain.ConnectorCallGranted,
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("ListByRun: %v", err)
	}
	if len(byRun) != 1 || byRun[0].CallID != "run-1#github.merge#1" || byRun[0].Decision != domain.ConnectorCallGranted {
		t.Fatalf("ListByRun = %+v", byRun)
	}

	byBindingCalls, err := client.ConnectorCalls().ListByBinding(t.Context(), "WS", "binding-1", store.ConnectorCallFilter{})
	if err != nil {
		t.Fatalf("audit ListByBinding: %v", err)
	}
	if len(byBindingCalls) != 1 || byBindingCalls[0].BindingID != "binding-1" {
		t.Fatalf("audit ListByBinding = %+v", byBindingCalls)
	}
}

// TestConnectorClientErrorClassification asserts the HTTP error → CV1
// sentinel mapping: 409 already_exists on connector create →
// domain.ErrConnectorExists, 404 → domain.ErrConnectorNotFound (each also
// matching its generic counterpart), 409 invalid_transition on grant revoke
// → domain.ErrGrantRevoked, and duplicate audit appends →
// domain.ErrAlreadyExists.
func TestConnectorClientErrorClassification(t *testing.T) {
	ts := connectorTestServer(t)
	defer ts.Close()
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("create duplicate connector", func(t *testing.T) {
		_, err := client.Connectors().Create(t.Context(), store.ConnectorCreate{
			WorkspaceKey: "WS",
			ConnectorID:  "gh-dupe",
			SourceKind:   domain.ConnectorSourceGitHub,
		})
		if !errors.Is(err, domain.ErrConnectorExists) || !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("Create dupe err = %v, want ErrConnectorExists", err)
		}
	})

	t.Run("get missing connector", func(t *testing.T) {
		_, err := client.Connectors().Get(t.Context(), "WS", "missing")
		if !errors.Is(err, domain.ErrConnectorNotFound) || !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Get missing err = %v, want ErrConnectorNotFound", err)
		}
	})

	t.Run("resolve missing connector", func(t *testing.T) {
		_, err := client.Connectors().ResolveInboundSecret(t.Context(), "WS", "missing")
		if !errors.Is(err, domain.ErrConnectorNotFound) {
			t.Fatalf("ResolveInboundSecret missing err = %v, want ErrConnectorNotFound", err)
		}
	})

	t.Run("revoke already-revoked grant", func(t *testing.T) {
		err := client.ConnectorGrants().Revoke(t.Context(), "WS", "grant-revoked")
		if !errors.Is(err, domain.ErrGrantRevoked) {
			t.Fatalf("Revoke revoked err = %v, want ErrGrantRevoked", err)
		}
	})

	t.Run("append duplicate audit row", func(t *testing.T) {
		err := client.ConnectorCalls().Append(t.Context(), &domain.ConnectorCallRecord{
			WorkspaceKey: "WS",
			CallID:       domain.ConnectorCallID("run-dupe", "github.merge", 1),
			Seq:          1,
			RunID:        "run-dupe",
			BindingID:    "binding-1",
			ConnectorID:  "gh-main",
			SourceKind:   domain.ConnectorSourceGitHub,
			Action:       "github.merge",
			Decision:     domain.ConnectorCallGranted,
			OccurredAt:   time.Now().UTC(),
		})
		if !errors.Is(err, domain.ErrAlreadyExists) {
			t.Fatalf("Append dupe err = %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("append invalid record fails client-side", func(t *testing.T) {
		err := client.ConnectorCalls().Append(t.Context(), &domain.ConnectorCallRecord{
			WorkspaceKey: "WS",
			CallID:       "mismatched",
			RunID:        "run-1",
			BindingID:    "binding-1",
			ConnectorID:  "gh-main",
			SourceKind:   domain.ConnectorSourceGitHub,
			Action:       "github.merge",
			Decision:     domain.ConnectorCallGranted,
		})
		if !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("Append invalid err = %v, want ErrInvalid", err)
		}
	})
}
