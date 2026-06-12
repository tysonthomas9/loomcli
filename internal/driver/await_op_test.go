//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// newAwaitOpRun seeds catalog + one claimed running run and returns the store
// and the claimed run (owner credentials included).
func newAwaitOpRun(t *testing.T) (*memstore.Store, *domain.DriverRun) {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "driver-1", Name: "wf",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "version-1", DriverID: "driver-1", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "run-1", DriverID: "driver-1", DriverVersionID: "version-1",
	}); err != nil {
		t.Fatalf("Create driver run: %v", err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, "WS", "run-1", "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim driver run: %v", err)
	}
	return st, claimed
}

func awaitOpOptions(run *domain.DriverRun) AwaitEventOptions {
	return AwaitEventOptions{
		WorkspaceKey: "WS",
		RunID:        run.RunID,
		NodeID:       run.NodeID,
		LeaseID:      run.LeaseID,
		FencingToken: run.FencingToken,
		Pattern:      "pr.merged:pr#7",
		TimeoutMs:    time.Minute.Milliseconds(),
		AwaitIndex:   1,
	}
}

// windowStore wraps the memstore so its DriverRuns().Suspend runs a hook
// first — the deterministic park->suspend window: a resolver lands between
// RegisterAwaitAndCheck and Suspend.
type windowStore struct {
	store.Store
	suspendHook func() error
}

func (w *windowStore) DriverRuns() store.DriverRunStore {
	return &windowDriverRuns{DriverRunStore: w.Store.DriverRuns(), hook: w.suspendHook}
}

type windowDriverRuns struct {
	store.DriverRunStore
	hook func() error
}

func (r *windowDriverRuns) Suspend(ctx context.Context, ws, runID, nodeID, leaseID string, fencingToken int64, awaitInstanceKey string) (*domain.DriverRun, error) {
	if r.hook != nil {
		if err := r.hook(); err != nil {
			return nil, err
		}
	}
	return r.DriverRunStore.Suspend(ctx, ws, runID, nodeID, leaseID, fencingToken, awaitInstanceKey)
}

// TestAwaitEventParkSuspendWindowRecheck drives the memstore-shaped window:
// the resolver resolves the parked await while the run is still running (its
// ResumeAwaiting loses with ErrInvalidTransition), then the suspend lands.
// The op's post-suspend recheck must resume the run — suspended outcome, run
// re-queued, no lost wakeup.
func TestAwaitEventParkSuspendWindowRecheck(t *testing.T) {
	ctx := context.Background()
	st, run := newAwaitOpRun(t)
	instanceKey := domain.AwaitInstanceKey(run.RunID, 1)
	wrapped := &windowStore{Store: st, suspendHook: func() error {
		res, err := st.Awaits().ResolveAwait(ctx, "WS", instanceKey, "event-window", []byte(`{"won":"window"}`), "alice")
		if err != nil || !res.Resume {
			t.Fatalf("ResolveAwait in window = %+v, %v; want Resume=true", res, err)
		}
		// The resolver's resume sees a still-running run and tolerates the
		// loss — the hole the recheck closes.
		if _, err := st.DriverRuns().ResumeAwaiting(ctx, "WS", run.RunID, instanceKey, "event-window"); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("ResumeAwaiting on running run = %v, want ErrInvalidTransition", err)
		}
		return nil
	}}

	outcome, err := AwaitEvent(ctx, wrapped, awaitOpOptions(run))
	if err != nil {
		t.Fatalf("AwaitEvent: %v", err)
	}
	if outcome.Status != AwaitOutcomeSuspended {
		t.Fatalf("outcome.Status = %q, want %q", outcome.Status, AwaitOutcomeSuspended)
	}
	final, err := st.DriverRuns().Get(ctx, "WS", run.RunID)
	if err != nil {
		t.Fatalf("Get run: %v", err)
	}
	if final.Status != domain.DriverRunQueued || final.ResumeSourceEventID != "event-window" {
		t.Fatalf("run = %s/%q, want queued resumed by event-window", final.Status, final.ResumeSourceEventID)
	}
}

