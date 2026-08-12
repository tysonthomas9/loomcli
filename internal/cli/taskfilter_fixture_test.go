package cli

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func TestPlanStatusParity_JSONFixtureCases(t *testing.T) {
	data, err := os.ReadFile("../webui/frontend/testdata/plan_status_parity_cases.json")
	if err != nil {
		t.Fatalf("failed to read parity fixture: %v", err)
	}
	var cases []struct {
		ID    string `json:"id"`
		Issue struct {
			Design           string   `json:"design"`
			DesignArtifactID string   `json:"design_artifact_id"`
			HasDesign        bool     `json:"has_design"`
			Labels           []string `json:"labels"`
		} `json:"issue"`
		Expected struct {
			HasNeedsRevision bool   `json:"has_needs_revision"`
			NeedsPlan        bool   `json:"needs_plan"`
			ReadyToImplement bool   `json:"ready_to_implement"`
			TsOpenStatus     string `json:"ts_open_status"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			issue := workitems.IssueSummary{Design: c.Issue.Design, DesignArtifactID: c.Issue.DesignArtifactID, HasDesign: c.Issue.HasDesign, Labels: c.Issue.Labels}
			if got := HasNeedsRevision(issue); got != c.Expected.HasNeedsRevision {
				t.Errorf("HasNeedsRevision()=%v, want %v", got, c.Expected.HasNeedsRevision)
			}
			if got := NeedsPlan(issue); got != c.Expected.NeedsPlan {
				t.Errorf("NeedsPlan()=%v, want %v", got, c.Expected.NeedsPlan)
			}
			if got := ReadyToImplement(issue); got != c.Expected.ReadyToImplement {
				t.Errorf("ReadyToImplement()=%v, want %v", got, c.Expected.ReadyToImplement)
			}
		})
	}
}
