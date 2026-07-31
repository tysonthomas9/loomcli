package fleetdb

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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
	if !errors.Is(err, store.ErrControlPlaneRateLimited) {
		t.Fatalf(
			"err = %v, want errors.Is ErrControlPlaneRateLimited",
			err,
		)
	}
	if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
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
	if !errors.Is(err, store.ErrControlPlaneUnavailable) {
		t.Fatalf(
			"err = %v, want errors.Is ErrControlPlaneUnavailable",
			err,
		)
	}
	if errors.Is(err, domain.ErrNotFound) ||
		errors.Is(err, domain.ErrConflict) {
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
	if !errors.Is(err, store.ErrControlPlaneUnavailable) {
		t.Fatalf(
			"err = %v, want errors.Is ErrControlPlaneUnavailable",
			err,
		)
	}
}

func TestClassifyHTTPError_AgentCommandForbiddenMapsToNotOwner(t *testing.T) {
	t.Parallel()
	err := classifyHTTPError(
		http.MethodPost,
		"/api/v1/WS/agent-commands/cmd-1/complete",
		http.StatusForbidden,
		[]byte(`{"error":{"code":"forbidden","message":"ownership proof rejected"}}`),
	)
	if !errors.Is(err, domain.ErrNotOwner) {
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
