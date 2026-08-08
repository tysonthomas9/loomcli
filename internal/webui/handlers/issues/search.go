package issues

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
)

// HandleSearchWorkItems routes full-text search through the Work Items public
// query surface and preserves the established raw JSON response envelope.
func HandleSearchWorkItems(api workitems.API) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			writeIssuesError(w, http.StatusBadRequest, "missing search query 'q'", "MISSING_QUERY")
			return
		}
		if api == nil {
			handler.HandleWorkItemsError(w, workitems.ErrUnavailable)
			return
		}
		values, err := api.Search(r.Context(), workitems.SearchQuery{Query: query, Limit: parseSearchLimit(r)})
		if err != nil {
			handler.HandleWorkItemsError(w, err)
			return
		}
		data, err := json.Marshal(values)
		if err != nil {
			writeIssuesError(w, http.StatusInternalServerError, "failed to encode response", "ENCODE_ERROR")
			return
		}
		handler.WriteJSON(w, http.StatusOK, IssuesResponse{Success: true, Data: data})
	}
}

// searchDefaultLimit is the default full-text search result cap when the
// caller does not specify `limit`. Mirrors the tuning used by the list
// endpoint for parity between the two list-shaped responses.
const searchDefaultLimit = 100

// searchMaxLimit caps the caller-specified limit so a pathological request
// cannot fan out an unbounded result set.
const searchMaxLimit = 500

// parseSearchLimit parses and clamps the limit query parameter.
// Invalid or non-positive values fall back to searchDefaultLimit; values above
// searchMaxLimit are clamped down.
func parseSearchLimit(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return searchDefaultLimit
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return searchDefaultLimit
	}
	if parsed > searchMaxLimit {
		return searchMaxLimit
	}
	return parsed
}
