package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"

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

func TestWorkspaceStoreCreatePersistsLifecycleFields(t *testing.T) {
	now := time.Now().UTC()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/admin/workspaces" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["key"] != "LOCALMODE" ||
			body["name"] != "Local Mode" ||
			body["description"] != "dogfood" ||
			body["default_branch"] != "localmode" {
			t.Fatalf("create body = %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, workspacemodule.Workspace{
			Key:           "LOCALMODE",
			Name:          "Local Mode",
			Description:   "dogfood",
			DefaultBranch: "localmode",
			CreatedAt:     now,
			UpdatedAt:     now,
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
	state := workspacemodule.StateReady
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
			if len(body) != 5 ||
				body["name"] != "Renamed" ||
				body["description"] != description ||
				body["default_branch"] != defaultBranch ||
				body["state"] != string(state) ||
				body["error_message"] != errorMessage {
				t.Fatalf("patch body = %+v, want all lifecycle fields", body)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			writeJSON(t, w, workspacemodule.Workspace{
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

func TestWorkspaceStoreCreateSendsDesignFormat(t *testing.T) {
	now := time.Now().UTC()
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/admin/workspaces" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["design_format"] != "html" {
			t.Fatalf("create body design_format = %v, want html (body=%+v)", body["design_format"], body)
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, workspacemodule.Workspace{
			Key:          "LOCALMODE",
			Name:         "Local Mode",
			DesignFormat: "html",
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}))

	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := client.Workspaces().Create(t.Context(), store.WorkspaceCreate{
		Key:          "LOCALMODE",
		Name:         "Local Mode",
		DesignFormat: "html",
	})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if ws.DesignFormat != "html" {
		t.Fatalf("DesignFormat = %q, want html", ws.DesignFormat)
	}
}

func TestWorkspaceStoreCreateOmitsEmptyDesignFormat(t *testing.T) {
	now := time.Now().UTC()
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/admin/workspaces" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if _, ok := body["design_format"]; ok {
			t.Fatalf("create body must omit design_format when empty: %+v", body)
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, workspacemodule.Workspace{Key: "LOCALMODE", Name: "Local Mode", CreatedAt: now, UpdatedAt: now})
	}))

	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := client.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "LOCALMODE", Name: "Local Mode"})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if ws.DesignFormat != "" {
		t.Fatalf("DesignFormat = %q, want empty", ws.DesignFormat)
	}
}

func TestWorkspaceStoreUpdateClearsDesignFormat(t *testing.T) {
	now := time.Now().UTC()
	var sawPatch bool
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			sawPatch = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			// A non-nil pointer to "" must serialize the field so the
			// server can clear it; omitempty only drops nil pointers.
			if len(body) != 1 || body["design_format"] != "" {
				t.Fatalf("patch body = %+v, want design_format=\"\" only", body)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			writeJSON(t, w, workspacemodule.Workspace{Key: "LOCALMODE", Name: "Local Mode", CreatedAt: now, UpdatedAt: now})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))

	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	ws, err := client.Workspaces().Update(t.Context(), "LOCALMODE", store.WorkspaceUpdate{DesignFormat: &empty})
	if err != nil {
		t.Fatalf("update workspace: %v", err)
	}
	if !sawPatch {
		t.Fatal("expected PATCH request")
	}
	if ws.DesignFormat != "" {
		t.Fatalf("DesignFormat = %q, want empty", ws.DesignFormat)
	}
}

