// Package fake provides a channel-driven execplane.ExecutionPlane for
// tests: each Invoke returns a scripted stream of events.
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/tysonthomas9/loomcli/internal/workflows/execplane"
)

// Invocation records one Invoke call.
type Invocation struct {
	Agent      string
	InstanceID string
	Request    execplane.InvokeRequest
}

// Script decides the events returned for one invocation.
type Script func(inv Invocation) []execplane.Event

// IdleScript returns a minimal successful stream.
func IdleScript(events ...execplane.Event) Script {
	return func(Invocation) []execplane.Event {
		return append(events, execplane.Event{Type: execplane.EventIdle, Data: json.RawMessage(`{"type":"idle"}`)})
	}
}

// Plane is a fake ExecutionPlane.
type Plane struct {
	mu          sync.Mutex
	script      Script
	healthErr   error
	invocations []Invocation
}

// New returns a Plane that answers every invocation with script.
func New(script Script) *Plane { return &Plane{script: script} }

var _ execplane.ExecutionPlane = (*Plane)(nil)

// SetScript swaps the script (e.g. mid-test).
func (p *Plane) SetScript(s Script) {
	p.mu.Lock()
	p.script = s
	p.mu.Unlock()
}

// SetHealthErr makes Healthy return err.
func (p *Plane) SetHealthErr(err error) {
	p.mu.Lock()
	p.healthErr = err
	p.mu.Unlock()
}

// Invocations returns a copy of all recorded invocations.
func (p *Plane) Invocations() []Invocation {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Invocation(nil), p.invocations...)
}

func (p *Plane) Healthy(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.healthErr
}

func (p *Plane) Invoke(ctx context.Context, agent, instanceID string, req execplane.InvokeRequest) (execplane.StreamHandle, error) {
	p.mu.Lock()
	inv := Invocation{Agent: agent, InstanceID: instanceID, Request: req}
	p.invocations = append(p.invocations, inv)
	script := p.script
	p.mu.Unlock()

	if script == nil {
		return nil, fmt.Errorf("fake plane: no script configured")
	}
	events := script(inv)
	s := &stream{events: make(chan execplane.Event, len(events)+1), done: make(chan struct{})}
	go func() {
		defer close(s.events)
		for _, e := range events {
			select {
			case <-ctx.Done():
				s.err = ctx.Err()
				return
			case <-s.done:
				return
			case s.events <- e:
			}
		}
	}()
	return s, nil
}

type stream struct {
	events chan execplane.Event
	done   chan struct{}
	once   sync.Once
	err    error
}

func (s *stream) Events() <-chan execplane.Event { return s.events }
func (s *stream) Cancel()                        { s.once.Do(func() { close(s.done) }) }
func (s *stream) Err() error                     { return s.err }
