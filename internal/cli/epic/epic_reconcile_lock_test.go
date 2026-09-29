package epic

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomgit/stacklock"
	sl "github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackpublish"
)

func TestReconcileEpicStackPassesMinuteWaitToPublisher(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	t.Setenv("GITHUB_TOKEN", "fixture")
	oldPublish := publishEpicFromOrigin
	t.Cleanup(func() { publishEpicFromOrigin = oldPublish })
	called := false
	publishEpicFromOrigin = func(_ *stackpublish.Reconciler, ctx context.Context, _ string, _ sl.StackID, _ string, _ string, _ stackpublish.Options) (*stackpublish.Report, error) {
		called = true
		if got := stacklock.WaitLimit(ctx); got != time.Minute {
			return nil, fmt.Errorf("epic lock wait = %s, want 1 minute", got)
		}
		return nil, nil
	}
	if err := reconcileEpicStack(context.Background(), "WS", &EpicStackProjection{StackID: "stack", RepoURL: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("epic publisher was not called")
	}
}
