package evals

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

type Module struct {
	svc service.EvalAdminService
}

func NewModule(svc service.EvalAdminService) *Module {
	return &Module{svc: svc}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || m.svc == nil {
		return
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/eval-rollup", HandleGetRollup(m.svc))
	mux.HandleFunc("GET /api/workspaces/{ws}/sessions/{sessionId}/eval", HandleGetSessionEval(m.svc))
	mux.HandleFunc("POST /api/workspaces/{ws}/sessions/{sessionId}/rejudge", HandleRejudgeSession(m.svc))
	mux.HandleFunc("GET /api/workspaces/{ws}/evals/cron", HandleGetCron(m.svc))
	mux.HandleFunc("PUT /api/workspaces/{ws}/evals/cron", HandlePutCron(m.svc))
}

type response struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

var rollupNow = func() time.Time { return time.Now().UTC() }

func HandleGetRollup(svc service.EvalAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts, err := parseRollupOptions(r)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		data, err := svc.GetRollup(r.Context(), middleware.WorkspaceFromContext(r.Context()), opts)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeSuccess(w, data)
	}
}

func HandleGetSessionEval(svc service.EvalAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := svc.GetSessionEvalState(r.Context(), middleware.WorkspaceFromContext(r.Context()), r.PathValue("sessionId"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeSuccess(w, data)
	}
}

func HandleRejudgeSession(svc service.EvalAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := svc.RejudgeSession(r.Context(), middleware.WorkspaceFromContext(r.Context()), r.PathValue("sessionId"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeSuccess(w, data)
	}
}

func HandleGetCron(svc service.EvalAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := svc.GetCron(r.Context(), middleware.WorkspaceFromContext(r.Context()))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeSuccess(w, data)
	}
}

type putCronRequest struct {
	Enabled *bool `json:"enabled"`
}

func HandlePutCron(svc service.EvalAdminService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, handler.MaxRequestBody)
		var req putCronRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				writeServiceError(w, service.ErrPayloadTooLarge("request body too large"))
				return
			}
			writeServiceError(w, service.ErrValidation("invalid request body"))
			return
		}
		if req.Enabled == nil {
			writeServiceError(w, service.ErrValidation("enabled is required"))
			return
		}
		data, err := svc.SetCronEnabled(r.Context(), middleware.WorkspaceFromContext(r.Context()), *req.Enabled)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeSuccess(w, data)
	}
}

func parseRollupOptions(r *http.Request) (service.EvalRollupOptions, error) {
	q := r.URL.Query()
	now := rollupNow().UTC()
	until := now
	if raw := strings.TrimSpace(q.Get("until")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return service.EvalRollupOptions{}, service.ErrValidation("invalid until: must be RFC3339")
		}
		until = parsed.UTC()
	}
	since := until.Add(-7 * 24 * time.Hour)
	if raw := strings.TrimSpace(q.Get("since")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return service.EvalRollupOptions{}, service.ErrValidation("invalid since: must be RFC3339")
		}
		since = parsed.UTC()
	}
	if since.After(until) {
		return service.EvalRollupOptions{}, service.ErrValidation("since must be before until")
	}
	return service.EvalRollupOptions{Since: since, Until: until}, nil
}

func writeSuccess(w http.ResponseWriter, data any) {
	handler.WriteJSON(w, http.StatusOK, response{Success: true, Data: data})
}

func writeServiceError(w http.ResponseWriter, err error) {
	var svcErr *service.ServiceError
	status := http.StatusInternalServerError
	msg := "internal server error"
	if errors.As(err, &svcErr) {
		status = handler.StatusForKind(svcErr.Kind)
		msg = svcErr.Message
	}
	handler.WriteJSON(w, status, response{Success: false, Error: msg})
}
