package misc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/app/query/sessionarchive"
	transcript "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

type durableSessionServiceStub struct {
	sessions []sessionarchive.SessionListItem
}

func (stub durableSessionServiceStub) ListTaskSessions(context.Context, string, string) ([]sessionarchive.SessionListItem, error) {
	if stub.sessions == nil {
		return []sessionarchive.SessionListItem{}, nil
	}
	return stub.sessions, nil
}
func (durableSessionServiceStub) GetSession(context.Context, string, string, string) (*sessionarchive.SessionDetailData, error) {
	return &sessionarchive.SessionDetailData{}, nil
}
func (durableSessionServiceStub) GetSessionTranscript(context.Context, string, string, string) ([]transcript.Event, error) {
	return []transcript.Event{}, nil
}
func (durableSessionServiceStub) GetSessionDiff(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (durableSessionServiceStub) ListSessionHistory(context.Context, string, string) ([]sessionarchive.SessionHistoryItem, error) {
	return nil, nil
}
func (durableSessionServiceStub) GetSessionScrollback(context.Context, string, string, string) (*sessionarchive.SessionScrollbackResult, error) {
	return nil, nil
}

func TestListTaskSessionsReturnsStableEmptyArray(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/tasks/TASK-1/sessions", nil)
	request.SetPathValue("taskId", "TASK-1")
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "WS"))
	response := httptest.NewRecorder()
	HandleListTaskSessions(durableSessionServiceStub{}).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sessions":[]`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestListTaskSessionsSerializesRequiredEvidenceStates(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/workspaces/WS/tasks/TASK-1/sessions", nil)
	request.SetPathValue("taskId", "TASK-1")
	request = request.WithContext(middleware.WithWorkspace(request.Context(), "WS"))
	response := httptest.NewRecorder()
	HandleListTaskSessions(durableSessionServiceStub{sessions: []sessionarchive.SessionListItem{{
		SessionRecordView:        sessionarchive.SessionRecordView{SessionID: "RUN-1", TaskID: "TASK-1"},
		TranscriptEvidenceStatus: "capture_failed",
		DiffEvidenceStatus:       "missing",
	}}}).ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"transcript_evidence_status":"capture_failed"`) ||
		!strings.Contains(response.Body.String(), `"diff_evidence_status":"missing"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}
