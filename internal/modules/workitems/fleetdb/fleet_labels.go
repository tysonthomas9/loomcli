// Label add/remove updates against fleet-db, including the read-back wait
// that keeps label mutations observable before Update returns.

package fleetdb

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func (b *Adapter) applyLabelUpdates(ctx context.Context, id string, params workitems.PatchCommand) error {
	for _, label := range params.AddLabels {
		if err := b.addLabel(ctx, id, label); err != nil {
			return err
		}
		if err := b.waitForLabelState(ctx, id, label, true); err != nil {
			return err
		}
	}
	for _, label := range params.RemoveLabels {
		if err := b.removeLabel(ctx, id, label); err != nil {
			return err
		}
		if err := b.waitForLabelState(ctx, id, label, false); err != nil {
			return err
		}
	}
	if len(params.SetLabels) == 0 {
		return nil
	}
	current, err := b.Get(ctx, workitems.GetQuery{IssueID: id})
	if err != nil {
		return err
	}
	for _, label := range current.Labels {
		if !containsString(params.SetLabels, label) {
			if err := b.removeLabel(ctx, id, label); err != nil {
				return err
			}
			if err := b.waitForLabelState(ctx, id, label, false); err != nil {
				return err
			}
		}
	}
	for _, label := range params.SetLabels {
		if !containsString(current.Labels, label) {
			if err := b.addLabel(ctx, id, label); err != nil {
				return err
			}
			if err := b.waitForLabelState(ctx, id, label, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Adapter) waitForLabelState(ctx context.Context, id, label string, wantPresent bool) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	var lastErr error
	for {
		detail, err := b.Get(ctx, workitems.GetQuery{IssueID: id})
		if err == nil && detail != nil && containsString(detail.Labels, label) == wantPresent {
			return nil
		}
		if err != nil {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			return workitems.AdapterTimeout("Patch", "label projection did not settle", ctx.Err())
		case <-timeout.C:
			return workitems.AdapterTimeout("Patch", "label projection did not settle", lastErr)
		case <-ticker.C:
		}
	}
}

func hasLabelUpdate(params workitems.PatchCommand) bool {
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
