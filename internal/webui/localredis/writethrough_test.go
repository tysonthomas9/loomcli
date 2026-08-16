package localredis

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

// Test seams: fast enough that nothing sleeps for seconds, slow enough
// that a burst genuinely coalesces rather than racing the assertion.
const (
	testWTDelay = 20 * time.Millisecond
	testWTGap   = 5 * time.Millisecond
	// waitBudget is generous on purpose — these tests assert that a dump
	// happens, and a loaded CI box may take far longer than the delay.
	waitBudget = 5 * time.Second
)

// newWriteThroughManager builds a started Manager with short debounce
// seams. It deliberately does NOT register Close as the assertion path:
// callers must be able to prove durability WITHOUT a graceful shutdown,
// which is the whole point of the ticket. Close runs in cleanup only to
// avoid leaking the miniredis server and run goroutine into later tests.
func newWriteThroughManager(t *testing.T, fleetKeys bool, opts ...Option) (*Manager, string) {
	t.Helper()
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	base := []Option{withWriteThroughDelay(testWTDelay), withWriteThroughGap(testWTGap)}
	m, err := NewManager(snapPath, fleetKeys, nil, append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	m.Start(context.Background())
	return m, snapPath
}

// waitForSnapshotContaining polls the snapshot FILE (not the in-memory
// keyspace) until it contains needle. Asserting on the file is what makes
// these tests meaningful: it is the artifact that survives a SIGKILL.
func waitForSnapshotContaining(t *testing.T, path, needle string) {
	t.Helper()
	deadline := time.Now().Add(waitBudget)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), needle) {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("snapshot %s never contained %q within %v (write-through did not fire)", path, needle, waitBudget)
}

// TestWriteThrough_PersistsTabMetaWithoutClose is THE regression test for
// PUPPET-84: a tab written to the store is durable without a graceful
// shutdown and without waiting for the 30s tick. Against the old code
// (tick + Close only) this fails, because neither trigger can fire inside
// the budget.
func TestWriteThrough_PersistsTabMetaWithoutClose(t *testing.T) {
	m, snapPath := newWriteThroughManager(t, false)
	ctx := context.Background()

	if err := m.Client().HSet(ctx, "terminal:meta:ws1:sess1", map[string]any{"label": "new-tab"}).Err(); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	waitForSnapshotContaining(t, snapPath, "terminal:meta:ws1:sess1")
}

// TestWriteThrough_CoversAllTerminalPrefixes exercises one real key shape
// per includedPrefixes entry. The ui-state case matters most: it is the
// hottest production mutation (every tab switch) and the only one with no
// store abstraction in front of it — PatchTerminalState issues a raw HSet.
func TestWriteThrough_CoversAllTerminalPrefixes(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		write func(ctx context.Context, c *redis.Client, key string) error
	}{
		{
			name: "tabmeta hash",
			key:  "terminal:meta:ws1:sess1",
			write: func(ctx context.Context, c *redis.Client, key string) error {
				return c.HSet(ctx, key, map[string]any{"label": "tab"}).Err()
			},
		},
		{
			name: "ui-state hash",
			key:  "terminal:ui-state:ws1",
			write: func(ctx context.Context, c *redis.Client, key string) error {
				return c.HSet(ctx, key, map[string]any{"active_tab": "sess1"}).Err()
			},
		},
		{
			name: "issue tabs string",
			key:  "ws:ws1:issue:tabs:ISSUE-1",
			write: func(ctx context.Context, c *redis.Client, key string) error {
				return c.Set(ctx, key, `{"tabs":[]}`, 0).Err()
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, snapPath := newWriteThroughManager(t, false)
			if err := tc.write(context.Background(), m.Client(), tc.key); err != nil {
				t.Fatalf("write: %v", err)
			}
			waitForSnapshotContaining(t, snapPath, tc.key)
		})
	}
}

// TestWriteThrough_TriggersFromForeignClient is the test that justifies
// hooking the miniredis SERVER rather than Manager.Client(). The real
// stores never use Manager.Client() — appstores builds an independent
// client per store via fleet.NewRedisClient — so a client-side hook would
// catch exactly zero production writes.
func TestWriteThrough_TriggersFromForeignClient(t *testing.T) {
	m, snapPath := newWriteThroughManager(t, false)
	ctx := context.Background()

	foreign := redis.NewClient(&redis.Options{Addr: m.Addr()})
	defer foreign.Close()

	if err := foreign.HSet(ctx, "terminal:meta:ws2:sess9", map[string]any{"label": "foreign"}).Err(); err != nil {
		t.Fatalf("HSet from foreign client: %v", err)
	}
	waitForSnapshotContaining(t, snapPath, "terminal:meta:ws2:sess9")
}

