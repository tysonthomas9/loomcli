package service

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

func TestPoolStatsFromDaemon(t *testing.T) {
	input := daemon.PoolStats{
		Size:      10,
		Created:   7,
		Active:    3,
		Available: 4,
		Closed:    true,
	}

	got := poolStatsFromDaemon(input)

	if got.Size != input.Size {
		t.Errorf("Size = %d, want %d", got.Size, input.Size)
	}
	if got.Created != input.Created {
		t.Errorf("Created = %d, want %d", got.Created, input.Created)
	}
	if got.Active != input.Active {
		t.Errorf("Active = %d, want %d", got.Active, input.Active)
	}
	if got.Available != input.Available {
		t.Errorf("Available = %d, want %d", got.Available, input.Available)
	}
	if got.Closed != input.Closed {
		t.Errorf("Closed = %v, want %v", got.Closed, input.Closed)
	}
}

func TestPoolStatsFromDaemon_Zero(t *testing.T) {
	got := poolStatsFromDaemon(daemon.PoolStats{})

	if got.Size != 0 {
		t.Errorf("Size = %d, want 0", got.Size)
	}
	if got.Created != 0 {
		t.Errorf("Created = %d, want 0", got.Created)
	}
	if got.Active != 0 {
		t.Errorf("Active = %d, want 0", got.Active)
	}
	if got.Available != 0 {
		t.Errorf("Available = %d, want 0", got.Available)
	}
	if got.Closed != false {
		t.Errorf("Closed = %v, want false", got.Closed)
	}
}
