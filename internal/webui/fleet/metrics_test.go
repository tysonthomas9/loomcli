package fleet

import (
	"sync"
	"testing"
)

// TestNewClaimMetrics_ZeroInitialized verifies all counters start at zero.
func TestNewClaimMetrics_ZeroInitialized(t *testing.T) {
	m := NewClaimMetrics()
	snap := m.Snapshot()

	if snap.Success != 0 {
		t.Errorf("expected Success=0, got %d", snap.Success)
	}
	if snap.Collision != 0 {
		t.Errorf("expected Collision=0, got %d", snap.Collision)
	}
	if snap.Timeout != 0 {
		t.Errorf("expected Timeout=0, got %d", snap.Timeout)
	}
	if snap.Total != 0 {
		t.Errorf("expected Total=0, got %d", snap.Total)
	}
}

// TestClaimMetrics_RecordSuccess verifies that recording a success increments
// only the success counter.
func TestClaimMetrics_RecordSuccess(t *testing.T) {
	m := NewClaimMetrics()
	m.RecordClaim(ClaimResultSuccess)

	snap := m.Snapshot()
	if snap.Success != 1 {
		t.Errorf("expected Success=1, got %d", snap.Success)
	}
	if snap.Collision != 0 {
		t.Errorf("expected Collision=0, got %d", snap.Collision)
	}
	if snap.Timeout != 0 {
		t.Errorf("expected Timeout=0, got %d", snap.Timeout)
	}
	if snap.Total != 1 {
		t.Errorf("expected Total=1, got %d", snap.Total)
	}
}

// TestClaimMetrics_RecordCollision verifies that recording a collision
// increments only the collision counter.
func TestClaimMetrics_RecordCollision(t *testing.T) {
	m := NewClaimMetrics()
	m.RecordClaim(ClaimResultCollision)

	snap := m.Snapshot()
	if snap.Success != 0 {
		t.Errorf("expected Success=0, got %d", snap.Success)
	}
	if snap.Collision != 1 {
		t.Errorf("expected Collision=1, got %d", snap.Collision)
	}
	if snap.Timeout != 0 {
		t.Errorf("expected Timeout=0, got %d", snap.Timeout)
	}
	if snap.Total != 1 {
		t.Errorf("expected Total=1, got %d", snap.Total)
	}
}

// TestClaimMetrics_RecordTimeout verifies that recording a timeout increments
// only the timeout counter.
func TestClaimMetrics_RecordTimeout(t *testing.T) {
	m := NewClaimMetrics()
	m.RecordClaim(ClaimResultTimeout)

	snap := m.Snapshot()
	if snap.Success != 0 {
		t.Errorf("expected Success=0, got %d", snap.Success)
	}
	if snap.Collision != 0 {
		t.Errorf("expected Collision=0, got %d", snap.Collision)
	}
	if snap.Timeout != 1 {
		t.Errorf("expected Timeout=1, got %d", snap.Timeout)
	}
	if snap.Total != 1 {
		t.Errorf("expected Total=1, got %d", snap.Total)
	}
}

// TestClaimMetrics_MultipleRecords verifies that multiple calls increment the
// correct counters and that Total is the sum of all three.
func TestClaimMetrics_MultipleRecords(t *testing.T) {
	m := NewClaimMetrics()

	// Record 5 successes, 3 collisions, 2 timeouts.
	for i := 0; i < 5; i++ {
		m.RecordClaim(ClaimResultSuccess)
	}
	for i := 0; i < 3; i++ {
		m.RecordClaim(ClaimResultCollision)
	}
	for i := 0; i < 2; i++ {
		m.RecordClaim(ClaimResultTimeout)
	}

	snap := m.Snapshot()
	if snap.Success != 5 {
		t.Errorf("expected Success=5, got %d", snap.Success)
	}
	if snap.Collision != 3 {
		t.Errorf("expected Collision=3, got %d", snap.Collision)
	}
	if snap.Timeout != 2 {
		t.Errorf("expected Timeout=2, got %d", snap.Timeout)
	}
	if snap.Total != 10 {
		t.Errorf("expected Total=10, got %d", snap.Total)
	}
}

// TestClaimMetrics_ConcurrentAccess spawns 100 goroutines, each recording 100
// claims, and verifies the final count is correct. Designed to be run with
// go test -race.
func TestClaimMetrics_ConcurrentAccess(t *testing.T) {
	m := NewClaimMetrics()

	const goroutines = 100
	const recordsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 3) // 3 result types

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				m.RecordClaim(ClaimResultSuccess)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				m.RecordClaim(ClaimResultCollision)
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				m.RecordClaim(ClaimResultTimeout)
			}
		}()
	}

	wg.Wait()

	snap := m.Snapshot()

	expectedPerType := int64(goroutines * recordsPerGoroutine)
	if snap.Success != expectedPerType {
		t.Errorf("expected Success=%d, got %d", expectedPerType, snap.Success)
	}
	if snap.Collision != expectedPerType {
		t.Errorf("expected Collision=%d, got %d", expectedPerType, snap.Collision)
	}
	if snap.Timeout != expectedPerType {
		t.Errorf("expected Timeout=%d, got %d", expectedPerType, snap.Timeout)
	}
	expectedTotal := expectedPerType * 3
	if snap.Total != expectedTotal {
		t.Errorf("expected Total=%d, got %d", expectedTotal, snap.Total)
	}
}

// TestClaimMetrics_SnapshotIsPointInTime verifies that a snapshot captures
// values at the time it was taken and is not affected by subsequent recordings.
func TestClaimMetrics_SnapshotIsPointInTime(t *testing.T) {
	m := NewClaimMetrics()

	// Record some initial claims.
	m.RecordClaim(ClaimResultSuccess)
	m.RecordClaim(ClaimResultSuccess)
	m.RecordClaim(ClaimResultCollision)

	// Take a snapshot.
	snap := m.Snapshot()

	// Record more claims after the snapshot.
	m.RecordClaim(ClaimResultSuccess)
	m.RecordClaim(ClaimResultSuccess)
	m.RecordClaim(ClaimResultSuccess)
	m.RecordClaim(ClaimResultCollision)
	m.RecordClaim(ClaimResultTimeout)

	// The snapshot should reflect the state at the time it was taken.
	if snap.Success != 2 {
		t.Errorf("expected snapshot Success=2, got %d", snap.Success)
	}
	if snap.Collision != 1 {
		t.Errorf("expected snapshot Collision=1, got %d", snap.Collision)
	}
	if snap.Timeout != 0 {
		t.Errorf("expected snapshot Timeout=0, got %d", snap.Timeout)
	}
	if snap.Total != 3 {
		t.Errorf("expected snapshot Total=3, got %d", snap.Total)
	}

	// A new snapshot should reflect the updated state.
	snap2 := m.Snapshot()
	if snap2.Success != 5 {
		t.Errorf("expected new snapshot Success=5, got %d", snap2.Success)
	}
	if snap2.Collision != 2 {
		t.Errorf("expected new snapshot Collision=2, got %d", snap2.Collision)
	}
	if snap2.Timeout != 1 {
		t.Errorf("expected new snapshot Timeout=1, got %d", snap2.Timeout)
	}
	if snap2.Total != 8 {
		t.Errorf("expected new snapshot Total=8, got %d", snap2.Total)
	}
}
