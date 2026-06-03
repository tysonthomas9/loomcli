// Package sessionwrite holds the loom-serve handlers for the TaskRun session
// write path (PRD Phase C, docs/product/loom-typescript-sdk-spec.md): a flue
// runner reports artifacts / usage / logs and heartbeats its session via
// @loom/sdk instead of the LOOMRUNNER blob. Handlers persist through the
// existing control-plane stores; the workspace is resolved from the request
// context and the session id from the {sessionId} path value.
package sessionwrite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

const maxBodyBytes = 1 << 20 // 1 MiB — artifact/usage/log bodies are small metadata.

type envelope struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

func writeOK(w http.ResponseWriter, status int, data any) {
	handler.WriteJSON(w, status, envelope{Success: true, Data: data})
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	handler.WriteJSON(w, status, envelope{Success: false, Error: msg})
}

// scope returns the workspace (from context) and session id (from the path).
func scope(r *http.Request) (workspace, sessionID string) {
	return middleware.WorkspaceFromContext(r.Context()), r.PathValue("sessionId")
}

// statusForErr maps a store error to an HTTP status (404 for not-found, else 500).
func statusForErr(err error) int {
	if errors.Is(err, domain.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func decodeJSON(r *http.Request, v any) error {
	defer func() { _ = r.Body.Close() }()
	return json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes)).Decode(v)
}

func newArtifactID() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "art_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "art_" + hex.EncodeToString(b[:])
}

// idempotencyHeader carries a client-supplied key that makes an artifact write
// idempotent: a retried POST with the same key maps to the same artifact id, so
// no duplicate row is created (a transient 5xx + retry can't double-register).
const idempotencyHeader = "Idempotency-Key"

// artifactID returns the id for an artifact write — deterministic from the
// idempotency key (same key → same id) when provided, else random.
func artifactID(ws, sessionID, typ, idemKey string) string {
	if idemKey == "" {
		return newArtifactID()
	}
	sum := sha256.Sum256([]byte(ws + "|" + sessionID + "|" + typ + "|" + idemKey))
	return "art_" + hex.EncodeToString(sum[:10])
}

