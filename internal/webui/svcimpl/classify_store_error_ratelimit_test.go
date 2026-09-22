package svcimpl

import (
	"errors"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

func serviceKind(t *testing.T, err error) service.ErrorKind {
	t.Helper()
	var se *service.ServiceError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v (%T), want *service.ServiceError", err, err)
	}
	return se.Kind
}

// A throttled agent mutation must reach the handler layer as KindRateLimited
// (429), not KindConflict (409). Every classifyStoreError caller is an agent
// CRUD mutation, so this is the arm that decides what an operator sees when
// fleet-db pushes back.
func TestClassifyStoreError_RateLimitedIsNotAConflict(t *testing.T) {
	t.Parallel()
	err := classifyStoreError("create agent", &domain.RateLimitError{
		RetryAfter: "30",
		Detail:     "fleetdb: POST /agents: HTTP 429: slow down",
	})
	if got := serviceKind(t, err); got != service.KindRateLimited {
		t.Fatalf("kind = %q, want %q", got, service.KindRateLimited)
	}
}

// The sentinel alone is enough: a wrapped ErrRateLimited with no
// *RateLimitError still classifies, so a future caller that only has the class
// does not silently fall through to ErrInternal.
func TestClassifyStoreError_RateLimitedSentinelWrapped(t *testing.T) {
	t.Parallel()
	err := classifyStoreError("update agent", fmt.Errorf("update agent: %w", domain.ErrRateLimited))
	if got := serviceKind(t, err); got != service.KindRateLimited {
		t.Fatalf("kind = %q, want %q", got, service.KindRateLimited)
	}
}

// The other arms are untouched: this change carves 429 out of the conflict
// bucket, it does not reshape the rest of the table.
func TestClassifyStoreError_OtherKindsUnchanged(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want service.ErrorKind
	}{
		{"not found", domain.ErrNotFound, service.KindNotFound},
		{"already exists", domain.ErrAlreadyExists, service.KindConflict},
		{"invalid", domain.ErrInvalid, service.KindValidation},
		{"conflict", domain.ErrConflict, service.KindConflict},
		{"unmapped", errors.New("boom"), service.KindInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := serviceKind(t, classifyStoreError("op", tc.err)); got != tc.want {
				t.Fatalf("kind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyStoreError_NilStaysNil(t *testing.T) {
	t.Parallel()
	if err := classifyStoreError("op", nil); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}
