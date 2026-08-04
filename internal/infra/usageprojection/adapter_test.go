package usageprojection

import (
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestAdapterOwnsUsageProjectionMutations(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := usage.SessionUsage{
		SessionID: "session-1", AgentName: "planner", Backend: "codex",
		StartedAt: time.Now().Add(-48 * time.Hour), EndedAt: time.Now().Add(-47 * time.Hour),
	}
	if err := Append(store, record); err != nil {
		t.Fatal(err)
	}
	if rows, err := store.Read(usage.Filter{}); err != nil || len(rows) != 1 {
		t.Fatalf("rows = %#v, %v", rows, err)
	}
	if purged, err := PurgeOlderThan(store, 24*time.Hour); err != nil || purged != 1 {
		t.Fatalf("purged = %d, %v", purged, err)
	}
}
