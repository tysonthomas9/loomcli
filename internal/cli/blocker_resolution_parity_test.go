package cli

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestBlockerResolutionParity_CLI(t *testing.T) {
	data, err := os.ReadFile("../webui/frontend/testdata/blocker_resolution_parity_cases.json")
	if err != nil {
		t.Fatalf("failed to read parity fixture: %v", err)
	}
	var cases []struct {
		ID              string `json:"id"`
		Description     string `json:"description"`
		BlockerStatus   string `json:"blocker_status"`
		DepType         string `json:"dep_type"`
		ExpectedBlocked bool   `json:"expected_blocked"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			deps := []backend.DependencyData{{Type: c.DepType, DependsOnID: "BLOCKER-1"}}
			unclosedIDs := map[string]bool{}
			if c.BlockerStatus != "closed" {
				unclosedIDs["BLOCKER-1"] = true
			}
			got := HasUnclosedBlockers(deps, unclosedIDs)
			if got != c.ExpectedBlocked {
				t.Errorf("HasUnclosedBlockers(%s): dep_type=%s blocker_status=%s got=%v want=%v",
					c.Description, c.DepType, c.BlockerStatus, got, c.ExpectedBlocked)
			}
		})
	}
}
