//go:build daemon_bugreplay

package localredis

import (
	"testing"
	"time"
)

// Found bug #22 (#112): an embedded Redis lock must age without FastForward.
func TestBugReplay_Found22_EmbeddedRedisTTLExpires(t *testing.T) {
	m, err := NewManager("", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := t.Context()
	if err := m.Client().Set(ctx, "claim-lock", "worker-1", time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if n, err := m.Client().Exists(ctx, "claim-lock").Result(); err != nil {
			t.Fatal(err)
		} else if n == 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("embedded Redis claim lock did not expire after its one-second TTL")
}
