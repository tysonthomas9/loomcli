package git

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

func writeReviewError(w http.ResponseWriter, err error) {
	code, status := "internal_error", http.StatusInternalServerError
	var coded *loomgit.Error
	if errors.As(err, &coded) {
		code, status = coded.Code(), http.StatusConflict
	} else if review.IsNotFound(err) {
		code, status = "not_found", http.StatusNotFound
	}
	handler.WriteJSON(w, status, map[string]any{"success": false, "error": code})
}

type verdictRequest struct {
	HeadSHA string       `json:"head_sha"`
	Verdict string       `json:"verdict"`
	Reason  string       `json:"reason"`
	Actor   review.Actor `json:"actor"`
}

func handleVerdict(w http.ResponseWriter, req *http.Request) {
	number, err := strconv.Atoi(req.PathValue("r"))
	if err != nil || number < 1 {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "invalid_revision"})
		return
	}
	var body verdictRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.HeadSHA == "" {
		handler.WriteJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "invalid_verdict"})
		return
	}
	store, err := review.OpenLocal()
	if err != nil {
		writeReviewError(w, err)
		return
	}
	defer func() { _ = store.Close() }()
	v, err := store.Submit(req.Context(), req.PathValue("ws"), req.PathValue("change"), number,
		body.HeadSHA, body.Verdict, body.Reason, body.Actor)
	if err != nil {
		writeReviewError(w, err)
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": v})
}

func handleTaskRevisions(w http.ResponseWriter, req *http.Request) {
	store, err := review.OpenLocal()
	if err != nil {
		writeReviewError(w, err)
		return
	}
	defer func() { _ = store.Close() }()
	revisions, err := store.TaskRevisions(req.Context(), req.PathValue("ws"), req.PathValue("id"))
	if err != nil {
		writeReviewError(w, err)
		return
	}
	handler.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "data": revisions})
}
