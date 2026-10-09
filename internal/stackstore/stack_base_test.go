package stackstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend/fleet"
)

// Readiness asks the stack store for a dependent's base: a task starts behind
// its blocker's code review only when that blocker is its base.
func TestStackBaseIsRegisteredForReadiness(t *testing.T) {
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	ctx := context.Background()
	s, err := Default()
	require.NoError(t, err)
	seedStack(t, s)
	_, err = s.AddNode(ctx, ws, "epic:E1", "T1", "", "")
	require.NoError(t, err)
	_, err = s.AddNode(ctx, ws, "epic:E1", "T2", "T1", "")
	require.NoError(t, err)

	lookup := fleet.RegisteredStackBase()
	require.NotNil(t, lookup, "the stack store registers the readiness lookup")
	for task, want := range map[string]string{"T2": "T1", "T1": "", "T9": ""} {
		got, err := lookup(ctx, ws, task)
		require.NoError(t, err)
		require.Equal(t, want, got, "base of %s", task)
	}
	got, err := lookup(ctx, "other", "T2")
	require.NoError(t, err)
	require.Empty(t, got, "another workspace's stacks do not count")
}
