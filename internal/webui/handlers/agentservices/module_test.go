package agentservices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/taskrunlogs"
	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
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

func TestGetAgentServiceJournalReturnsScoutJournal(t *testing.T) {
	st, _ := seededAgentServiceStore(t)
	runtimeDir := t.TempDir()
	journalPath := filepath.Join(runtimeDir, journalFilename)
	content := "# Scout journal\n\nReviewed the backlog.\n"
	if err := os.WriteFile(journalPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write journal: %v", err)
	}
	modifiedAt := time.Date(2026, time.August, 14, 23, 39, 22, 0, time.UTC)
	if err := os.Chtimes(journalPath, modifiedAt, modifiedAt); err != nil {
		t.Fatalf("set journal times: %v", err)
	}

	rec := getAgentServiceJournal(t, NewModule(st, runtimeDir), "scout")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response agentServiceJournalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || response.Data.ServiceID != "scout" || response.Data.Filename != journalFilename {
		t.Fatalf("response = %#v", response)
	}
	if response.Data.Content != content || !response.Data.ModifiedAt.Equal(modifiedAt) || response.Data.Truncated {
		t.Fatalf("journal = %#v, want content round trip and modifiedAt %v", response.Data, modifiedAt)
	}
}

func TestListAgentServiceRunTasksFiltersByDriverRunAndReportsLogAvailability(t *testing.T) {
	st, svc := seededAgentServiceStore(t)
	run := createAgentServiceRun(t, st, svc, "run-1")
	otherRun := createAgentServiceRun(t, st, svc, "run-2")
	for _, task := range []store.TaskRunCreate{
		{
			WorkspaceKey: "WS", TaskRunID: "task-1", DriverRunID: run.RunID, TaskID: "WS-1",
			Runner: "scout-task-runner", Status: domain.TaskRunRunning,
			RuntimeMetadata: map[string]string{"transcript_ref": "artifact://transcript-task-1"},
		},
		{
			WorkspaceKey: "WS", TaskRunID: "task-other", DriverRunID: otherRun.RunID, TaskID: "WS-2",
			Runner: "scout-task-runner", Status: domain.TaskRunRunning,
		},
		{
			WorkspaceKey: "WS", TaskRunID: "task-no-log", DriverRunID: run.RunID, TaskID: "WS-3",
			Runner: "scout-task-runner", Status: domain.TaskRunRunning,
		},
	} {
		if _, err := st.TaskRuns().Create(t.Context(), task); err != nil {
			t.Fatalf("create task run %s: %v", task.TaskRunID, err)
		}
	}
	ref, err := taskrunlogs.PutTask(t.Context(), st, "WS", "task-1", "AI output\n")
	if err != nil {
		t.Fatalf("PutTask: %v", err)
	}
	setTaskRunLogRef(t, st, "task-1", ref)

	mux := http.NewServeMux()
	NewModule(st).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agent-services/scout/runs/run-1/tasks", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var raw struct {
		Success bool
		Data    []map[string]interface{}
		Total   int
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !raw.Success || raw.Total != 2 || len(raw.Data) != 2 {
		t.Fatalf("response = %#v, want two filtered tasks", raw)
	}
	items := make(map[string]map[string]interface{}, len(raw.Data))
	for _, item := range raw.Data {
		items[item["taskRunId"].(string)] = item
	}
	item := items["task-1"]
	if item["taskRunId"] != "task-1" || item["taskId"] != "WS-1" ||
		item["runner"] != "scout-task-runner" || item["status"] != "running" ||
		item["logsAvailable"] != true || item["transcriptAvailable"] != true {
		t.Fatalf("task DTO = %#v", item)
	}
	if _, exists := item["task_run_id"]; exists {
		t.Fatalf("task DTO contains snake_case: %#v", item)
	}
	if item := items["task-no-log"]; item["logsAvailable"] != false {
		t.Fatalf("task without LogsRef = %#v, want logsAvailable false", item)
	}
	if item := items["task-no-log"]; item["transcriptAvailable"] != false {
		t.Fatalf("task without transcript_ref = %#v, want transcriptAvailable false", item)
	}
}

