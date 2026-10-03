package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/pull"
)

func TestBlockedLeadsScopesPullFailuresToTheirLead(t *testing.T) {
	failed := errors.Join(&pull.PlanError{Workspace: "W1", Lead: "L", Err: errors.New("no working area")},
		fmt.Errorf("wrapped: %w", &pull.PlanError{Workspace: "W2", Lead: "M", Err: errors.New("swap incomplete")}))
	blocked := map[string]bool{}
	if !blockedLeads(failed, blocked) || len(blocked) != 2 || !blocked["W1\x00L"] || !blocked["W2\x00M"] {
		t.Fatalf("blocked leads = %v", blocked)
	}
	// A failure not scoped to one lead keeps apply recovery waiting everywhere.
	if blockedLeads(errors.Join(failed, errors.New("open journal")), map[string]bool{}) {
		t.Fatal("unscoped pull failure was treated as one lead's")
	}
}

func TestRecoverPullThenApplyRunsApplyWithBlockedLeads(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	called := false
	err := recoverPullThenApply(context.Background(), func(_ context.Context, skip func(string, string) bool) error {
		called = true
		if skip("W2", "L") {
			t.Error("lead without a pull failure was skipped")
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("apply recovery called = %v, err = %v", called, err)
	}
}
