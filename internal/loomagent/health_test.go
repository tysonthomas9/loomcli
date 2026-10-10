package loomagent

import (
	"context"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
)

// stuck is a harness whose health read blocks, ignoring its context, until
// release closes, as one waiting on a lock a long service start holds.
type stuck struct {
	*fake.Harness
	release chan struct{}
}

func (h stuck) Health(context.Context) (loomharness.Health, error) {
	<-h.release
	return loomharness.Health{OK: true}, nil
}

// TestHealthIsBounded: a health read still blocked at healthTimeout answers
// unavailable, so a harness read never hangs on it.
func TestHealthIsBounded(t *testing.T) {
	defer func(d time.Duration) { healthTimeout = d }(healthTimeout)
	healthTimeout = 10 * time.Millisecond
	h := stuck{fake.New(), make(chan struct{})}
	defer close(h.release)
	s := newService(t, ServiceConfig{Harnesses: map[string]loomharness.Harness{"opencode": h}})
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
