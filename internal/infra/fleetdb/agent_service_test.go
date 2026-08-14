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

func TestAgentServiceClientRoutesBodiesAndQueries(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-services":
			var req struct {
				ServiceID       string                          `json:"service_id"`
				Name            string                          `json:"name"`
				Kind            domain.AgentServiceKind         `json:"kind"`
				DesiredState    domain.AgentServiceDesiredState `json:"desired_state"`
				RoleName        string                          `json:"role_name"`
				DriverID        string                          `json:"driver_id"`
				DriverVersionID string                          `json:"driver_version_id"`
				ProfileName     string                          `json:"profile_name"`
				EventSources    []string                        `json:"event_sources"`
				TriggerRefs     []string                        `json:"trigger_refs"`
				PlacementPolicy string                          `json:"placement_policy"`
				MaxInstances    int                             `json:"max_instances"`
				RestartPolicy   string                          `json:"restart_policy"`
				Permissions     []string                        `json:"permissions"`
				BudgetPolicy    string                          `json:"budget_policy"`
				StateRef        string                          `json:"state_ref"`
				Metadata        map[string]string               `json:"metadata"`
			}
			decodeAgentServiceJSONBody(t, r, &req)
			if req.ServiceID != "scout" || req.Kind != domain.AgentServiceKindCron || req.DesiredState != domain.AgentServiceDesiredRunning || req.RoleName != "" || req.DriverID != "scout-driver" || req.DriverVersionID != "scout-v1" {
				t.Fatalf("create body identity = %+v", req)
			}
			if req.MaxInstances != 2 || req.PlacementPolicy != "local" || req.RestartPolicy != "always" || req.BudgetPolicy != "daily:10" || req.StateRef != "state://lead" {
				t.Fatalf("create body policy fields = %+v", req)
			}
			if len(req.EventSources) != 1 || req.EventSources[0] != "github:issues" || len(req.TriggerRefs) != 1 || req.TriggerRefs[0] != "binding-1" || len(req.Permissions) != 1 || req.Permissions[0] != "task_run.create" || req.Metadata["tier"] != "gold" {
				t.Fatalf("create body collections = %+v", req)
			}
			writeJSON(t, w, domain.AgentService{WorkspaceKey: "WS", ServiceID: req.ServiceID, Name: req.Name, Kind: req.Kind, DesiredState: req.DesiredState, DriverID: req.DriverID, DriverVersionID: req.DriverVersionID, CreatedBy: "system", MaxInstances: req.MaxInstances})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-services":
			q := r.URL.Query()
			if q.Get("kind") != "cron" || q.Get("desired_state") != "running" || q.Get("include_deleted") != "true" || q.Get("limit") != "3" {
				t.Fatalf("list query = %s", r.URL.RawQuery)
			}
			deletedAt := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
			writeJSON(t, w, map[string]any{"agent_services": []*domain.AgentService{{WorkspaceKey: "WS", ServiceID: "scout", Kind: domain.AgentServiceKindCron, DesiredState: domain.AgentServiceDesiredRunning, DriverID: "scout-driver", DriverVersionID: "scout-v1", CreatedBy: "system", DeletedAt: &deletedAt, MaxInstances: 2}}, "count": 1})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-services/lead":
			writeJSON(t, w, domain.AgentService{WorkspaceKey: "WS", ServiceID: "lead", Kind: domain.AgentServiceKindLead, DesiredState: domain.AgentServiceDesiredRunning, RoleName: "lead", ProfileName: "falcon", MaxInstances: 2})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/agent-services/lead":
			var req struct {
				DesiredState *domain.AgentServiceDesiredState `json:"desired_state"`
				RoleName     *string                          `json:"role_name"`
				DriverID     *string                          `json:"driver_id"`
				VersionID    *string                          `json:"driver_version_id"`
				LeaseID      *string                          `json:"lease_id"`
				Metadata     *map[string]string               `json:"metadata"`
			}
			decodeAgentServiceJSONBody(t, r, &req)
			if req.DesiredState == nil || *req.DesiredState != domain.AgentServiceDesiredPaused || req.RoleName == nil || *req.RoleName != "" || req.DriverID == nil || *req.DriverID != "scout-driver" || req.VersionID == nil || *req.VersionID != "scout-v2" || req.LeaseID == nil || *req.LeaseID != "lease-service-1" || req.Metadata == nil || (*req.Metadata)["tier"] != "silver" {
				t.Fatalf("update body = %+v", req)
			}
			writeJSON(t, w, domain.AgentService{WorkspaceKey: "WS", ServiceID: "lead", Kind: domain.AgentServiceKindLead, DesiredState: *req.DesiredState, DriverID: *req.DriverID, DriverVersionID: *req.VersionID, ProfileName: "falcon", LeaseID: *req.LeaseID, Metadata: *req.Metadata, MaxInstances: 2})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/WS/agent-services/lead":
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
	created, err := client.AgentServices().Create(t.Context(), store.AgentServiceCreate{
		WorkspaceKey:    "WS",
		ServiceID:       "scout",
		Name:            "Scout",
		Kind:            domain.AgentServiceKindCron,
		DesiredState:    domain.AgentServiceDesiredRunning,
		DriverID:        "scout-driver",
		DriverVersionID: "scout-v1",
		CreatedBy:       "system",
		EventSources:    []string{"github:issues"},
		TriggerRefs:     []string{"binding-1"},
		PlacementPolicy: "local",
		MaxInstances:    2,
		RestartPolicy:   "always",
		Permissions:     []string{"task_run.create"},
		BudgetPolicy:    "daily:10",
		StateRef:        "state://lead",
		Metadata:        map[string]string{"tier": "gold"},
	})
	if err != nil {
		t.Fatalf("Create agent service: %v", err)
	}
	if created.ServiceID != "scout" || created.DriverID != "scout-driver" || created.DriverVersionID != "scout-v1" || created.CreatedBy != "system" || created.MaxInstances != 2 {
		t.Fatalf("created = %+v, want scripted scout", created)
	}

	services, err := client.AgentServices().List(t.Context(), "WS", store.AgentServiceFilter{Kind: domain.AgentServiceKindCron, DesiredState: domain.AgentServiceDesiredRunning, IncludeDeleted: true, Limit: 3})
	if err != nil {
		t.Fatalf("List agent services: %v", err)
	}
	if len(services) != 1 || services[0].ServiceID != "scout" || services[0].DeletedAt == nil {
		t.Fatalf("services = %+v, want archived scout wire fields", services)
	}
	got, err := client.AgentServices().Get(t.Context(), "WS", "lead")
	if err != nil {
		t.Fatalf("Get agent service: %v", err)
	}
	if got.ServiceID != "lead" {
		t.Fatalf("got = %+v, want lead", got)
	}

	paused := domain.AgentServiceDesiredPaused
	emptyRole := ""
	driverID := "scout-driver"
	versionID := "scout-v2"
	leaseID := "lease-service-1"
	metadata := map[string]string{"tier": "silver"}
	updated, err := client.AgentServices().Update(t.Context(), "WS", "lead", store.AgentServiceUpdate{DesiredState: &paused, RoleName: &emptyRole, DriverID: &driverID, DriverVersionID: &versionID, LeaseID: &leaseID, Metadata: &metadata})
	if err != nil {
		t.Fatalf("Update agent service: %v", err)
	}
	if updated.DesiredState != domain.AgentServiceDesiredPaused || updated.RoleName != "" || updated.DriverID != "scout-driver" || updated.DriverVersionID != "scout-v2" || updated.LeaseID != "lease-service-1" || updated.Metadata["tier"] != "silver" {
		t.Fatalf("updated = %+v, want paused leased silver service", updated)
	}
	if err := client.AgentServices().Delete(t.Context(), "WS", "lead"); err != nil {
		t.Fatalf("Delete agent service: %v", err)
	}
}

func decodeAgentServiceJSONBody(t *testing.T, r *http.Request, out any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
}
