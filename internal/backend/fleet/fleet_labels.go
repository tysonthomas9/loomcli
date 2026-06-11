// Label add/remove updates against fleet-db, including the read-back wait
// that keeps label mutations observable before Update returns.

package fleet

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func (b *FleetBackend) applyLabelUpdates(ctx context.Context, id string, params backend.UpdateParams) error {
	for _, label := range params.AddLabels {
		if err := b.AddLabel(ctx, id, label); err != nil {
			return err
		}
		if err := b.waitForLabelState(ctx, id, label, true); err != nil {
			return err
		}
	}
	for _, label := range params.RemoveLabels {
		if err := b.RemoveLabel(ctx, id, label); err != nil {
			return err
		}
		if err := b.waitForLabelState(ctx, id, label, false); err != nil {
			return err
		}
	}
	if len(params.SetLabels) == 0 {
		return nil
	}
	current, err := b.Get(ctx, id)
	if err != nil {
		return err
	}
	for _, label := range current.Labels {
		if !containsString(params.SetLabels, label) {
			if err := b.RemoveLabel(ctx, id, label); err != nil {
				return err
			}
			if err := b.waitForLabelState(ctx, id, label, false); err != nil {
				return err
			}
		}
	}
	for _, label := range params.SetLabels {
		if !containsString(current.Labels, label) {
			if err := b.AddLabel(ctx, id, label); err != nil {
				return err
			}
			if err := b.waitForLabelState(ctx, id, label, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *FleetBackend) waitForLabelState(ctx context.Context, id, label string, wantPresent bool) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	var lastErr error
	for {
		detail, err := b.Get(ctx, id)
		if err == nil && detail != nil && containsString(detail.Labels, label) == wantPresent {
			return nil
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return backend.ErrTimeout("Update", "label projection did not settle", ctx.Err())
		case <-timeout.C:
			return backend.ErrTimeout("Update", "label projection did not settle", lastErr)
		case <-ticker.C:
		}
	}
}

func hasLabelUpdate(params backend.UpdateParams) bool {
	return len(params.AddLabels) > 0 || len(params.RemoveLabels) > 0 || len(params.SetLabels) > 0
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
