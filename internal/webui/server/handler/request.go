// Package handler provides HTTP request/response helpers for the webui handler layer.
package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
)

// MaxRequestBody is the maximum request body size (1MB) to prevent DoS attacks.
const MaxRequestBody = 1 << 20

// DefaultListLimit is the default number of items returned by list endpoints.
const DefaultListLimit = 100

// MaxListLimit is the maximum number of items that can be requested in a single list call.
const MaxListLimit = 1000

// ReadJSON reads and decodes a JSON request body into dst.
// It enforces MaxRequestBody size limit. On failure it returns a
// *apperrors.ServiceError (KindPayloadTooLarge or KindValidation)
// that the caller can pass directly to HandleServiceError.
// ReadJSON applies its own MaxBytesReader; callers should NOT pre-wrap r.Body.
func ReadJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBody)

	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return apperrors.ErrPayloadTooLarge("request body too large (max 1MB)")
		}
		return apperrors.ErrValidation("invalid request body")
	}
	if dec.More() {
		return apperrors.ErrValidation("request body contains trailing content")
	}
	return nil
}

// WriteJSON writes a JSON response with the given status code.
// It sets Content-Type to application/json, writes the status header,
// and encodes v as JSON. Encoding errors are logged but not returned.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode JSON response", "err", err)
	}
}

// ListOpts holds common pagination and filter parameters for list endpoints.
type ListOpts struct {
	Limit     int    // Number of items to return (default 100, max 1000)
	Offset    int    // Number of items to skip (default 0)
	Query     string // Free-text search query
	Status    string // Status filter
	SortBy    string // Field to sort by
	SortOrder string // "asc" or "desc" (default "asc")
}

// ParseListOpts extracts common list pagination/filter parameters from query strings.
// Returns a validation error if limit or offset values are invalid.
func ParseListOpts(r *http.Request) (*ListOpts, error) {
	q := r.URL.Query()
	opts := &ListOpts{
		Limit:     DefaultListLimit,
		SortOrder: "asc",
	}

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, apperrors.ErrValidation("invalid limit: must be a non-negative integer")
		}
		if n == 0 {
			n = DefaultListLimit
		}
		if n > MaxListLimit {
			n = MaxListLimit
		}
		opts.Limit = n
	}

	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, apperrors.ErrValidation("invalid offset: must be a non-negative integer")
		}
		opts.Offset = n
	}

	opts.Query = q.Get("q")
	opts.Status = q.Get("status")
	opts.SortBy = q.Get("sort_by")

	if v := q.Get("sort_order"); v != "" {
		if v != "asc" && v != "desc" {
			return nil, apperrors.ErrValidation(`invalid sort_order: must be "asc" or "desc"`)
		}
		opts.SortOrder = v
	}

	return opts, nil
}
