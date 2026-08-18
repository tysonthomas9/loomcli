package driverapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/entity"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestDriverAPICreateIssue(t *testing.T) {
	h := newTestHarness(t, "")
	createdAt := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	h.backend.createResult = &backend.IssueData{
		ID:         "ISSUE-123",
		Title:      "Add retry to sync",
		Status:     "open",
		Priority:   2,
		IssueType:  "task",
		Labels:     []string{"recommended"},
		SourceRepo: "loomcli",
		Parent:     "EPIC-1",
		CreatedBy:  "driver-run:run-1",
		CreatedAt:  createdAt,
	}

	resp, decoded := h.do(t, opRequest{
		op:      "create-issue",
		headers: h.ownerHeaders(),
		body: map[string]any{
			"title":          "Add retry to sync",
			"description":    "It flakes.\n\n## Acceptance Criteria\n- retries",
			"issueType":      "task",
			"priority":       2,
			"labels":         []string{"recommended"},
			"repo":           "loomcli",
			"parent":         "EPIC-1",
			"design":         "one backoff loop",
			"status":         "open",
			"idempotencyKey": "key-1",
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	// The response is the camelCase wire struct, not raw snake_case IssueData.
	if decoded["id"] != "ISSUE-123" || decoded["issueType"] != "task" ||
		decoded["sourceRepo"] != "loomcli" || decoded["createdBy"] != "driver-run:run-1" {
		t.Fatalf("response = %v, want camelCase created-issue projection", decoded)
	}
	for _, key := range []string{"issue_type", "source_repo", "created_by", "created_at"} {
		if _, ok := decoded[key]; ok {
			t.Fatalf("response leaked snake_case key %q: %v", key, decoded)
		}
	}
	if decoded["createdAt"] != createdAt.Format(time.RFC3339) {
		t.Fatalf("createdAt = %v, want %s", decoded["createdAt"], createdAt.Format(time.RFC3339))
	}

	if len(h.backend.created) != 1 {
		t.Fatalf("backend creates = %d, want 1", len(h.backend.created))
	}
	got := h.backend.created[0]
	if got.Title != "Add retry to sync" || got.IssueType != "task" || got.Priority != 2 ||
		got.SourceRepo != "loomcli" || got.Parent != "EPIC-1" || got.Design != "one backoff loop" ||
		got.Status != "open" || len(got.Labels) != 1 || got.Labels[0] != "recommended" {
		t.Fatalf("backend CreateParams = %+v, want request field mapping", got)
	}
	if got.IdempotencyKey != "key-1" {
		t.Fatalf("IdempotencyKey = %q, want explicit key-1 to win over the default", got.IdempotencyKey)
	}
	// The backend always acts as the verified run actor; there is no override.
	if h.backend.actor != "driver-run:run-1" {
		t.Fatalf("backend actor = %q, want driver-run:run-1", h.backend.actor)
	}
}

func TestDriverAPICreateIssueDefaultsIdempotencyKey(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.createResult = &backend.IssueData{ID: "ISSUE-1", Title: "t", Status: "open"}

	resp, decoded := h.do(t, opRequest{
		op:      "create-issue",
		headers: h.ownerHeaders(),
		body:    map[string]any{"title": "t", "labels": []string{"recommended"}},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	if len(h.backend.created) != 1 {
		t.Fatalf("backend creates = %d, want 1", len(h.backend.created))
	}
	got := h.backend.created[0]
	want, err := got.FleetCreateIdempotencyKey(time.Now())
	if err != nil {
		t.Fatalf("FleetCreateIdempotencyKey: %v", err)
	}
	if got.IdempotencyKey == "" || got.IdempotencyKey != want {
		t.Fatalf("IdempotencyKey = %q, want the run+day+body default %q", got.IdempotencyKey, want)
	}
}

func TestDriverAPICreateIssueAcceptsReviewStatus(t *testing.T) {
	// The scout creates recommendations in review status so they surface in
	// the human review queue instead of the ready set.
	h := newTestHarness(t, "")
	h.backend.createResult = &backend.IssueData{ID: "ISSUE-2", Title: "t", Status: "review"}

	resp, decoded := h.do(t, opRequest{
		op:      "create-issue",
		headers: h.ownerHeaders(),
		body:    map[string]any{"title": "t", "labels": []string{"recommended"}, "status": "review"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, decoded)
	}
	if len(h.backend.created) != 1 || h.backend.created[0].Status != "review" {
		t.Fatalf("backend CreateParams = %+v, want status review passed through", h.backend.created)
	}
}

func TestDriverAPICreateIssueRejectsNotAcceptedFields(t *testing.T) {
	h := newTestHarness(t, "")
	// The strict decode makes the not-accepted list fail loudly: a client
	// actor/owner/force must never be silently dropped.
	for _, body := range []map[string]any{
		{"title": "t", "actor": "someone-else"},
		{"title": "t", "owner": "someone-else"},
		{"title": "t", "createdBy": "someone-else"},
		{"title": "t", "force": true},
		{"title": "t", "metadata": map[string]string{"k": "v"}},
	} {
		resp, decoded := h.do(t, opRequest{op: "create-issue", headers: h.ownerHeaders(), body: body})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status for %v = %d (%v), want 400", body, resp.StatusCode, decoded)
		}
		if code := errorCode(t, decoded); code != "invalid" {
			t.Fatalf("error code for %v = %q, want invalid", body, code)
		}
	}
	if len(h.backend.created) != 0 {
		t.Fatalf("backend creates = %d, want none for rejected params", len(h.backend.created))
	}
}

func TestDriverAPICreateIssueValidatesParams(t *testing.T) {
	h := newTestHarness(t, "")
	for name, body := range map[string]map[string]any{
		"missing title":      {},
		"blank title":        {"title": "   "},
		"oversize title":     {"title": strings.Repeat("x", 501)},
		"bad issueType":      {"title": "t", "issueType": "story"},
		"priority above 4":   {"title": "t", "priority": 5},
		"negative priority":  {"title": "t", "priority": -1},
		"bad status":         {"title": "t", "status": "closed"},
		"oversize idem key":  {"title": "t", "idempotencyKey": strings.Repeat("k", 129)},
		"non-ascii idem key": {"title": "t", "idempotencyKey": "bad key"},
	} {
		resp, decoded := h.do(t, opRequest{op: "create-issue", headers: h.ownerHeaders(), body: body})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d (%v), want 400", name, resp.StatusCode, decoded)
		}
		if code := errorCode(t, decoded); code != "invalid" {
			t.Fatalf("%s: error code = %q, want invalid", name, code)
		}
	}
	if len(h.backend.created) != 0 {
		t.Fatalf("backend creates = %d, want none for invalid params", len(h.backend.created))
	}
}

// The scout files its own recommendations through this op, so a locally-stale
// issue-type list would reject valid work as 400. Walk entity's vocabulary
// rather than a copy of it.
func TestDriverAPICreateIssueAcceptsEveryValidIssueType(t *testing.T) {
	for _, issueType := range []entity.IssueType{
		entity.TypeTask, entity.TypeBug, entity.TypeFeature, entity.TypeEpic, entity.TypeChore,
	} {
		h := newTestHarness(t, "")
		h.backend.createResult = &backend.IssueData{
			ID: "ISSUE-1", Title: "t", Status: "open", IssueType: string(issueType),
		}
		resp, decoded := h.do(t, opRequest{
			op: "create-issue", headers: h.ownerHeaders(),
			body: map[string]any{"title": "t", "issueType": string(issueType)},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("issueType %q: status = %d (%v), want 200", issueType, resp.StatusCode, decoded)
		}
	}
}

func TestDriverAPICreateIssueRequiresRunOwnership(t *testing.T) {
	h := newTestHarness(t, "")
	headers := h.ownerHeaders()
	headers[HeaderDriverFencingToken] = "999999"
	resp, decoded := h.do(t, opRequest{
		op:      "create-issue",
		headers: headers,
		body:    map[string]any{"title": "t"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d (%v), want 403", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "not_owner" {
		t.Fatalf("error code = %q, want not_owner", code)
	}
}

func TestDriverAPICreateIssueRequiresRunningRun(t *testing.T) {
	h := newTestHarness(t, "")
	if _, err := h.store.DriverRuns().Finish(context.Background(), "WS", h.runID, store.DriverRunFinish{
		NodeID:       h.nodeID,
		LeaseID:      h.leaseID,
		FencingToken: h.fence,
		Status:       domain.DriverRunCompleted,
		Summary:      "done",
	}); err != nil {
		t.Fatalf("Finish driver run: %v", err)
	}
	resp, decoded := h.do(t, opRequest{
		op:      "create-issue",
		headers: h.ownerHeaders(),
		body:    map[string]any{"title": "t"},
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d (%v), want 409", resp.StatusCode, decoded)
	}
	if code := errorCode(t, decoded); code != "invalid_transition" {
		t.Fatalf("error code = %q, want invalid_transition", code)
	}
}

func TestDriverAPICreateIssueTranslatesBackendErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		err       error
		status    int
		code      string
		retryable bool
	}{
		"conflict": {
			err:    backend.ErrConflict("Create", "idempotency key reused with a different body"),
			status: http.StatusConflict, code: "conflict",
		},
		"validation": {
			err:    backend.ErrValidation("Create", "title is required"),
			status: http.StatusBadRequest, code: "invalid",
		},
		"not found": {
			err:    backend.ErrNotFound("Create", "parent issue not found"),
			status: http.StatusNotFound, code: "not_found",
		},
		"canceled": {
			err:    backend.ErrCanceled("Create", "canceled", context.Canceled),
			status: 499, code: "canceled", retryable: true,
		},
		"timeout": {
			err:    backend.ErrTimeout("Create", "deadline exceeded", context.DeadlineExceeded),
			status: http.StatusGatewayTimeout, code: "timeout", retryable: true,
		},
		"internal": {
			err:    backend.ErrInternal("Create", "boom", nil),
			status: http.StatusInternalServerError, code: "internal",
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newTestHarness(t, "")
			h.backend.createErr = tc.err
			resp, decoded := h.do(t, opRequest{
				op:      "create-issue",
				headers: h.ownerHeaders(),
				body:    map[string]any{"title": "t"},
			})
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d (%v), want %d", resp.StatusCode, decoded, tc.status)
			}
			envelope := requireEnvelope(t, decoded)
			if envelope["code"] != tc.code {
				t.Fatalf("error code = %v, want %s", envelope["code"], tc.code)
			}
			if retryable, _ := envelope["retryable"].(bool); retryable != tc.retryable {
				t.Fatalf("retryable = %v, want %v", envelope["retryable"], tc.retryable)
			}
		})
	}
}
