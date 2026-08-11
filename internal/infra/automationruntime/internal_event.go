package trigger

import (
	"context"
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

// InternalEvent is one system-originated journal event submitted to
// Automation's admitted event workflow.
type InternalEvent struct {
	EventID        string
	EventType      string
	Origin         automation.EventOrigin
	ParentEventID  string
	EmittedByRunID string
	SubjectRef     string
	ActorRef       string
	EpicID         string
	Payload        json.RawMessage
	SubjectAttrs   map[string]string
}

// InternalEmitResult is the journal producer's projection of Automation's
// admission result.
type InternalEmitResult struct {
	Dropped    bool
	DropReason string
	EventType  string
	RouteKey   string
	Origin     automation.EventOrigin
	HopDepth   int
}

// InternalEventEmitter is the journal bridge's consumer-owned admission port.
type InternalEventEmitter interface {
	Emit(context.Context, string, InternalEvent) (*InternalEmitResult, error)
}
