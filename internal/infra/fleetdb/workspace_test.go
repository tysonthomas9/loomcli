package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type workspaceRoundTripFunc func(*http.Request) (*http.Response, error)

func (f workspaceRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newWorkspaceHTTPClient(handler http.HandlerFunc) *http.Client {
	return &http.Client{Transport: workspaceRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, r)
		return rr.Result(), nil
	})}
}

func TestWorkspaceStoreCreateOmitsUnsupportedFleetDBFields(t *testing.T) {
	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/admin/workspaces" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if _, ok := body["default_branch"]; ok {
			t.Fatalf("create body must not include default_branch: %+v", body)
		}
		if body["key"] != "LOCALMODE" || body["name"] != "Local Mode" || body["description"] != "dogfood" {
			t.Fatalf("create body = %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, domain.Workspace{
			Key:         "LOCALMODE",
			Name:        "Local Mode",
			Description: "dogfood",
			CreatedAt:   now,
			UpdatedAt:   now,
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := client.Workspaces().Create(t.Context(), store.WorkspaceCreate{
		Key:           "LOCALMODE",
		Name:          "Local Mode",
		Description:   "dogfood",
		DefaultBranch: "localmode",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if ws.DefaultBranch != "localmode" {
		t.Fatalf("DefaultBranch = %q, want localmode", ws.DefaultBranch)
	}
}

func TestWorkspaceStoreUpdateSendsSupportedFleetDBFields(t *testing.T) {
	now := time.Now().UTC()
	state := domain.WorkspaceStateReady
	description := "dogfood workspace"
	defaultBranch := "localmode"
	errorMessage := "clear"
	var sawPatch bool
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			sawPatch = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			if len(body) != 1 || body["name"] != "Renamed" {
				t.Fatalf("patch body = %+v, want name only", body)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			writeJSON(t, w, domain.Workspace{
				Key:           "LOCALMODE",
				Name:          "Renamed",
				Description:   "dogfood workspace",
				DefaultBranch: "localmode",
				State:         state,
				ErrorMessage:  "clear",
				CreatedAt:     now,
				UpdatedAt:     now,
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))

	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	ws, err := client.Workspaces().Update(t.Context(), "LOCALMODE", store.WorkspaceUpdate{
		Name:          &name,
		Description:   &description,
		DefaultBranch: &defaultBranch,
		State:         &state,
		ErrorMessage:  &errorMessage,
	})
	if err != nil {
		t.Fatalf("update workspace: %v", err)
	}
	if !sawPatch {
		t.Fatal("expected PATCH request")
	}
	if ws.Name != "Renamed" {
		t.Fatalf("Name = %q, want Renamed", ws.Name)
	}
	if ws.Description != description || ws.DefaultBranch != defaultBranch || ws.State != state || ws.ErrorMessage != errorMessage {
		t.Fatalf("workspace fields = %+v, want description/default branch/state/error", ws)
	}
}

func TestWorkspaceStoreUpdateStateOnlySkipsUnsupportedFleetDBPatch(t *testing.T) {
	now := time.Now().UTC()
	state := domain.WorkspaceStateReady
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			t.Fatalf("state-only update must not PATCH unsupported fleet-db fields")
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			writeJSON(t, w, domain.Workspace{Key: "LOCALMODE", Name: "Local Mode", State: state, CreatedAt: now, UpdatedAt: now})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))

	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := client.Workspaces().Update(t.Context(), "LOCALMODE", store.WorkspaceUpdate{State: &state})
	if err != nil {
		t.Fatalf("update workspace: %v", err)
	}
	if ws.Name != "Local Mode" {
		t.Fatalf("Name = %q, want Local Mode", ws.Name)
	}
	if ws.State != state {
		t.Fatalf("State = %q, want %q", ws.State, state)
	}
}
