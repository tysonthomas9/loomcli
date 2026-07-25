package localredis

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
)

type pipelineCountingHook struct {
	individual atomic.Int64
	pipelines  atomic.Int64
}

func (h *pipelineCountingHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return next(ctx, network, addr)
	}
}

func (h *pipelineCountingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.individual.Add(1)
		return next(ctx, cmd)
	}
}

func (h *pipelineCountingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipelines.Add(1)
		return next(ctx, cmds)
	}
}

// captureHandler is a minimal slog.Handler recording every emitted
// record so tests can assert on log volume and levels (the WARN-storm
// regression tests below hinge on exact counts).
type captureHandler struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recs = append(h.recs, r.Clone())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) countMsg(msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.recs {
		if r.Message == msg {
			n++
		}
	}
	return n
}

func (h *captureHandler) countAtLeast(level slog.Level) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.recs {
		if r.Level >= level {
			n++
		}
	}
	return n
}

// seedBulkKeys writes n persistable string keys through one pipeline.
func seedBulkKeys(t *testing.T, m *Manager, n int) {
	t.Helper()
	ctx := context.Background()
	pipe := m.Client().Pipeline()
	for i := 0; i < n; i++ {
		pipe.Set(ctx, fmt.Sprintf("terminal:meta:bulk:%05d", i), "x", 0)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed bulk keys: %v", err)
	}
}

func seedBulkHashes(t *testing.T, m *Manager, n int) {
	t.Helper()
	ctx := context.Background()
	pipe := m.Client().Pipeline()
	for i := 0; i < n; i++ {
		pipe.HSet(ctx, fmt.Sprintf("fleet-db:bulk:%05d", i), "field", "value")
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("seed bulk hashes: %v", err)
	}
}

func readPair(t *testing.T, snapPath string) (primary, backup []byte) {
	t.Helper()
	primary, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	backup, err = os.ReadFile(snapPath + ".bak")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	return primary, backup
}

// TestDump_AbortOnPartialDoesNotOverwrite is the fault-injection
// centerpiece: a sweep that cannot read its keys must leave BOTH
// on-disk generations byte-identical, fail with one summary error, and
// emit no per-key WARNs (the old behavior overwrote the good snapshot
// with a truncated one and logged one WARN per unread key).
func TestDump_AbortOnPartialDoesNotOverwrite(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()

	// Build two good generations on disk (snapshot.json + .bak).
	m1, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager m1: %v", err)
	}
	if err := m1.Client().HSet(ctx, "terminal:meta:gen", "v", "1").Err(); err != nil {
		t.Fatal(err)
	}
	if err := m1.Dump(); err != nil {
		t.Fatalf("Dump gen1: %v", err)
	}
	if err := m1.Client().HSet(ctx, "terminal:meta:gen", "v", "2").Err(); err != nil {
		t.Fatal(err)
	}
	if err := m1.Dump(); err != nil {
		t.Fatalf("Dump gen2: %v", err)
	}
	if err := m1.Close(); err != nil {
		t.Fatalf("Close m1: %v", err)
	}
	before, beforeBak := readPair(t, snapPath)

	// Second manager with a poisoned per-batch budget: every read fails
	// instantly, simulating the deadline blowing mid-sweep.
	capH := &captureHandler{}
	m2, err := NewManager(snapPath, false, slog.New(capH), withBatchTimeout(time.Nanosecond))
	if err != nil {
		t.Fatalf("NewManager m2: %v", err)
	}
	defer m2.Close()
	seedBulkKeys(t, m2, 2000)

	failsBefore := testutil.ToFloat64(snapshotFailuresTotal)
	dumpErr := m2.Dump()
	if dumpErr == nil {
		t.Fatal("Dump succeeded; want partial-read abort")
	}
	if !strings.Contains(dumpErr.Error(), "snapshot aborted:") || !strings.Contains(dumpErr.Error(), "keys unread") {
		t.Errorf("abort error = %q, want summary with 'snapshot aborted:' and 'keys unread'", dumpErr)
	}

	after, afterBak := readPair(t, snapPath)
	if !bytes.Equal(before, after) {
		t.Error("aborted sweep modified snapshot.json")
	}
	if !bytes.Equal(beforeBak, afterBak) {
		t.Error("aborted sweep modified snapshot.json.bak")
	}
	if got := testutil.ToFloat64(snapshotFailuresTotal) - failsBefore; got != 1 {
		t.Errorf("failures counter delta = %v, want 1", got)
	}
	// One Debug line for the first attempted key; the rest of the batch
	// and all later batches are bulk-accounted without per-key logs.
	if got := capH.countMsg("failed to read key for snapshot"); got != 1 {
		t.Errorf("per-key log records = %d, want exactly 1", got)
	}
	if got := capH.countAtLeast(slog.LevelWarn); got != 0 {
		t.Errorf("Warn+ records during abort = %d, want 0 (summary is the returned error)", got)
	}
}