func TestGetTaskRunLogHappyMissingTruncatedAndRejectsTraversal(t *testing.T) {
	st, svc := seededAgentServiceStore(t)
	run := createAgentServiceRun(t, st, svc, "run-logs")
	for _, taskRunID := range []string{"task-happy", "task-missing", "task-large"} {
		if _, err := st.TaskRuns().Create(t.Context(), store.TaskRunCreate{
			WorkspaceKey: "WS", TaskRunID: taskRunID, DriverRunID: run.RunID,
			TaskID: "WS-" + taskRunID, Runner: "scout-task-runner", Status: domain.TaskRunRunning,
		}); err != nil {
			t.Fatalf("create task run %s: %v", taskRunID, err)
		}
	}
	happyRef, err := taskrunlogs.PutTask(t.Context(), st, "WS", "task-happy", "repo discovery\ncodex CLI exit=0\nAI output\n")
	if err != nil {
		t.Fatalf("PutTask happy: %v", err)
	}
	tail := strings.Repeat("z", 1<<20)
	largeRef, err := taskrunlogs.PutTask(t.Context(), st, "WS", "task-large", "discarded"+tail)
	if err != nil {
		t.Fatalf("PutTask large: %v", err)
	}
	setTaskRunLogRef(t, st, "task-happy", happyRef)
	setTaskRunLogRef(t, st, "task-large", largeRef)
	module := NewModule(st)

	t.Run("happy", func(t *testing.T) {
		rec := getTaskRunLog(t, module, "task-happy")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		var response testPersistedLogResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !response.Success || response.Data.Content != "repo discovery\ncodex CLI exit=0\nAI output\n" || response.Data.ModifiedAt.IsZero() || response.Data.Truncated {
			t.Fatalf("response = %#v", response)
		}
	})

	t.Run("missing", func(t *testing.T) {
		rec := getTaskRunLog(t, module, "task-missing")
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "task log is not available") {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("truncated", func(t *testing.T) {
		rec := getTaskRunLog(t, module, "task-large")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		var response testPersistedLogResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !response.Data.Truncated || response.Data.Content != tail {
			t.Fatalf("truncated = %v content bytes = %d, want true/%d", response.Data.Truncated, len(response.Data.Content), len(tail))
		}
	})

	for _, taskRunID := range []string{"../evil", "..\\evil", "nested/task"} {
		t.Run("reject "+taskRunID, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.SetPathValue("ws", "WS")
			req.SetPathValue("taskRunId", taskRunID)
			rec := httptest.NewRecorder()
			module.getTaskRunLog(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid task run id") {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestGetTaskRunTranscriptHappyMissingAndRejectsTraversal(t *testing.T) {
	st, svc := seededAgentServiceStore(t)
	run := createAgentServiceRun(t, st, svc, "run-transcripts")
	for _, taskRunID := range []string{"task-happy", "task-claude", "task-missing"} {
		metadata := map[string]string(nil)
		if taskRunID != "task-missing" {
			metadata = map[string]string{"transcript_ref": "artifact://transcript-" + taskRunID}
		}
		if _, err := st.TaskRuns().Create(t.Context(), store.TaskRunCreate{
			WorkspaceKey: "WS", TaskRunID: taskRunID, DriverRunID: run.RunID,
			TaskID: "WS-" + taskRunID, Runner: "scout-task-runner", Status: domain.TaskRunRunning,
			RuntimeMetadata: metadata,
		}); err != nil {
			t.Fatalf("create task run %s: %v", taskRunID, err)
		}
	}
	transcriptBody := []byte(
		`{"seq":1,"timestamp":"2026-08-15T12:00:00Z","role":"system","type":"session_meta","text":"local-cli-codex session"}` + "\n" +
			`{"seq":2,"timestamp":"2026-08-15T12:00:01Z","role":"assistant","type":"text","text":"analysis complete"}` + "\n",
	)
	if _, err := store.UploadContentArtifact(t.Context(), st.Artifacts(), store.ArtifactCreate{
		WorkspaceKey: "WS", ArtifactID: "transcript-task-happy", TaskID: "WS-task-happy",
		OwnerType: "task_run", OwnerID: "task-happy", Type: "transcript",
		MIMEType: "application/x-ndjson", DurableStatus: "declared",
	}, transcriptBody); err != nil {
		t.Fatalf("seed transcript artifact: %v", err)
	}
	claudeTranscriptBody := []byte(
		`{"seq":1,"timestamp":"2026-08-15T12:00:00Z","role":"system","type":"session_meta","text":"local-cli-claude session"}` + "\n" +
			`{"seq":2,"timestamp":"2026-08-15T12:00:01Z","role":"assistant","type":"reasoning","text":"inspect repository history"}` + "\n",
	)
	if _, err := store.UploadContentArtifact(t.Context(), st.Artifacts(), store.ArtifactCreate{
		WorkspaceKey: "WS", ArtifactID: "transcript-task-claude", TaskID: "WS-task-claude",
		OwnerType: "task_run", OwnerID: "task-claude", Type: "transcript",
		MIMEType: "application/x-ndjson", DurableStatus: "declared",
	}, claudeTranscriptBody); err != nil {
		t.Fatalf("seed claude transcript artifact: %v", err)
	}
	module := NewModule(st)

	t.Run("happy", func(t *testing.T) {
		rec := getTaskRunTranscript(t, module, "task-happy")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				SessionID string             `json:"session_id"`
				Entries   []transcript.Event `json:"entries"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !response.Success || response.Data.SessionID != "task-happy" || len(response.Data.Entries) != 2 || response.Data.Entries[1].Text != "analysis complete" {
			t.Fatalf("response = %#v", response)
		}
	})

	t.Run("non-codex canonical fixture", func(t *testing.T) {
		rec := getTaskRunTranscript(t, module, "task-claude")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		var response misc.TranscriptResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !response.Success || response.Data == nil || response.Data.SessionID != "task-claude" || len(response.Data.Entries) != 2 || response.Data.Entries[1].Type != transcript.EventReasoning {
			t.Fatalf("response = %#v", response)
		}
	})

	t.Run("missing", func(t *testing.T) {
		rec := getTaskRunTranscript(t, module, "task-missing")
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "transcript is not available") {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
	})

	for _, taskRunID := range []string{"../evil", "..\\evil", "nested/task"} {
		t.Run("reject "+taskRunID, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.SetPathValue("ws", "WS")
			req.SetPathValue("taskRunId", taskRunID)
			rec := httptest.NewRecorder()
			module.getTaskRunTranscript(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid task run id") {
				t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

type testPersistedLogResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Content    string    `json:"content"`
		ModifiedAt time.Time `json:"modifiedAt"`
		Truncated  bool      `json:"truncated"`
	} `json:"data"`
}

func setTaskRunLogRef(t testing.TB, st store.Store, taskRunID, ref string) {
	t.Helper()
	if _, err := st.TaskRuns().Heartbeat(context.Background(), "WS", taskRunID, store.TaskRunHeartbeat{LogsRef: ref}); err != nil {
		t.Fatalf("set task run %s logs ref: %v", taskRunID, err)
	}
}

func TestGetAgentServiceJournalReturnsNotFoundBeforeFirstScoutRun(t *testing.T) {
	st, _ := seededAgentServiceStore(t)
	rec := getAgentServiceJournal(t, NewModule(st, t.TempDir()), "scout")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no journal yet") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetAgentServiceJournalRejectsServiceWithoutJournal(t *testing.T) {
	st, svc := seededAgentServiceStore(t)
	createAgentService(t, st, svc, "reviewer")
	rec := getAgentServiceJournal(t, NewModule(st, t.TempDir()), "reviewer")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "this agent has no journal") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetAgentServiceJournalRejectsUnknownService(t *testing.T) {
	st, _ := seededAgentServiceStore(t)
	rec := getAgentServiceJournal(t, NewModule(st, t.TempDir()), "missing")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "agent service not found") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetAgentServiceJournalReturnsLast512KiB(t *testing.T) {
	st, _ := seededAgentServiceStore(t)
	runtimeDir := t.TempDir()
	tail := bytes.Repeat([]byte("z"), maxJournalBytes)
	content := append(bytes.Repeat([]byte("discarded"), 100), tail...)
	if err := os.WriteFile(filepath.Join(runtimeDir, journalFilename), content, 0o600); err != nil {
		t.Fatalf("write journal: %v", err)
	}

	rec := getAgentServiceJournal(t, NewModule(st, runtimeDir), "scout")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response agentServiceJournalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Data.Truncated || response.Data.Content != string(tail) {
		t.Fatalf("truncated = %v content bytes = %d, want true/%d", response.Data.Truncated, len(response.Data.Content), len(tail))
	}
}

func TestGetAgentServiceJournalNeverUsesServiceIDAsAPath(t *testing.T) {
	st, svc := seededAgentServiceStore(t)
	createAgentService(t, st, svc, "../evil")
	runtimeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runtimeDir, journalFilename), []byte("safe journal"), 0o600); err != nil {
		t.Fatalf("write fixed journal: %v", err)
	}
	siblingDir := filepath.Join(filepath.Dir(runtimeDir), "evil")
	if err := os.Mkdir(siblingDir, 0o700); err != nil {
		t.Fatalf("make traversal target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(siblingDir, journalFilename), []byte("traversed journal"), 0o600); err != nil {
		t.Fatalf("write traversal target: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetPathValue("ws", "WS")
	req.SetPathValue("id", "../evil")
	rec := httptest.NewRecorder()
	NewModule(st, runtimeDir).getAgentServiceJournal(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "this agent has no journal") || strings.Contains(rec.Body.String(), "traversed journal") {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func getAgentServiceJournal(t *testing.T, module *Module, serviceID string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	module.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/agent-services/"+serviceID+"/journal", nil))
	return rec
}

func getTaskRunLog(t *testing.T, module *Module, taskRunID string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	module.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/task-runs/"+taskRunID+"/log", nil))
	return rec
}

func getTaskRunTranscript(t *testing.T, module *Module, taskRunID string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	module.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/task-runs/"+taskRunID+"/transcript", nil))
	return rec
}

func createAgentService(t *testing.T, st store.Store, behavior *domain.AgentService, serviceID string) {
	t.Helper()
	if _, err := st.AgentServices().Create(t.Context(), store.AgentServiceCreate{
		WorkspaceKey: "WS", ServiceID: serviceID, Name: serviceID, Kind: domain.AgentServiceKindCron,
		DesiredState: domain.AgentServiceDesiredRunning, DriverID: behavior.DriverID,
		DriverVersionID: behavior.DriverVersionID, CreatedBy: "system",
	}); err != nil {
		t.Fatalf("create agent service %q: %v", serviceID, err)
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

func createAgentServiceRun(t *testing.T, st store.Store, svc *domain.AgentService, runID string) *domain.DriverRun {
	t.Helper()
	run, err := st.DriverRuns().Create(t.Context(), store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: runID, DriverID: svc.DriverID,
		DriverVersionID: svc.DriverVersionID, AgentServiceID: svc.ServiceID,
	})
	if err != nil {
		t.Fatalf("create agent service run %s: %v", runID, err)
	}
	return run
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