// TestWriteThrough_IgnoresReads verifies reads never arm the debounce.
// Asserted on the counter rather than mtime so a sweep that happened but
// hash-matched still counts as a failure.
func TestWriteThrough_IgnoresReads(t *testing.T) {
	m, _ := newWriteThroughManager(t, false)
	ctx := context.Background()

	// Seed via a write, let that write-through settle, then read only.
	if err := m.Client().HSet(ctx, "terminal:meta:ws1:sess1", map[string]any{"label": "x"}).Err(); err != nil {
		t.Fatalf("seed HSet: %v", err)
	}
	time.Sleep(10 * testWTDelay)

	before := testutil.ToFloat64(writeThroughTotal)
	if err := m.Client().HGetAll(ctx, "terminal:meta:ws1:sess1").Err(); err != nil {
		t.Fatalf("HGetAll: %v", err)
	}
	if err := m.Client().Get(ctx, "ws:ws1:issue:tabs:ISSUE-1").Err(); err != nil && err != redis.Nil {
		t.Fatalf("Get: %v", err)
	}
	if err := m.Client().Exists(ctx, "terminal:ui-state:ws1").Err(); err != nil {
		t.Fatalf("Exists: %v", err)
	}
	time.Sleep(10 * testWTDelay)

	if delta := testutil.ToFloat64(writeThroughTotal) - before; delta != 0 {
		t.Errorf("reads triggered %v write-through sweeps, want 0", delta)
	}
}

