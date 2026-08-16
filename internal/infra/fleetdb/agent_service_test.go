package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/agents"
)

func TestAgentServiceClientRoutesBodiesAndQueries(t *testing.T) {
	deletedAt := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-services":
			var req struct {
				ServiceID       string              `json:"service_id"`
				Name            string              `json:"name"`
				Kind            agents.AgentKind    `json:"kind"`
				DesiredState    agents.DesiredState `json:"desired_state"`
				RoleName        string              `json:"role_name"`
				DriverID        string              `json:"driver_id"`
				DriverVersionID string              `json:"driver_version_id"`
				ProfileName     string              `json:"profile_name"`
				EventSources    []string            `json:"event_sources"`
				TriggerRefs     []string            `json:"trigger_refs"`
				PlacementPolicy string              `json:"placement_policy"`
				MaxInstances    int                 `json:"max_instances"`
				RestartPolicy   string              `json:"restart_policy"`
				Permissions     []string            `json:"permissions"`
				BudgetPolicy    string              `json:"budget_policy"`
				StateRef        string              `json:"state_ref"`
				Metadata        map[string]string   `json:"metadata"`
			}
			decodeAgentServiceJSONBody(t, r, &req)
			if req.ServiceID == "scripted" {
				if req.RoleName != "" || req.DriverID != "driver-1" || req.DriverVersionID != "version-1" {
					t.Fatalf("scripted create body behavior = %+v", req)
				}
				writeJSON(t, w, agents.AgentServiceRecord{WorkspaceKey: "WS", ServiceID: req.ServiceID, Name: req.Name, Kind: req.Kind, DesiredState: req.DesiredState, DriverID: req.DriverID, DriverVersionID: req.DriverVersionID, CreatedBy: "tester", DeletedAt: &deletedAt, MaxInstances: req.MaxInstances})
				return
			}
			if req.ServiceID != "lead" || req.Kind != agents.AgentKindLead || req.DesiredState != agents.DesiredRunning || req.RoleName != "lead" || req.ProfileName != "falcon" {
				t.Fatalf("create body identity = %+v", req)
			}
			if req.MaxInstances != 2 || req.PlacementPolicy != "local" || req.RestartPolicy != "always" || req.BudgetPolicy != "daily:10" || req.StateRef != "state://lead" {
				t.Fatalf("create body policy fields = %+v", req)
			}
			if len(req.EventSources) != 1 || req.EventSources[0] != "github:issues" || len(req.TriggerRefs) != 1 || req.TriggerRefs[0] != "binding-1" || len(req.Permissions) != 1 || req.Permissions[0] != "task_run.create" || req.Metadata["tier"] != "gold" {
				t.Fatalf("create body collections = %+v", req)
			}
			writeJSON(t, w, agents.AgentServiceRecord{WorkspaceKey: "WS", ServiceID: req.ServiceID, Name: req.Name, Kind: req.Kind, DesiredState: req.DesiredState, RoleName: req.RoleName, ProfileName: req.ProfileName, CreatedBy: "tester", MaxInstances: req.MaxInstances})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-services":
			q := r.URL.Query()
			if q.Get("kind") != "lead" || q.Get("desired_state") != "running" || q.Get("role_name") != "lead" || q.Get("profile_name") != "falcon" || q.Get("include_deleted") != "true" || q.Get("limit") != "3" {
				t.Fatalf("list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"agent_services": []*agents.AgentServiceRecord{{WorkspaceKey: "WS", ServiceID: "lead", Kind: agents.AgentKindLead, DesiredState: agents.DesiredRunning, RoleName: "lead", ProfileName: "falcon", CreatedBy: "tester", MaxInstances: 2}}, "count": 1})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/agent-services/lead":
			writeJSON(t, w, agents.AgentServiceRecord{WorkspaceKey: "WS", ServiceID: "lead", Kind: agents.AgentKindLead, DesiredState: agents.DesiredRunning, RoleName: "lead", ProfileName: "falcon", CreatedBy: "tester", MaxInstances: 2, UpdatedAt: deletedAt})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/agent-services/lead":
			var req struct {
				DesiredState *agents.DesiredState `json:"desired_state"`
				LeaseID      *string              `json:"lease_id"`
				Metadata     *map[string]string   `json:"metadata"`
			}
			decodeAgentServiceJSONBody(t, r, &req)
			if req.DesiredState == nil || *req.DesiredState != agents.DesiredPaused || req.LeaseID == nil || *req.LeaseID != "lease-service-1" || req.Metadata == nil || (*req.Metadata)["tier"] != "silver" {
				t.Fatalf("update body = %+v", req)
			}
			writeJSON(t, w, agents.AgentServiceRecord{WorkspaceKey: "WS", ServiceID: "lead", Kind: agents.AgentKindLead, DesiredState: *req.DesiredState, RoleName: "lead", ProfileName: "falcon", LeaseID: *req.LeaseID, Metadata: *req.Metadata, MaxInstances: 2})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/agent-services/lead/archive":
			if got := r.Header.Get(FleetDelegatedActorHeader); got != "tester" {
				t.Fatalf("%s = %q, want tester", FleetDelegatedActorHeader, got)
			}
			var req struct {
				ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
			}
			decodeAgentServiceJSONBody(t, r, &req)
			if !req.ExpectedUpdatedAt.Equal(deletedAt) {
				t.Fatalf("expected_updated_at = %s, want %s", req.ExpectedUpdatedAt, deletedAt)
			}
			writeJSON(t, w, agents.AgentServiceRecord{
				WorkspaceKey: "WS", ServiceID: "lead",
				Kind: agents.AgentKindLead, DesiredState: agents.DesiredPaused,
				DeletedAt: &deletedAt, UpdatedAt: deletedAt,
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.AgentServices().Create(t.Context(), agents.AgentServiceCreate{
		WorkspaceKey:    "WS",
		ServiceID:       "lead",
		Name:            "Lead",
		Kind:            agents.AgentKindLead,
		DesiredState:    agents.DesiredRunning,
		RoleName:        "lead",
		ProfileName:     "falcon",
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
	if created.ServiceID != "lead" || created.MaxInstances != 2 {
		t.Fatalf("created = %+v, want lead", created)
	}
	if created.CreatedBy != "tester" {
		t.Fatalf("created_by = %q, want tester", created.CreatedBy)
	}

	scripted, err := client.AgentServices().Create(t.Context(), agents.AgentServiceCreate{
		WorkspaceKey:    "WS",
		ServiceID:       "scripted",
		Name:            "Scripted",
		Kind:            agents.AgentKindEvent,
		DesiredState:    agents.DesiredRunning,
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		MaxInstances:    1,
	})
	if err != nil {
		t.Fatalf("Create scripted agent service: %v", err)
	}
	if scripted.RoleName != "" || scripted.DriverID != "driver-1" || scripted.DriverVersionID != "version-1" || scripted.CreatedBy != "tester" || scripted.DeletedAt == nil {
		t.Fatalf("scripted = %+v, want driver fields, created_by, deleted_at", scripted)
	}

	services, err := client.AgentServices().List(t.Context(), "WS", agents.AgentServiceFilter{Kind: agents.AgentKindLead, DesiredState: agents.DesiredRunning, RoleName: "lead", ProfileName: "falcon", IncludeDeleted: true, Limit: 3})
	if err != nil {
		t.Fatalf("List agent services: %v", err)
	}
	if len(services) != 1 || services[0].ServiceID != "lead" {
		t.Fatalf("services = %+v, want lead", services)
	}
	got, err := client.AgentServices().Get(t.Context(), "WS", "lead")
	if err != nil {
		t.Fatalf("Get agent service: %v", err)
	}
	if got.ServiceID != "lead" {
		t.Fatalf("got = %+v, want lead", got)
	}

	paused := agents.DesiredPaused
	leaseID := "lease-service-1"
	metadata := map[string]string{"tier": "silver"}
	updated, err := client.AgentServices().Update(t.Context(), "WS", "lead", agents.AgentServiceUpdate{DesiredState: &paused, LeaseID: &leaseID, Metadata: &metadata})
	if err != nil {
		t.Fatalf("Update agent service: %v", err)
	}
	if updated.DesiredState != agents.DesiredPaused || updated.LeaseID != "lease-service-1" || updated.Metadata["tier"] != "silver" {
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
