package epic

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/stacklock"
)

func TestReconcileEpicStackPassesMinuteWaitToPublisher(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	called := false
	publisher := func(ctx context.Context, workspace, stackID, lead string, changes []string) ([]publish.Result, error) {
		called = true
		if got := stacklock.WaitLimit(ctx); got != time.Minute {
			return nil, fmt.Errorf("epic lock wait = %s, want 1 minute", got)
		}
		if workspace != "WS" || stackID != publish.LeadStackID("lead") || lead != "lead" || changes != nil {
			return nil, fmt.Errorf("epic publish arguments = %q, %q, %q, %v", workspace, stackID, lead, changes)
		}
		return nil, nil
	}
	if err := ReconcileEpicStack(context.Background(), "WS", "lead", publisher); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("epic publisher was not called")
	}
}
