package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestReviewDecisionGitHubRefFailsClosedBeforeLoomMutation(t *testing.T) {
	mutations := 0
	issues := &mockIssueService{
		getIssueFunc: func(context.Context, string) (json.RawMessage, error) {
			return json.RawMessage(`{"id":"i-1","status":"review","external_ref":"https://github.com/acme/repo/pull/7"}`), nil
		},
		patchIssueFunc: func(context.Context, service.PatchIssueParams) error { mutations++; return nil },
		closeIssueFunc: func(context.Context, service.CloseIssueParams) (json.RawMessage, error) { mutations++; return nil, nil },
	}
	h := HandleReviewDecision(service.NewReviewDecisionService(issues))
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/issues/i-1/review-decision", strings.NewReader(`{"decision":"approve"}`))
	req.SetPathValue("id", "i-1")
	req.Header.Set("X-Idempotency-Key", "decision-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s, want 503", rec.Code, rec.Body.String())
	}
	if mutations != 0 {
		t.Fatalf("Loom mutations = %d, want 0", mutations)
	}
}

func TestReviewDecisionRequestChangesUsesOneIdempotentPatch(t *testing.T) {
	var patch service.PatchIssueParams
	issues := &mockIssueService{
		getIssueFunc: func(context.Context, string) (json.RawMessage, error) {
			return json.RawMessage(`{"id":"i-1","status":"review","notes":"existing","labels":["review"]}`), nil
		},
		patchIssueFunc: func(_ context.Context, got service.PatchIssueParams) error { patch = got; return nil },
	}
	h := HandleReviewDecision(service.NewReviewDecisionService(issues))
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/issues/i-1/review-decision", strings.NewReader(`{"decision":"request_changes","reason":"needs tests"}`))
	req.SetPathValue("id", "i-1")
	req.Header.Set("X-Idempotency-Key", "decision-2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", rec.Code, rec.Body.String())
	}
	if patch.Status == nil || *patch.Status != "open" || patch.Notes == nil || !strings.Contains(*patch.Notes, "[review-decision:decision-2]") {
		t.Fatalf("patch = %+v, want durable open decision marker", patch)
	}
	if len(patch.SetLabels) != 2 || patch.SetLabels[1] != "needs-revision" {
		t.Fatalf("labels = %v, want review + needs-revision", patch.SetLabels)
	}
}

func TestReviewDecisionRequiresStableIdempotencyKey(t *testing.T) {
	issues := &mockIssueService{}
	h := HandleReviewDecision(service.NewReviewDecisionService(issues))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"decision":"approve"}`))
	req.SetPathValue("id", "i-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestReviewDecisionReplaySkipsAlreadyAppliedPatch(t *testing.T) {
	state := `{"id":"i-1","status":"review","notes":"","labels":[]}`
	patches := 0
	issues := &mockIssueService{
		getIssueFunc: func(context.Context, string) (json.RawMessage, error) { return json.RawMessage(state), nil },
		patchIssueFunc: func(_ context.Context, patch service.PatchIssueParams) error {
			patches++
			body, _ := json.Marshal(map[string]any{"id": "i-1", "status": *patch.Status, "notes": *patch.Notes, "labels": patch.SetLabels})
			state = string(body)
			return nil
		},
	}
	svc := service.NewReviewDecisionService(issues)
	params := service.ReviewDecisionParams{IssueID: "i-1", Decision: service.ReviewDecisionRequestChanges, Reason: "needs tests", Actor: "reviewer", DecisionID: "same-intent"}
	first, err := svc.Apply(t.Context(), params)
	if err != nil || first.Replayed {
		t.Fatalf("first = %+v err=%v", first, err)
	}
	second, err := svc.Apply(t.Context(), params)
	if err != nil || !second.Replayed {
		t.Fatalf("second = %+v err=%v", second, err)
	}
	if patches != 1 {
		t.Fatalf("patches = %d, want 1", patches)
	}
}
