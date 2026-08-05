// Package httpapi exposes the local-only, one-time browser-session exchange
// used by the trusted Desktop launcher. It is a platform transport, not a
// Workflow Catalog product port.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const maxBrowserSessionRequestBytes = 4 << 10

type browserSessionBroker interface {
	MintLaunchCode(presentedBearer, serverDerivedWorkspace string) (authority.LocalBrowserLaunch, error)
	ExchangeLaunchCode(code, serverDerivedWorkspace string) (authority.LocalBrowserSession, error)
}

// Module owns only the trusted-launch and one-time exchange endpoints. It is
// composed in local/open mode and omitted entirely in external-auth mode.
type Module struct {
	broker               browserSessionBroker
	workspaceFromContext func(context.Context) string
}

func New(broker browserSessionBroker, workspaceFromContext func(context.Context) string) *Module {
	return &Module{broker: broker, workspaceFromContext: workspaceFromContext}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || mux == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/operator-sessions/launch", m.createLaunch)
	mux.HandleFunc("POST /api/workspaces/{ws}/operator-sessions/exchange", m.exchangeLaunch)
}

type launchResponse struct {
	LaunchCode string `json:"launch_code"`
	Workspace  string `json:"workspace"`
	ExpiresAt  string `json:"expires_at"`
}

func (m *Module) createLaunch(w http.ResponseWriter, r *http.Request) {
	setSensitiveResponseHeaders(w)
	workspace, ok := m.canonicalWorkspace(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "workspace is required")
		return
	}
	if m.broker == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "local operator sessions are unavailable")
		return
	}
	bearer := strings.TrimSpace(r.Header.Get("Authorization"))
	if bearer == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	launch, err := m.broker.MintLaunchCode(bearer, workspace)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, launchResponse{
		LaunchCode: launch.Code,
		Workspace:  launch.Workspace,
		ExpiresAt:  launch.ExpiresAt.UTC().Format("2006-01-02T15:04:05.000000000Z"),
	})
}

type exchangeRequest struct {
	LaunchCode string `json:"launch_code"`
}

type exchangeResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Workspace   string `json:"workspace"`
	ExpiresAt   string `json:"expires_at"`
}

func (m *Module) exchangeLaunch(w http.ResponseWriter, r *http.Request) {
	setSensitiveResponseHeaders(w)
	workspace, ok := m.canonicalWorkspace(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "workspace is required")
		return
	}
	if m.broker == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "local operator sessions are unavailable")
		return
	}
	request, err := decodeExchangeRequest(w, r)
	if err != nil || strings.TrimSpace(request.LaunchCode) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "a launch code is required")
		return
	}
	session, err := m.broker.ExchangeLaunchCode(request.LaunchCode, workspace)
	if err != nil {
		writeMappedError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, exchangeResponse{
		AccessToken: session.Bearer,
		TokenType:   "Bearer",
		Workspace:   session.Workspace,
		ExpiresAt:   session.ExpiresAt.UTC().Format("2006-01-02T15:04:05.000000000Z"),
	})
}

func (m *Module) canonicalWorkspace(r *http.Request) (string, bool) {
	if m == nil || r == nil || strings.TrimSpace(r.PathValue("ws")) == "" || m.workspaceFromContext == nil {
		return "", false
	}
	workspace := strings.TrimSpace(m.workspaceFromContext(r.Context()))
	return workspace, workspace != ""
}

func decodeExchangeRequest(w http.ResponseWriter, r *http.Request) (exchangeRequest, error) {
	if r == nil || r.Body == nil || r.Body == http.NoBody {
		return exchangeRequest{}, io.EOF
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBrowserSessionRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request exchangeRequest
	if err := decoder.Decode(&request); err != nil {
		return exchangeRequest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return exchangeRequest{}, errors.New("request body contains trailing content")
	}
	return request, nil
}

func setSensitiveResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func writeMappedError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, authority.ErrInvalidOperatorToken),
		errors.Is(err, authority.ErrPrincipalExpired),
		errors.Is(err, authority.ErrInvalidPrincipal):
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
	case errors.Is(err, authority.ErrWorkspaceMismatch),
		errors.Is(err, authority.ErrActionNotAllowed),
		errors.Is(err, authority.ErrAdmissionDenied):
		writeError(w, http.StatusForbidden, "forbidden", "forbidden")
	case errors.Is(err, authority.ErrInvalidScope):
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid local operator session request")
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
