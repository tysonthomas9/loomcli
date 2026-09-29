package fleetdb

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/stackstore/stackwire"
)

const (
	stackPublishLeaseBusyCode             = "stack_publish_lease_busy"
	stackPublishLeaseTokenMismatchCode    = "stack_publish_lease_token_mismatch"
	stackPublishLeaseStoreUnavailableCode = "stack_publish_lease_store_unavailable"
	stackPublishLeaseTokenHeader          = "X-Lease-Token" //nolint:gosec // G101: header NAME, not a credential
)

type releasePublishLeaseBody struct {
	Token string `json:"token"`
}

func (s *StackClient) publishLeasePath(ws, id string) string {
	return s.stackPath(ws, id) + "/publish-lease"
}

// AcquirePublishLease acquires a cross-machine publish admission grant.
func (s *StackClient) AcquirePublishLease(ctx context.Context, ws, id string, in stackwire.AcquirePublishLease) (*stackwire.PublishLease, error) {
	var out stackwire.PublishLease
	status, _, err := s.client.doWithResponseNoRedirect(ctx, http.MethodPost, s.publishLeasePath(ws, id), in, &out, nil)
	if err != nil {
		// Missing route (older fleet-db) is treated as store unavailable so
		// mutating publish fails closed — never degrade unlocked.
		if status == http.StatusNotFound {
			return nil, fmt.Errorf("stack publish lease endpoint unavailable: %w", domain.ErrStackPublishLeaseStoreUnavailable)
		}
		return nil, err
	}
	return &out, nil
}

// RenewPublishLease extends a live lease when the token matches.
func (s *StackClient) RenewPublishLease(ctx context.Context, ws, id string, in stackwire.RenewPublishLease) (*stackwire.PublishLease, error) {
	var out stackwire.PublishLease
	status, _, err := s.client.doWithResponseNoRedirect(ctx, http.MethodPut, s.publishLeasePath(ws, id), in, &out, nil)
	if err != nil {
		// Renew 404 means the lease generation is gone (OpenAPI NotFound), not
		// an absent route — treat as lease lost so the session fails closed.
		if status == http.StatusNotFound {
			return nil, fmt.Errorf("stack publish lease not found on renew: %w", domain.ErrStackPublishLeaseLost)
		}
		return nil, err
	}
	return &out, nil
}

// ReleasePublishLease clears the lease (including grace) when the token matches.
// Missing / already-released leases are idempotent success.
func (s *StackClient) ReleasePublishLease(ctx context.Context, ws, id, token string) error {
	headers := map[string]string{stackPublishLeaseTokenHeader: token}
	status, _, err := s.client.doWithResponseNoRedirect(ctx, http.MethodDelete,
		s.publishLeasePath(ws, id), releasePublishLeaseBody{Token: token}, nil, headers)
	if err != nil {
		// FleetDB Release maps absent leases to 204. A 404 is treated as
		// idempotent success so cleanup after settle never blocks on a missing
		// row or an older fleet-db without the route.
		if status == http.StatusNotFound {
			return nil
		}
		return err
	}
	return nil
}

func stackPublishLeaseBusyError(prefix string, body []byte) error {
	meta := extractErrorMeta(body)
	expiresAt, _ := time.Parse(time.RFC3339Nano, meta["expires_at"])
	reuseAfter, _ := time.Parse(time.RFC3339Nano, meta["reuse_after"])
	gen, _ := strconv.ParseInt(meta["generation"], 10, 64)
	return &domain.StackPublishLeaseBusyError{
		Message:    prefix,
		Holder:     meta["holder"],
		Generation: gen,
		ExpiresAt:  expiresAt,
		ReuseAfter: reuseAfter,
	}
}
