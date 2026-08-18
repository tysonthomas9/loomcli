// Package fleethttp shares low-level HTTP plumbing between the two
// fleet-db client callers: internal/backend/fleet (IssueBackend
// implementation) and internal/infra/fleetdb (Store implementation).
// Both target the same fleet-db API and previously duplicated the
// request-building, auth-header, and error-extraction code.
//
// What's shared: header construction (Authorization / X-API-Key /
// X-Fleet-API-Key / X-Actor), request body marshaling, bounded HTTP 429 retry,
// and response error-message extraction.
//
// What's not shared: status→sentinel mapping. The two callers map
// errors to different domains (backend.BackendError vs
// domain.Err{NotFound,AlreadyExists,Invalid,Conflict}), so the
// classification stays local.
package fleethttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	maxAttempts       = 4
	initialBackoff    = 250 * time.Millisecond
	maxErrorBodyDrain = 64 << 10
)

// ErrRateLimited identifies an exhausted fleet-db HTTP 429 response after the
// shared transport has used its bounded retry budget.
var ErrRateLimited = errors.New("fleethttp: rate limited")

// EnvFleetDBActor is the env var holding the X-Actor header value.
const EnvFleetDBActor = "LOOM_FLEET_DB_ACTOR"

// EnvAgentName is the env var used to identify the current agent process.
const EnvAgentName = "LOOM_AGENT_NAME"

// ResolveFleetDBActor returns the X-Actor identity, preferring worker-specific
// environment over the configured process actor and OS user fallback. It lives
// here rather than in bootstrap so that transport-level callers can resolve an
// actor without taking a dependency on store construction.
func ResolveFleetDBActor(configuredActor string) string {
	if v := os.Getenv(EnvFleetDBActor); v != "" {
		return v
	}
	if v := os.Getenv(EnvAgentName); v != "" {
		return v
	}
	if configuredActor != "" {
		return configuredActor
	}
	return os.Getenv("USER")
}

// Doer is the subset of http.Client used by Do.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Auth holds the optional auth headers fleet-db accepts. Fields default
// to empty (omitted from the request when zero).
type Auth struct {
	BearerToken string //nolint:gosec // G117: auth header value intentionally carried by request config.
	APIKey      string //nolint:gosec // G117: fleet-db API key intentionally carried by request config.
	Actor       string // → X-Actor (used by --auth-dev-mode)
}

type actorContextKey struct{}

// WithActor returns a context that overrides X-Actor for FleetDB requests
// built from it. Callers must authenticate or otherwise authoritatively derive
// actor before attaching it; the override is request-scoped and takes
// precedence over a client's configured process actor.
func WithActor(ctx context.Context, actor string) context.Context {
	if actor == "" {
		return ctx
	}
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// ActorFromContext returns the request-scoped FleetDB actor, if one was set.
func ActorFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	actor, ok := ctx.Value(actorContextKey{}).(string)
	return actor, ok && actor != ""
}

// Apply writes the auth headers onto req. Empty fields are skipped.
func (a Auth) Apply(req *http.Request) {
	if a.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.BearerToken)
	}
	if a.APIKey != "" {
		req.Header.Set("X-API-Key", a.APIKey)
		req.Header.Set("X-Fleet-API-Key", a.APIKey)
	}
	if a.Actor != "" {
		req.Header.Set("X-Actor", a.Actor)
	}
}

// BuildJSONRequest constructs an HTTP request with JSON Content-Type
// and Accept headers and the supplied auth. body is JSON-marshaled
// when non-nil. Mutation requests keep the JSON Content-Type even with
// an empty body because fleet-db validates write request content types.
// Returns the request ready to be sent.
func BuildJSONRequest(ctx context.Context, method, url string, auth Auth, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if reader != nil || methodExpectsJSONContent(method) {
		req.Header.Set("Content-Type", "application/json")
	}
	auth.Apply(req)
	if actor, ok := ActorFromContext(ctx); ok {
		req.Header.Set("X-Actor", actor)
	}
	return req, nil
}

// Do sends a fleet-db request, retrying only HTTP 429 responses. It makes at
// most four attempts, honors Retry-After when present, and otherwise uses
// exponential backoff with jitter starting around 250ms. BuildJSONRequest
// produces replayable bodies, so rejected mutation requests are safe to retry.
func Do(client Doer, req *http.Request) (*http.Response, error) {
	current := req
	for attempt := 0; attempt < maxAttempts; attempt++ {
		resp, err := client.Do(current)
		if err != nil || resp.StatusCode != http.StatusTooManyRequests || attempt == maxAttempts-1 {
			return resp, err
		}
		if req.Body != nil && req.GetBody == nil {
			// A caller that did not provide a replayable body keeps the original
			// response; retrying with an empty mutation body would be incorrect.
			return resp, nil
		}

		delay := retryDelay(resp.Header.Get("Retry-After"), attempt)
		drainAndClose(resp.Body)
		if err := waitForRetry(req.Context(), delay); err != nil {
			return nil, err
		}

		current = req.Clone(req.Context())
		if req.Body != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("replay request body: %w", err)
			}
			current.Body = body
		}
	}
	panic("unreachable")
}

func retryDelay(retryAfter string, retry int) time.Duration {
	if delay, ok := parseRetryAfter(retryAfter); ok {
		return delay
	}
	backoff := initialBackoff << retry
	// Retry jitter is not security-sensitive. Keep the delay within
	// [0.5*backoff, 1.5*backoff) to avoid synchronized retry bursts.
	return backoff/2 + time.Duration(rand.Int64N(int64(backoff))) //nolint:gosec
}

func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := time.Until(when)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxErrorBodyDrain))
	_ = body.Close()
}

func methodExpectsJSONContent(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}

// ExtractErrorMessage parses fleet-db's error-envelope shapes and
// returns the human-readable message. Returns "" when the body is
// empty or matches neither shape.
//
// Shapes:
//   - Wrapper:    {"success":false,"error":"..."}
//   - Structured: {"error":{"code":"...","message":"..."}}
func ExtractErrorMessage(body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	// Wrapper.
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error != "" {
		return envelope.Error
	}
	// Structured.
	var structured struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &structured); err == nil && structured.Error.Message != "" {
		return structured.Error.Message
	}
	return ""
}
