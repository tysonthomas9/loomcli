package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

func TestRecoverPullThenApplySkipsOnlyBlockedLeads(t *testing.T) {
	previous := recoverPull
	t.Cleanup(func() { recoverPull = previous })
	recoverPull = func(context.Context) error {
		return errors.Join(&pull.PlanError{Workspace: "W1", Lead: "L", Err: errors.New("no working area")})
	}
	var skipped []string
	err := recoverPullThenApply(context.Background(), func(_ context.Context, skip func(string, string) bool) error {
		for _, target := range [][2]string{{"W1", "L"}, {"W2", "L"}} {
			if skip(target[0], target[1]) {
				skipped = append(skipped, target[0]+"/"+target[1])
			}
		}
		return errors.New("recover apply for W2/L: broken")
	})
	if len(skipped) != 1 || skipped[0] != "W1/L" {
		t.Fatalf("skipped leads = %v", skipped)
	}
	if err == nil || !strings.Contains(err.Error(), "W1/L") || !strings.Contains(err.Error(), "W2/L") {
		t.Fatalf("aggregated error = %v", err)
	}
	recoverPull = func(context.Context) error { return errors.New("open journal") }
	called := false
	if err := recoverPullThenApply(context.Background(), func(context.Context, func(string, string) bool) error {
		called = true
		return nil
	}); err == nil || called {
		t.Fatalf("unscoped pull failure: apply called = %v, err = %v", called, err)
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
