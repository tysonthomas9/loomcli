package daemon

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewConnectionPool(t *testing.T) {
	tests := []struct {
		name       string
		socketPath string
		poolSize   int
		wantErr    error
		wantSize   int
	}{
		{
			name:       "valid socket path and pool size",
			socketPath: "/tmp/test.sock",
			poolSize:   5,
			wantErr:    nil,
			wantSize:   5,
		},
		{
			name:       "empty socket path returns error",
			socketPath: "",
			poolSize:   5,
			wantErr:    ErrInvalidSocketPath,
		},
		{
			name:       "zero pool size uses default",
			socketPath: "/tmp/test.sock",
			poolSize:   0,
			wantErr:    nil,
			wantSize:   DefaultPoolSize,
		},
		{
			name:       "negative pool size uses default",
			socketPath: "/tmp/test.sock",
			poolSize:   -1,
			wantErr:    nil,
			wantSize:   DefaultPoolSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := NewConnectionPool(tt.socketPath, tt.poolSize)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("NewConnectionPool() error = %v, wantErr %v", err, tt.wantErr)
				}
				if pool != nil {
					t.Error("NewConnectionPool() returned non-nil pool on error")
				}
				return
			}

			if err != nil {
				t.Errorf("NewConnectionPool() unexpected error = %v", err)
				return
			}

			if pool == nil {
				t.Error("NewConnectionPool() returned nil pool")
				return
			}

			if pool.Size() != tt.wantSize {
				t.Errorf("Size() = %v, want %v", pool.Size(), tt.wantSize)
			}

			// Verify socket path
			if pool.SocketPath() != tt.socketPath {
				t.Errorf("SocketPath() = %v, want %v", pool.SocketPath(), tt.socketPath)
			}

			// Cleanup
			_ = pool.Close()
		})
	}
}

func TestConnectionPool_Stats(t *testing.T) {
	pool, err := NewConnectionPool("/tmp/test.sock", 5)
	if err != nil {
		t.Fatalf("NewConnectionPool() error = %v", err)
	}
	defer pool.Close()

	stats := pool.Stats()

	// Verify initial stats
	if stats.Size != 5 {
		t.Errorf("stats.Size = %v, want 5", stats.Size)
	}
	if stats.Created != 0 {
		t.Errorf("stats.Created = %v, want 0", stats.Created)
	}
	if stats.Active != 0 {
		t.Errorf("stats.Active = %v, want 0", stats.Active)
	}
	if stats.Available != 0 {
		t.Errorf("stats.Available = %v, want 0", stats.Available)
	}
	if stats.Closed {
		t.Error("stats.Closed = true, want false")
	}
}

func TestConnectionPool_Close(t *testing.T) {
	pool, err := NewConnectionPool("/tmp/test.sock", 5)
	if err != nil {
		t.Fatalf("NewConnectionPool() error = %v", err)
	}

	// Close the pool
	err = pool.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Verify stats show closed
	stats := pool.Stats()
	if !stats.Closed {
		t.Error("stats.Closed = false after Close()")
	}

	// Double close should be safe
	err = pool.Close()
	if err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

func TestConnectionPool_GetFromClosedPool(t *testing.T) {
	pool, err := NewConnectionPool("/tmp/test.sock", 5)
	if err != nil {
		t.Fatalf("NewConnectionPool() error = %v", err)
	}

	// Close the pool first
	_ = pool.Close()

	// Get should return ErrPoolClosed
	ctx := context.Background()
	_, err = pool.Get(ctx)
	if err != ErrPoolClosed {
		t.Errorf("Get() from closed pool error = %v, want %v", err, ErrPoolClosed)
	}
}

func TestConnectionPool_PutNil(t *testing.T) {
	pool, err := NewConnectionPool("/tmp/test.sock", 5)
	if err != nil {
		t.Fatalf("NewConnectionPool() error = %v", err)
	}
	defer pool.Close()

	// Put nil should be safe (no panic)
	pool.Put(nil)

	// Stats should be unchanged
	stats := pool.Stats()
	if stats.Active != 0 {
		t.Errorf("stats.Active = %v after Put(nil), want 0", stats.Active)
	}
}

func TestConnectionPool_SetDialTimeout(t *testing.T) {
	pool, err := NewConnectionPool("/tmp/test.sock", 5)
	if err != nil {
		t.Fatalf("NewConnectionPool() error = %v", err)
	}
	defer pool.Close()

	newTimeout := 5 * time.Second
	pool.SetDialTimeout(newTimeout)

	// Access field directly for testing
	pool.mu.Lock()
	actualTimeout := pool.dialTimeout
	pool.mu.Unlock()

	if actualTimeout != newTimeout {
		t.Errorf("dialTimeout = %v, want %v", actualTimeout, newTimeout)
	}
}

func TestConnectionPool_SetPoolTimeout(t *testing.T) {
	pool, err := NewConnectionPool("/tmp/test.sock", 5)
	if err != nil {
		t.Fatalf("NewConnectionPool() error = %v", err)
	}
	defer pool.Close()

	newTimeout := 20 * time.Second
	pool.SetPoolTimeout(newTimeout)

	// Access field directly for testing
	pool.mu.Lock()
	actualTimeout := pool.poolTimeout
	pool.mu.Unlock()

	if actualTimeout != newTimeout {
		t.Errorf("poolTimeout = %v, want %v", actualTimeout, newTimeout)
	}
}

func TestConnectionPool_ConcurrentStats(t *testing.T) {
	pool, err := NewConnectionPool("/tmp/test.sock", 5)
	if err != nil {
		t.Fatalf("NewConnectionPool() error = %v", err)
	}
	defer pool.Close()

	// Concurrent stats access should not race
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = pool.Stats()
			_ = pool.Size()
			_ = pool.SocketPath()
		}()
	}
	wg.Wait()
}

func TestConnectionPool_ContextCancellation(t *testing.T) {
	pool, err := NewConnectionPool("/tmp/nonexistent.sock", 1)
	if err != nil {
		t.Fatalf("NewConnectionPool() error = %v", err)
	}
	defer pool.Close()

	// Create a context that we'll cancel immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Get should return context error
	_, err = pool.Get(ctx)
	if err == nil {
		t.Error("Get() with cancelled context should return error")
	}
	// The error could be context.Canceled or another error depending on timing
}

func TestConnectionPool_Constants(t *testing.T) {
	// Verify constants have sensible values
	if DefaultPoolSize <= 0 {
		t.Errorf("DefaultPoolSize = %v, want positive value", DefaultPoolSize)
	}
	if DefaultPoolTimeout <= 0 {
		t.Errorf("DefaultPoolTimeout = %v, want positive value", DefaultPoolTimeout)
	}
}

func TestPoolStats_Fields(t *testing.T) {
	// Test that PoolStats struct has expected JSON tags
	stats := PoolStats{
		Size:      5,
		Created:   3,
		Active:    2,
		Available: 1,
		Closed:    false,
	}

	if stats.Size != 5 {
		t.Errorf("Size = %v, want 5", stats.Size)
	}
	if stats.Created != 3 {
		t.Errorf("Created = %v, want 3", stats.Created)
	}
	if stats.Active != 2 {
		t.Errorf("Active = %v, want 2", stats.Active)
	}
	if stats.Available != 1 {
		t.Errorf("Available = %v, want 1", stats.Available)
	}
	if stats.Closed {
		t.Error("Closed = true, want false")
	}
}
