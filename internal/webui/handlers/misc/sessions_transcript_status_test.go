package misc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// transcriptStatusService returns a fixed error from GetSessionTranscript so the
// handler's status mapping can be asserted in isolation.
type transcriptStatusService struct {
	service.SessionService
	err error
}

func (s transcriptStatusService) GetSessionTranscript(context.Context, string, string, string) ([]transcript.Event, error) {
	return nil, s.err
}

// A transcript-store failure must reach the client as a status it can act on:
// 503 for a retryable upstream outage, 404 for content that is gone, and 500
// only for genuinely unclassified faults.
func TestGetSessionTranscriptStatusMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"upstream unavailable", service.ErrUnavailable("transcript store unavailable"), http.StatusServiceUnavailable},
		{"missing managed content", service.ErrNotFound("transcript not found"), http.StatusNotFound},
		{"unclassified failure", service.ErrInternal("failed to load transcript", errors.New("boom")), http.StatusInternalServerError},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := handleGetSessionTranscript(transcriptStatusService{err: tc.err})
			req := httptest.NewRequest(http.MethodGet, "/api/tasks/loom-abc/sessions/s1/transcript", nil)
			req.SetPathValue("taskId", "loom-abc")
			req.SetPathValue("sessionId", "s1")
			rr := httptest.NewRecorder()

			h(rr, req)

			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rr.Code, tc.want, rr.Body.String())
			}
			var body struct {
				Success bool   `json:"success"`
				Error   string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Success {
				t.Fatalf("success = true, want false for %v", tc.err)
			}
			if body.Error == "" {
				t.Fatal("error message is empty")
			}
		})
	}
}
