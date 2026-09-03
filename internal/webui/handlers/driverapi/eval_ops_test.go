package driverapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/evals"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestDriverEvalOpsListPutAndRejudge(t *testing.T) {
	h := newTestHarness(t, "")
	seedDriverEvalWorkspace(t, h)
	ended := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	seedDriverEvalSession(t, h, "sess-1", ended, map[string]string{evals.MetadataTranscriptRef: "artifact://transcript-1"})

	resp, decoded := h.do(t, opRequest{
		op:      "sessions-list-unevaluated",
		body:    map[string]any{"promptVersion": "v1"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d decoded=%v", resp.StatusCode, decoded)
	}
	sessions, _ := decoded["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %v, want one", decoded["sessions"])
	}
	first := sessions[0].(map[string]any)
	if first["session_id"] != "sess-1" || first["transcript_ref"] != "artifact://transcript-1" {
		t.Fatalf("candidate = %+v", first)
	}
	policy := decoded["policy"].(map[string]any)
	if policy["sampling_percent"].(float64) != 100 || policy["batch_size"].(float64) != 25 || policy["lookback_days"].(float64) != 30 {
		t.Fatalf("policy = %+v", policy)
	}

	resp, decoded = h.do(t, opRequest{
		op: "eval-metric-put",
		body: map[string]any{
			"sessionId":      "sess-1",
			"judgeSessionId": "judge-sess-1",
			"promptVersion":  "v1",
			"status":         "done",
			"eval":           driverEvalPayload(),
		},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metric status = %d decoded=%v", resp.StatusCode, decoded)
	}
	if decoded["evalId"] != "eval-sess-1-v1" || decoded["created"] != true {
		t.Fatalf("metric response = %+v", decoded)
	}
	metric, err := h.store.SessionEvals().Get(context.Background(), "WS", "eval-sess-1-v1")
	if err != nil || metric.JudgeSessionID != "judge-session-1" {
		t.Fatalf("metric judge linkage = %+v, err=%v", metric, err)
	}

	resp, decoded = h.do(t, opRequest{
		op:      "eval-rejudge",
		body:    map[string]any{"sessionId": "sess-1"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK || decoded["requested"] != true {
		t.Fatalf("rejudge status/decoded = %d/%v", resp.StatusCode, decoded)
	}
	if _, err := h.store.SessionEvals().Get(context.Background(), "WS", "eval-sess-1-v1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("eval after rejudge err = %v, want ErrNotFound", err)
	}
	session, err := h.store.AgentSessions().Get(context.Background(), "WS", "sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata[evals.MetadataEvalRequested] != "true" || session.Metadata[evals.MetadataEvalStatus] != "" {
		t.Fatalf("metadata after rejudge = %+v", session.Metadata)
	}
}

func TestDriverSessionTranscriptGetReturnsCanonicalEntries(t *testing.T) {
	h := newTestHarness(t, "")
	seedDriverEvalWorkspace(t, h)
	seedDriverEvalSession(t, h, "sess-1", time.Now().UTC(), map[string]string{evals.MetadataTranscriptRef: "artifact://transcript-1"})
	raw, err := json.Marshal([]transcript.Event{{
		Seq:  1,
		Role: transcript.RoleAssistant,
		Type: transcript.EventText,
		Text: "done",
	}})
	if err != nil {
		t.Fatalf("marshal transcript: %v", err)
	}
	if _, err := store.UploadContentArtifact(context.Background(), h.store.Artifacts(), store.ArtifactCreate{
		WorkspaceKey: "WS",
		ArtifactID:   "transcript-1",
		SessionID:    "sess-1",
		OwnerType:    "session",
		OwnerID:      "sess-1",
		Type:         "transcript",
		MIMEType:     "application/json",
	}, raw); err != nil {
		t.Fatalf("upload transcript: %v", err)
	}

	resp, decoded := h.do(t, opRequest{
		op:      "session-transcript-get",
		body:    map[string]any{"sessionId": "sess-1", "promptVersion": "v1"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("transcript status = %d decoded=%v", resp.StatusCode, decoded)
	}
	if decoded["sessionId"] != "sess-1" {
		t.Fatalf("sessionId = %v", decoded["sessionId"])
	}
	entries, _ := decoded["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["text"] != "done" {
		t.Fatalf("entries = %+v", decoded["entries"])
	}
}

func TestDriverSessionTranscriptGetFetchFailureStampsSession(t *testing.T) {
	h := newTestHarness(t, "")
	seedDriverEvalWorkspace(t, h)
	seedDriverEvalSession(t, h, "sess-1", time.Now().UTC(), map[string]string{evals.MetadataTranscriptRef: "artifact://missing"})

	resp, decoded := h.do(t, opRequest{
		op:      "session-transcript-get",
		body:    map[string]any{"sessionId": "sess-1", "promptVersion": "v1"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("transcript status = %d decoded=%v", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != evals.ErrorTranscriptFetchFailed {
		t.Fatalf("error code = %q, want %q", code, evals.ErrorTranscriptFetchFailed)
	}
	session, err := h.store.AgentSessions().Get(context.Background(), "WS", "sess-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.Metadata[evals.MetadataEvalStatus] != evals.EvalStatusFailed ||
		session.Metadata[evals.MetadataEvalPromptVersion] != "v1" ||
		session.Metadata[evals.MetadataEvalErrorClass] != evals.ErrorTranscriptFetchFailed {
		t.Fatalf("metadata after transcript fetch failure = %+v", session.Metadata)
	}
}

func seedDriverEvalWorkspace(t *testing.T, h *testHarness) {
	t.Helper()
	if _, err := h.store.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
}

func seedDriverEvalSession(t *testing.T, h *testHarness, sessionID string, ended time.Time, metadata map[string]string) {
	t.Helper()
	started := ended.Add(-5 * time.Minute)
	if _, err := h.store.AgentSessions().Create(context.Background(), store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    sessionID,
		AgentID:      "agent-1",
		Kind:         domain.AgentSessionKindTask,
		TaskID:       "TASK-1",
		Status:       domain.AgentSessionRunning,
		StartedAt:    started,
		Metadata:     metadata,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	finishedAt := ended.UTC()
	finishedAtPtr := &finishedAt
	completed := domain.AgentSessionCompleted
	if _, err := h.store.AgentSessions().Update(context.Background(), "WS", sessionID, store.AgentSessionUpdate{
		Status:     &completed,
		FinishedAt: &finishedAtPtr,
	}); err != nil {
		t.Fatalf("finish session: %v", err)
	}
}

func driverEvalPayload() map[string]any {
	return map[string]any{
		"scores": map[string]int{
			"outcome_success":       90,
			"instruction_adherence": 91,
			"efficiency":            92,
			"tool_use_quality":      93,
		},
		"score_rationales": map[string]string{
			"outcome_success":       "Entry 1 shows success.",
			"instruction_adherence": "Entry 2 shows adherence.",
			"efficiency":            "Entry 3 shows efficiency.",
			"tool_use_quality":      "Entry 4 shows tool quality.",
		},
		"error_taxonomy_tags": []string{"verification_skipped"},
		"improvement_categories": map[string][]string{
			"harness": {"Change harness so that verification is mandatory."},
			"linter":  {},
			"prompt":  {},
			"skill":   {},
		},
		"judge_summary": "Good result with a verification gap.",
		"judge_model":   "codex-test",
		"eval_cost": map[string]int{
			"input_tokens":  10,
			"output_tokens": 5,
			"total_tokens":  15,
		},
	}
}
