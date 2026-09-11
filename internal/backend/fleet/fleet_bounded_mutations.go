package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// GetMutationsThrough preserves FleetDB's raw-page fence even when filtering
// omits a source event. It requires the bounded mutation API; no timestamp or
// unbounded response can substitute for that contract.
func (b *FleetBackend) GetMutationsThrough(ctx context.Context, since, through string, limit int) (backend.MutationPage, error) {
	const op = "GetMutationsThrough"
	if !isFixedFleetCursor(since) || !isFixedFleetCursor(through) || limit < 1 || limit > 1000 {
		return backend.MutationPage{}, backend.ErrValidation(op, "invalid fixed replay interval")
	}
	params := url.Values{"since": {since}, "through": {through}, "limit": {strconv.Itoa(limit)}, "timeout": {"0"}}
	resp, status, err := b.doRequestURL(ctx, http.MethodGet, b.baseWorkspaceV2+"/events/mutations?"+params.Encode(), nil)
	if err != nil {
		return backend.MutationPage{}, classifyTransportError(op, err)
	}
	if err := classifyHTTPError(op, status, *resp); err != nil {
		return backend.MutationPage{}, err
	}
	if status != http.StatusOK || !hasData(resp) {
		return backend.MutationPage{}, backend.ErrInternal(op, "missing bounded mutation page", nil)
	}
	return decodeBoundedMutationPage(resp.Data, since, through, limit, b.workspaceID)
}

func decodeBoundedMutationPage(data json.RawMessage, since, through string, limit int, workspace string) (backend.MutationPage, error) {
	const op = "GetMutationsThrough"
	wire, err := decodeMutationWire(data, limit, workspace)
	if err != nil {
		return backend.MutationPage{}, err
	}
	if (*wire.Cursor == through) == *wire.HasMore || (*wire.Cursor == since && (*wire.HasMore || len(*wire.Events) > 0)) {
		return backend.MutationPage{}, backend.ErrInternal(op, "bounded mutation page did not honor replay fence", nil)
	}
	seen := make(map[string]bool, len(*wire.Events))
	for index, event := range *wire.Events {
		if !isFixedFleetCursor(event.ID) || event.ID == "0" || event.ID == since || seen[event.ID] ||
			(event.ID == *wire.Cursor && index != len(*wire.Events)-1) {
			return backend.MutationPage{}, backend.ErrInternal(op, fmt.Sprintf("invalid bounded event cursor %q", event.ID), nil)
		}
		seen[event.ID] = true
	}
	return backend.MutationPage{Events: fleetEventsToMutationData(*wire.Events), Cursor: *wire.Cursor, HasMore: *wire.HasMore, SourceIdentity: *wire.SourceIdentity}, nil
}

// Validate the token envelope without imposing order on opaque source IDs.
func isFixedFleetCursor(value string) bool { return value == "0" || backend.ValidMutationCursor(value) }

type mutationWire struct {
	Events         *[]fleetMutationEvent `json:"events"`
	Cursor         *string               `json:"cursor"`
	HasMore        *bool                 `json:"has_more"`
	SourceIdentity *string               `json:"source_identity"`
}

func decodeMutationWire(data json.RawMessage, limit int, workspace string) (mutationWire, error) {
	var wire mutationWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return wire, backend.ErrInternal("mutations", "invalid mutation page", err)
	}
	if wire.Events == nil || wire.Cursor == nil || *wire.Cursor == "0" || !isFixedFleetCursor(*wire.Cursor) || wire.HasMore == nil || wire.SourceIdentity == nil || !backend.ValidSourceIdentity(*wire.SourceIdentity) || len(*wire.Events) > limit {
		return wire, backend.ErrInternal("mutations", "incomplete mutation page", nil)
	}
	seen := map[string]bool{}
	for _, e := range *wire.Events {
		if e.WorkspaceID != workspace || e.ID == "0" || !isFixedFleetCursor(e.ID) || seen[e.ID] {
			return wire, backend.ErrInternal("mutations", "invalid event cursor", nil)
		}
		seen[e.ID] = true
	}
	return wire, nil
}
func decodeMutationPage(data json.RawMessage, limit int, workspace string) (backend.MutationPage, error) {
	wire, err := decodeMutationWire(data, limit, workspace)
	if err != nil {
		return backend.MutationPage{}, err
	}
	return backend.MutationPage{Events: fleetEventsToMutationData(*wire.Events), Cursor: *wire.Cursor, HasMore: *wire.HasMore, SourceIdentity: *wire.SourceIdentity}, nil
}
