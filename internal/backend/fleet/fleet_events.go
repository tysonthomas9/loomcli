package fleet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

type fleetCommentWire struct {
	ID        json.RawMessage `json:"id"`
	IssueID   string          `json:"issue_id"`
	Author    string          `json:"author"`
	Body      string          `json:"body"`
	CreatedAt time.Time       `json:"created_at"`
}

// toTypesComment projects the fleet-db wire shape back into Loom's canonical
// Work Items comment so consumers do not need to know about the dialect gap.
func (w fleetCommentWire) toTypesComment() workitems.Comment {
	var id int64
	if len(w.ID) > 0 {
		raw := strings.Trim(strings.TrimSpace(string(w.ID)), `"`)
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			id = n
		}
	}
	return workitems.Comment{
		ID:        id,
		IssueID:   w.IssueID,
		Author:    w.Author,
		Text:      w.Body,
		CreatedAt: w.CreatedAt,
	}
}

func (b *FleetBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	path := "/issues/" + url.PathEscape(id) + "/history"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	resp, err := b.exec(ctx, "ListEvents", "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []backend.EventData{}, nil
	}
	var history struct {
		History []struct {
			ID        string    `json:"id"`
			Timestamp time.Time `json:"timestamp"`
			Actor     string    `json:"actor"`
			Action    string    `json:"action"`
		} `json:"history"`
	}
	if err := json.Unmarshal(resp.Data, &history); err != nil {
		return nil, backend.ErrInternal("ListEvents", "unmarshal response", err)
	}
	result := make([]backend.EventData, 0, len(history.History))
	for _, event := range history.History {
		result = append(result, backend.EventData{
			ID:        event.ID,
			IssueID:   id,
			Kind:      event.Action,
			Actor:     event.Actor,
			CreatedAt: event.Timestamp,
		})
	}
	return result, nil
}

const fleetCursorZero = "0"
const fleetOpaqueCursorPrefix = "c1."

func normalizeFleetCursor(cursor string) string {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" || cursor == fleetCursorZero {
		return fleetCursorZero
	}
	if strings.HasPrefix(cursor, fleetOpaqueCursorPrefix) {
		if decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, fleetOpaqueCursorPrefix)); err == nil {
			cursor = string(decoded)
		}
	}
	if isFleetStreamID(cursor) {
		return cursor
	}
	if _, err := strconv.ParseInt(cursor, 10, 64); err == nil {
		return cursor + "-0"
	}
	return fleetCursorZero
}

func normalizeFleetCursorForV2(cursor string) string {
	cursor = normalizeFleetCursor(cursor)
	if cursor == fleetCursorZero {
		return fleetCursorZero
	}
	return fleetOpaqueCursorPrefix + base64.RawURLEncoding.EncodeToString([]byte(cursor))
}

func isFleetStreamID(cursor string) bool {
	parts := strings.Split(cursor, "-")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil {
		return false
	}
	if _, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
		return false
	}
	return true
}

func (b *FleetBackend) getMutationsAfter(ctx context.Context, op, cursor string, timeoutMs int64) ([]workitems.Mutation, error) {
	params := url.Values{}
	params.Set("since", normalizeFleetCursorForV2(cursor))
	if timeoutMs > 0 {
		params.Set("timeout", strconv.FormatInt(timeoutMs, 10))
	}
	rawURL := b.baseWorkspaceV2 + "/events/mutations?" + params.Encode()
	resp, err := b.execURL(ctx, op, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	if !hasData(resp) {
		return []workitems.Mutation{}, nil
	}
	var fleetResponse fleetMutationsResponse
	if err := json.Unmarshal(resp.Data, &fleetResponse); err != nil {
		return nil, backend.ErrInternal(op, "unmarshal response", err)
	}
	return fleetEventsToMutations(fleetResponse.Events), nil
}

// GetMutationsAfter returns mutation events after an opaque FleetDB cursor.
func (b *FleetBackend) GetMutationsAfter(ctx context.Context, cursor string) ([]workitems.Mutation, error) {
	return b.getMutationsAfter(ctx, "GetMutationsAfter", cursor, 0)
}

// WaitForMutationsAfter long-polls mutation events after an opaque FleetDB cursor.
func (b *FleetBackend) WaitForMutationsAfter(ctx context.Context, cursor string, timeoutMs int64) ([]workitems.Mutation, error) {
	return b.getMutationsAfter(ctx, "WaitForMutationsAfter", cursor, timeoutMs)
}
