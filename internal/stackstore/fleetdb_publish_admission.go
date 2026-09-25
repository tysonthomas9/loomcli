package stackstore

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore/stackwire"
)

func ttlSecondsPtr(d time.Duration) *int {
	s := int(d / time.Second)
	return &s
}

// AcquirePublishAdmission acquires the FleetDB per-(workspace, stack_id) lease.
// Fail closed on store/API unavailability — never degrade unlocked.
func (s *FleetDBStore) AcquirePublishAdmission(ctx context.Context, ws string, id sl.StackID, holder string) (*PublishAdmission, error) {
	lease, err := s.api.AcquirePublishLease(ctx, ws, string(id), stackwire.AcquirePublishLease{
		Holder:       holder,
		TTLSeconds:   ttlSecondsPtr(domain.DefaultStackPublishLeaseTTL),
		GraceSeconds: ttlSecondsPtr(domain.DefaultStackPublishLeaseGrace),
	})
	if err != nil {
		return nil, mapPublishLeaseErr(err)
	}
	adm, aerr := admissionFromWire(lease)
	if aerr != nil {
		return nil, aerr
	}
	return adm, nil
}

// RenewPublishAdmission extends a live FleetDB lease when the token matches.
func (s *FleetDBStore) RenewPublishAdmission(ctx context.Context, ws string, id sl.StackID, token string) (*PublishAdmission, error) {
	lease, err := s.api.RenewPublishLease(ctx, ws, string(id), stackwire.RenewPublishLease{
		Token:        token,
		TTLSeconds:   ttlSecondsPtr(domain.DefaultStackPublishLeaseTTL),
		GraceSeconds: ttlSecondsPtr(domain.DefaultStackPublishLeaseGrace),
	})
	if err != nil {
		return nil, mapPublishLeaseErr(err)
	}
	adm, aerr := admissionFromWire(lease)
	if aerr != nil {
		return nil, aerr
	}
	return adm, nil
}

// ReleasePublishAdmission clears the FleetDB lease (including grace) when the
// token matches. Callers must only release after prior forge calls have settled.
func (s *FleetDBStore) ReleasePublishAdmission(ctx context.Context, ws string, id sl.StackID, token string) error {
	if err := s.api.ReleasePublishLease(ctx, ws, string(id), token); err != nil {
		return mapPublishLeaseErr(err)
	}
	return nil
}

// AbandonPublishAdmission leaves the FleetDB lease in place so TTL+grace protect
// a successor while a prior remote effect may still complete. No API call.
func (s *FleetDBStore) AbandonPublishAdmission(context.Context, string, sl.StackID, string) error {
	return nil
}

func admissionFromWire(lease *stackwire.PublishLease) (*PublishAdmission, error) {
	if lease == nil || lease.Token == "" {
		return nil, fmt.Errorf("stack publish lease grant missing token: %w", domain.ErrStackPublishLeaseStoreUnavailable)
	}
	return &PublishAdmission{
		Token: lease.Token, Holder: lease.Holder, Generation: lease.Generation,
		ExpiresAt: lease.ExpiresAt, ReuseAfter: lease.ReuseAfter,
	}, nil
}

func mapPublishLeaseErr(err error) error {
	if err == nil {
		return nil
	}
	var busy *domain.StackPublishLeaseBusyError
	if errors.As(err, &busy) {
		return busy
	}
	if errors.Is(err, domain.ErrStackPublishLeaseStoreUnavailable) ||
		errors.Is(err, domain.ErrStackPublishLeaseTokenMismatch) ||
		errors.Is(err, domain.ErrStackPublishLeaseLost) {
		return err
	}
	// Renew 410 lease_expired → uncertain loss. A bare NotFound on the store
	// path is treated as unavailable (fail closed); StackClient.Renew already
	// maps renew-404 to ErrStackPublishLeaseLost before this helper runs.
	if errors.Is(err, domain.ErrGone) {
		return fmt.Errorf("%w: %w", domain.ErrStackPublishLeaseLost, err)
	}
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%w: %w", domain.ErrStackPublishLeaseStoreUnavailable, err)
	}
	var apiErr *stackwire.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Status == http.StatusServiceUnavailable:
			return fmt.Errorf("%w: %w", domain.ErrStackPublishLeaseStoreUnavailable, err)
		case apiErr.Status == http.StatusGone:
			return fmt.Errorf("%w: %w", domain.ErrStackPublishLeaseLost, err)
		case apiErr.Status == http.StatusNotFound:
			return fmt.Errorf("%w: %w", domain.ErrStackPublishLeaseStoreUnavailable, err)
		case apiErr.Code == "stack_publish_lease_token_mismatch":
			return fmt.Errorf("%w: %w", domain.ErrStackPublishLeaseTokenMismatch, err)
		}
	}
	// Transport / unexpected failures also fail closed.
	return fmt.Errorf("%w: %w", domain.ErrStackPublishLeaseStoreUnavailable, err)
}

var _ PublishAdmittable = (*FleetDBStore)(nil)
