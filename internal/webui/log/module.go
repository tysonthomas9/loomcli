package log

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/webui/handlers/misc"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// Module registers the workspace-scoped log streaming routes on a
// [*http.ServeMux].
//
// The agent archive route is conditional on agentSvc being non-nil. The agent
// stream and task archive routes are always registered.
type Module struct {
	agentSvc  service.AgentService // may be nil — agent archive route skipped
	sseTokens *realtime.TokenStore // may be nil in open auth mode
}

// NewModule returns a Module. agentSvc may be nil, in which case the agent
// archive route is omitted. sseTokens may be nil in open auth mode.
func NewModule(agentSvc service.AgentService, sseTokens *realtime.TokenStore) *Module {
	return &Module{agentSvc: agentSvc, sseTokens: sseTokens}
}

// Register implements [Module] by registering the log routes.
func (m *Module) Register(mux *http.ServeMux) {
	// Agent archive — conditional on agentSvc availability.
	if m.agentSvc != nil {
		mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/logs", misc.HandleGetAgentLog(m.agentSvc))
	}
	mux.HandleFunc("GET /api/workspaces/{ws}/agents/{name}/logs/stream", m.handleAgentLogStream)

	// Task log routes — always registered (zero-parameter constructors)
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs", misc.HandleListTaskPhases())
	mux.HandleFunc("GET /api/workspaces/{ws}/tasks/{id}/logs/{phase}", misc.HandleGetTaskLog())
}

func (m *Module) handleAgentLogStream(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("ws")
	if m.sseTokens != nil {
		if _, err := m.sseTokens.Validate(r.URL.Query().Get("token"), workspaceID); err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired stream token")
			return
		}
	}

	agentName := r.PathValue("name")
	if !service.IsValidAgentName(agentName) {
		writeError(w, http.StatusBadRequest, "invalid agent name")
		return
	}

	start, err := parseStreamStart(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	logPath, err := GetAgentLogPath(workspaceID, agentName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid agent log path")
		return
	}
	workspaceLogDir, err := GetWorkspaceLogDir(workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve workspace log directory")
		return
	}
	if err := ValidatePathWithinDir(logPath, workspaceLogDir); err != nil {
		writeError(w, http.StatusBadRequest, "invalid agent log path")
		return
	}

	streamer, err := NewLogStreamer(logPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to open agent log stream")
		return
	}
	defer streamer.Close()

	_ = streamer.Stream(r.Context(), w, start)
}

func parseStreamStart(r *http.Request) (StreamStart, error) {
	query := r.URL.Query()
	if query.Has("offset") {
		offset, err := parseNonNegativeInt64(query.Get("offset"), "offset")
		if err != nil {
			return StreamStart{}, err
		}
		return StreamStart{Offset: &offset}, nil
	}
	if query.Has("tail_bytes") {
		tailBytes, err := parseNonNegativeInt64(query.Get("tail_bytes"), "tail_bytes")
		if err != nil {
			return StreamStart{}, err
		}
		return StreamStart{TailBytes: &tailBytes}, nil
	}
	return StreamStart{}, nil
}

func parseNonNegativeInt64(raw, name string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, &streamParamError{name: name}
	}
	return value, nil
}

type streamParamError struct {
	name string
}

func (e *streamParamError) Error() string {
	return "invalid " + e.name + ": must be a non-negative integer"
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