// TestWriteThrough_IgnoresFleetKeys guards the load-bearing scoping
// decision: fleet-db's keyspace is large and its write churn is constant,
// so arming the debounce from it would put the process in a near-
// continuous full-keyspace sweep. Fleet keys keep the 30s tick they have
// today.
//
// Asserted on the counter, not mtime — the hash short-circuit would mask
// an mtime check.
func TestWriteThrough_IgnoresFleetKeys(t *testing.T) {
	m, _ := newWriteThroughManager(t, true)
	ctx := context.Background()

	before := testutil.ToFloat64(writeThroughTotal)
	if err := m.Client().Set(ctx, "fleet-db:issues:ISSUE-1", "payload", 0).Err(); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := m.Client().Set(ctx, "fleet:jwt-signing-key:default", "secret", 0).Err(); err != nil {
		t.Fatalf("Set: %v", err)
	}
	time.Sleep(10 * testWTDelay)

	if delta := testutil.ToFloat64(writeThroughTotal) - before; delta != 0 {
		t.Errorf("fleet-key writes triggered %v write-through sweeps, want 0", delta)
	}

	// ...but a terminal key on the SAME fleet-mode manager still does.
	if err := m.Client().HSet(ctx, "terminal:meta:ws1:sess1", map[string]any{"label": "x"}).Err(); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	deadline := time.Now().Add(waitBudget)
	for time.Now().Before(deadline) {
		if testutil.ToFloat64(writeThroughTotal) > before {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Error("terminal-key write on a fleet-mode manager did not trigger write-through")
}

// TestWriteThrough_CoalescesBurst verifies a burst of writes collapses
// into far fewer sweeps than writes — the property that keeps the cost
// bounded under sustained tab churn.
func TestWriteThrough_CoalescesBurst(t *testing.T) {
	m, _ := newWriteThroughManager(t, false, withWriteThroughDelay(100*time.Millisecond))
	ctx := context.Background()

	const writes = 50
	before := testutil.ToFloat64(writeThroughTotal)
	for i := 0; i < writes; i++ {
		if err := m.Client().HSet(ctx, "terminal:meta:ws1:sess"+strconvI(i), map[string]any{"label": "x"}).Err(); err != nil {
			t.Fatalf("HSet %d: %v", i, err)
		}
	}
	time.Sleep(500 * time.Millisecond)

	delta := testutil.ToFloat64(writeThroughTotal) - before
	if delta == 0 {
		t.Fatal("burst produced no write-through sweep at all")
	}
	if delta > writes/5 {
		t.Errorf("burst of %d writes produced %v sweeps; expected heavy coalescing", writes, delta)
	}
}

// TestWriteThrough_GapReArmsRatherThanDrops guards the one subtle bug in
// this design. A write landing inside the cooldown must be re-armed, not
// dropped — dropping it would reintroduce PUPPET-84 in miniature, leaving
// that write waiting for the 30s tick.
func TestWriteThrough_GapReArmsRatherThanDrops(t *testing.T) {
	// A gap long enough that the second write is unambiguously inside the
	// cooldown, but far short of the 30s tick — so if the key lands, only
	// the re-arm can have put it there.
	m, snapPath := newWriteThroughManager(t, false, withWriteThroughGap(300*time.Millisecond))
	ctx := context.Background()

	if err := m.Client().HSet(ctx, "terminal:meta:ws1:first", map[string]any{"label": "a"}).Err(); err != nil {
		t.Fatalf("HSet first: %v", err)
	}
	waitForSnapshotContaining(t, snapPath, "terminal:meta:ws1:first")

	// Immediately after that sweep => inside the gap.
	if err := m.Client().HSet(ctx, "terminal:meta:ws1:second", map[string]any{"label": "b"}).Err(); err != nil {
		t.Fatalf("HSet second: %v", err)
	}
	waitForSnapshotContaining(t, snapPath, "terminal:meta:ws1:second")
}

// TestWriteThrough_DisabledWithoutSnapshotPath verifies persistence-less
// managers (the common test construction) install no hook and are inert.
func TestWriteThrough_DisabledWithoutSnapshotPath(t *testing.T) {
	m, err := NewManager("", false, nil, withWriteThroughDelay(testWTDelay))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()
	m.Start(context.Background())

	before := testutil.ToFloat64(writeThroughTotal)
	if err := m.Client().HSet(context.Background(), "terminal:meta:ws1:sess1", map[string]any{"label": "x"}).Err(); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	time.Sleep(10 * testWTDelay)

	if delta := testutil.ToFloat64(writeThroughTotal) - before; delta != 0 {
		t.Errorf("persistence-less manager triggered %v write-through sweeps, want 0", delta)
	}
}

// TestPreHook_NeverConsumesCommand pins the hard invariant. A miniredis
// pre-hook returning true means the command is already handled and is
// swallowed — returning true here would silently drop every terminal
// write instead of persisting it.
func TestPreHook_NeverConsumesCommand(t *testing.T) {
	m, _ := newWriteThroughManager(t, false)
	if m.preHook(nil, "HSET", "terminal:meta:ws1:sess1", "label", "x") {
		t.Fatal("preHook consumed a mutating command; it must always return false")
	}
	if m.preHook(nil, "GET", "unrelated") {
		t.Fatal("preHook consumed a read command; it must always return false")
	}

	// And end to end: the value must actually be readable back.
	ctx := context.Background()
	if err := m.Client().Set(ctx, "terminal:meta:ws1:plain", "value", 0).Err(); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := m.Client().Get(ctx, "terminal:meta:ws1:plain").Result()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "value" {
		t.Errorf("value after hooked write = %q, want %q", got, "value")
	}
}

func TestTriggersWriteThrough(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		args []string
		want bool
	}{
		{"HSET on tabmeta", "HSET", []string{"terminal:meta:ws1:s1", "label", "x"}, true},
		{"HSET on ui-state", "HSET", []string{"terminal:ui-state:ws1", "active_tab", "s1"}, true},
		{"SET on issue tabs", "SET", []string{"ws:ws1:issue:tabs:I-1", "{}"}, true},
		{"DEL on tabmeta", "DEL", []string{"terminal:meta:ws1:s1"}, true},
		{"lowercase verb", "hset", []string{"terminal:meta:ws1:s1", "label", "x"}, true},
		{"mixed-case verb", "HsEt", []string{"terminal:meta:ws1:s1", "label", "x"}, true},

		{"GET is a read", "GET", []string{"terminal:meta:ws1:s1"}, false},
		{"HGETALL is a read", "HGETALL", []string{"terminal:ui-state:ws1"}, false},
		{"SCAN is a read", "SCAN", []string{"0", "MATCH", "terminal:meta:*"}, false},
		{"EXISTS is a read", "EXISTS", []string{"terminal:meta:ws1:s1"}, false},
		{"TYPE is a read", "TYPE", []string{"terminal:meta:ws1:s1"}, false},

		{"write to unrelated key", "SET", []string{"other:thing", "x"}, false},
		{"write to fleet key", "SET", []string{"fleet-db:issues:I-1", "x"}, false},
		{"write to jwt key", "SET", []string{"fleet:jwt-signing-key:default", "x"}, false},

		{"RENAME matches non-first arg", "RENAME", []string{"other:thing", "terminal:meta:ws1:s1"}, true},
		{"MSET matches later pair", "MSET", []string{"a", "1", "ws:ws1:issue:tabs:I-1", "{}"}, true},
		{"SMOVE matches destination", "SMOVE", []string{"src", "terminal:meta:ws1:set", "member"}, true},
		{"EVAL matches KEYS entry", "EVAL", []string{"return 1", "1", "terminal:meta:ws1:s1"}, true},
		{"EVALSHA matches KEYS entry", "EVALSHA", []string{"abc123", "1", "terminal:ui-state:ws1"}, true},
		{"EVAL on unrelated keys", "EVAL", []string{"return 1", "1", "fleet-db:x"}, false},

		{"unknown verb is inert", "WEIRDCMD", []string{"terminal:meta:ws1:s1"}, false},
		{"mutating verb, no args", "FLUSHDB", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := triggersWriteThrough(tc.cmd, tc.args); got != tc.want {
				t.Errorf("triggersWriteThrough(%q, %v) = %v, want %v", tc.cmd, tc.args, got, tc.want)
			}
		})
	}
}
