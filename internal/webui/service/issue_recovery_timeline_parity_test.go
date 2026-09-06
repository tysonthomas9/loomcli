package service

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// The inputs come from Fleet's actual formatter; expected JSON is shared with
// the browser adapter. Exercise the ordinary Go serving mapper without exporting
// it or introducing a second formatter for recovery.
func TestRecoveryTimelineOrdinaryEventParity(t *testing.T) {
	const directory = "../../backend/fleet/testdata/"
	data, err := os.ReadFile(directory + "issue_recovery_timeline.json")
	require.NoError(t, err)
	var fixture struct {
		IssueID  string `json:"issue_id"`
		Timeline []struct {
			ID        string                `json:"id"`
			Timestamp time.Time             `json:"timestamp"`
			Actor     string                `json:"actor"`
			Action    string                `json:"action"`
			Category  string                `json:"category"`
			Summary   string                `json:"summary"`
			Changes   []backend.FieldChange `json:"changes"`
			Metadata  map[string]string     `json:"metadata"`
		} `json:"timeline"`
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	expectedData, err := os.ReadFile(directory + "issue_recovery_timeline_expected.json")
	require.NoError(t, err)
	var expected []json.RawMessage
	require.NoError(t, json.Unmarshal(expectedData, &expected))
	require.Len(t, fixture.Timeline, len(expected))
	for i, row := range fixture.Timeline {
		event := eventDataToTypesEvent(backend.EventData{ID: row.ID, IssueID: fixture.IssueID, Kind: row.Action, Actor: row.Actor, Category: row.Category, Summary: row.Summary, Changes: row.Changes, Metadata: row.Metadata, CreatedAt: row.Timestamp})
		actual, err := json.Marshal(event)
		require.NoError(t, err)
		require.JSONEq(t, string(expected[i]), string(actual), "event %s", row.ID)
	}
}
