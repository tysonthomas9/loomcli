package leadcontrol

import (
	"testing"
	"time"
)

func TestNewestCodexThreadWaitsForThreadCreatedAfterRuntimeStart(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 6, 5, 36, 0, time.UTC)
	threads := []CodexThread{
		{
			ID:          "old-lead-thread",
			Cwd:         "/repo",
			CreatedAtMS: float64(startedAt.Add(-3 * time.Minute).UnixMilli()),
			UpdatedAtMS: float64(startedAt.Add(-1 * time.Second).UnixMilli()),
		},
		{
			ID:          "new-lead-thread",
			Cwd:         "/repo",
			CreatedAtMS: float64(startedAt.Add(500 * time.Millisecond).UnixMilli()),
			UpdatedAtMS: float64(startedAt.Add(2 * time.Second).UnixMilli()),
		},
	}

	got := newestCodexThread(threads, "/repo", startedAt)
	if got == nil || got.ID != "new-lead-thread" {
		t.Fatalf("newestCodexThread() = %+v, want new-lead-thread", got)
	}
}

func TestNewestCodexThreadReturnsNilUntilFreshLeadThreadExists(t *testing.T) {
	startedAt := time.Date(2026, 5, 17, 6, 5, 36, 0, time.UTC)
	threads := []CodexThread{{
		ID:          "old-lead-thread",
		Cwd:         "/repo",
		CreatedAtMS: float64(startedAt.Add(-3 * time.Minute).UnixMilli()),
		UpdatedAtMS: float64(startedAt.Add(5 * time.Second).UnixMilli()),
	}}

	got := newestCodexThread(threads, "/repo", startedAt)
	if got != nil {
		t.Fatalf("newestCodexThread() = %+v, want nil before fresh lead thread exists", got)
	}
}
