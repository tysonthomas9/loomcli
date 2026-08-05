package runtime //nolint:revive // The approved target architecture names this platform mechanism runtime.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime/debug"
	"slices"
	"sync"
	"time"
)

var (
	// ErrHostStarted means registration or a second start was attempted after
	// lifecycle execution began.
	ErrHostStarted = errors.New("runtime host already started")
	// ErrDuplicateComponent means two registrations used the same stable ID.
	ErrDuplicateComponent = errors.New("runtime component already registered")
)

// Options supplies capability-independent runtime mechanisms. Nil values use
// production defaults.
type Options struct {
	Clock  Clock
	Jitter JitterSource
	Logger *slog.Logger
}

// Host owns lifecycle, scheduling, isolation, and health for registered
// components. Construction and registration never start goroutines.
type Host struct {
	mu sync.RWMutex

	clock  Clock
	jitter JitterSource
	logger *slog.Logger

	status        HostStatus
	components    map[ComponentID]*componentState
	order         []ComponentID
	cancel        context.CancelFunc
	activeWorkers int
}

type componentState struct {
	component Component
	health    ComponentHealth
	done      chan struct{}
}

// NewHost constructs an inert host. Call Register for every component, then
// Start exactly once.
func NewHost(options Options) *Host {
	clock := options.Clock
	if isNilInterface(clock) {
		clock = realClock{}
	}
	jitter := options.Jitter
	if jitter == nil {
		jitter = randomJitter
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Host{
		clock:      clock,
		jitter:     jitter,
		logger:     logger,
		status:     HostCreated,
		components: make(map[ComponentID]*componentState),
	}
}

// Register adds one inert component. Registration is rejected after Start and
// duplicate IDs fail closed.
func (host *Host) Register(registration Registration) error {
	if host == nil {
		return fmt.Errorf("runtime host is required")
	}
	if err := validateRegistration(registration); err != nil {
		return err
	}
	id := registration.Component.ID()

	host.mu.Lock()
	defer host.mu.Unlock()
	if host.status != HostCreated {
		return ErrHostStarted
	}
	if _, exists := host.components[id]; exists {
		return fmt.Errorf("%w: %q", ErrDuplicateComponent, id)
	}
	host.components[id] = &componentState{
		component: registration.Component,
		health: ComponentHealth{
			ID:     id,
			Status: ComponentRegistered,
			Policy: registration.Policy,
		},
	}
	host.order = append(host.order, id)
	return nil
}

// Start launches one isolated worker for each registration. All states are
// prepared before any worker is launched, so concurrent Stop sees a complete
// lifecycle set.
func (host *Host) Start(parent context.Context) error {
	if host == nil {
		return fmt.Errorf("runtime host is required")
	}
	if parent == nil {
		return fmt.Errorf("runtime host parent context is required")
	}
	if err := parent.Err(); err != nil {
		return err
	}

	host.mu.Lock()
	if host.status != HostCreated {
		host.mu.Unlock()
		return ErrHostStarted
	}
	workerContext, cancel := context.WithCancel(parent)
	host.cancel = cancel
	host.status = HostRunning
	states := make([]*componentState, 0, len(host.order))
	for _, id := range host.order {
		state := host.components[id]
		state.done = make(chan struct{})
		state.health.Status = ComponentStarting
		states = append(states, state)
	}
	host.activeWorkers = len(states)
	host.mu.Unlock()

	for _, state := range states {
		go host.runComponent(workerContext, state)
	}
	return nil
}

// Stop cancels every worker and waits until each exits or the caller's context
// expires. A timed-out Stop can be retried; no extra waiter goroutine is left
// behind.
func (host *Host) Stop(ctx context.Context) error {
	if host == nil {
		return fmt.Errorf("runtime host is required")
	}
	if ctx == nil {
		return fmt.Errorf("runtime host stop context is required")
	}

	host.mu.Lock()
	switch host.status {
	case HostCreated:
		host.status = HostStopped
		for _, state := range host.components {
			state.health.Status = ComponentStopped
		}
		host.mu.Unlock()
		return nil
	case HostStopped:
		// A worker marks the host stopped immediately before closing its done
		// channel. Still collect and await channels so Stop means every worker
		// has fully returned.
	case HostRunning:
		host.status = HostStopping
		if host.cancel != nil {
			host.cancel()
		}
	}
	done := make([]<-chan struct{}, 0, len(host.order))
	for _, id := range host.order {
		if channel := host.components[id].done; channel != nil {
			done = append(done, channel)
		}
	}
	host.mu.Unlock()

	for _, channel := range done {
		select {
		case <-channel:
			continue
		default:
		}
		select {
		case <-channel:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	host.mu.Lock()
	host.status = HostStopped
	host.mu.Unlock()
	return nil
}

// Snapshot returns a copy of current health sorted by component ID.
func (host *Host) Snapshot() Snapshot {
	if host == nil {
		return Snapshot{Status: HostStopped}
	}
	host.mu.RLock()
	snapshot := Snapshot{
		Status:     host.status,
		Components: make([]ComponentHealth, 0, len(host.components)),
	}
	for _, state := range host.components {
		snapshot.Components = append(snapshot.Components, state.health)
	}
	host.mu.RUnlock()
	slices.SortFunc(snapshot.Components, func(left, right ComponentHealth) int {
		return stringCompare(string(left.ID), string(right.ID))
	})
	return snapshot
}

func (host *Host) runComponent(ctx context.Context, state *componentState) {
	defer host.finishComponent(state)

	policy := state.health.Policy
	nextCadence := host.clock.Now().Add(policy.Cadence)
	if !policy.Immediate {
		due := host.jittered(nextCadence, policy.Jitter)
		nextCadence = nextCadence.Add(policy.Cadence)
		host.setNextRun(state, due)
		if !host.waitUntil(ctx, due) {
			return
		}
	}

	for {
		if ctx.Err() != nil {
			return
		}
		host.runOnce(ctx, state)
		if ctx.Err() != nil {
			return
		}

		now := host.clock.Now()
		cadenceDue := consumeCadence(now, &nextCadence, policy.Cadence)
		due := host.jittered(cadenceDue, policy.Jitter)
		failures := host.consecutiveFailures(state)
		if delay := policy.FailureBackoff.delay(failures); delay > 0 {
			backoffDue := now.Add(delay)
			if backoffDue.After(due) {
				due = backoffDue
			}
		}
		host.setNextRun(state, due)
		if !host.waitUntil(ctx, due) {
			return
		}
	}
}

// consumeCadence models a fixed-rate ticker with a one-element coalescing
// buffer: if one or many cadence boundaries passed during a run, exactly one
// immediate catch-up is scheduled and older boundaries are dropped.
func consumeCadence(now time.Time, next *time.Time, cadence time.Duration) time.Time {
	if next.After(now) {
		due := *next
		*next = next.Add(cadence)
		return due
	}
	elapsed := now.Sub(*next)
	delta := cadence - elapsed%cadence
	*next = now.Add(delta)
	return now
}

func (host *Host) jittered(base time.Time, maximum time.Duration) time.Time {
	if maximum <= 0 {
		return base
	}
	delay := host.jitter(maximum)
	if delay < 0 {
		delay = 0
	}
	if delay > maximum {
		delay = maximum
	}
	return base.Add(delay)
}

func (host *Host) waitUntil(ctx context.Context, due time.Time) bool {
	delay := due.Sub(host.clock.Now())
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := host.clock.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C():
		return ctx.Err() == nil
	}
}

func (host *Host) runOnce(ctx context.Context, state *componentState) {
	started := host.clock.Now()
	host.beginRun(state, started)

	runContext := ctx
	cancel := func() {}
	if state.health.Policy.Timeout > 0 {
		runContext, cancel = host.clock.WithTimeout(ctx, state.health.Policy.Timeout)
	}
	panicStack, err := invokeComponent(runContext, state.component, started)
	timedOut := errors.Is(context.Cause(runContext), context.DeadlineExceeded)
	cancel()
	if timedOut && err == nil {
		err = context.DeadlineExceeded
	}
	failure := host.finishRun(ctx, state, host.clock.Now(), err, timedOut, panicStack != nil)
	if failure == nil {
		return
	}

	attributes := []any{
		"component", failure.componentID,
		"err", failure.err,
		"consecutive_failures", failure.consecutive,
	}
	if panicStack != nil {
		attributes = append(attributes, "panic_stack", string(panicStack))
	}
	host.logger.Warn("runtime component pass failed", attributes...)
}

func (host *Host) beginRun(state *componentState, started time.Time) {
	host.mu.Lock()
	defer host.mu.Unlock()
	state.health.InFlight = true
	state.health.Runs++
	state.health.LastStartedAt = started
	state.health.NextRunAt = time.Time{}
}

type componentFailure struct {
	componentID ComponentID
	err         error
	consecutive uint64
}

func (host *Host) finishRun(ctx context.Context, state *componentState, finished time.Time, err error, timedOut, panicked bool) *componentFailure {
	host.mu.Lock()
	defer host.mu.Unlock()
	state.health.InFlight = false
	state.health.LastFinishedAt = finished
	if err == nil {
		recordSuccess(&state.health, finished)
		return nil
	}
	if ctx.Err() != nil && !panicked {
		// Lifecycle cancellation is not component degradation.
		return nil
	}
	recordFailure(&state.health, finished, err, timedOut, panicked)
	return &componentFailure{componentID: state.health.ID, err: err, consecutive: state.health.ConsecutiveFailures}
}

func recordSuccess(health *ComponentHealth, finished time.Time) {
	health.Status = ComponentHealthy
	health.Successes++
	health.ConsecutiveFailures = 0
	health.LastSuccessAt = finished
	health.LastError = ""
}

func recordFailure(health *ComponentHealth, finished time.Time, err error, timedOut, panicked bool) {
	health.Status = ComponentDegraded
	health.Failures++
	health.ConsecutiveFailures++
	health.LastFailureAt = finished
	health.LastError = err.Error()
	if timedOut {
		health.Timeouts++
	}
	if panicked {
		health.Panics++
	}
}

func invokeComponent(ctx context.Context, component Component, now time.Time) (panicStack []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("component panic: %v", recovered)
			panicStack = debug.Stack()
		}
	}()
	return nil, component.RunOnce(ctx, now)
}

func (host *Host) setNextRun(state *componentState, due time.Time) {
	host.mu.Lock()
	state.health.NextRunAt = due
	host.mu.Unlock()
}

func (host *Host) consecutiveFailures(state *componentState) uint64 {
	host.mu.RLock()
	defer host.mu.RUnlock()
	return state.health.ConsecutiveFailures
}

func (host *Host) finishComponent(state *componentState) {
	host.mu.Lock()
	state.health.Status = ComponentStopped
	state.health.InFlight = false
	state.health.NextRunAt = time.Time{}
	host.activeWorkers--
	if host.activeWorkers == 0 && host.status != HostCreated {
		host.status = HostStopped
	}
	done := state.done
	host.mu.Unlock()
	close(done)
}

func stringCompare(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
