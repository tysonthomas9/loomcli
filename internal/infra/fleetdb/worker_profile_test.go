package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestWorkerProfileClientRoutesBodiesAndQueries(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/worker-profiles":
			var req struct {
				ProfileID     string            `json:"profile_id"`
				Name          string            `json:"name"`
				Role          string            `json:"role"`
				Backend       string            `json:"backend"`
				RuntimePolicy map[string]string `json:"runtime_policy"`
				Repos         []string          `json:"repos"`
				MaxPriority   *int              `json:"max_priority"`
				MaxParallel   int               `json:"max_parallel"`
				ParentEpic    string            `json:"parent_epic"`
				Labels        []string          `json:"labels"`
				Capabilities  []string          `json:"capabilities"`
				Enabled       *bool             `json:"enabled"`
				Metadata      map[string]string `json:"metadata"`
			}
			decodeWorkerProfileJSONBody(t, r, &req)
			if req.ProfileID != "falcon" || req.Name != "Falcon" || req.Role != "task" || req.Backend != "codex" {
				t.Fatalf("create body identity = %+v", req)
			}
			if req.MaxPriority == nil || *req.MaxPriority != 2 || req.Enabled == nil || *req.Enabled {
				t.Fatalf("create body max_priority/enabled = %+v", req)
			}
			if req.RuntimePolicy["network"] != "restricted" || req.MaxParallel != 2 {
				t.Fatalf("create body scheduling = %+v, want runtime_policy and max_parallel", req)
			}
			if req.ParentEpic != "EPIC-1" || len(req.Repos) != 1 || req.Repos[0] != "api" || len(req.Labels) != 1 || req.Labels[0] != "gpu" || len(req.Capabilities) != 1 || req.Capabilities[0] != "tests" || req.Metadata["tier"] != "gold" {
				t.Fatalf("create body collections = %+v", req)
			}
			writeJSON(t, w, domain.WorkerProfile{WorkspaceKey: "WS", ProfileID: req.ProfileID, Name: req.Name, Role: req.Role, Backend: req.Backend, RuntimePolicy: req.RuntimePolicy, MaxPriority: req.MaxPriority, MaxParallel: req.MaxParallel, Enabled: *req.Enabled})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/worker-profiles":
			q := r.URL.Query()
			if q.Get("role") != "task" || q.Get("backend") != "codex" || q.Get("enabled") != "false" || q.Get("limit") != "3" {
				t.Fatalf("list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"worker_profiles": []*domain.WorkerProfile{{WorkspaceKey: "WS", ProfileID: "falcon", Role: "task", Backend: "codex", Enabled: false}}, "count": 1})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/worker-profiles/falcon":
			writeJSON(t, w, domain.WorkerProfile{WorkspaceKey: "WS", ProfileID: "falcon", Role: "task", Backend: "codex", Enabled: false})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/worker-profiles/falcon":
			var req struct {
				Name             *string            `json:"name"`
				Backend          *string            `json:"backend"`
				RuntimePolicy    *map[string]string `json:"runtime_policy"`
				Repos            *[]string          `json:"repos"`
				MaxParallel      *int               `json:"max_parallel"`
				ClearMaxPriority bool               `json:"clear_max_priority"`
				Enabled          *bool              `json:"enabled"`
				Metadata         *map[string]string `json:"metadata"`
			}
			decodeWorkerProfileJSONBody(t, r, &req)
			if req.Name == nil || *req.Name != "Falcon v2" || req.Backend == nil || *req.Backend != "claude" || req.Enabled == nil || !*req.Enabled {
				t.Fatalf("update body scalars = %+v", req)
			}
			if !req.ClearMaxPriority {
				t.Fatalf("update body clear_max_priority = false, want true")
			}
			if req.RuntimePolicy == nil || (*req.RuntimePolicy)["network"] != "open" || req.MaxParallel == nil || *req.MaxParallel != 3 {
				t.Fatalf("update body scheduling = %+v, want runtime_policy and max_parallel", req)
			}
			if req.Repos == nil || len(*req.Repos) != 0 || req.Metadata == nil || (*req.Metadata)["tier"] != "platinum" {
				t.Fatalf("update body collections = %+v", req)
			}
			writeJSON(t, w, domain.WorkerProfile{WorkspaceKey: "WS", ProfileID: "falcon", Name: *req.Name, Role: "task", Backend: *req.Backend, RuntimePolicy: *req.RuntimePolicy, MaxParallel: *req.MaxParallel, Repos: *req.Repos, Enabled: *req.Enabled, Metadata: *req.Metadata})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/WS/worker-profiles/falcon":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	maxPriority := 2
	enabled := false
	created, err := client.WorkerProfiles().Create(t.Context(), store.WorkerProfileCreate{
		WorkspaceKey:  "WS",
		ProfileID:     "falcon",
		Name:          "Falcon",
		Role:          "task",
		Backend:       "codex",
		RuntimePolicy: map[string]string{"network": "restricted"},
		Repos:         []string{"api"},
		MaxPriority:   &maxPriority,
		MaxParallel:   2,
		ParentEpic:    "EPIC-1",
		Labels:        []string{"gpu"},
		Capabilities:  []string{"tests"},
		Enabled:       &enabled,
		Metadata:      map[string]string{"tier": "gold"},
	})
	if err != nil {
		t.Fatalf("Create worker profile: %v", err)
	}
	if created.ProfileID != "falcon" || created.Enabled || created.RuntimePolicy["network"] != "restricted" || created.MaxParallel != 2 {
		t.Fatalf("created = %+v, want falcon disabled", created)
	}

	profiles, err := client.WorkerProfiles().List(t.Context(), "WS", store.WorkerProfileFilter{Role: "task", Backend: "codex", Enabled: &enabled, Limit: 3})
	if err != nil {
		t.Fatalf("List worker profiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].ProfileID != "falcon" {
		t.Fatalf("profiles = %+v, want falcon", profiles)
	}
	got, err := client.WorkerProfiles().Get(t.Context(), "WS", "falcon")
	if err != nil {
		t.Fatalf("Get worker profile: %v", err)
	}
	if got.ProfileID != "falcon" {
		t.Fatalf("got = %+v, want falcon", got)
	}

	name := "Falcon v2"
	backend := "claude"
	repos := []string{}
	runtimePolicy := map[string]string{"network": "open"}
	maxParallel := 3
	metadata := map[string]string{"tier": "platinum"}
	enabled = true
	updated, err := client.WorkerProfiles().Update(t.Context(), "WS", "falcon", store.WorkerProfileUpdate{Name: &name, Backend: &backend, RuntimePolicy: &runtimePolicy, Repos: &repos, MaxParallel: &maxParallel, ClearMaxPriority: true, Enabled: &enabled, Metadata: &metadata})
	if err != nil {
		t.Fatalf("Update worker profile: %v", err)
	}
	if updated.Name != name || updated.Backend != backend || !updated.Enabled || updated.RuntimePolicy["network"] != "open" || updated.MaxParallel != 3 || len(updated.Repos) != 0 || updated.Metadata["tier"] != "platinum" {
		t.Fatalf("updated = %+v, want patched profile", updated)
	}
	if err := client.WorkerProfiles().Delete(t.Context(), "WS", "falcon"); err != nil {
		t.Fatalf("Delete worker profile: %v", err)
	}
}

func decodeWorkerProfileJSONBody(t *testing.T, r *http.Request, out any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
}
