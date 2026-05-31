package usage

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCollector_BasicAccumulation(t *testing.T) {
	c := NewCollector("claude", "falcon")
	c.Accumulate("msg-1", 100, 50, 10, 5)
	c.Accumulate("msg-2", 200, 100, 20, 10)

	now := time.Now()
	u := c.Finalize("task-1", "epic-1", now, now.Add(time.Minute), 0)

	if u.InputTokens != 300 {
		t.Errorf("InputTokens = %d, want 300", u.InputTokens)
	}
	if u.OutputTokens != 150 {
		t.Errorf("OutputTokens = %d, want 150", u.OutputTokens)
	}
	if u.CacheReadTokens != 30 {
		t.Errorf("CacheReadTokens = %d, want 30", u.CacheReadTokens)
	}
	if u.CacheWriteTokens != 15 {
		t.Errorf("CacheWriteTokens = %d, want 15", u.CacheWriteTokens)
	}
	if u.AgentName != "falcon" {
		t.Errorf("AgentName = %q, want %q", u.AgentName, "falcon")
	}
	if u.Backend != "claude" {
		t.Errorf("Backend = %q, want %q", u.Backend, "claude")
	}
	if u.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", u.TaskID, "task-1")
	}
	if u.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", u.ExitCode)
	}
}

func TestCollector_ReplacesDuplicateMessageSnapshot(t *testing.T) {
	c := NewCollector("claude", "test-agent")

	// Same messageID should keep the latest cumulative snapshot.
	c.Accumulate("msg-1", 100, 50, 10, 5)
	c.Accumulate("msg-1", 200, 80, 20, 10)

	now := time.Now()
	u := c.Finalize("", "", now, now, 0)

	if u.InputTokens != 200 {
		t.Errorf("InputTokens = %d, want 200 (latest duplicate snapshot wins)", u.InputTokens)
	}
	if u.OutputTokens != 80 {
		t.Errorf("OutputTokens = %d, want 80", u.OutputTokens)
	}
}

func TestCollector_EmptyMessageID_NoDedupe(t *testing.T) {
	c := NewCollector("codex", "test-agent")

	// Empty messageID should always be counted (no dedup)
	c.Accumulate("", 100, 50, 0, 0)
	c.Accumulate("", 100, 50, 0, 0)
	c.Accumulate("", 100, 50, 0, 0)

	now := time.Now()
	u := c.Finalize("", "", now, now, 0)

	if u.InputTokens != 300 {
		t.Errorf("InputTokens = %d, want 300 (empty messageID should not dedup)", u.InputTokens)
	}
}

func TestCollector_ThreadSafety(t *testing.T) {
	c := NewCollector("claude", "concurrent-agent")
	var wg sync.WaitGroup
	n := 100

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Use unique message IDs to exercise concurrent seen-map access
			msgID := fmt.Sprintf("msg-%d", idx)
			c.Accumulate(msgID, 1, 1, 0, 0)
		}(i)
	}
	wg.Wait()

	now := time.Now()
	u := c.Finalize("", "", now, now, 0)

	// All 100 unique IDs should be counted exactly once
	if u.InputTokens != int64(n) {
		t.Errorf("InputTokens = %d, want %d", u.InputTokens, n)
	}
}

func TestCollector_ThreadSafety_DuplicateIDs(t *testing.T) {
	c := NewCollector("claude", "concurrent-agent")
	var wg sync.WaitGroup
	n := 100

	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// All goroutines use the same messageID to exercise dedup under contention
			c.Accumulate("shared-msg", 1, 1, 0, 0)
		}(i)
	}
	wg.Wait()

	now := time.Now()
	u := c.Finalize("", "", now, now, 0)

	// Only one should be counted due to deduplication
	if u.InputTokens != 1 {
		t.Errorf("InputTokens = %d, want 1 (dedup under contention)", u.InputTokens)
	}
}

func TestCollector_Finalize_DoesNotEstimateCost(t *testing.T) {
	c := NewCollector("claude", "agent")
	c.Accumulate("msg-1", 1_000_000, 1_000_000, 0, 0)

	now := time.Now()
	u := c.Finalize("task", "epic", now, now.Add(time.Hour), 1)

	if u.EstimatedCostUSD != 0 {
		t.Errorf("EstimatedCostUSD = %f, want 0 when backend did not report cost", u.EstimatedCostUSD)
	}
	if u.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", u.ExitCode)
	}
}

