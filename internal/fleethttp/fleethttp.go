// Package fleethttp shares low-level HTTP plumbing between the two
// fleet-db client callers: internal/backend/fleet (IssueBackend
// implementation) and internal/infra/fleetdb (Store implementation).
// Both target the same fleet-db API and previously duplicated the
// request-building, auth-header, and error-extraction code.
//
// What's shared: header construction (Authorization / X-API-Key /
// X-Fleet-API-Key / X-Actor), request body marshaling, response error-message
// extraction.
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
	"fmt"
	"io"
	"net/http"
)

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