// TestAwaitEventSuspendAlreadyResumed covers the fleet-db-shaped window: the
// backend recorded a pending-resume marker and Suspend refuses with
// ErrDriverRunAlreadyResumed. The op must continue inline with the recorded
// event instead of parking.
func TestAwaitEventSuspendAlreadyResumed(t *testing.T) {
	ctx := context.Background()
	st, run := newAwaitOpRun(t)
	instanceKey := domain.AwaitInstanceKey(run.RunID, 1)
	wrapped := &windowStore{Store: st, suspendHook: func() error {
		if _, err := st.Awaits().ResolveAwait(ctx, "WS", instanceKey, "event-marker", []byte(`{"ok":true}`), "alice"); err != nil {
			t.Fatalf("ResolveAwait in window: %v", err)
		}
		return domain.ErrDriverRunAlreadyResumed
	}}

	outcome, err := AwaitEvent(ctx, wrapped, awaitOpOptions(run))
	if err != nil {
		t.Fatalf("AwaitEvent: %v", err)
	}
	if outcome.Status != string(domain.AwaitSatisfied) {
		t.Fatalf("outcome.Status = %q, want satisfied", outcome.Status)
	}
	if outcome.Instance == nil || outcome.Instance.SatisfiedByEventID != "event-marker" {
		t.Fatalf("outcome.Instance = %+v, want satisfied by event-marker", outcome.Instance)
	}
	final, err := st.DriverRuns().Get(ctx, "WS", run.RunID)
	if err != nil || final.Status != domain.DriverRunRunning {
		t.Fatalf("run = %+v, %v; want still running (continue inline)", final, err)
	}
}

// TestAwaitTimeoutBounds covers the RULE 5 wire-field validation including
// the configurable maximum.
func TestAwaitTimeoutBounds(t *testing.T) {
	cases := []struct {
		name      string
		timeoutMs int64
		max       time.Duration
		wantErr   bool
	}{
		{name: "missing", timeoutMs: 0, max: DefaultAwaitMaxTimeout, wantErr: true},
		{name: "negative", timeoutMs: -5, max: DefaultAwaitMaxTimeout, wantErr: true},
		{name: "over max", timeoutMs: (2 * time.Hour).Milliseconds(), max: time.Hour, wantErr: true},
		{name: "at max", timeoutMs: time.Hour.Milliseconds(), max: time.Hour},
		{name: "in range", timeoutMs: 1500, max: DefaultAwaitMaxTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			timeout, err := awaitTimeout(tc.timeoutMs, tc.max)
			if tc.wantErr {
				if !errors.Is(err, domain.ErrAwaitTimeoutRequired) {
					t.Fatalf("awaitTimeout(%d) err = %v, want ErrAwaitTimeoutRequired", tc.timeoutMs, err)
				}
				return
			}
			if err != nil || timeout != time.Duration(tc.timeoutMs)*time.Millisecond {
				t.Fatalf("awaitTimeout(%d) = %s, %v", tc.timeoutMs, timeout, err)
			}
		})
	}
}

// TestAwaitEventPerRunBudget pins the locked per-run await budget (AW8): the
// dense awaitIndex is the count, so an index past the budget is rejected
// before anything registers, while the last in-budget index still parks.
func TestAwaitEventPerRunBudget(t *testing.T) {
	ctx := context.Background()
	st, run := newAwaitOpRun(t)

	over := awaitOpOptions(run)
	over.AwaitIndex, over.MaxPerRun = 3, 2
	if _, err := AwaitEvent(ctx, st, over); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("AwaitEvent over budget = %v, want ErrInvalid", err)
	}
	if _, err := st.Awaits().GetSatisfiedAwait(ctx, "WS", domain.AwaitInstanceKey(run.RunID, 3)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("over-budget await = %v, want nothing registered", err)
	}

	within := awaitOpOptions(run)
	within.AwaitIndex, within.MaxPerRun = 2, 2
	outcome, err := AwaitEvent(ctx, st, within)
	if err != nil || outcome.Status != AwaitOutcomeSuspended {
		t.Fatalf("AwaitEvent at budget = %+v, %v; want suspended", outcome, err)
	}
}

