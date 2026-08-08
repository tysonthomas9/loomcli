// Package connectorsproviders holds the egress provider adapters for step-7
// connectors: the Provider interface and registry plus the shared structured
// errors every adapter maps upstream failures into (provider.go), and the
// per-source implementations (github.go, ...).
//
// Placement rules baked into this package:
//
//   - Credentials travel only in CallSpec.Credential (plaintext, unsealed by
//     the dispatch layer, stack-only) and leave the process only inside the
//     Authorization header. Upstream response text is sanitized before it can
//     reach an error string or audit summary, so a provider echoing the token
//     back never leaks it into journals or logs.
//   - No retry loops live here. Retryability is signaled upward through
//     Retryable; the workflow/task-retry machinery owns retries because only
//     it can guarantee the idempotent-or-fenced contract.
//   - Wire shapes (CallSpec.Args keys, CallResult.Body keys) are camelCase;
//     adapters translate to each upstream API's native field names.
package connectorsproviders

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	connectorsmodule "github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

// Preconditions carries the freshness assertions an egress call must hold.
// The dispatch layer (CV8) refuses irreversible actions missing their
// registered fields with decision precondition_required; providers enforce
// the same contract again as defense in depth.
type Preconditions = connectorsmodule.DispatchPreconditions

// CallSpec is the Connectors-owned egress call shape. Keeping the alias here
// makes provider code readable without introducing a credential-bearing
// translation model.
type CallSpec = connectorsmodule.ProviderCall

// CallResult is the Connectors-owned sanitized provider result.
type CallResult = connectorsmodule.ProviderResult

// Provider is the Connectors-owned egress port.
type Provider = connectorsmodule.Provider

// Registry maps source kinds to their Provider adapters.
type Registry struct {
	mu        sync.RWMutex
	providers map[connectorsmodule.ConnectorSourceKind]Provider
}

var _ connectorsmodule.ProviderRegistry = (*Registry)(nil)

// NewRegistry returns an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[connectorsmodule.ConnectorSourceKind]Provider)}
}

