package loomagent

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
)

// stuck is a harness whose version check never returns on its own.
type stuck struct{ *fake.Harness }

func (stuck) Health(ctx context.Context) (loomharness.Health, error) {
	<-ctx.Done()
	return loomharness.Health{Warning: ctx.Err().Error()}, nil
}

// TestHealthIsBounded: a hung version check ends at healthTimeout, so a
// harness read never hangs on it.
func TestHealthIsBounded(t *testing.T) {
	defer func(d time.Duration) { healthTimeout = d }(healthTimeout)
	healthTimeout = 10 * time.Millisecond
	s := newService(t, ServiceConfig{Harnesses: map[string]loomharness.Harness{"opencode": stuck{fake.New()}}})
	done := make(chan loomharness.Health, 1)
	go func() { h, _ := s.Health(context.Background(), "opencode"); done <- h }()
	select {
	case h := <-done:
		if h.OK || h.Warning == "" {
			t.Fatalf("health = %+v", h)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Health hung on the version check")
	}
}
