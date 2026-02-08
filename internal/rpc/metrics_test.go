package rpc

import (
	"sync"
	"testing"
	"time"
)

func TestNewMetrics(t *testing.T) {
	t.Parallel()

	m := NewMetrics()

	if m == nil {
		t.Fatal("NewMetrics() returned nil")
	}

	if m.requestCounts == nil {
		t.Error("requestCounts map should be initialized")
	}

	if m.requestErrors == nil {
		t.Error("requestErrors map should be initialized")
	}

	if m.requestLatency == nil {
		t.Error("requestLatency map should be initialized")
	}

	if m.maxSamples != 1000 {
		t.Errorf("maxSamples = %d, want 1000", m.maxSamples)
	}

	if m.startTime.IsZero() {
		t.Error("startTime should be set")
	}

	// startTime should be recent
	if time.Since(m.startTime) > time.Second {
		t.Error("startTime should be recent")
	}
}

func TestMetrics_RecordRequest(t *testing.T) {
	t.Parallel()

	m := NewMetrics()

	m.RecordRequest("create", 10*time.Millisecond)
	m.RecordRequest("create", 20*time.Millisecond)
	m.RecordRequest("list", 5*time.Millisecond)

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.requestCounts["create"] != 2 {
		t.Errorf("create count = %d, want 2", m.requestCounts["create"])
	}

	if m.requestCounts["list"] != 1 {
		t.Errorf("list count = %d, want 1", m.requestCounts["list"])
	}

	if len(m.requestLatency["create"]) != 2 {
		t.Errorf("create latency samples = %d, want 2", len(m.requestLatency["create"]))
	}
}

func TestMetrics_RecordRequest_BoundedSamples(t *testing.T) {
	t.Parallel()

	m := NewMetrics()
	m.maxSamples = 10 // Lower for testing

	// Add more samples than max
	for i := 0; i < 15; i++ {
		m.RecordRequest("test", time.Duration(i)*time.Millisecond)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.requestLatency["test"]) != 10 {
		t.Errorf("latency samples = %d, want 10 (bounded)", len(m.requestLatency["test"]))
	}

	// Should have dropped the oldest samples (0-4), keeping 5-14
	// First sample should be 5ms
	if m.requestLatency["test"][0] != 5*time.Millisecond {
		t.Errorf("first sample = %v, want 5ms (oldest dropped)", m.requestLatency["test"][0])
	}
}

func TestMetrics_RecordError(t *testing.T) {
	t.Parallel()

	m := NewMetrics()

	m.RecordError("create")
	m.RecordError("create")
	m.RecordError("list")

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.requestErrors["create"] != 2 {
		t.Errorf("create errors = %d, want 2", m.requestErrors["create"])
	}

	if m.requestErrors["list"] != 1 {
		t.Errorf("list errors = %d, want 1", m.requestErrors["list"])
	}
}

func TestMetrics_RecordConnection(t *testing.T) {
	t.Parallel()

	m := NewMetrics()

	m.RecordConnection()
	m.RecordConnection()
	m.RecordConnection()

	if m.totalConns != 3 {
		t.Errorf("totalConns = %d, want 3", m.totalConns)
	}
}

func TestMetrics_RecordRejectedConnection(t *testing.T) {
	t.Parallel()

	m := NewMetrics()

	m.RecordRejectedConnection()
	m.RecordRejectedConnection()

	if m.rejectedConns != 2 {
		t.Errorf("rejectedConns = %d, want 2", m.rejectedConns)
	}
}

func TestMetrics_Snapshot(t *testing.T) {
	t.Parallel()

	m := NewMetrics()

	// Record some activity
	m.RecordRequest("create", 10*time.Millisecond)
	m.RecordRequest("create", 20*time.Millisecond)
	m.RecordRequest("list", 5*time.Millisecond)
	m.RecordError("create")
	m.RecordConnection()
	m.RecordConnection()
	m.RecordRejectedConnection()

	// Wait a bit for uptime
	time.Sleep(10 * time.Millisecond)

	snap := m.Snapshot(5) // 5 active connections

	// Check uptime (minimum 1 second due to rounding)
	if snap.UptimeSeconds < 1 {
		t.Errorf("UptimeSeconds = %f, want >= 1", snap.UptimeSeconds)
	}

	// Check connections
	if snap.TotalConns != 2 {
		t.Errorf("TotalConns = %d, want 2", snap.TotalConns)
	}

	if snap.ActiveConns != 5 {
		t.Errorf("ActiveConns = %d, want 5", snap.ActiveConns)
	}

	if snap.RejectedConns != 1 {
		t.Errorf("RejectedConns = %d, want 1", snap.RejectedConns)
	}

	// Check operations
	if len(snap.Operations) != 2 {
		t.Errorf("Operations length = %d, want 2", len(snap.Operations))
	}

	// Operations should be sorted by total count (create first)
	if len(snap.Operations) > 0 && snap.Operations[0].Operation != "create" {
		t.Errorf("First operation = %q, want %q (sorted by count)", snap.Operations[0].Operation, "create")
	}

	// Check operation metrics
	for _, op := range snap.Operations {
		if op.Operation == "create" {
			if op.TotalCount != 2 {
				t.Errorf("create TotalCount = %d, want 2", op.TotalCount)
			}
			if op.ErrorCount != 1 {
				t.Errorf("create ErrorCount = %d, want 1", op.ErrorCount)
			}
			if op.SuccessCount != 1 {
				t.Errorf("create SuccessCount = %d, want 1", op.SuccessCount)
			}
		}
	}

	// Check memory stats are populated
	if snap.GoroutineCount == 0 {
		t.Error("GoroutineCount should be > 0")
	}
}

