package agent

import (
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// FormatIssueText renders a Work Items detail projection as human-readable text.
// Used by analyzeTaskCompletion to provide context to Claude.
func FormatIssueText(detail *workitems.IssueDetail) string {
	if detail == nil {
		return ""
	}

	const maxFieldLen = 4000

	var b strings.Builder
	fmt.Fprintf(&b, "ID: %s\n", detail.ID)
	fmt.Fprintf(&b, "Title: %s\n", detail.Title)
	fmt.Fprintf(&b, "Status: %s\n", detail.Status)
	fmt.Fprintf(&b, "Priority: P%d\n", detail.Priority)
	if detail.IssueType != "" {
		fmt.Fprintf(&b, "Type: %s\n", detail.IssueType)
	}
	if detail.Assignee != "" {
		fmt.Fprintf(&b, "Assignee: %s\n", detail.Assignee)
	}
	if len(detail.Labels) > 0 {
		fmt.Fprintf(&b, "Labels: %s\n", strings.Join(detail.Labels, ", "))
	}

	if detail.Description != "" {
		fmt.Fprintf(&b, "\nDescription:\n%s\n", truncateUTF8Safe(detail.Description, maxFieldLen))
	}
	if detail.Design != "" {
		fmt.Fprintf(&b, "\nDesign:\n%s\n", truncateUTF8Safe(detail.Design, maxFieldLen))
	}
	if detail.AcceptanceCriteria != "" {
		fmt.Fprintf(&b, "\nAcceptance Criteria:\n%s\n", truncateUTF8Safe(detail.AcceptanceCriteria, maxFieldLen))
	}
	if detail.Notes != "" {
		fmt.Fprintf(&b, "\nNotes:\n%s\n", truncateUTF8Safe(detail.Notes, maxFieldLen))
	}

	if len(detail.Dependencies) > 0 {
		fmt.Fprintf(&b, "\nDependencies:\n")
		for _, dep := range detail.Dependencies {
			fmt.Fprintf(&b, "  - %s (%s) [%s]\n", dep.ID, dep.DependencyType, dep.Status)
		}
	}

	return b.String()
}
