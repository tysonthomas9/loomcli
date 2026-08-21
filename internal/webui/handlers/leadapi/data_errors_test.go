package leadapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/handlers/issues"
)

func TestWriteDataErrorMatchesIssuesEnvelope(t *testing.T) {
	want := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/WS/issues", strings.NewReader("not json"))
	issues.HandleCreateIssue(nil).ServeHTTP(want, req)

	got := httptest.NewRecorder()
	writeDataError(got, http.StatusBadRequest, "INVALID_JSON", "invalid request body")

	if got.Code != want.Code || got.Header().Get("Content-Type") != want.Header().Get("Content-Type") || got.Body.String() != want.Body.String() {
		t.Fatalf("data error = (%d, %q, %q), want issues envelope (%d, %q, %q)",
			got.Code, got.Header().Get("Content-Type"), got.Body.String(),
			want.Code, want.Header().Get("Content-Type"), want.Body.String())
	}
}

func TestWriteDataStatusErrorUsesOpStatusTuple(t *testing.T) {
	rec := httptest.NewRecorder()
	writeDataStatusError(rec, newStatusError(http.StatusTooManyRequests, "rate_limited", "placement request rate exceeded", true))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if got, want := rec.Body.String(), "{\"success\":false,\"error\":\"placement request rate exceeded\",\"code\":\"rate_limited\"}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}
