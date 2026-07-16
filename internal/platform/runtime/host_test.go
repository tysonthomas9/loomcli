package runtime //nolint:revive // Tests intentionally share the platform runtime package.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var testEpoch = time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

func TestHostConstructionAndRegistrationAreInert(t *testing.T) {
	clock := newFakeClock(testEpoch)
	var calls atomic.Uint64
	host := newTestHost(clock, nil)
	err := host.Register(Registration{
		Component: componentFunc{id: "inert-component", run: func(context.Context, time.Time) error {
			calls.Add(1)
			return nil
		}},
		Policy: Policy{Cadence: time.Hour, Immediate: true},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("calls before Start = %d, want 0", got)
	}
	if got := clock.createdTimers(); got != 0 {
		t.Fatalf("timers before Start = %d, want 0", got)
	}
	snapshot := host.Snapshot()
	if snapshot.Status != HostCreated || len(snapshot.Components) != 1 || snapshot.Components[0].Status != ComponentRegistered {
		t.Fatalf("pre-start snapshot = %+v", snapshot)
	}

	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	eventually(t, func() bool { return calls.Load() == 1 }, "immediate component pass")
	if err := host.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	assertComponentHealth(t, host, "inert-component", func(health ComponentHealth) bool {
		return health.Status == ComponentStopped && health.Runs == 1 && health.Successes == 1
	})
}

func TestHostRegistrationValidation(t *testing.T) {
	validComponent := componentFunc{id: "valid-component", run: func(context.Context, time.Time) error { return nil }}
	validPolicy := Policy{Cadence: time.Second}
	var typedNil *pointerComponent
	tests := []struct {
		name         string
		registration Registration
		want         string
	}{
		{name: "nil component", registration: Registration{Policy: validPolicy}, want: "component is required"},
		{name: "typed nil component", registration: Registration{Component: typedNil, Policy: validPolicy}, want: "component is required"},
		{name: "invalid id", registration: Registration{Component: componentFunc{id: "Not Valid"}, Policy: validPolicy}, want: "lowercase kebab-case"},
		{name: "zero cadence", registration: Registration{Component: validComponent}, want: "cadence must be positive"},
		{name: "negative jitter", registration: Registration{Component: validComponent, Policy: Policy{Cadence: time.Second, Jitter: -1}}, want: "jitter must not be negative"},
		{name: "jitter equals cadence", registration: Registration{Component: validComponent, Policy: Policy{Cadence: time.Second, Jitter: time.Second}}, want: "jitter must be less than cadence"},
		{name: "negative timeout", registration: Registration{Component: validComponent, Policy: Policy{Cadence: time.Second, Timeout: -1}}, want: "timeout must not be negative"},
		{name: "backoff missing initial", registration: Registration{Component: validComponent, Policy: Policy{Cadence: time.Second, FailureBackoff: Backoff{Max: time.Second, Multiplier: 2}}}, want: "initial delay must be positive"},
		{name: "backoff max below initial", registration: Registration{Component: validComponent, Policy: Policy{Cadence: time.Second, FailureBackoff: Backoff{Initial: time.Second, Max: time.Millisecond, Multiplier: 2}}}, want: "maximum must not be less"},
		{name: "backoff multiplier below one", registration: Registration{Component: validComponent, Policy: Policy{Cadence: time.Second, FailureBackoff: Backoff{Initial: time.Second, Max: time.Second, Multiplier: .5}}}, want: "multiplier must be finite"},
		{name: "backoff multiplier NaN", registration: Registration{Component: validComponent, Policy: Policy{Cadence: time.Second, FailureBackoff: Backoff{Initial: time.Second, Max: time.Second, Multiplier: math.NaN()}}}, want: "multiplier must be finite"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			host := newTestHost(newFakeClock(testEpoch), nil)
			err := host.Register(test.registration)
			if err == nil || !contains(err.Error(), test.want) {
				t.Fatalf("Register error = %v, want containing %q", err, test.want)
			}
		})
	}

	host := newTestHost(newFakeClock(testEpoch), nil)
	if err := host.Register(Registration{Component: validComponent, Policy: validPolicy}); err != nil {
		t.Fatal(err)
	}
	if err := host.Register(Registration{Component: validComponent, Policy: validPolicy}); !errors.Is(err, ErrDuplicateComponent) {
		t.Fatalf("duplicate Register error = %v, want %v", err, ErrDuplicateComponent)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Register(Registration{Component: componentFunc{id: "late-component"}, Policy: validPolicy}); !errors.Is(err, ErrHostStarted) {
		t.Fatalf("late Register error = %v, want %v", err, ErrHostStarted)
	}
	if err := host.Start(context.Background()); !errors.Is(err, ErrHostStarted) {
		t.Fatalf("second Start error = %v, want %v", err, ErrHostStarted)
	}
	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHostFixedRateCadenceCoalescesAndNeverOverlaps(t *testing.T) {
	clock := newFakeClock(testEpoch)
	started := make(chan uint64, 8)
	releases := make(chan struct{}, 8)
	var calls atomic.Uint64
	var active atomic.Int64
	var maximumActive atomic.Int64
	component := componentFunc{id: "fixed-rate", run: func(ctx context.Context, _ time.Time) error {
		call := calls.Add(1)
		current := active.Add(1)
		updateMaximum(&maximumActive, current)
		defer active.Add(-1)
		started <- call
		select {
		case <-releases:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	host := newTestHost(clock, nil)
	mustRegister(t, host, component, Policy{Cadence: 30 * time.Second, Immediate: true})
	mustStart(t, host)

	wantCall(t, started, 1)
	clock.Advance(35 * time.Second)
	assertNoCall(t, started)
	releases <- struct{}{}
	wantCall(t, started, 2) // one buffered cadence tick, immediately coalesced
	releases <- struct{}{}

	assertComponentHealth(t, host, "fixed-rate", func(health ComponentHealth) bool {
		return health.Successes == 2 && health.NextRunAt.Equal(testEpoch.Add(time.Minute))
	})
	clock.Advance(24 * time.Second)
	assertNoCall(t, started)
	clock.Advance(time.Second)
	wantCall(t, started, 3)
	releases <- struct{}{}

	if got := maximumActive.Load(); got != 1 {
		t.Fatalf("maximum concurrent calls = %d, want 1", got)
	}
	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHostAppliesDeterministicBoundedJitter(t *testing.T) {
	clock := newFakeClock(testEpoch)
	started := make(chan time.Time, 2)
	var jitterCalls atomic.Uint64
	jitter := func(maximum time.Duration) time.Duration {
		if maximum != 3*time.Second {
			t.Errorf("jitter maximum = %s, want 3s", maximum)
		}
		jitterCalls.Add(1)
		return 2 * time.Second
	}
	host := newTestHost(clock, jitter)
	mustRegister(t, host, componentFunc{id: "jittered", run: func(_ context.Context, now time.Time) error {
		started <- now
		return nil
	}}, Policy{Cadence: 10 * time.Second, Jitter: 3 * time.Second})
	mustStart(t, host)

	assertComponentHealth(t, host, "jittered", func(health ComponentHealth) bool {
		return health.NextRunAt.Equal(testEpoch.Add(12 * time.Second))
	})
	clock.Advance(11 * time.Second)
	assertNoTime(t, started)
	clock.Advance(time.Second)
	if got := wantTime(t, started); !got.Equal(testEpoch.Add(12 * time.Second)) {
		t.Fatalf("component time = %s, want %s", got, testEpoch.Add(12*time.Second))
	}
	if got := jitterCalls.Load(); got == 0 {
		t.Fatal("jitter source was not used")
	}
	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHostOwnsTimeoutAndCappedFailureBackoff(t *testing.T) {
	clock := newFakeClock(testEpoch)
	started := make(chan uint64, 8)
	var calls atomic.Uint64
	component := componentFunc{id: "backing-off", run: func(ctx context.Context, _ time.Time) error {
		call := calls.Add(1)
		started <- call
		if call <= 2 {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	host := newTestHost(clock, nil)
	mustRegister(t, host, component, Policy{
		Cadence:   time.Second,
		Immediate: true,
		Timeout:   5 * time.Second,
		FailureBackoff: Backoff{
			Initial: 10 * time.Second, Max: 20 * time.Second, Multiplier: 2,
		},
	})
	mustStart(t, host)

	wantCall(t, started, 1)
	clock.Advance(5 * time.Second)
	assertComponentHealth(t, host, "backing-off", func(health ComponentHealth) bool {
		return health.Failures == 1 && health.Timeouts == 1 && health.ConsecutiveFailures == 1 &&
			health.NextRunAt.Equal(testEpoch.Add(15*time.Second))
	})
	clock.Advance(9 * time.Second)
	assertNoCall(t, started)
	clock.Advance(time.Second)
	wantCall(t, started, 2)

	clock.Advance(5 * time.Second)
	assertComponentHealth(t, host, "backing-off", func(health ComponentHealth) bool {
		return health.Failures == 2 && health.Timeouts == 2 && health.ConsecutiveFailures == 2 &&
			health.NextRunAt.Equal(testEpoch.Add(40*time.Second))
	})
	clock.Advance(20 * time.Second)
	wantCall(t, started, 3)
	assertComponentHealth(t, host, "backing-off", func(health ComponentHealth) bool {
		return health.Successes >= 1 && health.ConsecutiveFailures == 0 && health.LastError == ""
	})

	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHostIsolatesPanickingAndHealthyComponents(t *testing.T) {
	clock := newFakeClock(testEpoch)
	badRan := make(chan struct{}, 1)
	goodRan := make(chan struct{}, 1)
	host := newTestHost(clock, nil)
	mustRegister(t, host, componentFunc{id: "bad-component", run: func(context.Context, time.Time) error {
		badRan <- struct{}{}
		panic("boom")
	}}, Policy{Cadence: time.Hour, Immediate: true})
	mustRegister(t, host, componentFunc{id: "good-component", run: func(context.Context, time.Time) error {
		goodRan <- struct{}{}
		return nil
	}}, Policy{Cadence: time.Hour, Immediate: true})
	mustStart(t, host)

	wantSignal(t, badRan, "panicking component")
	wantSignal(t, goodRan, "healthy component")
	assertComponentHealth(t, host, "bad-component", func(health ComponentHealth) bool {
		return health.Status == ComponentDegraded && health.Failures == 1 && health.Panics == 1 && contains(health.LastError, "boom")
	})
	assertComponentHealth(t, host, "good-component", func(health ComponentHealth) bool {
		return health.Status == ComponentHealthy && health.Successes == 1 && health.Failures == 0
	})

	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHostStopIsBoundedAndRetryable(t *testing.T) {
	clock := newFakeClock(testEpoch)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	host := newTestHost(clock, nil)
	mustRegister(t, host, componentFunc{id: "ignores-cancellation", run: func(context.Context, time.Time) error {
		started <- struct{}{}
		<-release
		return nil
	}}, Policy{Cadence: time.Hour, Immediate: true})
	mustStart(t, host)
	wantSignal(t, started, "blocking component")

	stopContext, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := host.Stop(stopContext); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded Stop error = %v, want deadline exceeded", err)
	}
	if got := host.Snapshot().Status; got != HostStopping {
		t.Fatalf("status after timed-out Stop = %q, want %q", got, HostStopping)
	}
	close(release)
	if err := host.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop: %v", err)
	}
	if got := host.Snapshot().Status; got != HostStopped {
		t.Fatalf("final status = %q, want %q", got, HostStopped)
	}
}

func TestHostCancellationIsNotComponentFailure(t *testing.T) {
	clock := newFakeClock(testEpoch)
	started := make(chan struct{}, 1)
	host := newTestHost(clock, nil)
	mustRegister(t, host, componentFunc{id: "cancellable", run: func(ctx context.Context, _ time.Time) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}}, Policy{Cadence: time.Hour, Immediate: true})
	mustStart(t, host)
	wantSignal(t, started, "cancellable component")
	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	health := componentHealth(t, host, "cancellable")
	if health.Failures != 0 || health.Status != ComponentStopped || health.InFlight {
		t.Fatalf("cancellation health = %+v", health)
	}
}

func TestHostSnapshotIsSortedAndConcurrentSafe(t *testing.T) {
	clock := newFakeClock(testEpoch)
	host := newTestHost(clock, nil)
	for _, id := range []ComponentID{"zulu", "alpha", "middle"} {
		mustRegister(t, host, componentFunc{id: id, run: func(context.Context, time.Time) error { return nil }}, Policy{
			Cadence: time.Second, Immediate: true,
		})
	}
	mustStart(t, host)
	assertComponentHealth(t, host, "alpha", func(health ComponentHealth) bool { return health.Successes == 1 })

	var readers sync.WaitGroup
	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 500 {
				snapshot := host.Snapshot()
				if len(snapshot.Components) != 3 {
					t.Errorf("component count = %d, want 3", len(snapshot.Components))
					return
				}
				if snapshot.Components[0].ID != "alpha" || snapshot.Components[1].ID != "middle" || snapshot.Components[2].ID != "zulu" {
					t.Errorf("component order = %v", []ComponentID{snapshot.Components[0].ID, snapshot.Components[1].ID, snapshot.Components[2].ID})
					return
				}
			}
		}()
	}
	for range 20 {
		clock.Advance(time.Second)
	}
	readers.Wait()
	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHostStopBeforeStartIsIdempotent(t *testing.T) {
	host := newTestHost(newFakeClock(testEpoch), nil)
	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); !errors.Is(err, ErrHostStarted) {
		t.Fatalf("Start after Stop error = %v, want %v", err, ErrHostStarted)
	}
}

type componentFunc struct {
	id  ComponentID
	run func(context.Context, time.Time) error
}

func (component componentFunc) ID() ComponentID { return component.id }

func (component componentFunc) RunOnce(ctx context.Context, now time.Time) error {
	if component.run == nil {
		return nil
	}
	return component.run(ctx, now)
}

type pointerComponent struct{}

func (*pointerComponent) ID() ComponentID { return "pointer-component" }

func (*pointerComponent) RunOnce(context.Context, time.Time) error { return nil }

type fakeClock struct {
	mu         sync.Mutex
	now        time.Time
	timers     []*fakeTimer
	timerCount uint64
}

type fakeTimer struct {
	clock    *fakeClock
	deadline time.Time
	channel  chan time.Time
	callback func()
	stopped  bool
	fired    bool
}

func newFakeClock(now time.Time) *fakeClock { return &fakeClock{now: now} }

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) NewTimer(delay time.Duration) Timer {
	return clock.newTimer(delay, nil)
}

func (clock *fakeClock) WithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancelCause := context.WithCancelCause(parent)
	timer := clock.newTimer(timeout, func() { cancelCause(context.DeadlineExceeded) })
	return ctx, func() {
		timer.Stop()
		cancelCause(context.Canceled)
	}
}

func (clock *fakeClock) newTimer(delay time.Duration, callback func()) *fakeTimer {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	timer := &fakeTimer{
		clock:    clock,
		deadline: clock.now.Add(delay),
		channel:  make(chan time.Time, 1),
		callback: callback,
	}
	clock.timers = append(clock.timers, timer)
	clock.timerCount++
	return timer
}

func (clock *fakeClock) Advance(duration time.Duration) {
	if duration < 0 {
		panic("fake clock cannot move backwards")
	}
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	var due []*fakeTimer
	for _, timer := range clock.timers {
		if timer.stopped || timer.fired || timer.deadline.After(clock.now) {
			continue
		}
		timer.fired = true
		due = append(due, timer)
	}
	clock.mu.Unlock()
	for _, timer := range due {
		if timer.callback != nil {
			timer.callback()
			continue
		}
		timer.channel <- timer.deadline
	}
}

func (clock *fakeClock) createdTimers() uint64 {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.timerCount
}

func (timer *fakeTimer) C() <-chan time.Time { return timer.channel }

func (timer *fakeTimer) Stop() bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	if timer.stopped || timer.fired {
		return false
	}
	timer.stopped = true
	return true
}

func newTestHost(clock Clock, jitter JitterSource) *Host {
	return NewHost(Options{
		Clock:  clock,
		Jitter: jitter,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func mustRegister(t *testing.T, host *Host, component Component, policy Policy) {
	t.Helper()
	if err := host.Register(Registration{Component: component, Policy: policy}); err != nil {
		t.Fatalf("Register %q: %v", component.ID(), err)
	}
}

func mustStart(t *testing.T, host *Host) {
	t.Helper()
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func componentHealth(t *testing.T, host *Host, id ComponentID) ComponentHealth {
	t.Helper()
	for _, health := range host.Snapshot().Components {
		if health.ID == id {
			return health
		}
	}
	t.Fatalf("component %q absent from snapshot", id)
	return ComponentHealth{}
}

func assertComponentHealth(t *testing.T, host *Host, id ComponentID, predicate func(ComponentHealth) bool) {
	t.Helper()
	eventually(t, func() bool { return predicate(componentHealth(t, host, id)) }, "health for "+string(id))
}

func eventually(t *testing.T, predicate func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func wantCall(t *testing.T, calls <-chan uint64, want uint64) {
	t.Helper()
	select {
	case got := <-calls:
		if got != want {
			t.Fatalf("call = %d, want %d", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for call %d", want)
	}
}

func assertNoCall(t *testing.T, calls <-chan uint64) {
	t.Helper()
	select {
	case got := <-calls:
		t.Fatalf("unexpected call %d", got)
	case <-time.After(10 * time.Millisecond):
	}
}

func wantSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func wantTime(t *testing.T, values <-chan time.Time) time.Time {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for component time")
		return time.Time{}
	}
}

func assertNoTime(t *testing.T, values <-chan time.Time) {
	t.Helper()
	select {
	case value := <-values:
		t.Fatalf("unexpected component time %s", value)
	case <-time.After(10 * time.Millisecond):
	}
}

func updateMaximum(maximum *atomic.Int64, candidate int64) {
	for {
		current := maximum.Load()
		if candidate <= current || maximum.CompareAndSwap(current, candidate) {
			return
		}
	}
}

func contains(value, part string) bool {
	for index := 0; index+len(part) <= len(value); index++ {
		if value[index:index+len(part)] == part {
			return true
		}
	}
	return false
}