// TestAwaitEventTotalSuspendCap pins the locked total-suspend cap (AW8): time
// actually spent suspended on prior awaits plus the new timeout is bounded.
// Await 1 is seeded with a backdated RegisteredAt and resolved now, recording
// ~2h of suspension; await 2 then fails under a 1h cap and parks under a 3h
// one.
func TestAwaitEventTotalSuspendCap(t *testing.T) {
	ctx := context.Background()
	st, run := newAwaitOpRun(t)
	priorKey := domain.AwaitInstanceKey(run.RunID, 1)
	if _, err := st.Awaits().RegisterAwaitAndCheck(ctx, "WS", store.AwaitRegistration{
		InstanceKey: priorKey, RunID: run.RunID, Pattern: "pr.merged:pr#6",
		Deadline:     time.Now().UTC().Add(time.Hour),
		RegisteredAt: time.Now().UTC().Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("seed prior await: %v", err)
	}
	if _, err := st.Awaits().ResolveAwait(ctx, "WS", priorKey, "evt-prior", nil, "alice"); err != nil {
		t.Fatalf("resolve prior await: %v", err)
	}

	capped := awaitOpOptions(run)
	capped.AwaitIndex, capped.TotalSuspendCap = 2, time.Hour
	if _, err := AwaitEvent(ctx, st, capped); !errors.Is(err, domain.ErrAwaitTimeoutRequired) {
		t.Fatalf("AwaitEvent over suspend cap = %v, want ErrAwaitTimeoutRequired", err)
	}

	roomy := awaitOpOptions(run)
	roomy.AwaitIndex, roomy.TotalSuspendCap = 2, 3*time.Hour
	outcome, err := AwaitEvent(ctx, st, roomy)
	if err != nil || outcome.Status != AwaitOutcomeSuspended {
		t.Fatalf("AwaitEvent within suspend cap = %+v, %v; want suspended", outcome, err)
	}
}

// TestAwaitBudgetEnvOverrides pins the env-tunable budget knobs.
func TestAwaitBudgetEnvOverrides(t *testing.T) {
	t.Setenv(AwaitMaxPerRunEnvVar, "7")
	t.Setenv(AwaitTotalSuspendCapEnvVar, "60000")
	if got := (AwaitEventOptions{}).maxPerRun(); got != 7 {
		t.Fatalf("maxPerRun with env override = %d, want 7", got)
	}
	if got := (AwaitEventOptions{}).totalSuspendCap(); got != time.Minute {
		t.Fatalf("totalSuspendCap with env override = %s, want 1m", got)
	}
	if got := (AwaitEventOptions{MaxPerRun: 3}).maxPerRun(); got != 3 {
		t.Fatalf("explicit MaxPerRun = %d, want 3", got)
	}
	t.Setenv(AwaitMaxPerRunEnvVar, "not-a-number")
	t.Setenv(AwaitTotalSuspendCapEnvVar, "-1")
	if got := (AwaitEventOptions{}).maxPerRun(); got != DefaultAwaitMaxPerRun {
		t.Fatalf("maxPerRun with bad env = %d, want default", got)
	}
	if got := (AwaitEventOptions{}).totalSuspendCap(); got != DefaultAwaitTotalSuspendCap {
		t.Fatalf("totalSuspendCap with bad env = %s, want default", got)
	}
}

// TestAwaitMaxTimeoutEnvOverride pins the env-tunable bound.
func TestAwaitMaxTimeoutEnvOverride(t *testing.T) {
	t.Setenv(AwaitMaxTimeoutEnvVar, "1000")
	if got := (AwaitEventOptions{}).maxTimeout(); got != time.Second {
		t.Fatalf("maxTimeout with env override = %s, want 1s", got)
	}
	if got := (AwaitEventOptions{MaxTimeout: time.Minute}).maxTimeout(); got != time.Minute {
		t.Fatalf("explicit MaxTimeout = %s, want 1m", got)
	}
	t.Setenv(AwaitMaxTimeoutEnvVar, "not-a-number")
	if got := (AwaitEventOptions{}).maxTimeout(); got != DefaultAwaitMaxTimeout {
		t.Fatalf("maxTimeout with bad env = %s, want default", got)
	}
}
