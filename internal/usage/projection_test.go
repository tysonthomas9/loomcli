package usage

import (
	"testing"
	"time"
)

func TestProjectionOwnsUsageMutations(t *testing.T) {
	projection, err := NewProjection(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := SessionUsage{
		SessionID: "session-1", AgentName: "planner", Backend: "codex",
		StartedAt: time.Now().Add(-48 * time.Hour), EndedAt: time.Now().Add(-47 * time.Hour),
	}
	if err := projection.Append(record); err != nil {
		t.Fatal(err)
	}
	if rows, err := projection.Read(Filter{}); err != nil || len(rows) != 1 {
		t.Fatalf("rows = %#v, %v", rows, err)
	}
	if purged, err := projection.PurgeOlderThan(24 * time.Hour); err != nil || purged != 1 {
		t.Fatalf("purged = %d, %v", purged, err)
	}
}
