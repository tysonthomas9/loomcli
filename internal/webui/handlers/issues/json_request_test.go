package issues

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func TestDecodeOptionalIssueJSONTransportPolicy(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantTrailing bool
		wantTooLarge bool
	}{
		{name: "empty body"},
		{name: "one value", body: `{"reason":"retry"}`},
		{name: "trailing value", body: `{"reason":"retry"} {}`, wantTrailing: true},
		{
			name:         "body too large",
			body:         `{"reason":"` + strings.Repeat("x", handler.MaxRequestBody) + `"}`,
			wantTooLarge: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			rec := httptest.NewRecorder()
			var got ReopenRequest
			err := decodeOptionalIssueJSON(rec, req, &got)

			if test.wantTrailing {
				if !errors.Is(err, handler.ErrTrailingJSON) {
					t.Fatalf("error = %v, want ErrTrailingJSON", err)
				}
				return
			}
			if test.wantTooLarge {
				var maxBytesErr *http.MaxBytesError
				if !errors.As(err, &maxBytesErr) {
					t.Fatalf("error = %T %v, want *http.MaxBytesError", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeOptionalIssueJSON() error = %v", err)
			}
		})
	}
}