// Register installs p as the adapter for kind. Unknown kinds and nil
// providers wrap the Connectors owner error; duplicate registrations use the
// owner's replay-safe collision category.
func (r *Registry) Register(kind connectorsmodule.ConnectorSourceKind, p Provider) error {
	if !kind.Valid() {
		return fmt.Errorf("providers: source kind %q unknown: %w", kind, connectorsmodule.ErrInvalid)
	}
	if p == nil {
		return fmt.Errorf("providers: nil provider for %q: %w", kind, connectorsmodule.ErrInvalid)
	}
	if r == nil {
		return fmt.Errorf("providers: registry unavailable: %w", connectorsmodule.ErrUnavailable)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers == nil {
		r.providers = make(map[connectorsmodule.ConnectorSourceKind]Provider)
	}
	if _, ok := r.providers[kind]; ok {
		return fmt.Errorf("providers: provider for %q already registered: %w", kind, connectorsmodule.ErrAlreadyExists)
	}
	r.providers[kind] = p
	return nil
}

// Get returns the adapter for kind, wrapping the owner error when none is
// registered.
func (r *Registry) Get(kind connectorsmodule.ConnectorSourceKind) (Provider, error) {
	if r == nil {
		return nil, fmt.Errorf("providers: registry unavailable: %w", connectorsmodule.ErrUnavailable)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[kind]
	if !ok {
		return nil, fmt.Errorf("providers: no provider for %q: %w", kind, connectorsmodule.ErrNotFound)
	}
	return p, nil
}

// Upstream error classes recorded in ConnectorCallRecord.ErrorClass.
const (
	// ClassClientError: upstream rejected the request (4xx other than
	// rate limits and freshness conflicts). Not retryable.
	ClassClientError = "client_error"
	// ClassServerError: upstream failed (5xx). Retryable.
	ClassServerError = "server_error"
	// ClassNetwork: the request never produced an HTTP response. Retryable.
	ClassNetwork = "network"
)

// Structured provider errors. Each wraps a sentinel for errors.Is matching
// and exposes its audit decision; none ever contains credential material.
var (
	// ErrUnknownAction indicates the provider does not implement the
	// requested action. Wraps the Connectors owner error.
	ErrUnknownAction = fmt.Errorf("providers: unknown action: %w", connectorsmodule.ErrInvalid)

	// ErrUpstream is the generic sentinel under RateLimited and
	// UpstreamError so callers can match "egress reached the provider and
	// failed" without caring which way.
	ErrUpstream = errors.New("providers: upstream call failed")
)

// StaleSubject indicates the call's subject moved relative to its freshness
// precondition: either the pre-egress liveness read disagreed (vet A1, write
// never issued) or the provider's native server-side precondition rejected
// the write (vet A2, e.g. GitHub merge 409). errors.Is matches the Connectors
// owner conflict category.
type StaleSubject struct {
	Action   string
	Resource string
	// Expected is the precondition value the caller pinned (sha/revision).
	Expected string
	// Reason is a redaction-safe explanation, e.g. "pull request not open".
	Reason string
}

// Error implements error with identifiers only — never payloads.
func (e *StaleSubject) Error() string {
	return fmt.Sprintf("providers: stale subject for %s on %q (expected %s): %s",
		e.Action, e.Resource, e.Expected, e.Reason)
}

// Unwrap matches the Connectors owner conflict category via errors.Is.
func (e *StaleSubject) Unwrap() error { return connectorsmodule.ErrConflict }

func (e *StaleSubject) ConnectorFailure() connectorsmodule.DispatchFailure {
	return connectorsmodule.DispatchFailure{Kind: connectorsmodule.DispatchFailureStaleSubject}
}

// PreconditionRequired is owner policy, reused by providers for defense in
// depth instead of maintaining an adapter-local copy.
type PreconditionRequired = connectorsmodule.PreconditionRequired

// RateLimited indicates the provider throttled the call. Always retryable —
// by the task-retry machinery above, never by the provider itself.
type RateLimited struct {
	Action string
	Status int
	// RetryAfter is the provider-suggested wait, zero when unspecified.
	RetryAfter time.Duration
}

// Error implements error.
func (e *RateLimited) Error() string {
	return fmt.Sprintf("providers: %s rate limited by upstream (status %d)", e.Action, e.Status)
}

// Unwrap matches ErrUpstream via errors.Is.
func (e *RateLimited) Unwrap() error { return ErrUpstream }

// Retryable reports true: rate limits are always worth retrying later.
func (e *RateLimited) Retryable() bool { return true }

func (e *RateLimited) ConnectorFailure() connectorsmodule.DispatchFailure {
	return connectorsmodule.DispatchFailure{
		Kind: connectorsmodule.DispatchFailureRateLimited, Retryable: true, ErrorClass: "rate_limited",
	}
}

// UpstreamError indicates the provider call failed for a non-freshness,
// non-rate-limit reason. Summary is pre-sanitized: credential material is
// stripped before construction.
type UpstreamError struct {
	Action string
	// Class is one of ClassClientError, ClassServerError, ClassNetwork.
	Class  string
	Status int
	// Summary is a short, sanitized upstream message (may be empty).
	Summary string
}

// Error implements error using only sanitized fields.
func (e *UpstreamError) Error() string {
	if e.Summary == "" {
		return fmt.Sprintf("providers: %s upstream %s (status %d)", e.Action, e.Class, e.Status)
	}
	return fmt.Sprintf("providers: %s upstream %s (status %d): %s", e.Action, e.Class, e.Status, e.Summary)
}

// Unwrap matches ErrUpstream via errors.Is.
func (e *UpstreamError) Unwrap() error { return ErrUpstream }

// Retryable reports whether the failure class is transient: server errors
// and network failures are, client errors are not.
func (e *UpstreamError) Retryable() bool {
	return e.Class == ClassServerError || e.Class == ClassNetwork
}

func (e *UpstreamError) ConnectorFailure() connectorsmodule.DispatchFailure {
	return connectorsmodule.DispatchFailure{
		Kind: connectorsmodule.DispatchFailureUpstream, Retryable: e.Retryable(), ErrorClass: e.Class,
	}
}

// Retryable reports whether err (anywhere in its chain) signals a transient
// failure the retry machinery may safely re-attempt under the same
// idempotency key.
func Retryable(err error) bool {
	for err != nil {
		if r, ok := err.(interface{ Retryable() bool }); ok && r.Retryable() {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}

// DecisionForError maps a provider error to its connector-call audit
// decision. It covers only errors produced by this package; anything else
// (including argument-validation failures) classifies as upstream_error so
// the journal always records a valid decision.
func DecisionForError(err error) connectorsmodule.ConnectorCallDecision {
	return connectorsmodule.DecisionForDispatchError(err)
}

// tokenPattern matches common credential shapes (bearer/token header echoes,
// GitHub token prefixes) so sanitization holds even when the exact
// credential string is unavailable or transformed.
var tokenPattern = regexp.MustCompile(`(?i)(bearer\s+\S+|token\s+\S+|gh[pousr]_[A-Za-z0-9_]{4,}|github_pat_[A-Za-z0-9_]{4,})`)

// maxSanitizedLen caps sanitized upstream messages so journals stay small.
const maxSanitizedLen = 160

// sanitizeUpstreamMessage strips credential material from upstream-provided
// text before it can reach an error string or audit summary: the literal
// credential is removed, token-shaped substrings are removed, control
// characters collapse to spaces, and the result is length-capped.
func sanitizeUpstreamMessage(msg, credential string) string {
	if credential != "" {
		msg = strings.ReplaceAll(msg, credential, "[redacted]")
	}
	msg = tokenPattern.ReplaceAllString(msg, "[redacted]")
	msg = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, msg)
	msg = strings.TrimSpace(msg)
	if len(msg) > maxSanitizedLen {
		msg = msg[:maxSanitizedLen] + "..."
	}
	return msg
}
