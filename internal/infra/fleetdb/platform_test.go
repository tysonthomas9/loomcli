package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestPlatformClientDriverRunLifecycleRoutesAndErrors(t *testing.T) {
	var claimCount int
	recoveryBefore := time.Date(2026, 6, 6, 12, 34, 56, 0, time.UTC)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/drivers":
			var req struct {
				DriverID string `json:"driver_id"`
				Name     string `json:"name"`
			}
			decodeJSONBody(t, r, &req)
			if req.DriverID != "driver-1" || req.Name != "epic-runner" {
				t.Fatalf("driver create body = %+v", req)
			}
			writeJSON(t, w, domain.Driver{WorkspaceKey: "WS", DriverID: req.DriverID, Name: req.Name, Status: domain.DriverStatusActive})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/drivers/driver-1/versions":
			var req struct {
				VersionID    string `json:"version_id"`
				Version      int    `json:"version"`
				BundleDigest string `json:"bundle_digest"`
			}
			decodeJSONBody(t, r, &req)
			if req.VersionID != "version-1" || req.Version != 1 || req.BundleDigest != "sha256:bundle" {
				t.Fatalf("version create body = %+v", req)
			}
			writeJSON(t, w, domain.DriverVersion{WorkspaceKey: "WS", VersionID: req.VersionID, DriverID: "driver-1", Version: req.Version})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/driver-versions":
			if r.URL.Query().Get("validation_status") != "passed" || r.URL.Query().Get("limit") != "1" {
				t.Fatalf("driver version list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"driver_versions": []domain.DriverVersion{{WorkspaceKey: "WS", VersionID: "version-1"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/trigger-bindings":
			var req struct {
				BindingID       string `json:"binding_id"`
				RouteKey        string `json:"route_key"`
				DriverID        string `json:"driver_id"`
				DriverVersionID string `json:"driver_version_id"`
			}
			decodeJSONBody(t, r, &req)
			if req.BindingID != "binding-1" || req.RouteKey != "epics.runs.create" || req.DriverVersionID != "version-1" {
				t.Fatalf("trigger binding create body = %+v", req)
			}
			writeJSON(t, w, domain.TriggerBinding{WorkspaceKey: "WS", BindingID: req.BindingID, RouteKey: req.RouteKey, DriverID: req.DriverID, DriverVersionID: req.DriverVersionID, Enabled: true})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/trigger-bindings":
			if r.URL.Query().Get("route_key") != "" {
				if r.URL.Query().Get("route_key") != "epics.runs.create" || r.URL.Query().Get("limit") != "1" {
					t.Fatalf("trigger binding list query = %s", r.URL.RawQuery)
				}
			} else if r.URL.Query().Get("target_agent_service_id") != "lead" || r.URL.Query().Get("limit") != "1" {
				t.Fatalf("trigger binding target list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"trigger_bindings": []domain.TriggerBinding{{WorkspaceKey: "WS", BindingID: "binding-1", RouteKey: "epics.runs.create", DriverID: "driver-1", DriverVersionID: "version-1", TargetAgentServiceID: "lead", Enabled: true}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/trigger-bindings/binding-1":
			writeJSON(t, w, domain.TriggerBinding{WorkspaceKey: "WS", BindingID: "binding-1", RouteKey: "epics.runs.create", DriverID: "driver-1", DriverVersionID: "version-1", Enabled: true})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/trigger-bindings/binding-1":
			var req struct {
				Name    *string `json:"name"`
				Enabled *bool   `json:"enabled"`
			}
			decodeJSONBody(t, r, &req)
			if req.Name == nil || *req.Name != "Epic runner route" || req.Enabled == nil || *req.Enabled {
				t.Fatalf("trigger binding update body = %+v", req)
			}
			writeJSON(t, w, domain.TriggerBinding{WorkspaceKey: "WS", BindingID: "binding-1", Name: *req.Name, RouteKey: "epics.runs.create", DriverID: "driver-1", DriverVersionID: "version-1", Enabled: *req.Enabled})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/epics/WS-1/runs":
			var req struct {
				RunID          string          `json:"run_id"`
				IdempotencyKey string          `json:"idempotency_key"`
				Payload        json.RawMessage `json:"payload"`
			}
			decodeJSONBody(t, r, &req)
			if req.RunID != "run-epic-1" || req.IdempotencyKey != "idem-epic-1" || string(req.Payload) != `{"epicId":"wrong"}` || r.Header.Get("Idempotency-Key") != "idem-epic-1" {
				t.Fatalf("epic run body/header = %+v header=%q", req, r.Header.Get("Idempotency-Key"))
			}
			writeJSON(t, w, domain.DriverRun{WorkspaceKey: "WS", RunID: req.RunID, DriverID: "driver-1", DriverVersionID: "version-1", EpicID: "WS-1", Status: domain.DriverRunQueued})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/driver-runs":
			var req struct {
				RunID           string          `json:"run_id"`
				DriverID        string          `json:"driver_id"`
				DriverVersionID string          `json:"driver_version_id"`
				EpicID          string          `json:"epic_id"`
				Payload         json.RawMessage `json:"payload"`
			}
			decodeJSONBody(t, r, &req)
			if req.RunID != "run-1" || req.DriverID != "driver-1" || req.DriverVersionID != "version-1" || string(req.Payload) != `{"epicId":"WS-1"}` {
				t.Fatalf("driver run create body = %+v", req)
			}
			writeJSON(t, w, domain.DriverRun{WorkspaceKey: "WS", RunID: req.RunID, DriverID: req.DriverID, DriverVersionID: req.DriverVersionID, EpicID: req.EpicID, Status: domain.DriverRunQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/driver-runs":
			if r.URL.Query().Get("status") != "queued" || r.URL.Query().Get("epic_id") != "WS-1" {
				t.Fatalf("driver run list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"driver_runs": []domain.DriverRun{{WorkspaceKey: "WS", RunID: "run-1", Status: domain.DriverRunQueued}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/driver-runs/run-1/claim":
			var req struct {
				NodeID  string `json:"node_id"`
				LeaseID string `json:"lease_id"`
			}
			decodeJSONBody(t, r, &req)
			if req.NodeID != "node-1" || req.LeaseID != "lease-1" {
				t.Fatalf("claim body = %+v", req)
			}
			claimCount++
			if claimCount > 1 {
				w.WriteHeader(http.StatusConflict)
				writeJSON(t, w, map[string]any{"error": map[string]string{"code": "already_claimed", "message": "claim driver run failed"}})
				return
			}
			writeJSON(t, w, domain.DriverRun{WorkspaceKey: "WS", RunID: "run-1", Status: domain.DriverRunRunning, NodeID: req.NodeID, LeaseID: req.LeaseID, FencingToken: 1})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/driver-runs/run-1/finish":
			var req struct {
				NodeID       string                 `json:"node_id"`
				LeaseID      string                 `json:"lease_id"`
				FencingToken int64                  `json:"fencing_token"`
				Status       domain.DriverRunStatus `json:"status"`
				Output       map[string]string      `json:"output"`
			}
			decodeJSONBody(t, r, &req)
			if req.NodeID != "node-1" || req.LeaseID != "lease-1" || req.FencingToken != 1 || req.Status != domain.DriverRunCompleted || req.Output["logs_ref"] != "driver-run://run-1/flue-local" {
				t.Fatalf("finish body = %+v", req)
			}
			writeJSON(t, w, domain.DriverRun{WorkspaceKey: "WS", RunID: "run-1", Status: domain.DriverRunCompleted, NodeID: req.NodeID})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/driver-runs/recover-stale":
			var req struct {
				StaleBefore   *time.Time `json:"stale_before"`
				MaxAgeSeconds int64      `json:"max_age_seconds"`
				ErrorClass    string     `json:"error_class"`
				Summary       string     `json:"summary"`
				Limit         int        `json:"limit"`
			}
			decodeJSONBody(t, r, &req)
			if req.StaleBefore == nil || !req.StaleBefore.Equal(recoveryBefore) || req.MaxAgeSeconds != 60 || req.ErrorClass != "stale_driver_run" || req.Summary != "operator stale run recovery" || req.Limit != 2 {
				t.Fatalf("recover stale driver runs body = %+v, want cutoff/max-age/error/summary fields", req)
			}
			writeJSON(t, w, store.StaleDriverRunRecoveryResult{WorkspaceKey: "WS", StaleBefore: recoveryBefore, Recovered: 1, SkippedFresh: 1, RecoveredRunIDs: []string{"run-stale"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/driver-runs/run-1/recover-stale-tasks":
			var req struct {
				StaleBefore   *time.Time `json:"stale_before"`
				MaxAgeSeconds int64      `json:"max_age_seconds"`
				ErrorClass    string     `json:"error_class"`
				ErrorMessage  string     `json:"error_message"`
			}
			decodeJSONBody(t, r, &req)
			if req.StaleBefore == nil || !req.StaleBefore.Equal(recoveryBefore) || req.MaxAgeSeconds != 60 || req.ErrorClass != "stale_task_run" || req.ErrorMessage != "operator recovery" {
				t.Fatalf("recover stale task runs body = %+v, want cutoff/max-age/error fields", req)
			}
			writeJSON(t, w, store.StaleTaskRunRecoveryResult{WorkspaceKey: "WS", DriverRunID: "run-1", StaleBefore: recoveryBefore, Recovered: 2, Released: 1, SkippedFresh: 1, RecoveredTaskRunIDs: []string{"task-run-1", "task-run-2"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/driver-runs/run-1/steps":
			var req struct {
				StepID       string                  `json:"step_id"`
				DriverRunID  string                  `json:"driver_run_id"`
				StepKind     string                  `json:"step_kind"`
				Status       domain.DriverStepStatus `json:"status"`
				InputRef     string                  `json:"input_ref"`
				NodeID       string                  `json:"node_id"`
				LeaseID      string                  `json:"lease_id"`
				FencingToken int64                   `json:"fencing_token"`
			}
			decodeJSONBody(t, r, &req)
			if req.StepID != "step-1" || req.DriverRunID != "" || req.StepKind != "custom_vendor_gate" || req.Status != domain.DriverStepWaiting || req.InputRef != "artifact://input-1" || req.NodeID != "node-1" || req.LeaseID != "lease-1" || req.FencingToken != 1 {
				t.Fatalf("driver run step create body = %+v", req)
			}
			writeJSON(t, w, domain.DriverStep{WorkspaceKey: "WS", StepID: req.StepID, DriverRunID: "run-1", StepKind: req.StepKind, Status: req.Status, InputRef: req.InputRef})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/driver-runs/run-1/steps":
			if r.URL.Query().Get("status") != "waiting" || r.URL.Query().Get("step_kind") != "custom_vendor_gate" {
				t.Fatalf("driver run step list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"driver_steps": []domain.DriverStep{{WorkspaceKey: "WS", StepID: "step-1", DriverRunID: "run-1", StepKind: "custom_vendor_gate", Status: domain.DriverStepWaiting}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/driver-steps":
			var req struct {
				StepID       string `json:"step_id"`
				DriverRunID  string `json:"driver_run_id"`
				StepKind     string `json:"step_kind"`
				NodeID       string `json:"node_id"`
				LeaseID      string `json:"lease_id"`
				FencingToken int64  `json:"fencing_token"`
			}
			decodeJSONBody(t, r, &req)
			if req.StepID != "step-top" || req.DriverRunID != "run-1" || req.StepKind != "run_agent" || req.NodeID != "node-1" || req.LeaseID != "lease-1" || req.FencingToken != 1 {
				t.Fatalf("driver step create body = %+v", req)
			}
			writeJSON(t, w, domain.DriverStep{WorkspaceKey: "WS", StepID: req.StepID, DriverRunID: req.DriverRunID, StepKind: req.StepKind, Status: domain.DriverStepQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/driver-steps":
			if r.URL.Query().Get("driver_run_id") != "run-1" || r.URL.Query().Get("task_run_id") != "task-run-1" || r.URL.Query().Get("status") != "completed" || r.URL.Query().Get("limit") != "1" {
				t.Fatalf("driver step list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"driver_steps": []domain.DriverStep{{WorkspaceKey: "WS", StepID: "step-1", DriverRunID: "run-1", TaskRunID: "task-run-1", Status: domain.DriverStepCompleted}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/driver-steps/step-1":
			writeJSON(t, w, domain.DriverStep{WorkspaceKey: "WS", StepID: "step-1", DriverRunID: "run-1", StepKind: "custom_vendor_gate", Status: domain.DriverStepWaiting, InputRef: "artifact://input-1"})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/WS/driver-steps/step-1":
			var req store.DriverStepUpdate
			decodeJSONBody(t, r, &req)
			if req.Status == nil || *req.Status != domain.DriverStepCompleted || req.TaskRunID == nil || *req.TaskRunID != "task-run-1" || req.OutputRef == nil || *req.OutputRef != "artifact://output-1" || req.NodeID != "node-1" || req.LeaseID != "lease-1" || req.FencingToken != 1 {
				t.Fatalf("driver step update body = %+v", req)
			}
			writeJSON(t, w, domain.DriverStep{WorkspaceKey: "WS", StepID: "step-1", DriverRunID: "run-1", StepKind: "custom_vendor_gate", Status: domain.DriverStepCompleted, TaskRunID: *req.TaskRunID, OutputRef: *req.OutputRef})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/task-runs":
			var req struct {
				TaskRunID        string                  `json:"task_run_id"`
				DriverRunID      string                  `json:"driver_run_id"`
				DriverStepID     string                  `json:"driver_step_id"`
				TaskID           string                  `json:"task_id"`
				WorkerProfileID  string                  `json:"worker_profile_id"`
				NodeID           string                  `json:"node_id"`
				LeaseID          string                  `json:"lease_id"`
				FencingToken     int64                   `json:"fencing_token"`
				SandboxPlacement domain.TaskRunPlacement `json:"sandbox_placement"`
			}
			decodeJSONBody(t, r, &req)
			if req.TaskRunID != "task-run-1" || req.DriverRunID != "run-1" || req.DriverStepID != "step-1" || req.TaskID != "WS-1" || req.WorkerProfileID != "falcon" || req.NodeID != "node-1" || req.LeaseID != "task-lease-1" {
				t.Fatalf("task run create body = %+v", req)
			}
			if req.SandboxPlacement.Provider != "daytona" || req.SandboxPlacement.CWD != "/workspace" {
				t.Fatalf("task run create placement = %+v, want daytona /workspace", req.SandboxPlacement)
			}
			writeJSON(t, w, domain.TaskRun{WorkspaceKey: "WS", TaskRunID: req.TaskRunID, DriverRunID: req.DriverRunID, DriverStepID: req.DriverStepID, TaskID: req.TaskID, WorkerProfileID: req.WorkerProfileID, Status: domain.TaskRunRunning, NodeID: req.NodeID, LeaseID: req.LeaseID, FencingToken: 42, SandboxPlacement: req.SandboxPlacement})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/task-runs/claim":
			var req struct {
				TaskRunID          string                  `json:"task_run_id"`
				NodeID             string                  `json:"node_id"`
				RunnerID           string                  `json:"runner_id"`
				LeaseID            string                  `json:"lease_id"`
				SupportedProviders []string                `json:"supported_providers"`
				Capabilities       []string                `json:"capabilities"`
				WorkerProfileIDs   []string                `json:"worker_profile_ids"`
				SandboxPlacement   domain.TaskRunPlacement `json:"sandbox_placement"`
			}
			decodeJSONBody(t, r, &req)
			if req.TaskRunID != "task-run-1" || req.NodeID != "node-1" || req.RunnerID != "runner-1" || req.LeaseID != "task-lease-1" {
				t.Fatalf("task run claim body = %+v", req)
			}
			if r.Header.Get("X-Lease-Token") != "claim-token" {
				t.Fatalf("task run claim lease token header = %q, want claim-token", r.Header.Get("X-Lease-Token"))
			}
			if len(req.SupportedProviders) != 1 || req.SupportedProviders[0] != "daytona" || len(req.WorkerProfileIDs) != 1 || req.WorkerProfileIDs[0] != "falcon" || req.SandboxPlacement.SandboxID != "sandbox-1" {
				t.Fatalf("task run claim selectors/placement = %+v", req)
			}
			writeJSON(t, w, domain.TaskRun{WorkspaceKey: "WS", TaskRunID: req.TaskRunID, WorkerProfileID: "falcon", Status: domain.TaskRunRunning, NodeID: req.NodeID, LeaseID: req.LeaseID, FencingToken: 42, RunnerPlacement: domain.TaskRunPlacement{Provider: "daemon", NodeID: req.NodeID, RunnerID: req.RunnerID}, SandboxPlacement: req.SandboxPlacement})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/task-runs":
			if r.URL.Query().Get("driver_run_id") != "run-1" || r.URL.Query().Get("driver_step_id") != "step-1" || r.URL.Query().Get("worker_profile_id") != "falcon" || r.URL.Query().Get("status") != "running" {
				t.Fatalf("task run list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"task_runs": []domain.TaskRun{{WorkspaceKey: "WS", TaskRunID: "task-run-1", DriverRunID: "run-1", DriverStepID: "step-1", Status: domain.TaskRunRunning}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/task-runs/task-run-1/finish":
			var req struct {
				NodeID           string               `json:"node_id"`
				LeaseID          string               `json:"lease_id"`
				FencingToken     int64                `json:"fencing_token"`
				Status           domain.TaskRunStatus `json:"status"`
				ExitCode         *int                 `json:"exit_code"`
				LogsRef          string               `json:"logs_ref"`
				InputTokens      int64                `json:"input_tokens"`
				OutputTokens     int64                `json:"output_tokens"`
				CacheReadTokens  int64                `json:"cache_read_tokens"`
				CacheWriteTokens int64                `json:"cache_write_tokens"`
				EstimatedCostUSD float64              `json:"estimated_cost_usd"`
			}
			decodeJSONBody(t, r, &req)
			if req.NodeID != "node-1" || req.LeaseID != "task-lease-1" || req.FencingToken != 42 || req.Status != domain.TaskRunCompleted || req.ExitCode == nil || *req.ExitCode != 0 || req.LogsRef != "logs://task-run-1" {
				t.Fatalf("task finish body = %+v", req)
			}
			if req.InputTokens != 11 || req.OutputTokens != 7 || req.CacheReadTokens != 5 || req.CacheWriteTokens != 3 || req.EstimatedCostUSD != 0.125 {
				t.Fatalf("task finish usage = %+v, want request usage", req)
			}
			if r.Header.Get("X-Lease-Token") != "task-run-token" {
				t.Fatalf("task finish lease token header = %q, want task-run-token", r.Header.Get("X-Lease-Token"))
			}
			writeJSON(t, w, domain.TaskRun{WorkspaceKey: "WS", TaskRunID: "task-run-1", Status: domain.TaskRunCompleted, ExitCode: req.ExitCode, InputTokens: req.InputTokens, OutputTokens: req.OutputTokens, CacheReadTokens: req.CacheReadTokens, CacheWriteTokens: req.CacheWriteTokens, EstimatedCostUSD: req.EstimatedCostUSD})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/task-runs/task-run-1/heartbeat":
			var req struct {
				NodeID          string            `json:"node_id"`
				LeaseID         string            `json:"lease_id"`
				FencingToken    int64             `json:"fencing_token"`
				RuntimeMetadata map[string]string `json:"runtime_metadata"`
				LogsRef         string            `json:"logs_ref"`
			}
			decodeJSONBody(t, r, &req)
			if req.NodeID != "node-1" || req.LeaseID != "task-lease-1" || req.FencingToken != 42 || req.LogsRef != "logs://task-run-1" || req.RuntimeMetadata["phase"] != "running" {
				t.Fatalf("task heartbeat body = %+v", req)
			}
			if r.Header.Get("X-Lease-Token") != "task-run-token" {
				t.Fatalf("task heartbeat lease token header = %q, want task-run-token", r.Header.Get("X-Lease-Token"))
			}
			writeJSON(t, w, domain.TaskRun{WorkspaceKey: "WS", TaskRunID: "task-run-1", Status: domain.TaskRunRunning, NodeID: req.NodeID, LeaseID: req.LeaseID, FencingToken: req.FencingToken, LogsRef: req.LogsRef})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/task-runs/task-run-1/logs":
			var req struct {
				NodeID       string `json:"node_id"`
				LeaseID      string `json:"lease_id"`
				FencingToken int64  `json:"fencing_token"`
				Stream       string `json:"stream"`
				Text         string `json:"text"`
			}
			decodeJSONBody(t, r, &req)
			if req.NodeID != "node-1" || req.LeaseID != "task-lease-1" || req.FencingToken != 42 || req.Stream != "stdout" || req.Text != "starting\n" {
				t.Fatalf("task log append body = %+v", req)
			}
			if r.Header.Get("X-Lease-Token") != "task-run-token" {
				t.Fatalf("task log append lease token header = %q, want task-run-token", r.Header.Get("X-Lease-Token"))
			}
			writeJSON(t, w, domain.TaskRunLogEntry{WorkspaceKey: "WS", TaskRunID: "task-run-1", Sequence: 1, Stream: req.Stream, Text: req.Text, NodeID: req.NodeID, LeaseID: req.LeaseID, FencingToken: req.FencingToken})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/task-runs/task-run-1/logs":
			if r.URL.Query().Get("after_sequence") != "1" || r.URL.Query().Get("limit") != "10" {
				t.Fatalf("task log list query = %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"logs": []domain.TaskRunLogEntry{{WorkspaceKey: "WS", TaskRunID: "task-run-1", Sequence: 2, Stream: "stderr", Text: "warning\n"}}, "count": 1, "next_sequence": 2})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/task-runs/task-run-1/complete":
			var req struct {
				CompletionID        string               `json:"completion_id"`
				NodeID              string               `json:"node_id"`
				LeaseID             string               `json:"lease_id"`
				FencingToken        int64                `json:"fencing_token"`
				Status              domain.TaskRunStatus `json:"status"`
				RequiredArtifactIDs []string             `json:"required_artifact_ids"`
				RequireArtifacts    bool                 `json:"require_artifacts"`
				InputTokens         int64                `json:"input_tokens"`
				OutputTokens        int64                `json:"output_tokens"`
				CacheReadTokens     int64                `json:"cache_read_tokens"`
				CacheWriteTokens    int64                `json:"cache_write_tokens"`
				EstimatedCostUSD    float64              `json:"estimated_cost_usd"`
				CloseTask           bool                 `json:"close_task"`
				CloseReason         string               `json:"close_reason"`
			}
			decodeJSONBody(t, r, &req)
			if req.CompletionID != "completion-1" || req.NodeID != "node-1" || req.LeaseID != "task-lease-1" || req.FencingToken != 42 || req.Status != domain.TaskRunCompleted || !req.RequireArtifacts || !req.CloseTask || len(req.RequiredArtifactIDs) != 1 || req.RequiredArtifactIDs[0] != "artifact-1" {
				t.Fatalf("task complete body = %+v", req)
			}
			if req.InputTokens != 23 || req.OutputTokens != 19 || req.CacheReadTokens != 13 || req.CacheWriteTokens != 2 || req.EstimatedCostUSD != 0.25 {
				t.Fatalf("task complete usage = %+v, want request usage", req)
			}
			if r.Header.Get("X-Lease-Token") != "task-run-token" {
				t.Fatalf("task complete lease token header = %q, want task-run-token", r.Header.Get("X-Lease-Token"))
			}
			writeJSON(t, w, map[string]any{
				"task_run": domain.TaskRun{WorkspaceKey: "WS", TaskRunID: "task-run-1", TaskID: "WS-1", Status: domain.TaskRunCompleted, NodeID: req.NodeID, LeaseID: req.LeaseID, FencingToken: req.FencingToken, InputTokens: req.InputTokens, OutputTokens: req.OutputTokens, CacheReadTokens: req.CacheReadTokens, CacheWriteTokens: req.CacheWriteTokens, EstimatedCostUSD: req.EstimatedCostUSD},
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

	if _, err := client.Drivers().Create(t.Context(), store.DriverCreate{WorkspaceKey: "WS", DriverID: "driver-1", Name: "epic-runner", Status: domain.DriverStatusActive}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := client.DriverVersions().Create(t.Context(), store.DriverVersionCreate{WorkspaceKey: "WS", DriverID: "driver-1", VersionID: "version-1", Version: 1, SourceDigest: "sha256:source", BundleDigest: "sha256:bundle"}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if versions, err := client.DriverVersions().List(t.Context(), "WS", store.DriverVersionFilter{ValidationStatus: domain.DriverVersionValidationPassed, Limit: 1}); err != nil || len(versions) != 1 {
		t.Fatalf("List driver versions = %+v err=%v, want one", versions, err)
	}
	if binding, err := client.TriggerBindings().Create(t.Context(), store.TriggerBindingCreate{
		WorkspaceKey:      "WS",
		BindingID:         "binding-1",
		Name:              "Epic runner",
		SourceKind:        "http",
		RouteKey:          "epics.runs.create",
		Method:            "POST",
		PathTemplate:      "/epics/{epic_id}/runs",
		DriverID:          "driver-1",
		DriverVersionID:   "version-1",
		TargetEntrypoint:  "run",
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyOneActivePerEpic,
		IdempotencyPolicy: "header:Idempotency-Key",
		AuthPolicy:        "workspace_user",
		Permissions:       []string{"driver_run.create"},
		Enabled:           true,
	}); err != nil || binding.DriverVersionID != "version-1" {
		t.Fatalf("Create trigger binding = %+v err=%v, want pinned version-1", binding, err)
	}
	if binding, err := client.TriggerBindings().GetByRouteKey(t.Context(), "WS", "epics.runs.create"); err != nil || binding.BindingID != "binding-1" {
		t.Fatalf("GetByRouteKey trigger binding = %+v err=%v, want binding-1", binding, err)
	}
	if bindings, err := client.TriggerBindings().List(t.Context(), "WS", store.TriggerBindingFilter{TargetAgentServiceID: "lead", Limit: 1}); err != nil || len(bindings) != 1 || bindings[0].TargetAgentServiceID != "lead" {
		t.Fatalf("List target trigger bindings = %+v err=%v, want lead binding", bindings, err)
	}
	if binding, err := client.TriggerBindings().Get(t.Context(), "WS", "binding-1"); err != nil || binding.RouteKey != "epics.runs.create" {
		t.Fatalf("Get trigger binding = %+v err=%v, want epics route", binding, err)
	}
	name := "Epic runner route"
	enabled := false
	if binding, err := client.TriggerBindings().Update(t.Context(), "WS", "binding-1", store.TriggerBindingUpdate{Name: &name, Enabled: &enabled}); err != nil || binding.Name != name || binding.Enabled {
		t.Fatalf("Update trigger binding = %+v err=%v, want disabled renamed binding", binding, err)
	}
	if run, err := client.DriverRuns().CreateEpic(t.Context(), "WS", "WS-1", store.EpicRunCreate{
		RunID:          "run-epic-1",
		IdempotencyKey: "idem-epic-1",
		Payload:        json.RawMessage(`{"epicId":"wrong"}`),
	}); err != nil || run.DriverVersionID != "version-1" || run.EpicID != "WS-1" {
		t.Fatalf("CreateEpic driver run = %+v err=%v, want pinned version-1 WS-1", run, err)
	}
	if _, err := client.DriverRuns().Create(t.Context(), store.DriverRunCreate{WorkspaceKey: "WS", RunID: "run-1", DriverID: "driver-1", DriverVersionID: "version-1", EpicID: "WS-1", Payload: json.RawMessage(`{"epicId":"WS-1"}`)}); err != nil {
		t.Fatalf("Create driver run: %v", err)
	}
	if runs, err := client.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{EpicID: "WS-1", Status: domain.DriverRunQueued}); err != nil || len(runs) != 1 {
		t.Fatalf("List driver runs = %+v err=%v, want one", runs, err)
	}
	if _, err := client.DriverRuns().Claim(t.Context(), "WS", "run-1", "node-1", "lease-1"); err != nil {
		t.Fatalf("Claim driver run: %v", err)
	}
	if _, err := client.DriverRuns().Claim(t.Context(), "WS", "run-1", "node-1", "lease-1"); !errors.Is(err, domain.ErrAlreadyClaimed) {
		t.Fatalf("second Claim err = %v, want ErrAlreadyClaimed", err)
	}
	if step, err := client.DriverSteps().CreateForRun(t.Context(), "WS", "run-1", store.DriverStepCreate{StepID: "step-1", StepKind: "custom_vendor_gate", Status: domain.DriverStepWaiting, InputRef: "artifact://input-1", NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1}); err != nil || step.StepID != "step-1" || step.DriverRunID != "run-1" {
		t.Fatalf("CreateForRun driver step = %+v err=%v, want step-1 under run-1", step, err)
	}
	if steps, err := client.DriverSteps().ListForRun(t.Context(), "WS", "run-1", store.DriverStepFilter{StepKind: "custom_vendor_gate", Status: domain.DriverStepWaiting}); err != nil || len(steps) != 1 {
		t.Fatalf("ListForRun driver steps = %+v err=%v, want one", steps, err)
	}
	if step, err := client.DriverSteps().Create(t.Context(), store.DriverStepCreate{WorkspaceKey: "WS", StepID: "step-top", DriverRunID: "run-1", StepKind: "run_agent", NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1}); err != nil || step.StepID != "step-top" {
		t.Fatalf("Create driver step = %+v err=%v, want step-top", step, err)
	}
	if step, err := client.DriverSteps().Get(t.Context(), "WS", "step-1"); err != nil || step.InputRef != "artifact://input-1" {
		t.Fatalf("Get driver step = %+v err=%v, want input ref", step, err)
	}
	completedStep := domain.DriverStepCompleted
	stepTaskRunID := "task-run-1"
	stepOutputRef := "artifact://output-1"
	if step, err := client.DriverSteps().Update(t.Context(), "WS", "step-1", store.DriverStepUpdate{Status: &completedStep, TaskRunID: &stepTaskRunID, OutputRef: &stepOutputRef, NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1}); err != nil || step.Status != domain.DriverStepCompleted || step.TaskRunID != "task-run-1" {
		t.Fatalf("Update driver step = %+v err=%v, want completed task step", step, err)
	}
	if steps, err := client.DriverSteps().List(t.Context(), "WS", store.DriverStepFilter{DriverRunID: "run-1", TaskRunID: "task-run-1", Status: domain.DriverStepCompleted, Limit: 1}); err != nil || len(steps) != 1 {
		t.Fatalf("List driver steps = %+v err=%v, want one", steps, err)
	}
	if _, err := client.DriverRuns().Finish(t.Context(), "WS", "run-1", store.DriverRunFinish{NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1, Status: domain.DriverRunCompleted, Output: map[string]string{"logs_ref": "driver-run://run-1/flue-local"}}); err != nil {
		t.Fatalf("Finish driver run: %v", err)
	}
	if recovered, err := client.DriverRuns().RecoverStale(t.Context(), "WS", store.StaleDriverRunRecovery{StaleBefore: recoveryBefore, MaxAgeSeconds: 60, ErrorClass: "stale_driver_run", Summary: "operator stale run recovery", Limit: 2}); err != nil || recovered.Recovered != 1 || recovered.SkippedFresh != 1 {
		t.Fatalf("RecoverStale = %+v err=%v, want recovery counts", recovered, err)
	}
	if recovered, err := client.DriverRuns().RecoverStaleTaskRuns(t.Context(), "WS", "run-1", store.StaleTaskRunRecovery{StaleBefore: recoveryBefore, MaxAgeSeconds: 60, ErrorClass: "stale_task_run", ErrorMessage: "operator recovery"}); err != nil || recovered.Recovered != 2 || recovered.Released != 1 || recovered.SkippedFresh != 1 {
		t.Fatalf("RecoverStaleTaskRuns = %+v err=%v, want recovery counts", recovered, err)
	}

	taskRun, err := client.TaskRuns().Create(t.Context(), store.TaskRunCreate{WorkspaceKey: "WS", TaskRunID: "task-run-1", DriverRunID: "run-1", DriverStepID: "step-1", TaskID: "WS-1", WorkerProfileID: "falcon", Status: domain.TaskRunRunning, NodeID: "node-1", LeaseID: "task-lease-1", SandboxPlacement: domain.TaskRunPlacement{Provider: "daytona", CWD: "/workspace"}})
	if err != nil {
		t.Fatalf("Create task run: %v", err)
	}
	taskRun, err = client.TaskRuns().ClaimQueued(t.Context(), "WS", store.TaskRunClaim{TaskRunID: "task-run-1", NodeID: "node-1", RunnerID: "runner-1", LeaseID: "task-lease-1", LeaseToken: "claim-token", SupportedProviders: []string{"daytona"}, Capabilities: []string{"git"}, WorkerProfileIDs: []string{"falcon"}, SandboxPlacement: domain.TaskRunPlacement{Provider: "daytona", SandboxID: "sandbox-1", CWD: "/workspace"}})
	if err != nil {
		t.Fatalf("ClaimQueued task run: %v", err)
	}
	if taskRun.RunnerPlacement.RunnerID != "runner-1" || taskRun.SandboxPlacement.SandboxID != "sandbox-1" {
		t.Fatalf("ClaimQueued placement = %+v/%+v, want runner-1 sandbox-1", taskRun.RunnerPlacement, taskRun.SandboxPlacement)
	}
	if taskRuns, err := client.TaskRuns().List(t.Context(), "WS", store.TaskRunFilter{DriverRunID: "run-1", DriverStepID: "step-1", WorkerProfileID: "falcon", Status: domain.TaskRunRunning}); err != nil || len(taskRuns) != 1 {
		t.Fatalf("List task runs = %+v err=%v, want one", taskRuns, err)
	}
	exitCode := 0
	if heartbeat, err := client.TaskRuns().Heartbeat(t.Context(), "WS", "task-run-1", store.TaskRunHeartbeat{NodeID: taskRun.NodeID, LeaseID: taskRun.LeaseID, LeaseToken: "task-run-token", FencingToken: taskRun.FencingToken, LogsRef: "logs://task-run-1", RuntimeMetadata: map[string]string{"phase": "running"}}); err != nil || heartbeat.Status != domain.TaskRunRunning {
		t.Fatalf("Heartbeat task run = %+v err=%v, want running", heartbeat, err)
	}
	if entry, err := client.TaskRuns().AppendLog(t.Context(), "WS", "task-run-1", store.TaskRunLogAppend{NodeID: taskRun.NodeID, LeaseID: taskRun.LeaseID, LeaseToken: "task-run-token", FencingToken: taskRun.FencingToken, Stream: "stdout", Text: "starting\n"}); err != nil || entry.Sequence != 1 {
		t.Fatalf("AppendLog = %+v err=%v, want sequence 1", entry, err)
	}
	if logs, err := client.TaskRuns().ListLogs(t.Context(), "WS", "task-run-1", store.TaskRunLogFilter{AfterSequence: 1, Limit: 10}); err != nil || len(logs) != 1 || logs[0].Sequence != 2 {
		t.Fatalf("ListLogs = %+v err=%v, want one sequence 2", logs, err)
	}
	if finished, err := client.TaskRuns().Finish(t.Context(), "WS", "task-run-1", store.TaskRunFinish{NodeID: taskRun.NodeID, LeaseID: taskRun.LeaseID, LeaseToken: "task-run-token", FencingToken: taskRun.FencingToken, Status: domain.TaskRunCompleted, ExitCode: &exitCode, LogsRef: "logs://task-run-1", InputTokens: 11, OutputTokens: 7, CacheReadTokens: 5, CacheWriteTokens: 3, EstimatedCostUSD: 0.125}); err != nil {
		t.Fatalf("Finish task run: %v", err)
	} else if finished.InputTokens != 11 || finished.OutputTokens != 7 || finished.CacheReadTokens != 5 || finished.CacheWriteTokens != 3 || finished.EstimatedCostUSD != 0.125 {
		t.Fatalf("Finish task run usage = %+v, want response usage", finished)
	}
	if completed, err := client.TaskRuns().Complete(t.Context(), "WS", "task-run-1", store.TaskRunComplete{CompletionID: "completion-1", NodeID: taskRun.NodeID, LeaseID: taskRun.LeaseID, LeaseToken: "task-run-token", FencingToken: taskRun.FencingToken, Status: domain.TaskRunCompleted, RequiredArtifactIDs: []string{"artifact-1"}, RequireArtifacts: true, InputTokens: 23, OutputTokens: 19, CacheReadTokens: 13, CacheWriteTokens: 2, EstimatedCostUSD: 0.25, CloseTask: true, CloseReason: "done"}); err != nil || completed.Status != domain.TaskRunCompleted {
		t.Fatalf("Complete task run = %+v err=%v, want completed", completed, err)
	} else if completed.InputTokens != 23 || completed.OutputTokens != 19 || completed.CacheReadTokens != 13 || completed.CacheWriteTokens != 2 || completed.EstimatedCostUSD != 0.25 {
		t.Fatalf("Complete task run usage = %+v, want response usage", completed)
	}
}

func TestPlatformClientFinishesDriverRunWithNeedsReviewStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/driver-runs/run-1/finish" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var req struct {
			NodeID       string                 `json:"node_id"`
			LeaseID      string                 `json:"lease_id"`
			FencingToken int64                  `json:"fencing_token"`
			Status       domain.DriverRunStatus `json:"status"`
		}
		decodeJSONBody(t, r, &req)
		if req.NodeID != "node-1" || req.LeaseID != "lease-1" || req.FencingToken != 1 || req.Status != domain.DriverRunNeedsReview {
			t.Fatalf("finish needs_review body = %+v", req)
		}
		writeJSON(t, w, domain.DriverRun{WorkspaceKey: "WS", RunID: "run-1", Status: domain.DriverRunNeedsReview, NodeID: req.NodeID, LeaseID: req.LeaseID, FencingToken: req.FencingToken})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := client.DriverRuns().Finish(t.Context(), "WS", "run-1", store.DriverRunFinish{
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		FencingToken: 1,
		Status:       domain.DriverRunNeedsReview,
		Summary:      "blocked",
		ErrorClass:   "epic_blocked",
	})
	if err != nil {
		t.Fatalf("Finish needs_review driver run: %v", err)
	}
	if finished.Status != domain.DriverRunNeedsReview {
		t.Fatalf("finished status = %q, want needs_review", finished.Status)
	}
}

func TestPlatformClientDecodesTaskRunRepositorySet(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/WS/task-runs/task-run-1" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		writeJSON(t, w, map[string]any{
			"workspace_key":  "WS",
			"task_run_id":    "task-run-1",
			"task_id":        "WS-1",
			"status":         "running",
			"repository_set": []string{"loom", "fleet-db"},
		})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := client.TaskRuns().Get(t.Context(), "WS", "task-run-1")
	if err != nil {
		t.Fatalf("Get task run: %v", err)
	}

	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("marshal task run: %v", err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("decode task run wire form: %v", err)
	}
	if got := string(wire["repository_set"]); got != `["loom","fleet-db"]` {
		t.Fatalf("repository_set = %s, want %s", got, `["loom","fleet-db"]`)
	}
}

func decodeJSONBody(t *testing.T, r *http.Request, out any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
}