func TestWorkspaceStoreUpdateSendsDesignFormat(t *testing.T) {
	now := time.Now().UTC()
	var sawPatch bool
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			sawPatch = true
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			if len(body) != 1 || body["design_format"] != "html" {
				t.Fatalf("patch body = %+v, want design_format only", body)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			writeJSON(t, w, workspacemodule.Workspace{
				Key:          "LOCALMODE",
				Name:         "Local Mode",
				DesignFormat: "html",
				CreatedAt:    now,
				UpdatedAt:    now,
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))

	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	format := "html"
	ws, err := client.Workspaces().Update(t.Context(), "LOCALMODE", store.WorkspaceUpdate{DesignFormat: &format})
	if err != nil {
		t.Fatalf("update workspace: %v", err)
	}
	if !sawPatch {
		t.Fatal("expected PATCH request")
	}
	if ws.DesignFormat != "html" {
		t.Fatalf("DesignFormat = %q, want html", ws.DesignFormat)
	}
}

func TestWorkspaceStoreUpdateSendsNameAndDesignFormat(t *testing.T) {
	now := time.Now().UTC()
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			if len(body) != 2 || body["name"] != "Renamed" || body["design_format"] != "markdown" {
				t.Fatalf("patch body = %+v, want name + design_format", body)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			writeJSON(t, w, workspacemodule.Workspace{Key: "LOCALMODE", Name: "Renamed", DesignFormat: "markdown", CreatedAt: now, UpdatedAt: now})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))

	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	format := "markdown"
	ws, err := client.Workspaces().Update(t.Context(), "LOCALMODE", store.WorkspaceUpdate{Name: &name, DesignFormat: &format})
	if err != nil {
		t.Fatalf("update workspace: %v", err)
	}
	if ws.Name != "Renamed" || ws.DesignFormat != "markdown" {
		t.Fatalf("workspace = %+v, want Renamed/markdown", ws)
	}
}

func TestWorkspaceStoreUpdatePersistsLifecycleFields(t *testing.T) {
	now := time.Now().UTC()
	state := workspacemodule.StateReady
	description := "desc"
	defaultBranch := "main"
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			if len(body) != 3 ||
				body["state"] != string(state) ||
				body["description"] != description ||
				body["default_branch"] != defaultBranch {
				t.Fatalf("patch body = %+v, want lifecycle fields", body)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			writeJSON(t, w, workspacemodule.Workspace{
				Key:           "LOCALMODE",
				Name:          "Local Mode",
				State:         state,
				Description:   description,
				DefaultBranch: defaultBranch,
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
	ws, err := client.Workspaces().Update(t.Context(), "LOCALMODE", store.WorkspaceUpdate{
		State:         &state,
		Description:   &description,
		DefaultBranch: &defaultBranch,
	})
	if err != nil {
		t.Fatalf("update workspace: %v", err)
	}
	if ws.State != state || ws.Description != description || ws.DefaultBranch != defaultBranch {
		t.Fatalf("workspace fields = %+v, want persisted lifecycle fields", ws)
	}
}

func TestWorkspaceStoreGetDecodesDesignFormat(t *testing.T) {
	now := time.Now().UTC()
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/workspaces/LOCALMODE" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		writeJSON(t, w, map[string]any{
			"key":           "LOCALMODE",
			"name":          "Local Mode",
			"design_format": "html",
			"created_at":    now,
			"updated_at":    now,
		})
	}))

	client, err := New(Config{BaseURL: "http://fleet.test", Actor: "tester", HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := client.Workspaces().Get(t.Context(), "LOCALMODE")
	if err != nil {
		t.Fatalf("get workspace: %v", err)
	}
	if ws.DesignFormat != "html" {
		t.Fatalf("DesignFormat = %q, want html", ws.DesignFormat)
	}
}

func TestWorkspaceStoreUpdateStateOnlyPersistsState(t *testing.T) {
	now := time.Now().UTC()
	state := workspacemodule.StateReady
	httpClient := newWorkspaceHTTPClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode patch: %v", err)
			}
			if len(body) != 1 || body["state"] != string(state) {
				t.Fatalf("patch body = %+v, want state only", body)
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/admin/workspaces/LOCALMODE":
			writeJSON(t, w, workspacemodule.Workspace{Key: "LOCALMODE", Name: "Local Mode", State: state, CreatedAt: now, UpdatedAt: now})
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
