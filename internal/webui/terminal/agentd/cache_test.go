package agentd

import (
	"sync"
	"testing"
	"time"
)

func TestRoutingCache_PutGetRoundTrip(t *testing.T) {
	c := newRoutingCache(time.Minute)
	c.Put("ws", "agent", "vm-1.local", 9100, "cert-pem", "key-pem")
	got, ok := c.Get("ws", "agent")
	if !ok {
		t.Fatalf("Get after Put = !ok, want ok")
	}
	if got.vmHost != "vm-1.local" || got.agentdPort != 9100 {
		t.Errorf("Get returned vmHost=%q port=%d, want vm-1.local/9100", got.vmHost, got.agentdPort)
	}
	if got.certPEM != "cert-pem" || got.keyPEM != "key-pem" {
		t.Errorf("Get returned cert/key %q/%q, want cert-pem/key-pem", got.certPEM, got.keyPEM)
	}
}

func TestRoutingCache_MissOnUnknownKey(t *testing.T) {
	c := newRoutingCache(time.Minute)
	c.Put("ws", "agent", "vm", 1, "c", "k")
	if _, ok := c.Get("other-ws", "agent"); ok {
		t.Errorf("Get(other-ws, agent) = ok, want miss")
	}
	if _, ok := c.Get("ws", "other-agent"); ok {
		t.Errorf("Get(ws, other-agent) = ok, want miss")
	}
}

func TestRoutingCache_ExpiresLazilyOnRead(t *testing.T) {
	c := newRoutingCache(time.Minute)
	// Drive the clock manually so the test is deterministic without sleeps.
	current := time.Now()
	c.now = func() time.Time { return current }

	c.Put("ws", "a", "vm", 1, "c", "k")
	if _, ok := c.Get("ws", "a"); !ok {
		t.Fatalf("Get immediately after Put = !ok, want ok")
	}
	// Advance past expiry.
	current = current.Add(2 * time.Minute)
	if _, ok := c.Get("ws", "a"); ok {
		t.Errorf("Get after expiry = ok, want miss")
	}
	// Internal map should be empty (lazy eviction during the failing Get).
	if len(c.entries) != 0 {
		t.Errorf("entries after expired Get = %d, want 0", len(c.entries))
	}
}

func TestRoutingCache_Invalidate(t *testing.T) {
	c := newRoutingCache(time.Minute)
	c.Put("ws", "a", "vm", 1, "c", "k")
	c.Invalidate("ws", "a")
	if _, ok := c.Get("ws", "a"); ok {
		t.Errorf("Get after Invalidate = ok, want miss")
	}
	// Invalidate of a missing key is a no-op.
	c.Invalidate("ws", "never-existed")
}

func TestRoutingCache_OverwriteResetsExpiry(t *testing.T) {
	c := newRoutingCache(time.Minute)
	current := time.Now()
	c.now = func() time.Time { return current }

	c.Put("ws", "a", "vm-old", 1, "c", "k")
	current = current.Add(30 * time.Second)
	c.Put("ws", "a", "vm-new", 2, "c2", "k2")
	current = current.Add(45 * time.Second)
	// 75 s after the original Put — would have expired the first entry,
	// but the second Put bumped expiry to t=30+60=90s, so the entry must
	// still be live at t=75s.
	got, ok := c.Get("ws", "a")
	if !ok {
		t.Fatalf("Get after overwrite at 75 s = !ok, want ok")
	}
	if got.vmHost != "vm-new" || got.agentdPort != 2 {
		t.Errorf("Get returned %q/%d, want vm-new/2", got.vmHost, got.agentdPort)
	}
}

func TestRoutingCache_ConcurrentAccess(t *testing.T) {
	// The cache claims concurrent-safety; -race will flag any violation
	// when this test fans out enough goroutines to overlap.
	c := newRoutingCache(time.Minute)
	const writers = 8
	const reads = 200
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		i := i
		go func() {
			defer wg.Done()
			for r := 0; r < reads; r++ {
				c.Put("ws", "a", "vm", int32(i), "c", "k")
				_, _ = c.Get("ws", "a")
				if r%17 == 0 {
					c.Invalidate("ws", "a")
				}
			}
		}()
	}
	wg.Wait()
}
