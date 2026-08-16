package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

const (
	DefaultRunsLimit = 50
	MaxRunsLimit     = 200
	defaultRunsLimit = DefaultRunsLimit
	maxRunsLimit     = MaxRunsLimit
)

// ParseRunLimit reads the optional ?limit= (default 50, capped at 200). A
// non-numeric or < 1 value is a 400; a value above the cap is clamped, not
// rejected.
func ParseRunLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return defaultRunsLimit, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		RespondError(w, http.StatusBadRequest, "invalid limit: "+raw)
		return 0, false
	}
	if limit > maxRunsLimit {
		limit = maxRunsLimit
	}
	return limit, true
}

// SortAndTrim orders runs newest-first and trims to limit. Stores should apply
// this order before pushdown limits; this stays as defense in depth for mixed
// backends and tests.
func SortAndTrim(runs []*execution.DriverRunRecord, limit int) []*execution.DriverRunRecord {
	execution.SortDriverRunsNewestFirst(runs)
	if limit > 0 && len(runs) > limit {
		return runs[:limit]
	}
	return runs
}
