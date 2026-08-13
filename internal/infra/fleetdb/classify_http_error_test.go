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

// Upstream 5xx is a retryable availability failure, not an opaque error: without
// a sentinel, callers cannot tell "fleet-db is down" from "this service has a
// bug", and every transcript/artifact read failure surfaces as a 500.
func TestClassifyHTTPError_ServerErrorsMapToErrUnavailable(t *testing.T) {
	t.Parallel()
	for _, status := range []int{
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		err := classifyHTTPError(http.MethodGet, "/api/v1/WS/artifacts/a/content", status, nil)
		if !errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("status %d: err = %v, want errors.Is ErrUnavailable", status, err)
		}
		if errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrConflict) {
			t.Fatalf("status %d: err = %v, must not satisfy ErrNotFound/ErrConflict", status, err)
		}
	}
}

// 4xx must keep its existing classification and never become "unavailable".
func TestClassifyHTTPError_ClientErrorsAreNotUnavailable(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusNotFound, http.StatusConflict, http.StatusBadRequest, http.StatusTeapot} {
		err := classifyHTTPError(http.MethodGet, "/x", status, nil)
		if errors.Is(err, domain.ErrUnavailable) {
			t.Fatalf("status %d: err = %v, must not satisfy ErrUnavailable", status, err)
		}
	}
}
