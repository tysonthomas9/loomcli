package data

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestPrintIssueDetailTextIncludesDesign(t *testing.T) {
	var out bytes.Buffer
	detail := &backend.IssueDetailData{
		IssueData: backend.IssueData{
			ID:       "TASK-1",
			Title:    "Implement fixture",
			Status:   "open",
			Priority: 1,
			Design:   "Approved design text",
		},
		Description: "Task description",
	}

	if err := printIssueDetail(&out, detail, formatText); err != nil {
		t.Fatalf("printIssueDetail: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Design (--design):\nApproved design text\n") {
		t.Fatalf("output missing design field:\n%s", got)
	}
}
