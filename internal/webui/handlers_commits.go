package webui

import (
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/commits"
)

const defaultCommitLimit = 100

// CommitsResponse wraps commit records for JSON response.
type CommitsResponse struct {
	Success bool             `json:"success"`
	Data    []commits.Record `json:"data"`
	Error   string           `json:"error,omitempty"`
}

// handleGetIssueCommits returns a handler that retrieves commits for an issue.
func handleGetIssueCommits(beadsDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := r.PathValue("id")
		if issueID == "" {
			respondJSON(w, http.StatusBadRequest, CommitsResponse{
				Success: false,
				Error:   "missing issue ID",
			})
			return
		}

		if beadsDir == "" {
			respondJSON(w, http.StatusServiceUnavailable, CommitsResponse{
				Success: false,
				Error:   "beads directory not configured",
			})
			return
		}

		// Parse optional limit parameter
		limit := defaultCommitLimit
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}

		records, err := commits.LoadForTask(beadsDir, issueID, limit)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, CommitsResponse{
				Success: false,
				Error:   "failed to load commits",
			})
			return
		}

		if records == nil {
			records = []commits.Record{}
		}

		respondJSON(w, http.StatusOK, CommitsResponse{
			Success: true,
			Data:    records,
		})
	}
}
