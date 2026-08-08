package workspace

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func TestDecodeCreateRequestTransportPolicy(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantError  string
	}{
		{
			name:       "trailing JSON",
			body:       `{"name":"workspace"} {}`,
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid request body",
		},
		{
			name:       "body too large",
			body:       `{"name":"` + strings.Repeat("x", handler.MaxRequestBody) + `"}`,
			wantStatus: http.StatusRequestEntityTooLarge,
			wantError:  "request body too large",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(test.body))
			rec := httptest.NewRecorder()

			if _, ok := decodeCreateRequest(rec, req); ok {
				t.Fatal("decodeCreateRequest() ok = true, want false")
			}
			if rec.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, test.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), test.wantError) {
				t.Fatalf("body = %s, want error %q", rec.Body.String(), test.wantError)
			}
		})
	}
}
