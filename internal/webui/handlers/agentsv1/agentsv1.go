// Package agentsv1 serves the Agent API over REST (design v2 §9.1). One
// route module serves every harness; it never talks to a harness itself.
package agentsv1

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/loomagent"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Handler routes Agent API calls to the workspace's loomagent service.
type Handler struct {
	services      func(ws string) *loomagent.Service
	presets       loomagent.Presets
	validateToken func(token, workspace string) (string, error)
	tokens        *Tokens
	github        GitHubReader
}

// New returns a Handler. services returns the workspace's Agent API service,
// or nil when the workspace has none; presets nil uses the built-in presets.
func New(services func(ws string) *loomagent.Service, presets loomagent.Presets) *Handler {
	if presets == nil {
		presets = loomagent.BuiltinPresets{}
	}
	return &Handler{services: services, presets: presets}
}

// WithTokens sets the bridge and daemon tokens the routes accept; with none,
// every bridge token is refused.
func (h *Handler) WithTokens(t *Tokens) *Handler {
	h.tokens = t
	return h
}

type route func(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error)

// Register adds every route to the outer mux, each wrapped in the workspace
// middleware. PATCH must not go through the nested workspace mux.
// validateToken checks the event stream's one-time token (the webui SSE
// token from GET /api/workspaces/{ws}/events/token); nil is open mode, where
// the stream, like every route, needs no token.
func (h *Handler) Register(mux *http.ServeMux, workspace middleware.Middleware,
	validateToken func(token, workspace string) (string, error)) {
	h.validateToken = validateToken
	const p = "/api/workspaces/{ws}/v1/"
	mux.Handle("GET "+p+"events", workspace(http.HandlerFunc(h.stream)))
	for pattern, fn := range map[string]route{
		"POST " + p + "agents":                         h.create,
		"GET " + p + "agents":                          list,
		"GET " + p + "agents/{id}":                     get,
		"PATCH " + p + "agents/{id}":                   update,
		"DELETE " + p + "agents/{id}":                  del,
		"POST " + p + "agents/{id}/archive":            archive,
		"POST " + p + "agents/{id}/unarchive":          unarchive,
		"POST " + p + "agents/{id}/messages":           send,
		"DELETE " + p + "agents/{id}/messages/waiting": withdraw,
		"POST " + p + "agents/{id}/asks/{askId}":       respond,
		"GET " + p + "agents/{id}/events":              listEvents,
		"GET " + p + "presets":                         h.listPresets,
		"GET " + p + "presets/{name}":                  h.getPreset,
		"GET " + p + "harnesses/{harness}/models":      h.listModels,
		"POST " + p + "github/read":                    h.githubRead,
	} {
		mux.Handle(pattern, workspace(h.serve(fn)))
	}
}

func (h *Handler) serve(fn route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := h.services(middleware.WorkspaceFromContext(r.Context()))
		if s == nil {
			handler.RespondError(w, http.StatusNotFound, "agent API not available in this workspace")
			return
		}
		c, ok := h.caller(r, s)
		if !ok {
			handler.RespondError(w, http.StatusUnauthorized, "invalid agent token")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), callerKey{}, c))
		var (
			status int
			body   any
			err    error
		)
		if id := r.PathValue("id"); c.Kind == "agent" && id != "" {
			err = ownChild(r.Context(), s, c.ID, id)
		}
		if err == nil {
			status, body, err = fn(w, r, s)
		}
		switch {
		case err != nil:
			writeError(w, err)
		case body == nil:
			w.WriteHeader(http.StatusNoContent)
		default:
			handler.WriteJSON(w, status, body)
		}
	})
}

// envelope reads the JSON body (capped at handler.MaxRequestBody) into dst
// when there is one, and returns the RequestID from Idempotency-Key.
func envelope(w http.ResponseWriter, r *http.Request, dst any) (string, error) {
	if r.ContentLength != 0 {
		if err := handler.ReadJSON(w, r, dst); err != nil {
			return "", err
		}
	}
	return r.Header.Get("Idempotency-Key"), nil
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	var body CreateBody
	id, err := envelope(w, r, &body)
	if err != nil {
		return 0, nil, err
	}
	req := body.request()
	req.RequestID, req.Actor = id, actor(r)
	if req.Actor.Kind == "agent" {
		req.Parent = req.Actor.ID
	}
	a, err := s.Create(r.Context(), req)
	return http.StatusCreated, agentOut(a), err
}

