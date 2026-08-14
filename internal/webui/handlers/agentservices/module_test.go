package agentservices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestListAgentServicesIncludesZeroBindingRecordWithExplicitHealth(t *testing.T) {
	st, _ := seededAgentServiceStore(t)
	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agent-services", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var raw struct {
		Success bool                     `json:"success"`
		Data    []map[string]interface{} `json:"data"`
		Total   int                      `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !raw.Success || raw.Total != 1 || len(raw.Data) != 1 {
		t.Fatalf("response = %#v", raw)
	}
	item := raw.Data[0]
	if item["id"] != "scout" || item["kind"] != "scripted" || item["enabled"] != true {
		t.Fatalf("identity = %#v", item)
	}
	bindings, ok := item["bindings"].([]interface{})
	if !ok || len(bindings) != 0 {
		t.Fatalf("bindings = %#v, want explicit empty array", item["bindings"])
	}
	if status, ok := item["lastRunStatus"]; !ok || status != "" {
		t.Fatalf("lastRunStatus = %#v present=%v, want explicit blank", status, ok)
	}
	if failures, ok := item["consecutiveFailures"]; !ok || failures != float64(0) {
		t.Fatalf("consecutiveFailures = %#v present=%v, want explicit zero", failures, ok)
	}
	if errs, ok := item["errors"].([]interface{}); !ok || len(errs) != 0 {
		t.Fatalf("errors = %#v, want explicit empty array", item["errors"])
	}
}

func TestListAgentServicesDecoratesCronAndManualRunHealth(t *testing.T) {
	st, svc := seededAgentServiceStore(t)
	if _, err := st.TriggerBindings().Create(t.Context(), store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "binding-cron-scout-weekly", Name: "Scout weekly", SourceKind: "cron",
		RouteKey: "cron.scout.weekly", DriverID: svc.DriverID, DriverVersionID: svc.DriverVersionID,
		TargetAgentServiceID: svc.ServiceID, Schedule: "@weekly", Enabled: true,
	}); err != nil {
		t.Fatalf("create binding: %v", err)
	}
	finishAgentServiceRun(t, st, svc, "run-completed", domain.DriverRunCompleted)
	finishAgentServiceRun(t, st, svc, "run-failed-1", domain.DriverRunFailed)
	finishAgentServiceRun(t, st, svc, "run-failed-2", domain.DriverRunFailed)
	if _, err := st.DriverRuns().Create(t.Context(), store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "run-manual", DriverID: svc.DriverID, DriverVersionID: svc.DriverVersionID,
		SourceKind: "api", SourceRef: "/manual", AgentServiceID: svc.ServiceID,
	}); err != nil {
		t.Fatalf("create manual run: %v", err)
	}

	mux := http.NewServeMux()
	NewModule(st).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agent-services", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data []agentServiceDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 {
		t.Fatalf("data = %#v", response.Data)
	}
	got := response.Data[0]
	if got.LastRunStatus != string(domain.DriverRunQueued) || got.ConsecutiveFailures != 2 {
		t.Fatalf("health = %q/%d, want queued/2", got.LastRunStatus, got.ConsecutiveFailures)
	}
	if got.NextFireAt == nil || !got.NextFireAt.After(time.Now()) || got.NextFireAt.After(time.Now().Add(8*24*time.Hour)) {
		t.Fatalf("nextFireAt = %v, want within the next week", got.NextFireAt)
	}
	if len(got.Bindings) != 1 || got.Bindings[0].ID != "binding-cron-scout-weekly" || got.Bindings[0].RouteKey != "cron.scout.weekly" {
		t.Fatalf("bindings = %#v", got.Bindings)
	}
}

func TestListAgentServicesSurfacesRunQueryErrors(t *testing.T) {
	base, _ := seededAgentServiceStore(t)
	st := &failingRunListStore{Store: base, err: errors.New("run backend unavailable")}
	mux := http.NewServeMux()
	NewModule(st).Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agent-services", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Data []agentServiceDTO `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 || len(response.Data[0].Errors) != 1 || response.Data[0].LastRunStatus != "" || response.Data[0].ConsecutiveFailures != 0 {
		t.Fatalf("data = %#v, want explicit unknown health with one error", response.Data)
	}
}

