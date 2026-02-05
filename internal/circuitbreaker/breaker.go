// Package circuitbreaker implements the circuit breaker pattern for protecting
// against cascading failures when calling unreliable services.
package circuitbreaker

import (
	"errors"
	"sync"
	"time"
)

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = iota // Normal operation — requests pass through
	StateOpen                  // Fast-fail — requests rejected immediately
	StateHalfOpen              // Probe — limited requests allowed to test recovery
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ErrCircuitOpen is returned when the circuit breaker is in the open state.
var ErrCircuitOpen = errors.New("circuit breaker open")

// Config configures a circuit breaker instance.
type Config struct {
	// FailureThreshold is the number of consecutive failures to trip the breaker.
	FailureThreshold int

	// OpenTimeout is how long the breaker stays open before transitioning to half-open.
	OpenTimeout time.Duration

	// HalfOpenMaxProbes is the number of concurrent probe requests allowed in half-open state.
	HalfOpenMaxProbes int

	// ShouldTrip classifies whether an error should count as a failure.
	// If nil, all non-nil errors are counted.
	ShouldTrip func(error) bool

	// OnStateChange is called when the breaker transitions between states.
	// Must not call back into the breaker (the lock is not held during the callback).
	OnStateChange func(from, to State)
}

// BreakerStats contains runtime statistics about the circuit breaker.
type BreakerStats struct {
	State           State     `json:"state"`
	Failures        int       `json:"failures"`
	Successes       int       `json:"successes"`
	ConsecutiveFail int       `json:"consecutive_failures"`
	LastStateChange time.Time `json:"last_state_change"`
	LastFailure     time.Time `json:"last_failure,omitempty"`
}

// Breaker implements the circuit breaker pattern with three states:
// Closed (normal), Open (fast-fail), and HalfOpen (probe).
type Breaker struct {
	name   string
	config Config

	mu              sync.Mutex
	state           State
	failures        int
	successes       int
	consecutiveFail int
	halfOpenProbes  int
	lastStateChange time.Time
	lastFailure     time.Time
	openedAt        time.Time
}

// NewBreaker creates a new circuit breaker with the given name and configuration.
func NewBreaker(name string, config Config) *Breaker {
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = 5
	}
	if config.OpenTimeout <= 0 {
		config.OpenTimeout = 30 * time.Second
	}
	if config.HalfOpenMaxProbes <= 0 {
		config.HalfOpenMaxProbes = 1
	}
	if config.ShouldTrip == nil {
		config.ShouldTrip = func(err error) bool { return err != nil }
	}

	return &Breaker{
		name:            name,
		config:          config,
		state:           StateClosed,
		lastStateChange: time.Now(),
	}
}

// Execute wraps an operation with circuit breaker logic.
// Returns ErrCircuitOpen if the breaker is open.
func (b *Breaker) Execute(fn func() error) error {
	if err := b.beforeRequest(); err != nil {
		return err
	}

	err := fn()
	b.afterRequest(err)
	return err
}

// ExecuteWithResult wraps an operation that returns a value with circuit breaker logic.
func ExecuteWithResult[T any](b *Breaker, fn func() (T, error)) (T, error) {
	var zero T
	if err := b.beforeRequest(); err != nil {
		return zero, err
	}

	result, err := fn()
	b.afterRequest(err)
	return result, err
}

// State returns the current breaker state, accounting for open timeout expiry.
func (b *Breaker) GetState() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.currentState()
}

// Stats returns runtime statistics about the breaker.
func (b *Breaker) Stats() BreakerStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BreakerStats{
		State:           b.currentState(),
		Failures:        b.failures,
		Successes:       b.successes,
		ConsecutiveFail: b.consecutiveFail,
		LastStateChange: b.lastStateChange,
		LastFailure:     b.lastFailure,
	}
}

// Reset manually resets the breaker to closed state.
func (b *Breaker) Reset() {
	b.mu.Lock()
	old := b.state
	b.state = StateClosed
	b.consecutiveFail = 0
	b.halfOpenProbes = 0
	b.lastStateChange = time.Now()
	cb := b.config.OnStateChange
	b.mu.Unlock()

	if old != StateClosed && cb != nil {
		cb(old, StateClosed)
	}
}

// currentState returns the effective state, checking if open timeout has expired.
// Must be called with mu held.
func (b *Breaker) currentState() State {
	if b.state == StateOpen && time.Since(b.openedAt) >= b.config.OpenTimeout {
		return StateHalfOpen
	}
	return b.state
}

// beforeRequest checks if the request should be allowed through.
func (b *Breaker) beforeRequest() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return nil
	case StateOpen:
		if time.Since(b.openedAt) >= b.config.OpenTimeout {
			b.setState(StateHalfOpen)
			b.halfOpenProbes = 1
			return nil
		}
		return ErrCircuitOpen
	case StateHalfOpen:
		if b.halfOpenProbes < b.config.HalfOpenMaxProbes {
			b.halfOpenProbes++
			return nil
		}
		return ErrCircuitOpen
	}
	return nil
}

// afterRequest records the result and potentially transitions state.
func (b *Breaker) afterRequest(err error) {
	var transition *[2]State

	b.mu.Lock()
	if err != nil && b.config.ShouldTrip(err) {
		b.failures++
		b.consecutiveFail++
		b.lastFailure = time.Now()

		switch b.state {
		case StateClosed:
			if b.consecutiveFail >= b.config.FailureThreshold {
				transition = &[2]State{StateClosed, StateOpen}
				b.setState(StateOpen)
				b.openedAt = time.Now()
			}
		case StateHalfOpen:
			transition = &[2]State{StateHalfOpen, StateOpen}
			b.setState(StateOpen)
			b.openedAt = time.Now()
			b.halfOpenProbes = 0
		}
	} else if err == nil {
		b.successes++

		switch b.state {
		case StateClosed:
			b.consecutiveFail = 0
		case StateHalfOpen:
			transition = &[2]State{StateHalfOpen, StateClosed}
			b.setState(StateClosed)
			b.consecutiveFail = 0
			b.halfOpenProbes = 0
		}
	}
	cb := b.config.OnStateChange
	b.mu.Unlock()

	if transition != nil && cb != nil {
		cb(transition[0], transition[1])
	}
}

// setState updates the state and records the transition time.
// Must be called with mu held.
func (b *Breaker) setState(s State) {
	b.state = s
	b.lastStateChange = time.Now()
}
