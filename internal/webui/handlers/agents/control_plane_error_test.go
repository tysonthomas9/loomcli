package agents

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

func TestAgentReadErrorsPreserveControlPlaneAvailability(t *testing.T) {
	tests := []struct {
		name       string
		writeError func(http.ResponseWriter, error, string)
		err        error
		wantStatus int
	}{
		{
			name:       "binding read is rate limited",
			writeError: writeBindingError,
			err:        fmt.Errorf("list runs: %w", persistence.ErrRateLimited),
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "binding read is unavailable",
			writeError: writeBindingError,
			err:        fmt.Errorf("list runs: %w", persistence.ErrUnavailable),
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "agent read is rate limited",
			writeError: writeAgentRecordError,
			err:        fmt.Errorf("get agent: %w", persistence.ErrRateLimited),
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name:       "agent read is unavailable",
			writeError: writeAgentRecordError,
			err:        fmt.Errorf("get agent: %w", persistence.ErrUnavailable),
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.writeError(response, test.err, "agent history temporarily unavailable")
			if response.Code != test.wantStatus {
				t.Fatalf(
					"status = %d body=%s, want %d",
					response.Code,
					response.Body.String(),
					test.wantStatus,
				)
			}
		})
	}
}
