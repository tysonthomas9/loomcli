package agent

import (
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestFormatIssueTextIncludesAllOptionalSectionsAndTruncates(t *testing.T) {
	if got := FormatIssueText(nil); got != "" {
		t.Fatalf("FormatIssueText(nil) = %q, want empty", got)
	}

	longDescription := strings.Repeat("x", 4100) + "é"
	got := FormatIssueText(&backend.IssueDetailData{
		IssueData: backend.IssueData{
			ID:        "TASK-1",
			Title:     "Implement coverage",
			Status:    "open",
			Priority:  2,
			IssueType: "task",
			Assignee:  "nova",
			Labels:    []string{"go", "tests"},
			Design:    "Use package-local tests.",
		},
		Description:        longDescription,
		AcceptanceCriteria: "coverage increases",
		Notes:              "watch the global denominator",
		Dependencies: []backend.DependencyData{{
			DependsOnID: "TASK-0",
			Type:        "blocks",
			Status:      "closed",
		}},
	})

	for _, want := range []string{
		"ID: TASK-1",
		"Title: Implement coverage",
		"Status: open",
		"Priority: P2",
		"Type: task",
		"Assignee: nova",
		"Labels: go, tests",
		"Description:",
		"... [truncated]",
		"Design:\nUse package-local tests.",
		"Acceptance Criteria:\ncoverage increases",
		"Notes:\nwatch the global denominator",
		"Dependencies:\n  - TASK-0 (blocks) [closed]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted issue missing %q:\n%s", want, got)
		}
	}
}
