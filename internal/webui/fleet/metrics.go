package fleet

import (
	"sync"
	"time"
)

// Claim result constants for use with RecordClaim.
const (
	ClaimResultSuccess   = "success"
	ClaimResultCollision = "collision"
	ClaimResultTimeout   = "timeout"
)

// ClaimMetrics tracks fleet claim operation outcomes.
// Thread-safe for concurrent use by multiple HTTP handlers.
type ClaimMetrics struct {
	mu          sync.RWMutex
	claimCounts map[string]int64
	startTime   time.Time
}

// ClaimMetricsSnapshot is a point-in-time copy of claim metrics.
type ClaimMetricsSnapshot struct {
	Success   int64 `json:"claims_success"`
	Collision int64 `json:"claims_collision"`
	Timeout   int64 `json:"claims_timeout"`
	Total     int64 `json:"claims_total"`
}

// NewClaimMetrics initializes a new ClaimMetrics instance.
func NewClaimMetrics() *ClaimMetrics {
	return &ClaimMetrics{
		claimCounts: make(map[string]int64),
		startTime:   time.Now(),
	}
}

// RecordClaim increments the counter for the given result type.
func (m *ClaimMetrics) RecordClaim(result string) {
	m.mu.Lock()
	m.claimCounts[result]++
	m.mu.Unlock()
}

// Snapshot returns a point-in-time copy of all claim counters.
func (m *ClaimMetrics) Snapshot() ClaimMetricsSnapshot {
	m.mu.RLock()
	s := ClaimMetricsSnapshot{
		Success:   m.claimCounts[ClaimResultSuccess],
		Collision: m.claimCounts[ClaimResultCollision],
		Timeout:   m.claimCounts[ClaimResultTimeout],
	}
	m.mu.RUnlock()
	s.Total = s.Success + s.Collision + s.Timeout
	return s
}