func TestCollector_Finalize_CapturedCostAndModel(t *testing.T) {
	c := NewCollector("claude", "agent")
	c.Accumulate("msg-1", 100, 50, 0, 0)
	c.SetCostUSD(0.0123)
	c.SetModel("claude-opus-4-8")

	now := time.Now()
	u := c.Finalize("task", "epic", now, now.Add(time.Hour), 0)

	if u.EstimatedCostUSD != 0.0123 {
		t.Errorf("EstimatedCostUSD = %f, want 0.0123", u.EstimatedCostUSD)
	}
	if u.Model != "claude-opus-4-8" {
		t.Errorf("Model = %q, want claude-opus-4-8", u.Model)
	}
}

func TestCollector_Finalize_Metadata(t *testing.T) {
	c := NewCollector("codex", "spark")
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 1, 10, 5, 0, 0, time.UTC)

	u := c.Finalize("task-42", "epic-7", start, end, 137)

	if u.Backend != "codex" {
		t.Errorf("Backend = %q, want %q", u.Backend, "codex")
	}
	if u.AgentName != "spark" {
		t.Errorf("AgentName = %q, want %q", u.AgentName, "spark")
	}
	if u.TaskID != "task-42" {
		t.Errorf("TaskID = %q, want %q", u.TaskID, "task-42")
	}
	if u.EpicID != "epic-7" {
		t.Errorf("EpicID = %q, want %q", u.EpicID, "epic-7")
	}
	if !u.StartedAt.Equal(start) {
		t.Errorf("StartedAt = %v, want %v", u.StartedAt, start)
	}
	if !u.EndedAt.Equal(end) {
		t.Errorf("EndedAt = %v, want %v", u.EndedAt, end)
	}
	if u.ExitCode != 137 {
		t.Errorf("ExitCode = %d, want 137", u.ExitCode)
	}
}

func TestCollector_Totals(t *testing.T) {
	c := NewCollector("claude", "agent")
	c.Accumulate("msg-1", 100, 50, 10, 5)
	c.Accumulate("msg-2", 200, 100, 20, 10)

	inTok, outTok, cacheRead, cacheWrite := c.Totals()
	if inTok != 300 {
		t.Errorf("inputTokens = %d, want 300", inTok)
	}
	if outTok != 150 {
		t.Errorf("outputTokens = %d, want 150", outTok)
	}
	if cacheRead != 30 {
		t.Errorf("cacheReadTokens = %d, want 30", cacheRead)
	}
	if cacheWrite != 15 {
		t.Errorf("cacheWriteTokens = %d, want 15", cacheWrite)
	}
}

func TestCollector_TotalsEmpty(t *testing.T) {
	c := NewCollector("claude", "agent")
	inTok, outTok, cacheRead, cacheWrite := c.Totals()
	if inTok != 0 || outTok != 0 || cacheRead != 0 || cacheWrite != 0 {
		t.Errorf("expected all zeros, got (%d, %d, %d, %d)", inTok, outTok, cacheRead, cacheWrite)
	}
}

func TestCollector_TotalsAfterFinalize(t *testing.T) {
	c := NewCollector("claude", "agent")
	c.Accumulate("msg-1", 100, 50, 10, 5)

	now := time.Now()
	_ = c.Finalize("task", "epic", now, now.Add(time.Minute), 0)

	// Totals should still return correct values after Finalize
	inTok, outTok, cacheRead, cacheWrite := c.Totals()
	if inTok != 100 {
		t.Errorf("inputTokens after Finalize = %d, want 100", inTok)
	}
	if outTok != 50 {
		t.Errorf("outputTokens after Finalize = %d, want 50", outTok)
	}
	if cacheRead != 10 {
		t.Errorf("cacheReadTokens after Finalize = %d, want 10", cacheRead)
	}
	if cacheWrite != 5 {
		t.Errorf("cacheWriteTokens after Finalize = %d, want 5", cacheWrite)
	}
}