func list(_ http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	q := r.URL.Query()
	limit, err := intParam(q.Get("limit"))
	if err != nil {
		return 0, nil, err
	}
	parent := q.Get("parent")
	if c := actor(r); c.Kind == "agent" {
		parent = c.ID
	}
	agents, next, err := s.List(r.Context(), loomstore.AgentFilter{
		WorkspaceID: middleware.WorkspaceFromContext(r.Context()),
		OwnerKind:   q.Get("owner_kind"), OwnerID: q.Get("owner_id"), Parent: parent,
		Root: q.Get("root"), Preset: q.Get("preset"), Mode: q.Get("mode"), Harness: q.Get("harness"),
		RoleKind: q.Get("role_kind"), State: q.Get("state"), SubjectType: q.Get("subject_type"),
		SubjectID: q.Get("subject_id"), ExternalKeyPrefix: q.Get("external_key_prefix"), Name: q.Get("name"),
		IncludeArchived: q.Get("include_archived") == "true", After: q.Get("after"), Limit: int(limit),
	})
	out := []Agent{}
	for _, a := range agents {
		out = append(out, agentOut(a))
	}
	return http.StatusOK, AgentList{Agents: out, Next: next}, err
}

func get(_ http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	a, err := s.Get(r.Context(), r.PathValue("id"))
	return http.StatusOK, agentOut(a), err
}

func update(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	var body UpdateBody
	id, err := envelope(w, r, &body)
	if err != nil {
		return 0, nil, err
	}
	opts, err := options(body.Options)
	if err != nil {
		return 0, nil, err
	}
	a, err := s.Update(r.Context(), loomagent.UpdateRequest{
		Envelope: loomagent.Envelope{RequestID: id, Expect: body.Expect.expect()},
		AgentID:  r.PathValue("id"), Name: body.Name, Model: body.Model, Harness: body.Harness,
		Effort: body.Effort, Options: opts,
	})
	return http.StatusOK, agentOut(a), err
}

func del(_ http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	q := r.URL.Query()
	return http.StatusNoContent, nil, s.Delete(r.Context(), loomagent.DeleteRequest{
		Envelope: loomagent.Envelope{RequestID: r.Header.Get("Idempotency-Key")},
		AgentID:  r.PathValue("id"), Cascade: q.Get("cascade") == "true", Fingerprint: q.Get("fingerprint"),
	})
}

func archive(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	return archiving(w, r, s.Archive)
}

func unarchive(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	return archiving(w, r, s.Unarchive)
}

func archiving(w http.ResponseWriter, r *http.Request, op func(context.Context, loomagent.ArchiveRequest) error) (int, any, error) {
	var body ArchiveBody
	id, err := envelope(w, r, &body)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, op(r.Context(), loomagent.ArchiveRequest{
		Envelope: loomagent.Envelope{RequestID: id}, AgentID: r.PathValue("id"), Reason: body.Reason})
}

func send(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	var body SendBody
	id, err := envelope(w, r, &body)
	if err != nil {
		return 0, nil, err
	}
	res, err := s.Send(r.Context(), loomagent.SendRequest{Envelope: loomagent.Envelope{RequestID: id},
		AgentID: r.PathValue("id"), Text: body.Text, Source: "user_chat", Delivery: body.Delivery, Actor: actor(r)})
	return http.StatusAccepted, SendResult{res.MessageID, res.State, res.Replaced, res.TurnID, res.Interrupted}, err
}

func withdraw(_ http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	res, err := s.Withdraw(r.Context(), loomagent.WithdrawRequest{
		Envelope: loomagent.Envelope{RequestID: r.Header.Get("Idempotency-Key")},
		AgentID:  r.PathValue("id"), Actor: actor(r),
	})
	return http.StatusOK, WithdrawResult{res.Result}, err
}

