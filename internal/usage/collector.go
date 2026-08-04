package usage

import (
	"sync"
	"time"
)

// Collector accumulates token usage during a single agent session.
// It deduplicates Claude's per-content-block usage reporting by tracking
// seen message IDs. Safe for concurrent use.
type Collector struct {
	mu sync.Mutex

	backend   string
	agentName string

	inputTokens      int64
	outputTokens     int64
	cacheReadTokens  int64
	cacheWriteTokens int64

	seen map[string]bool // messageID → already counted
}

// NewCollector creates a new usage collector for the given backend and agent.
func NewCollector(backend, agentName string) *Collector {
	return &Collector{
		backend:   backend,
		agentName: agentName,
		seen:      make(map[string]bool),
	}
}

// Accumulate adds token counts from a single event. If messageID is non-empty
// and has been seen before, the call is a no-op (deduplication for Claude's
// multi-content-block messages).
func (c *Collector) Accumulate(messageID string, input, output, cacheRead, cacheWrite int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if messageID != "" {
		if c.seen[messageID] {
			return
		}
		c.seen[messageID] = true
	}

	c.inputTokens += input
	c.outputTokens += output
	c.cacheReadTokens += cacheRead
	c.cacheWriteTokens += cacheWrite
}

// Totals returns the raw accumulated token counts without constructing a full
// SessionUsage record. Safe to call before or after Finalize.
func (c *Collector) Totals() (inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inputTokens, c.outputTokens, c.cacheReadTokens, c.cacheWriteTokens
}

// Finalize produces a SessionUsage record from the accumulated totals.
func (c *Collector) Finalize(taskID, epicID string, startedAt, endedAt time.Time, exitCode int) SessionUsage {
	c.mu.Lock()
	defer c.mu.Unlock()

	u := SessionUsage{
		AgentName:        c.agentName,
		Backend:          c.backend,
		TaskID:           taskID,
		EpicID:           epicID,
		InputTokens:      c.inputTokens,
		OutputTokens:     c.outputTokens,
		CacheReadTokens:  c.cacheReadTokens,
		CacheWriteTokens: c.cacheWriteTokens,
		StartedAt:        startedAt,
		EndedAt:          endedAt,
		ExitCode:         exitCode,
	}

	tier := ResolvePricing(c.backend)
	u.EstimatedCostUSD = EstimateCost(tier, u)
	return u
}
