package cli

import (
	"encoding/json"
	"os"
	"testing"
)

func TestTaskfilterParity_JSONFixtureCases(t *testing.T) {
	data, err := os.ReadFile("../webui/frontend/testdata/blocker_parity_cases.json")
	if err != nil {
		t.Fatalf("failed to read parity fixture: %v", err)
	}
	var cases []struct {
		ID    string `json:"id"`
		Issue struct {
			Design string   `json:"design"`
			Labels []string `json:"labels"`
		} `json:"issue"`
		Expected struct {
			GoNeedsPlan        bool   `json:"go_needs_plan"`
			GoReadyToImplement bool   `json:"go_ready_to_implement"`
			TsOpenStatus       string `json:"ts_open_status"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			issue := BdIssue{Design: c.Issue.Design, Labels: c.Issue.Labels}
			if got := NeedsPlan(issue); got != c.Expected.GoNeedsPlan {
				t.Errorf("NeedsPlan()=%v, want %v", got, c.Expected.GoNeedsPlan)
			}
			if got := ReadyToImplement(issue); got != c.Expected.GoReadyToImplement {
				t.Errorf("ReadyToImplement()=%v, want %v", got, c.Expected.GoReadyToImplement)
			}
		})
	}
}