func respond(w http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	var body RespondBody
	id, err := envelope(w, r, &body)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusNoContent, nil, s.Respond(r.Context(), loomagent.RespondRequest{
		Envelope: loomagent.Envelope{RequestID: id}, AgentID: r.PathValue("id"), AskID: r.PathValue("askId"),
		Decision: body.Decision, Answer: body.Answer})
}

func listEvents(_ http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	q := r.URL.Query()
	var n [3]int64
	for i, k := range []string{"after", "snapshot", "limit"} {
		v, err := intParam(q.Get(k))
		if err != nil {
			return 0, nil, err
		}
		n[i] = v
	}
	page, err := s.ListEvents(r.Context(), loomstore.EventQuery{AgentID: r.PathValue("id"),
		After: n[0], Snapshot: n[1], Limit: int(n[2]), Kinds: handler.ParseArrayParam(q, "kind")})
	return http.StatusOK, eventPageOut(page), err
}

func (h *Handler) listPresets(_ http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	ps, err := h.presets.List(r.Context())
	out := []Preset{}
	for _, p := range ps {
		out = append(out, presetOut(p, s.Wired()))
	}
	return http.StatusOK, PresetList{Presets: out}, err
}

func (h *Handler) getPreset(_ http.ResponseWriter, r *http.Request, s *loomagent.Service) (int, any, error) {
	p, err := h.presets.Get(r.Context(), r.PathValue("name"))
	return http.StatusOK, presetOut(p, s.Wired()), err
}

func intParam(v string) (int64, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, service.ErrValidation("invalid number: " + v)
	}
	return n, nil
}

// statusOf maps each public code (design v2 §12.1) to its HTTP status.
var statusOf = map[loomagent.Code]int{
	loomagent.CodeAgentNotFound:       http.StatusNotFound,
	loomagent.CodePresetNotFound:      http.StatusNotFound,
	loomagent.CodeAskNotFound:         http.StatusNotFound,
	loomagent.CodeAgentNameTaken:      http.StatusConflict,
	loomagent.CodeAgentArchived:       http.StatusConflict,
	loomagent.CodeAgentBusy:           http.StatusConflict,
	loomagent.CodeChildrenLive:        http.StatusConflict,
	loomagent.CodeSpecVersionMismatch: http.StatusConflict,
	loomagent.CodeExternalKeyConflict: http.StatusConflict,
	loomagent.CodeExternalKeyTaken:    http.StatusConflict,
	loomagent.CodeWorktreeTaken:       http.StatusConflict,
	loomagent.CodeUnsavedWork:         http.StatusConflict,
	loomagent.CodeStaleSubject:        http.StatusConflict,
	loomagent.CodeSubscriberLagged:    http.StatusConflict,
	loomagent.CodePresetInvalid:       http.StatusBadRequest,
	loomagent.CodeHarnessUnavailable:  http.StatusServiceUnavailable,
	loomagent.CodeHarnessError:        http.StatusBadGateway,
	loomagent.CodeGitFailed:           http.StatusBadGateway,
	loomagent.CodeHistoryExpired:      http.StatusGone,
	loomagent.CodeCursorExpired:       http.StatusGone,
	CodeGitHubInvalid:                 http.StatusBadRequest,
	CodeGitHubDenied:                  http.StatusForbidden,
	CodeGitHubNotFound:                http.StatusNotFound,
	CodeGitHubRateLimited:             http.StatusTooManyRequests,
	CodeGitHubUnavailable:             http.StatusServiceUnavailable,
}

// writeError writes a loomagent error as {error, code, allowed, paths,
// fingerprint}; any other error goes to the shared webui error writer.
func writeError(w http.ResponseWriter, err error) {
	var e *loomagent.Error
	if !errors.As(err, &e) {
		handler.HandleServiceError(w, err)
		return
	}
	status, ok := statusOf[e.Code]
	if !ok {
		status = http.StatusInternalServerError
	}
	handler.WriteJSON(w, status, Error{e.Message, e.Code, e.Allowed, e.Paths, e.Fingerprint})
}
