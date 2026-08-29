package issues

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func TestHandleGetIssueJourney_ReturnsServiceFold(t *testing.T) {
	start := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	svc := &mockIssueService{
		getJourneyFunc: func(_ context.Context, issueID string) (*service.Journey, error) {
			if issueID != "task-1" {
				t.Errorf("issue ID = %q, want task-1", issueID)
			}
			return &service.Journey{
				Spans: []service.JourneySpan{{Kind: "status", Stage: "open", Start: start}},
				Honesty: service.JourneyHonesty{
					CompleteHistory: true,
				},
			}, nil
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/issues/task-1/journey", nil)
	req.SetPathValue("id", "task-1")
	recorder := httptest.NewRecorder()

	HandleGetIssueJourney(svc).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var response JourneyResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Success || response.Data == nil || len(response.Data.Spans) != 1 {
		t.Errorf("response = %+v", response)
	}
}

func TestHandleGetIssueJourney_ValidatesIDAndMapsServiceErrors(t *testing.T) {
	t.Run("missing issue ID", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/issues//journey", nil)
		HandleGetIssueJourney(&mockIssueService{}).ServeHTTP(recorder, req)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("service unavailable", func(t *testing.T) {
		svc := &mockIssueService{
			getJourneyFunc: func(context.Context, string) (*service.Journey, error) {
				return nil, service.ErrUnavailable("history backend unavailable")
			},
		}
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/issues/task-1/journey", nil)
		req.SetPathValue("id", "task-1")
		HandleGetIssueJourney(svc).ServeHTTP(recorder, req)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503: %s", recorder.Code, recorder.Body.String())
		}
	})
}
