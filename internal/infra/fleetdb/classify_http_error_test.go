package fleetdb

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type classifyRoundTripFunc func(*http.Request) (*http.Response, error)

func (function classifyRoundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

func TestClassifyHTTPError_GoneMapsToErrGone(t *testing.T) {
	t.Parallel()
	body := []byte(`{"error":{"code":"lease_expired","message":"heartbeat agent ownership lease failed"}}`)
	err := classifyHTTPError(http.MethodPost, "/api/v1/WS/agent-ownership-leases/a/heartbeat", http.StatusGone, body)
	if !errors.Is(err, persistence.ErrGone) {
		t.Fatalf("err = %v, want errors.Is ErrGone", err)
	}
	if errors.Is(err, persistence.ErrAlreadyExists) || errors.Is(err, persistence.ErrConflict) {
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
	if !errors.Is(err, persistence.ErrRateLimited) {
		t.Fatalf(
			"err = %v, want errors.Is persistence.ErrRateLimited",
			err,
		)
	}
	if errors.Is(err, persistence.ErrAlreadyExists) || errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("err = %v, must not satisfy a deterministic conflict", err)
	}
	mapped := mapExecutionTransportError("request TaskRun", err)
	if !errors.Is(mapped, ErrExecutionUnavailable) || errors.Is(mapped, ErrExecutionConflict) {
		t.Fatalf("execution mapping = %v, want unavailable and not conflict", mapped)
	}
}

func TestClassifyHTTPError_ServerFailureIsControlPlaneUnavailable(
	t *testing.T,
) {
	t.Parallel()
	err := classifyHTTPError(
		http.MethodGet,
		"/api/v1/WS/agent-sessions/session-1",
		http.StatusServiceUnavailable,
		[]byte(`{"error":{"code":"unavailable","message":"try again"}}`),
	)
	if !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf(
			"err = %v, want errors.Is persistence.ErrUnavailable",
			err,
		)
	}
	if errors.Is(err, persistence.ErrNotFound) ||
		errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("err = %v, must not be a deterministic read failure", err)
	}
}

func TestClientTransportFailureIsControlPlaneUnavailable(t *testing.T) {
	t.Parallel()
	client, err := New(Config{
		BaseURL: "http://fleet.invalid",
		HTTPClient: &http.Client{Transport: classifyRoundTripFunc(
			func(*http.Request) (*http.Response, error) {
				return nil, errors.New("connection refused")
			},
		)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AgentSessions().Get(
		t.Context(),
		"WS",
		"session-1",
	)
	if !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf(
			"err = %v, want errors.Is persistence.ErrUnavailable",
			err,
		)
	}
}

func TestClassifyHTTPError_AgentOwnershipForbiddenMapsToNotOwner(t *testing.T) {
	t.Parallel()
	err := classifyHTTPError(
		http.MethodPost,
		"/api/v1/WS/agent-ownership-leases/agent-1/heartbeat",
		http.StatusForbidden,
		[]byte(`{"error":{"code":"forbidden","message":"ownership proof rejected"}}`),
	)
	if !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("err = %v, want errors.Is ErrNotOwner", err)
	}
}

// The pre-410 mappings must be unchanged (back-compat matrix).
func TestClassifyHTTPError_ExistingMappingsUnchanged(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, persistence.ErrNotFound},
		{http.StatusConflict, persistence.ErrAlreadyExists},
		{http.StatusBadRequest, persistence.ErrInvalid},
		{http.StatusUnprocessableEntity, persistence.ErrInvalid},
		{http.StatusForbidden, persistence.ErrConflict},
	}
	for _, tc := range cases {
		err := classifyHTTPError(http.MethodPost, "/x", tc.status, nil)
		if !errors.Is(err, tc.want) {
			t.Fatalf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
	}
}
