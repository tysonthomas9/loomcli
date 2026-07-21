package fleetdb

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestClassifyHTTPError_GoneMapsToErrGone(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"code":"lease_expired","message":"heartbeat agent ownership lease failed"}}`)
	err := classifyHTTPError(http.MethodPost, "/api/v1/WS/agent-ownership-leases/a/heartbeat", http.StatusGone, body)
	if !errors.Is(err, domain.ErrGone) {
		t.Fatalf("err = %v, want errors.Is ErrGone", err)
	}
	if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, must not satisfy ErrAlreadyExists/ErrConflict", err)
	}
}

func TestClassifyHTTPError_RateLimitRemainsRetryable(t *testing.T) {
	t.Parallel()
	err := classifyHTTPError(
		http.MethodPost,
		"/api/v1/WS/driver-runs/run-1/task-runs/request",
		http.StatusTooManyRequests,
		[]byte(`{"error":{"code":"rate_limit_exceeded","message":"try again"}}`),
	)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want errors.Is ErrRateLimited", err)
	}
	if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
		t.Fatalf("err = %v, must not satisfy a deterministic conflict", err)
	}
	mapped := mapExecutionTransportError("request TaskRun", err)
	if !errors.Is(mapped, ErrExecutionUnavailable) || errors.Is(mapped, ErrExecutionConflict) {
		t.Fatalf("execution mapping = %v, want unavailable and not conflict", mapped)
	}
}

// The pre-410 mappings must be unchanged (back-compat matrix).
func TestClassifyHTTPError_ExistingMappingsUnchanged(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, domain.ErrNotFound},
		{http.StatusConflict, domain.ErrAlreadyExists},
		{http.StatusBadRequest, domain.ErrInvalid},
		{http.StatusUnprocessableEntity, domain.ErrInvalid},
		{http.StatusForbidden, domain.ErrConflict},
	}
	for _, tc := range cases {
		err := classifyHTTPError(http.MethodPost, "/x", tc.status, nil)
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
	}
}