// artifactRecord is the ArtifactRecord response shape (mirrors openapi.yaml).
type artifactRecord struct {
	ArtifactID string `json:"artifact_id"`
	SessionID  string `json:"session_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	Type       string `json:"type"`
	URI        string `json:"uri"`
	Summary    string `json:"summary,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

func toArtifactRecord(a *domain.Artifact) artifactRecord {
	return artifactRecord{
		ArtifactID: a.ArtifactID,
		SessionID:  a.SessionID,
		TaskID:     a.TaskID,
		Type:       a.Type,
		URI:        a.URI,
		Summary:    a.Summary,
		CreatedAt:  a.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// HandlePostSessionArtifact registers a result artifact (patch/commit/log/…)
// against a TaskRun session. POST /api/workspaces/{ws}/sessions/{sessionId}/artifacts
func HandlePostSessionArtifact(sessions store.AgentSessionStore, artifacts store.ArtifactStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, sessionID := scope(r)
		var req struct {
			Type         string `json:"type"`
			URI          string `json:"uri"`
			Summary      string `json:"summary"`
			FilesChanged int    `json:"files_changed"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Type == "" || req.URI == "" {
			writeErr(w, http.StatusBadRequest, "type and uri are required")
			return
		}
		sess, err := sessions.Get(r.Context(), ws, sessionID)
		if err != nil {
			writeErr(w, statusForErr(err), "session not found")
			return
		}
		meta := map[string]string{}
		if req.FilesChanged > 0 {
			meta["files_changed"] = strconv.Itoa(req.FilesChanged)
		}
		id := artifactID(ws, sessionID, req.Type, r.Header.Get(idempotencyHeader))
		art, err := artifacts.Create(r.Context(), store.ArtifactCreate{
			WorkspaceKey: ws,
			ArtifactID:   id,
			AgentID:      sess.AgentID,
			SessionID:    sessionID,
			TaskID:       sess.TaskID,
			Type:         req.Type,
			URI:          req.URI,
			Summary:      req.Summary,
			Metadata:     meta,
		})
		if errors.Is(err, domain.ErrAlreadyExists) {
			// Idempotent retry: the deterministic id already exists — return it.
			if existing, getErr := artifacts.Get(r.Context(), ws, id); getErr == nil {
				writeOK(w, http.StatusCreated, toArtifactRecord(existing))
				return
			}
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not register artifact")
			return
		}
		writeOK(w, http.StatusCreated, toArtifactRecord(art))
	}
}

// HandleRecordSessionUsage records token usage as a typed "usage" Artifact on
// the session. We deliberately do NOT store usage in the session's metadata map:
// loom's session finalizer rewrites that map wholesale (AgentSessionUpdate
// replaces, not merges), which would clobber the usage keys. An Artifact is
// durable, queryable by session, and matches the domain's documented "usage"
// artifact type. POST /api/workspaces/{ws}/sessions/{sessionId}/usage
func HandleRecordSessionUsage(sessions store.AgentSessionStore, artifacts store.ArtifactStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, sessionID := scope(r)
		var req struct {
			InputTokens      int64 `json:"input_tokens"`
			OutputTokens     int64 `json:"output_tokens"`
			CacheReadTokens  int64 `json:"cache_read_tokens"`
			CacheWriteTokens int64 `json:"cache_write_tokens"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		sess, err := sessions.Get(r.Context(), ws, sessionID)
		if err != nil {
			writeErr(w, statusForErr(err), "session not found")
			return
		}
		meta := map[string]string{
			"input_tokens":  strconv.FormatInt(req.InputTokens, 10),
			"output_tokens": strconv.FormatInt(req.OutputTokens, 10),
		}
		if req.CacheReadTokens > 0 {
			meta["cache_read_tokens"] = strconv.FormatInt(req.CacheReadTokens, 10)
		}
		if req.CacheWriteTokens > 0 {
			meta["cache_write_tokens"] = strconv.FormatInt(req.CacheWriteTokens, 10)
		}
		if _, err := artifacts.Create(r.Context(), store.ArtifactCreate{
			WorkspaceKey: ws,
			ArtifactID:   newArtifactID(),
			AgentID:      sess.AgentID,
			SessionID:    sessionID,
			TaskID:       sess.TaskID,
			Type:         "usage",
			URI:          "usage://" + sessionID,
			Metadata:     meta,
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not record usage")
			return
		}
		writeOK(w, http.StatusOK, nil)
	}
}

// sessionHeartbeat is the SessionHeartbeat response shape (mirrors openapi.yaml).
type sessionHeartbeat struct {
	SessionID     string `json:"session_id"`
	LastHeartbeat string `json:"last_heartbeat,omitempty"`
	Status        string `json:"status,omitempty"`
}

// HandleHeartbeatSession bumps the session's last-heartbeat to keep its lease
// alive. POST /api/workspaces/{ws}/sessions/{sessionId}/heartbeat
func HandleHeartbeatSession(sessions store.AgentSessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, sessionID := scope(r)
		sess, err := sessions.Heartbeat(r.Context(), ws, sessionID)
		if err != nil {
			writeErr(w, statusForErr(err), "session not found")
			return
		}
		writeOK(w, http.StatusOK, sessionHeartbeat{
			SessionID:     sess.SessionID,
			LastHeartbeat: sess.LastHeartbeat.UTC().Format(time.RFC3339Nano),
			Status:        string(sess.Status),
		})
	}
}

// HandleAppendSessionLog accepts a runner log line for the session. Durable,
// server-visible log storage is a later phase; for now the line is validated
// (and the session existence checked) so the SDK's appendLog works end to end.
// POST /api/workspaces/{ws}/sessions/{sessionId}/logs
func HandleAppendSessionLog(sessions store.AgentSessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, sessionID := scope(r)
		var req struct {
			Stream string `json:"stream"`
			Text   string `json:"text"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.Stream != "stdout" && req.Stream != "stderr" {
			writeErr(w, http.StatusBadRequest, "stream must be stdout or stderr")
			return
		}
		if _, err := sessions.Get(r.Context(), ws, sessionID); err != nil {
			writeErr(w, statusForErr(err), "session not found")
			return
		}
		slog.Debug("taskrun session log", "workspace", ws, "session", sessionID, "stream", req.Stream, "bytes", len(req.Text))
		writeOK(w, http.StatusAccepted, nil)
	}
}
