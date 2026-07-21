package fleetdb

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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

func TestClassifyHTTPError_SessionLifecycleAttemptMismatch(t *testing.T) {
	body := []byte(`{"error":{"code":"session_attempt_mismatch","message":"attempt mismatch"}}`)
	err := classifyHTTPError(http.MethodPost, "/agent-sessions/open", http.StatusConflict, body)
	var lifecycleErr *store.SessionLifecycleError
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != store.SessionLifecycleErrAttemptMismatch || lifecycleErr.Retryable() {
		t.Fatalf("err = %v, want non-retryable attempt mismatch", err)
	}
}

func TestClassifyHTTPError_SessionLifecycleContentionIsRetryable(t *testing.T) {
	body := []byte(`{"error":{"code":"session_lifecycle_contention","message":"retry"}}`)
	err := classifyHTTPError(http.MethodPost, "/agent-sessions/open", http.StatusServiceUnavailable, body)
	var transient *store.SessionLifecycleTransientError
	if !errors.As(err, &transient) || transient.Code != store.SessionLifecycleErrContention || !transient.Retryable() {
		t.Fatalf("err = %v, want retryable lifecycle contention", err)
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
