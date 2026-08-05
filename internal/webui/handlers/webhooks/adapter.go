// Package webhooks implements the inbound external-event ingestion surface for
// Loom's Automation capability.
//
// A single generic route, POST /api/workspaces/{ws}/webhooks/{name}, selects a
// source-specific Adapter by {name} (e.g. "github"). The adapter normalizes the
// request into a route key and source event id, then the named
// webhookingestion workflow verifies it and enters Automation admission.
//
// The webhook handler never executes workflow TypeScript; the existing Loom
// driver executor claims and runs the enqueued DriverRun asynchronously.
package webhooks

import "net/http"

// ErrBadRequest marks an adapter normalization failure that should surface as
// HTTP 400 (e.g. a missing required header). ErrUnverified marks a signature
// verification failure that should surface as HTTP 401.
type adapterError struct {
	status  int
	message string
}

func (e *adapterError) Error() string { return e.message }

func badRequest(message string) error {
	return &adapterError{status: http.StatusBadRequest, message: message}
}
func unverified(message string) error {
	return &adapterError{status: http.StatusUnauthorized, message: message}
}

// NormalizedEvent is the source-agnostic view of an inbound webhook that the
// dispatch path needs. Adapters never trust the payload beyond extracting this
// routing metadata; authenticity is established separately by Verify.
type NormalizedEvent struct {
	// RouteKey selects the TriggerBinding, e.g. "github.pull_request.opened".
	RouteKey string
	// EventType is the source event name recorded on the TriggerEvent.
	EventType string
	// SubjectRef identifies what the event is about (repo/PR/issue/branch).
	SubjectRef string
	// ActorRef identifies who caused the event, when available.
	ActorRef string
	// DeliveryID is the source-assigned unique delivery identifier. It seeds
	// both the idempotency key and the TriggerEvent's source_event_id, so
	// redelivering the same event does not create duplicate effects.
	DeliveryID string
	// SubjectAttrs carries adapter-extracted payload attributes (e.g.
	// "head_sha", "repo", "pr_number", "base_ref") consumed by server-side
	// subject_key_template rendering ("{{attrs.head_sha}}"). The adapter is
	// the single source of truth for these values: the router and template
	// renderer never re-parse the raw payload. Keys are only present when the
	// payload actually carries the value; the map is nil when none apply.
	SubjectAttrs map[string]string
}

// Adapter is the only source-specific code in the webhook path. Implementations
// are registered by Name and selected via the {name} URL segment.
type Adapter interface {
	// Name is the {name} path segment that selects this adapter.
	Name() string
	// Normalize parses the request headers and raw body into routing metadata.
	// It must not require the payload to be authentic — the named workflow's
	// verifier does that. It returns a *adapterError (via badRequest) for
	// malformed requests.
	Normalize(r *http.Request, body []byte) (NormalizedEvent, error)
	// PresentedSignature extracts only the caller-provided proof. Server-side
	// secret material stays inside CompatibilityVerifier and never enters the
	// webhookingestion workflow or Automation.
	PresentedSignature(r *http.Request) string
	// VerifySignature checks caller-provided proof against one secret resolved
	// inside CompatibilityVerifier. Implementations compare in constant time.
	VerifySignature(body []byte, presentedSignature, secret string) error
}

// registry maps an adapter name to its implementation.
type registry map[string]Adapter

func defaultRegistry() registry {
	return registry{githubAdapter{}.Name(): githubAdapter{}}
}