func TestMetrics_Snapshot_Percentiles(t *testing.T) {
	t.Parallel()

	t.Run("empty samples", func(t *testing.T) {
		m := NewMetrics()
		// Record request count but no latency samples yet
		m.mu.Lock()
		m.requestCounts["test"] = 1
		m.mu.Unlock()

		snap := m.Snapshot(0)
		for _, op := range snap.Operations {
			if op.Operation == "test" {
				if op.Latency.P50MS != 0 {
					t.Errorf("P50MS should be 0 with no samples")
				}
			}
		}
	})

	t.Run("single sample", func(t *testing.T) {
		m := NewMetrics()
		m.RecordRequest("test", 100*time.Millisecond)

		snap := m.Snapshot(0)
		for _, op := range snap.Operations {
			if op.Operation == "test" {
				// All percentiles should equal the single sample
				if op.Latency.MinMS != 100 {
					t.Errorf("MinMS = %f, want 100", op.Latency.MinMS)
				}
				if op.Latency.MaxMS != 100 {
					t.Errorf("MaxMS = %f, want 100", op.Latency.MaxMS)
				}
				if op.Latency.P50MS != 100 {
					t.Errorf("P50MS = %f, want 100", op.Latency.P50MS)
				}
			}
		}
	})

	t.Run("multiple samples", func(t *testing.T) {
		m := NewMetrics()

		// Add 100 samples: 1ms, 2ms, ..., 100ms
		for i := 1; i <= 100; i++ {
			m.RecordRequest("test", time.Duration(i)*time.Millisecond)
		}

		snap := m.Snapshot(0)
		for _, op := range snap.Operations {
			if op.Operation == "test" {
				if op.Latency.MinMS != 1 {
					t.Errorf("MinMS = %f, want 1", op.Latency.MinMS)
				}
				if op.Latency.MaxMS != 100 {
					t.Errorf("MaxMS = %f, want 100", op.Latency.MaxMS)
				}
				// P50 should be around 50ms
				if op.Latency.P50MS < 45 || op.Latency.P50MS > 55 {
					t.Errorf("P50MS = %f, want ~50", op.Latency.P50MS)
				}
				// P95 should be around 95ms
				if op.Latency.P95MS < 90 || op.Latency.P95MS > 100 {
					t.Errorf("P95MS = %f, want ~95", op.Latency.P95MS)
				}
				// P99 should be around 99ms
				if op.Latency.P99MS < 95 || op.Latency.P99MS > 100 {
					t.Errorf("P99MS = %f, want ~99", op.Latency.P99MS)
				}
				// Avg should be around 50.5ms
				if op.Latency.AvgMS < 45 || op.Latency.AvgMS > 55 {
					t.Errorf("AvgMS = %f, want ~50.5", op.Latency.AvgMS)
				}
			}
		}
	})
}

func TestMetrics_Concurrency(t *testing.T) {
	t.Parallel()

	m := NewMetrics()
	var wg sync.WaitGroup

	// Spawn multiple goroutines to record concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.RecordRequest("concurrent", time.Duration(j)*time.Microsecond)
				if j%10 == 0 {
					m.RecordError("concurrent")
				}
				m.RecordConnection()
			}
		}(i)
	}

	// Also take snapshots concurrently
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = m.Snapshot(0)
			}
		}()
	}

	wg.Wait()

	// Verify final counts
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.requestCounts["concurrent"] != 1000 { // 10 goroutines * 100 requests
		t.Errorf("concurrent count = %d, want 1000", m.requestCounts["concurrent"])
	}

	if m.totalConns != 1000 {
		t.Errorf("totalConns = %d, want 1000", m.totalConns)
	}
}

func TestMetrics_SuccessCountNeverNegative(t *testing.T) {
	t.Parallel()

	m := NewMetrics()

	// Record more errors than total requests (edge case)
	m.RecordError("test")
	m.RecordError("test")
	m.RecordError("test")

	snap := m.Snapshot(0)

	for _, op := range snap.Operations {
		if op.Operation == "test" {
			if op.SuccessCount < 0 {
				t.Errorf("SuccessCount = %d, should never be negative", op.SuccessCount)
			}
		}
	}
}

func TestMinInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b, want int
	}{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{0, 0, 0},
		{-1, 0, -1},
	}

	for _, tt := range tests {
		got := minInt(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("minInt(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestCalculateLatencyStats(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		stats := calculateLatencyStats(nil)
		if stats.P50MS != 0 || stats.AvgMS != 0 {
			t.Error("Empty samples should return zero stats")
		}
	})

	t.Run("single", func(t *testing.T) {
		samples := []time.Duration{50 * time.Millisecond}
		stats := calculateLatencyStats(samples)

		if stats.MinMS != 50 || stats.MaxMS != 50 || stats.P50MS != 50 {
			t.Error("Single sample stats incorrect")
		}
	})

	t.Run("sorted output", func(t *testing.T) {
		// Unsorted input
		samples := []time.Duration{
			30 * time.Millisecond,
			10 * time.Millisecond,
			50 * time.Millisecond,
			20 * time.Millisecond,
		}
		stats := calculateLatencyStats(samples)

		if stats.MinMS != 10 {
			t.Errorf("MinMS = %f, want 10", stats.MinMS)
		}
		if stats.MaxMS != 50 {
			t.Errorf("MaxMS = %f, want 50", stats.MaxMS)
		}
	})
}
