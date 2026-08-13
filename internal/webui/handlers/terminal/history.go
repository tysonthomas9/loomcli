package terminal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	webuterminal "github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

type terminalHistoryMetaResponse struct {
	Generation         string                                   `json:"generation"`
	TotalLines         uint64                                   `json:"totalLines"`
	FirstScreenLine    uint64                                   `json:"firstScreenLine"`
	StartedAt          int64                                    `json:"startedAt"`
	Cols               uint16                                   `json:"cols"`
	Rows               uint16                                   `json:"rows"`
	AltScreen          bool                                     `json:"altScreen"`
	Gaps               uint64                                   `json:"gaps"`
	UnhandledSequences webuterminal.RecordingUnhandledSequences `json:"unhandledSequences"`
	HistoryLimited     bool                                     `json:"historyLimited"`
	RecordingStopped   bool                                     `json:"recordingStopped"`
	Closed             bool                                     `json:"closed"`
}

type terminalHistoryRangeResponse struct {
	Generation string                       `json:"generation"`
	Lines      []webuterminal.RecordingLine `json:"lines"`
}

// HandleTerminalHistory serves a bounded random-access range. Historical
// committed rows are immutable and cache forever; any range touching the live
// screen is explicitly no-store.
func HandleTerminalHistory(store *webuterminal.RecordingStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := terminalHistoryKey(r)
		generation := r.URL.Query().Get("generation")
		if generation == "" {
			handler.RespondError(w, http.StatusBadRequest, "generation is required")
			return
		}
		from, err := parseUintQuery(r, "from", 0)
		if err != nil {
			handler.RespondError(w, http.StatusBadRequest, err.Error())
			return
		}
		count64, err := parseUintQuery(r, "count", 200)
		if err != nil || count64 == 0 {
			handler.RespondError(w, http.StatusBadRequest, "count must be a positive integer")
			return
		}
		if count64 > webuterminal.MaxHistoryRangeCount {
			count64 = webuterminal.MaxHistoryRangeCount
		}
		//nolint:gosec // G115: clamped to MaxHistoryRangeCount immediately above.
		history, err := store.HistoryGeneration(r.Context(), key, generation, from, int(count64))
		if err != nil {
			respondRecordingError(w, err)
			return
		}
		// Mutable coordinates live exclusively on the no-store metadata endpoint.
		// This payload contains only the requested rows, so an immutable response
		// can never overwrite newer total/closed state in the browser.
		payload, err := json.Marshal(terminalHistoryRangeResponse{Generation: history.Generation, Lines: history.Lines})
		if err != nil {
			handler.RespondError(w, http.StatusInternalServerError, "failed to encode terminal history")
			return
		}
		if history.Immutable {
			// The URL already carries the generation cache key. Scope the validator
			// too, so an ETag copied from generation A can never validate B even if
			// both generations happen to contain byte-identical rows.
			digest := sha256.Sum256(append(append([]byte(generation), 0), payload...))
			etag := `"` + hex.EncodeToString(digest[:]) + `"`
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		} else {
			w.Header().Set("Cache-Control", "private, no-store")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}
}

func HandleTerminalHistoryMeta(store *webuterminal.RecordingStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		meta, total, firstScreen, err := store.Meta(r.Context(), terminalHistoryKey(r))
		if err != nil {
			respondRecordingError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		handler.WriteJSON(w, http.StatusOK, terminalHistoryMetaResponse{
			Generation: meta.Generation,
			TotalLines: total, FirstScreenLine: firstScreen,
			StartedAt: meta.StartedAt, Cols: meta.Cols, Rows: meta.Rows,
			AltScreen: meta.AltScreen, Gaps: meta.Gaps,
			UnhandledSequences: meta.UnhandledSequences,
			HistoryLimited:     meta.HistoryLimited, RecordingStopped: meta.RecordingStopped,
			Closed: meta.Closed,
		})
	}
}

func HandleTerminalHistoryRaw(store *webuterminal.RecordingStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := terminalHistoryKey(r)
		file, _, err := store.OpenRaw(r.Context(), key)
		if err != nil {
			respondRecordingError(w, err)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, key.Name+".raw.seg"))
		w.Header().Set("Cache-Control", "private, no-store")
		if _, err := io.Copy(w, file); err != nil {
			return
		}
	}
}

func terminalHistoryKey(r *http.Request) webuterminal.SessionKey {
	return webuterminal.SessionKey{
		Workspace: middleware.WorkspaceFromContext(r.Context()),
		Name:      r.PathValue("session"),
	}
}

func parseUintQuery(r *http.Request, name string, fallback uint64) (uint64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return value, nil
}

func respondRecordingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, webuterminal.ErrRecordingNotFound):
		handler.RespondError(w, http.StatusNotFound, "terminal recording not found")
	case errors.Is(err, webuterminal.ErrInvalidRecording):
		handler.RespondError(w, http.StatusBadRequest, "invalid terminal recording identifier")
	default:
		handler.RespondError(w, http.StatusInternalServerError, "failed to read terminal recording")
	}
}
