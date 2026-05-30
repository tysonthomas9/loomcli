package localredis

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestClock_StartedManagerExpiresRelativeTTL is the fix: once Start drives the
// clock, miniredis ages relative TTLs (SET ... PX/EX) in real wall time so
// PTTL counts down and the key disappears. This is the behavior fleet-db's
// issue locks / worker leases / leader election all depend on.
func TestClock_StartedManagerExpiresRelativeTTL(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	m, err := NewManager(snapPath, true, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// Fine-grained ticks so the test runs fast; FastForward still advances by
	// real elapsed wall time, so a short TTL expires on schedule.
	m.clockInterval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	defer m.Close()

	const key = "fleet-db:locks:issue:demo"
	if err := m.Client().Set(ctx, key, "holder-1", 150*time.Millisecond).Err(); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Initially present with a positive TTL.
	if pttl, err := m.Client().PTTL(ctx, key).Result(); err != nil || pttl <= 0 {
		t.Fatalf("PTTL right after Set = %v, err %v; want > 0", pttl, err)
	}

	// The clock ticker must drive it to expiry within a generous wall-clock
	// budget (TTL is 150ms; allow ample slack for CI scheduling jitter).
	deadline := time.Now().Add(3 * time.Second)
	for {
		n, err := m.Client().Exists(ctx, key).Result()
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if n == 0 {
			break // expired — fix works
		}
		if time.Now().After(deadline) {
			pttl, _ := m.Client().PTTL(ctx, key).Result()
			t.Fatalf("key %q never expired (PTTL still %v) — clock not advancing", key, pttl)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if pttl, err := m.Client().PTTL(ctx, key).Result(); err != nil {
		t.Fatalf("PTTL after expiry: %v", err)
	} else if pttl != -2*time.Nanosecond {
		// go-redis maps the "no such key" PTTL reply (-2) to -2ns.
		t.Errorf("PTTL of expired key = %v; want -2ns (no such key)", pttl)
	}
}

// TestClock_FrozenWithoutStart reproduces the bug: a Manager whose clock is
// never advanced (Start not called) leaves relative TTLs frozen forever — the
// stored PTTL does not move and the key is immortal. This is the exact failure
// behind locks showing PTTL=300000ms hours later.
func TestClock_FrozenWithoutStart(t *testing.T) {
	m, err := NewManager("", true, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	const key = "fleet-db:locks:issue:frozen"
	if err := m.Client().Set(ctx, key, "holder-1", 100*time.Millisecond).Err(); err != nil {
		t.Fatalf("Set: %v", err)
	}

	before, err := m.Client().PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("PTTL: %v", err)
	}

	// Real time passes well beyond the TTL, but nothing advances the clock.
	time.Sleep(250 * time.Millisecond)

	n, err := m.Client().Exists(ctx, key).Result()
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if n != 1 {
		t.Fatalf("key expired without a running clock (n=%d) — frozen-clock bug not reproduced", n)
	}
	after, err := m.Client().PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("PTTL: %v", err)
	}
	if after != before {
		t.Errorf("PTTL moved without a running clock: before=%v after=%v", before, after)
	}
}

// TestClock_SnapshotAgesAcrossReload covers the snapshot-fidelity half noted in
// the plan: a TTL'd key dumped and reloaded into a fresh Manager comes back with
// elapsed-since-dump subtracted, never with a larger TTL than was written.
func TestClock_SnapshotAgesAcrossReload(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")

	m1, err := NewManager(snapPath, true, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ctx := context.Background()
	const key = "fleet-db:lease:worker:1"
	if err := m1.Client().Set(ctx, key, "v", 10*time.Second).Err(); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := m1.Dump(); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	full, _ := m1.Client().PTTL(ctx, key).Result()
	_ = m1.Close()

	// Reload into a fresh manager. replay() subtracts elapsed-since-DumpedAt,
	// so the restored TTL must be no larger than what was dumped (it ages even
	// while the process was down). This is the snapshot-fidelity half noted in
	// the plan; it works because TTLs are stored as remaining durations.
	m2, err := NewManager(snapPath, true, nil)
	if err != nil {
		t.Fatalf("NewManager reload: %v", err)
	}
	defer m2.Close()

	restored, err := m2.Client().PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("PTTL after reload: %v", err)
	}
	if restored <= 0 {
		t.Fatalf("restored TTL = %v; want > 0 (key should survive reload)", restored)
	}
	if restored > full {
		t.Errorf("restored TTL %v exceeds dumped TTL %v — elapsed not subtracted", restored, full)
	}
}
