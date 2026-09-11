package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoveryHistoryActionContract(t *testing.T) {
	require.Len(t, recoveryHistoryActionEntities, len(fleetActionContract))
	for _, row := range fleetActionContract {
		require.Equal(t, row.EntityType, recoveryHistoryActionEntities[row.Action], row.Action)
	}
}

func TestReadIssueRecoverySelectedScope(t *testing.T) {
	for _, mode := range []string{"selected", "foreign echo", "missing selection", "unexpected selection", "blank request"} {
		t.Run(mode, func(t *testing.T) {
			selected := "WS-1 &part=two"
			doc := recoveryTestDocument()
			doc["issues"] = []any{recoveryTestIssue(selected)}
			doc["total"] = 1
			history := map[string]any{"issue_id": selected, "present": true, "events": []any{}, "has_older": false, "timeline": []any{}}
			doc["history"] = history
			if mode == "foreign echo" {
				history["issue_id"] = "absent"
				history["present"] = false
			}
			if mode == "missing selection" {
				doc["history"] = nil
			}
			raw, err := json.Marshal(doc)
			require.NoError(t, err)
			calls := 0
			b := recoveryTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, http.MethodPost, r.Method)
				if mode == "unexpected selection" {
					require.Empty(t, r.URL.RawQuery)
				} else {
					require.Equal(t, selected, r.URL.Query().Get("issue_id"))
					require.Len(t, r.URL.Query(), 1)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Fleet-Source-Identity", "s1.Zml4dHVyZQ")
				_, _ = w.Write(raw)
			})
			if mode == "unexpected selection" {
				result, err := b.ReadIssueRecovery(context.Background())
				require.Error(t, err)
				require.Empty(t, result.Document)
				return
			}
			if mode == "blank request" {
				selected = " \u0085"
			}
			result, err := b.ReadIssueRecoveryForIssue(context.Background(), selected)
			if mode == "selected" {
				require.NoError(t, err)
				require.Equal(t, json.RawMessage(raw), result.Document)
			} else {
				require.Error(t, err)
				require.Empty(t, result.Document)
			}
			if mode == "blank request" {
				require.Zero(t, calls)
			}
		})
	}
}
