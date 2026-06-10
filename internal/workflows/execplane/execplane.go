// Package execplane defines Loom's interface to the execution plane —
// the Flue server that runs workflow agents. Loom only ever pushes
// direct streamed invocations (never Flue-side dispatch) so that it
// always owns the connection and can cancel by closing it.
//
// Implementations: flue.Client (HTTP/SSE against a Flue server) and
// fake.Plane (channel-driven, tests).
package execplane

import (
	"context"
	"encoding/json"
)

// Event is one parsed event from a Flue invocation stream.
//
// Flue agent SSE event types include: agent_start, turn_start,
// text_delta, thinking_*, tool_start, tool_call, turn, log, agent_end,
// idle (terminal success) and error (terminal failure). Data is the
// raw JSON payload of the frame.
type Event struct {
	Type string
	Data json.RawMessage
}

// Terminal event types.
const (
	EventIdle  = "idle"
	EventError = "error"
)

// IsTerminal reports whether the event ends the invocation stream.
func (e Event) IsTerminal() bool { return e.Type == EventIdle || e.Type == EventError }

// ErrorMessage extracts the error message from an EventError payload
// ({"error":{"type":...,"message":...}}). Empty for other events.
func (e Event) ErrorMessage() string {
	if e.Type != EventError {
		return ""
	}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(e.Data, &body); err != nil {
		return "unparseable error event"
	}
	if body.Error.Message != "" {
		return body.Error.Message
	}
	return body.Error.Type
}

// InvokeRequest is the payload for one agent invocation.
type InvokeRequest struct {
	// Message is the prompt delivered to the agent.
	Message string `json:"message"`
	// Session optionally names the session thread within the instance.
	Session string `json:"session,omitempty"`
}

// StreamHandle is an active invocation stream. The caller must drain
// Events until it closes (terminal event, transport error, or Cancel)
// and should check Err afterwards.
type StreamHandle interface {
	// Events delivers parsed events in order. Closed after a terminal
	// event, a transport error, or Cancel.
	Events() <-chan Event
	// Cancel closes the underlying connection. Idempotent. For Flue
	// this aborts the streamed invocation at the caller's side — the
	// documented cancellation lever.
	Cancel()
	// Err returns the transport error that ended the stream, nil for a
	// clean terminal event. Valid only after Events is closed.
	Err() error
}

// ExecutionPlane invokes agents on the execution plane.
type ExecutionPlane interface {
	// Invoke starts agent instance <agent>/<instanceID> with a streamed
	// invocation and returns the live event stream.
	Invoke(ctx context.Context, agent, instanceID string, req InvokeRequest) (StreamHandle, error)
	// Healthy reports whether the plane is reachable and serving.
	Healthy(ctx context.Context) error
}