func TestDump_MultiBatchSuccess(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()

	m, err := NewManager(snapPath, false, nil, withBatchSize(100))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	for i := 0; i < 250; i++ { // 3 batches at batchSize=100
		if err := m.Client().Set(ctx, fmt.Sprintf("terminal:meta:k%03d", i), fmt.Sprintf("v%03d", i), 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Dump(); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	_ = m.Close()

	m2, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager m2: %v", err)
	}
	defer m2.Close()
	// First, middle, and crucially the LAST sorted key — the tail is
	// what the old shared-deadline sweep silently dropped.
	for _, i := range []int{0, 124, 249} {
		key := fmt.Sprintf("terminal:meta:k%03d", i)
		got, err := m2.Client().Get(ctx, key).Result()
		if err != nil {
			t.Fatalf("Get %s after reload: %v", key, err)
		}
		if want := fmt.Sprintf("v%03d", i); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestDump_UsesDirectMetadataAndPipelinesHashValues(t *testing.T) {
	m, err := NewManager(filepath.Join(t.TempDir(), "snapshot.json"), true, nil, withBatchSize(50))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()
	seedBulkHashes(t, m, 100)

	hook := &pipelineCountingHook{}
	m.Client().AddHook(hook)
	if err := m.Dump(); err != nil {
		t.Fatalf("Dump: %v", err)
	}

	if got := hook.pipelines.Load(); got != 2 {
		t.Fatalf("pipeline calls = %d, want 2 hash-value pipelines", got)
	}
	if got := hook.individual.Load(); got > 10 {
		t.Fatalf("individual Redis calls = %d, want only bounded SCAN/administrative calls", got)
	}
}

func TestDump_AbortDoesNotPoisonHashThenRecovers(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()

	m1, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m1.Client().Set(ctx, "terminal:meta:base", "1", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := m1.Dump(); err != nil {
		t.Fatalf("Dump gen1: %v", err)
	}
	_ = m1.Close()

	// Aborting manager: mutates the keyspace but every read fails.
	m2, err := NewManager(snapPath, false, nil, withBatchTimeout(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if err := m2.Client().Set(ctx, "terminal:meta:extra", "x", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := m2.Dump(); err == nil {
		t.Fatal("Dump succeeded; want abort")
	}
	data, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatal(err)
	}
	if containsKey(t, data, "terminal:meta:extra") {
		t.Fatal("aborted sweep persisted the new key")
	}
	_ = m2.Close() // final dump also aborts under the poisoned budget; ignore

	// Healthy manager on the same path: the prior aborts must not have
	// recorded any hash that would short-circuit this write.
	m3, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m3.Close()
	if err := m3.Client().Set(ctx, "terminal:meta:extra", "x", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := m3.Dump(); err != nil {
		t.Fatalf("recovery Dump: %v", err)
	}
	data2, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatal(err)
	}
	if !containsKey(t, data2, "terminal:meta:extra") {
		t.Error("healthy sweep after aborts did not persist the new key")
	}
}

func TestDump_ScanFailureAborts(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()

	m, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Client().Set(ctx, "terminal:meta:x", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := m.Dump(); err != nil {
		t.Fatalf("healthy Dump: %v", err)
	}
	before, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatal(err)
	}

	failsBefore := testutil.ToFloat64(snapshotFailuresTotal)
	m.mr.SetError("scan boom")
	dumpErr := m.Dump()
	m.mr.SetError("")
	if dumpErr == nil {
		t.Fatal("Dump succeeded under SetError; want scan failure")
	}
	if !strings.Contains(dumpErr.Error(), "collect entries: scan:") {
		t.Errorf("error = %q, want wrapped scan failure", dumpErr)
	}
	after, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("scan-failed sweep modified the snapshot")
	}
	if got := testutil.ToFloat64(snapshotFailuresTotal) - failsBefore; got != 1 {
		t.Errorf("failures counter delta = %v, want 1", got)
	}
}

// TestDump_SweepCapStopsIssuingReads: with the whole-sweep cap already
// expired, the dump must abort without issuing a single per-key read
// (or per-key log line) — the cap bounds Redis calls and CPU, not just
// wall time.
func TestDump_SweepCapStopsIssuingReads(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")

	capH := &captureHandler{}
	m, err := NewManager(snapPath, false, slog.New(capH),
		withSweepCap(time.Nanosecond), withScanTimeout(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	seedBulkKeys(t, m, 2000)

	failsBefore := testutil.ToFloat64(snapshotFailuresTotal)
	dumpErr := m.Dump()
	if dumpErr == nil {
		t.Fatal("Dump succeeded; want cap-expired abort")
	}
	if !strings.Contains(dumpErr.Error(), "collect entries:") {
		t.Errorf("error = %q, want collect-entries failure", dumpErr)
	}
	if _, statErr := os.Stat(snapPath); !os.IsNotExist(statErr) {
		t.Errorf("snapshot written despite expired cap (stat err=%v)", statErr)
	}
	if got := capH.countMsg("failed to read key for snapshot"); got != 0 {
		t.Errorf("per-key reads logged = %d, want 0 (no reads should be issued)", got)
	}
	if got := testutil.ToFloat64(snapshotFailuresTotal) - failsBefore; got != 1 {
		t.Errorf("failures counter delta = %v, want 1", got)
	}
}

func TestIsVanishedKeyErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"redis.Nil", redis.Nil, true},
		{"wrapped redis.Nil", fmt.Errorf("read: %w", redis.Nil), true},
		{"deadline", context.DeadlineExceeded, false},
		{"canceled", context.Canceled, false},
		{"transport", errors.New("connection reset"), false},
	}
	for _, tc := range cases {
		if got := isVanishedKeyErr(tc.err); got != tc.want {
			t.Errorf("%s: isVanishedKeyErr = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestDump_GhostKeyIsSkipNotFailure exercises the triage for a key that
// vanished between SCAN and the read (TYPE reports "none"): it must be
// a silent skip, never an abort trigger.
func TestDump_GhostKeyIsSkipNotFailure(t *testing.T) {
	m, err := NewManager("", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()
	if err := m.Client().Set(ctx, "terminal:meta:real", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}

	var out []snapshotEntry
	st := sweepStats{persisted: 2}
	m.readBatch(ctx, []string{"terminal:meta:ghost", "terminal:meta:real"}, &out, &st)

	if st.aborted() {
		t.Fatalf("ghost key aborted the sweep: %+v", st)
	}
	if st.skipped != 1 || st.read != 1 || len(out) != 1 {
		t.Errorf("stats = %+v, out=%d; want skipped=1 read=1 out=1", st, len(out))
	}
	if got := st.read + st.skipped + st.unread; got != st.persisted {
		t.Errorf("accounting invariant violated: read+skipped+unread=%d, persisted=%d", got, st.persisted)
	}
}

// TestClose_FinalDumpUsesTighterCap: the final dump in Close must run
// under closeSweepCap, not the (here: huge) periodic sweepCap — an
// expired close budget surfaces as a bounded error, not a hang.
func TestClose_FinalDumpUsesTighterCap(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	m, err := NewManager(snapPath, false, nil,
		withSweepCap(time.Hour), withCloseSweepCap(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Client().Set(context.Background(), "terminal:meta:x", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	closeErr := m.Close()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Close took %v; want bounded by closeSweepCap, not sweepCap", elapsed)
	}
	if closeErr == nil {
		t.Fatal("Close final dump succeeded under 1ns cap; want bounded abort")
	}
	if _, statErr := os.Stat(snapPath); !os.IsNotExist(statErr) {
		t.Errorf("aborted final dump wrote a snapshot (stat err=%v)", statErr)
	}
}

// TestClose_InterruptsInFlightSweep: Close must cancel an in-flight
// periodic/manual sweep promptly (via the Manager-lifetime context)
// instead of waiting out its generous cap, then write the final dump.
func TestClose_InterruptsInFlightSweep(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	started := make(chan struct{})
	var blockFirstRead atomic.Bool
	blockFirstRead.Store(true)
	readHook := func(ctx context.Context) {
		if blockFirstRead.CompareAndSwap(true, false) {
			close(started)
			<-ctx.Done()
		}
	}
	m, err := NewManager(snapPath, false, nil,
		withSweepCap(time.Hour), withBatchSize(1), withDirectReadHook(readHook))
	if err != nil {
		t.Fatal(err)
	}
	seedBulkKeys(t, m, 8000) // batchSize=1 → 8000 batches: a multi-second sweep

	errCh := make(chan error, 1)
	go func() { errCh <- m.Dump() }()
	<-started

	closeStart := time.Now()
	closeErr := m.Close()
	closeDur := time.Since(closeStart)

	dumpErr := <-errCh
	if dumpErr == nil {
		t.Fatal("in-flight Dump completed before Close — keyspace too small for this test")
	}
	if !strings.Contains(dumpErr.Error(), "canceled") {
		t.Errorf("in-flight dump error = %q, want context-canceled abort", dumpErr)
	}
	if closeErr != nil {
		t.Errorf("Close (final dump) failed: %v", closeErr)
	}
	if closeDur > 30*time.Second {
		t.Errorf("Close took %v; want prompt interrupt + bounded final dump", closeDur)
	}
	// The final dump must have produced a complete snapshot.
	data, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("snapshot after Close: %v", err)
	}
	if !containsKey(t, data, "terminal:meta:bulk:07999") {
		t.Error("final dump missing the tail of the keyspace")
	}
}

func TestSweepStats_AbortErrorFormat(t *testing.T) {
	st := sweepStats{
		unread:    4213,
		persisted: 6737,
		firstErr:  context.DeadlineExceeded,
		elapsed:   5 * time.Second,
	}
	want := "snapshot aborted: 4213/6737 keys unread (first: context deadline exceeded), elapsed=5s"
	if got := st.abortError().Error(); got != want {
		t.Errorf("abortError = %q, want %q", got, want)
	}
}

func TestMetrics_SuccessTimestampUpdatesOnIdleSweep(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	m, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Client().Set(context.Background(), "terminal:meta:x", "v", 0).Err(); err != nil {
		t.Fatal(err)
	}

	if err := m.Dump(); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	g1 := testutil.ToFloat64(snapshotLastSuccess)
	if g1 <= 0 {
		t.Fatalf("last-success gauge = %v after successful dump, want > 0", g1)
	}

	failsBefore := testutil.ToFloat64(snapshotFailuresTotal)
	if err := m.Dump(); err != nil { // unchanged keyspace → hash short-circuit
		t.Fatalf("idle Dump: %v", err)
	}
	g2 := testutil.ToFloat64(snapshotLastSuccess)
	if g2 < g1 {
		t.Errorf("idle sweep moved the gauge backwards: %v -> %v", g1, g2)
	}
	if got := testutil.ToFloat64(snapshotFailuresTotal) - failsBefore; got != 0 {
		t.Errorf("idle sweep incremented failures by %v, want 0", got)
	}
}
