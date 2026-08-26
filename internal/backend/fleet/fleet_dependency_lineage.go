package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

const dependencyHistoryPageSize = 200

type dependencyHistoryResponse struct {
	History []struct {
		Action   string            `json:"action"`
		Metadata map[string]string `json:"metadata"`
	} `json:"history"`
	Cursor  string `json:"cursor"`
	HasMore bool   `json:"has_more"`
}

// DependencyTaskIDs replays the durable dependency audit trail instead of the
// mutable ready projection. FleetDB removes blocks edges as blockers close, but
// does not emit dep.remove for that scheduler-only cleanup.
func (b *FleetBackend) DependencyTaskIDs(ctx context.Context, id string) ([]string, error) {
	const op = "DependencyTaskIDs"
	active := make(map[string]struct{})
	cursor := "0"
	for {
		params := url.Values{}
		params.Set("action", "dep.add,dep.remove")
		params.Set("limit", fmt.Sprintf("%d", dependencyHistoryPageSize))
		params.Set("since", cursor)
		rawURL := b.baseWorkspaceV2 + "/issues/" + url.PathEscape(id) + "/history?" + params.Encode()
		resp, err := b.execURL(ctx, op, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		if !hasData(resp) {
			return []string{}, nil
		}
		var page dependencyHistoryResponse
		if err := json.Unmarshal(resp.Data, &page); err != nil {
			return nil, backend.ErrInternal(op, "unmarshal response", err)
		}
		for _, event := range page.History {
			if event.Metadata["dep_type"] != "blocks" {
				continue
			}
			dependencyID := event.Metadata["depends_on_id"]
			if dependencyID == "" {
				continue
			}
			switch event.Action {
			case "dep.add":
				active[dependencyID] = struct{}{}
			case "dep.remove":
				delete(active, dependencyID)
			}
		}
		if !page.HasMore {
			break
		}
		if page.Cursor == "" || page.Cursor == cursor {
			return nil, fmt.Errorf("%s: dependency history cursor did not advance", op)
		}
		cursor = page.Cursor
	}
	ids := make([]string, 0, len(active))
	for id := range active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
