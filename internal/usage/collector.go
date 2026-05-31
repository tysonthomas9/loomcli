package usage

import (
	"sync"
	"time"
)

type countedUsage struct {
	input      int64
	output     int64
	cacheRead  int64
	cacheWrite int64
}

// Collector accumulates token usage during a single agent session.
// It deduplicates Claude's per-content-block usage reporting by tracking the
// latest usage snapshot per message ID. Safe for concurrent use.
type Collector struct {
	mu sync.Mutex

	backend   string
	agentName string

	inputTokens      int64
	outputTokens     int64
	cacheReadTokens  int64
	cacheWriteTokens int64
	costUSD          float64
	model            string

	seen map[string]countedUsage // messageID → latest counted usage
}

// NewCollector creates a new usage collector for the given backend and agent.
func NewCollector(backend, agentName string) *Collector {
	return &Collector{
		backend:   backend,
		agentName: agentName,
		seen:      make(map[string]countedUsage),
	}
}

// Accumulate adds token counts from a single event. If messageID is non-empty
// and has been seen before, the previous counts are replaced by the latest
// snapshot (Claude message_delta usage is cumulative and more complete than
// message_start).
func (c *Collector) Accumulate(messageID string, input, output, cacheRead, cacheWrite int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	next := countedUsage{input: input, output: output, cacheRead: cacheRead, cacheWrite: cacheWrite}
	if messageID != "" {
		if prev, ok := c.seen[messageID]; ok {
			c.inputTokens -= prev.input
			c.outputTokens -= prev.output
			c.cacheReadTokens -= prev.cacheRead
			c.cacheWriteTokens -= prev.cacheWrite
		}
		c.seen[messageID] = next
	}

	c.inputTokens += next.input
	c.outputTokens += next.output
	c.cacheReadTokens += next.cacheRead
	c.cacheWriteTokens += next.cacheWrite
}

// SetCostUSD records a backend-reported cost for the session. Values are not
// estimated locally; the latest positive value wins.
func (c *Collector) SetCostUSD(costUSD float64) {
	if costUSD <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.costUSD = costUSD
}

// SetModel records the backend-reported model for the session.
func (c *Collector) SetModel(model string) {
	if model == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.model = model
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
		EstimatedCostUSD: c.costUSD,
		StartedAt:        startedAt,
		EndedAt:          endedAt,
		ExitCode:         exitCode,
		Model:            c.model,
	}
	return u
}
