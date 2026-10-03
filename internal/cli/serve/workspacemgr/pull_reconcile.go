package workspacemgr

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
)

// recoverPullThenApply keeps pull recovery ahead of apply recovery for each
// lead. A lead whose pull could not be recovered skips apply; other leads go on.
func recoverPullThenApply(ctx context.Context, recoverApply func(context.Context, func(string, string) bool) error) error {
	pullErr := pull.Recover(ctx)
	blocked := map[string]bool{}
	if pullErr != nil && !blockedLeads(pullErr, blocked) {
		return pullErr
	}
	return errors.Join(pullErr, recoverApply(ctx, func(workspace, lead string) bool {
		return blocked[workspace+"\x00"+lead]
	}))
}

// blockedLeads collects the leads of per-lead pull failures. It reports false
// when any failure is not scoped to a lead, so apply recovery waits everywhere.
func blockedLeads(err error, blocked map[string]bool) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !blockedLeads(child, blocked) {
				return false
			}
		}
		return true
	}
	var plan *pull.PlanError
	if !errors.As(err, &plan) {
		return false
	}
	blocked[plan.Workspace+"\x00"+plan.Lead] = true
	return true
}
