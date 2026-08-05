package fleetdb

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
)

var _ store.DriverRunOutcomeStore = (*driverRunStore)(nil)

func (s *driverRunStore) ClaimDriverRunOutcomes(ctx context.Context, claim store.DriverRunOutcomeClaim) ([]store.DriverRunOutcome, error) {
	body := map[string]any{
		"claim_id": claim.ClaimID, "before": claim.Before, "claim_until": claim.ClaimUntil, "limit": claim.Limit,
	}
	var response struct {
		Outcomes []store.DriverRunOutcome `json:"outcomes"`
	}
	path := "/api/v1/" + pathEscape(claim.WorkspaceKey) + "/driver-run-outcomes/claim"
	if err := s.client.do(ctx, "POST", path, body, &response); err != nil {
		return nil, err
	}
	if response.Outcomes == nil {
		response.Outcomes = []store.DriverRunOutcome{}
	}
	return response.Outcomes, nil
}

func (s *driverRunStore) CompleteDriverRunOutcome(ctx context.Context, completion store.DriverRunOutcomeCompletion) error {
	completedAt := completion.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	body := map[string]any{"run_id": completion.RunID, "claim_id": completion.ClaimID, "completed_at": completedAt}
	path := "/api/v1/" + pathEscape(completion.WorkspaceKey) + "/driver-run-outcomes/complete"
	return s.client.do(ctx, "POST", path, body, nil)
}

func (s *driverRunStore) RetryDriverRunOutcome(ctx context.Context, retry store.DriverRunOutcomeRetry) error {
	body := map[string]any{"run_id": retry.RunID, "claim_id": retry.ClaimID, "available_at": retry.AvailableAt, "error": retry.Error}
	path := "/api/v1/" + pathEscape(retry.WorkspaceKey) + "/driver-run-outcomes/retry"
	return s.client.do(ctx, "POST", path, body, nil)
}