func TestListAgentServiceRunsDefaultsToTwentyNewestCamelCaseRuns(t *testing.T) {
	st, svc := seededAgentServiceStore(t)
	for i := 0; i < 21; i++ {
		if _, err := st.DriverRuns().Create(t.Context(), store.DriverRunCreate{
			WorkspaceKey: "WS", RunID: fmt.Sprintf("run-%02d", i), DriverID: svc.DriverID,
			DriverVersionID: svc.DriverVersionID, AgentServiceID: svc.ServiceID, SourceKind: "api",
		}); err != nil {
			t.Fatalf("create run %d: %v", i, err)
		}
	}
	mux := http.NewServeMux()
	NewModule(st).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agent-services/scout/runs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var raw struct {
		Success bool                     `json:"success"`
		Data    []map[string]interface{} `json:"data"`
		Total   int                      `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !raw.Success || raw.Total != 20 || len(raw.Data) != 20 || raw.Data[0]["runId"] != "run-20" {
		t.Fatalf("response = %#v", raw)
	}
	if _, snakeCase := raw.Data[0]["run_id"]; snakeCase {
		t.Fatalf("run wire contains snake_case field: %#v", raw.Data[0])
	}
	if raw.Data[0]["agentServiceId"] != "scout" || raw.Data[0]["sourceKind"] != "api" {
		t.Fatalf("run attribution = %#v", raw.Data[0])
	}
}

func seededAgentServiceStore(t *testing.T) (*memstore.Store, *domain.AgentService) {
	t.Helper()
	st := memstore.New()
	if _, err := st.Drivers().Create(t.Context(), store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "scout", Name: "scout", Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(t.Context(), store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "scout-v1", DriverID: "scout", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("create driver version: %v", err)
	}
	svc, err := st.AgentServices().Create(t.Context(), store.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: "scout", Name: "Scout", Kind: domain.AgentServiceKindCron,
		DesiredState: domain.AgentServiceDesiredRunning, DriverID: "scout", DriverVersionID: "scout-v1", CreatedBy: "system",
	})
	if err != nil {
		t.Fatalf("create agent service: %v", err)
	}
	return st, svc
}

func finishAgentServiceRun(t *testing.T, st store.Store, svc *domain.AgentService, runID string, status domain.DriverRunStatus) {
	t.Helper()
	if _, err := st.DriverRuns().Create(t.Context(), store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: runID, DriverID: svc.DriverID, DriverVersionID: svc.DriverVersionID,
		AgentServiceID: svc.ServiceID,
	}); err != nil {
		t.Fatalf("create %s: %v", runID, err)
	}
	claimed, err := st.DriverRuns().Claim(t.Context(), "WS", runID, "node-"+runID, "lease-"+runID)
	if err != nil {
		t.Fatalf("claim %s: %v", runID, err)
	}
	if _, err := st.DriverRuns().Finish(t.Context(), "WS", runID, store.DriverRunFinish{
		NodeID: claimed.NodeID, LeaseID: claimed.LeaseID, FencingToken: claimed.FencingToken, Status: status,
	}); err != nil {
		t.Fatalf("finish %s: %v", runID, err)
	}
}

type failingRunListStore struct {
	store.Store
	err error
}

func (s *failingRunListStore) DriverRuns() store.DriverRunStore {
	return &failingDriverRunStore{DriverRunStore: s.Store.DriverRuns(), err: s.err}
}

type failingDriverRunStore struct {
	store.DriverRunStore
	err error
}

func (s *failingDriverRunStore) List(context.Context, string, store.DriverRunFilter) ([]*domain.DriverRun, error) {
	return nil, s.err
}
