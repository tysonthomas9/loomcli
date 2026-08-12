package data

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/platform/loomapi/gen"
)

const (
	formatText = "text"
	formatJSON = "json"
)

// writeJSON writes v as indented JSON to w and returns any encode error.
func writeJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// printIssueDetail renders a Work Items detail in the requested format.
func printIssueDetail(w io.Writer, d *workitems.IssueDetail, format string) error {
	if d == nil {
		return fmt.Errorf("issue detail is nil")
	}
	if format == formatJSON {
		return writeJSON(w, d)
	}
	fmt.Fprintf(w, "ID:       %s\n", d.ID)
	fmt.Fprintf(w, "Title:    %s\n", d.Title)
	fmt.Fprintf(w, "Status:   %s\n", d.Status)
	fmt.Fprintf(w, "Priority: %d\n", d.Priority)
	if d.IssueType != "" {
		fmt.Fprintf(w, "Type:     %s\n", d.IssueType)
	}
	if d.Assignee != "" {
		fmt.Fprintf(w, "Assignee: %s\n", d.Assignee)
	}
	if d.Owner != "" {
		fmt.Fprintf(w, "Owner:    %s\n", d.Owner)
	}
	if d.Parent != "" {
		fmt.Fprintf(w, "Parent:   %s\n", d.Parent)
	}
	if len(d.Labels) > 0 {
		fmt.Fprintf(w, "Labels:   %v\n", d.Labels)
	}
	if d.Description != "" {
		fmt.Fprintf(w, "\nDescription:\n%s\n", d.Description)
	}
	if d.Design != "" {
		fmt.Fprintf(w, "\nDesign (--design):\n%s\n", d.Design)
	}
	if d.AcceptanceCriteria != "" {
		fmt.Fprintf(w, "\nAcceptance Criteria:\n%s\n", d.AcceptanceCriteria)
	}
	if len(d.Comments) > 0 {
		fmt.Fprintf(w, "\nComments (%d):\n", len(d.Comments))
		for _, c := range d.Comments {
			fmt.Fprintf(w, "  [%s] %s: %s\n", c.CreatedAt.Format("2006-01-02 15:04"), c.Author, c.Text)
		}
	}
	return nil
}

func printWorkItemSummaries(w io.Writer, items []workitems.IssueSummary, format string) error {
	if items == nil {
		items = []workitems.IssueSummary{}
	}
	if format == formatJSON {
		return writeJSON(w, items)
	}
	return printIssueRows(w, items, func(item workitems.IssueSummary) issueListRow {
		return issueListRow{item.ID, item.Title, item.Status, item.IssueType, item.Priority}
	})
}

type issueListRow struct {
	id, title, status, issueType string
	priority                     int
}

func printIssueRows[T any](w io.Writer, items []T, project func(T) issueListRow) error {
	if len(items) == 0 {
		fmt.Fprintln(w, "(no issues)")
		return nil
	}
	for _, item := range items {
		row := project(item)
		if row.issueType == "" {
			row.issueType = "-"
		}
		fmt.Fprintf(w, "%-24s  P%d  %-10s  %-10s  %s\n",
			row.id, row.priority, row.status, row.issueType, row.title)
	}
	return nil
}

// printCreatedIssue renders the issue returned by a create call and always
// emits one stable machine-checkable line, "CREATED <id>", so agents and
// pipelines can confirm success even when the rest of the output is consumed
// by a parser:
//   - text mode: issue summary on stdout, then "CREATED <id>" as the LAST
//     stdout line;
//   - JSON mode: stdout stays pure JSON (the issue object); "CREATED <id>"
//     goes to errW (stderr) so `... | jq .` keeps working.
func printCreatedIssue(w, errW io.Writer, issue *workitems.IssueSummary, format string) error {
	if issue == nil {
		return fmt.Errorf("created issue is nil")
	}
	if format == formatJSON {
		if err := writeJSON(w, issue); err != nil {
			return err
		}
		fmt.Fprintf(errW, "CREATED %s\n", issue.ID)
		return nil
	}
	if err := printWorkItemSummaries(w, []workitems.IssueSummary{*issue}, format); err != nil {
		return err
	}
	fmt.Fprintf(w, "CREATED %s\n", issue.ID)
	return nil
}

// printAgentList renders canonical Agent identities in the requested format.
func printAgentList(w io.Writer, entries []agentListEntry, format string) error {
	if entries == nil {
		entries = []agentListEntry{}
	}
	if format == formatJSON {
		return writeJSON(w, entries)
	}
	if len(entries) == 0 {
		fmt.Fprintln(w, "(no agents)")
		return nil
	}
	fmt.Fprintf(w, "%-20s  %-15s  %-20s  %s\n", "NAME", "KIND", "BEHAVIOR", "STATE")
	for _, a := range entries {
		behavior := a.Behavior.RoleName
		if behavior == "" {
			behavior = a.Behavior.DriverID
		}
		state := "paused"
		if a.Enabled {
			state = "running"
		}
		fmt.Fprintf(w, "%-20s  %-15s  %-20s  %s\n", a.Name, a.Kind, behavior, state)
	}
	return nil
}

// printMessageResult renders a single human-readable message (e.g., "agent
// falcon stopped"). It honors JSON output by wrapping the message in a
// {"message": "..."} object for pipeline consumers.
func printMessageResult(w io.Writer, msg, format string) error {
	if format == formatJSON {
		return writeJSON(w, map[string]string{"message": msg})
	}
	fmt.Fprintln(w, msg)
	return nil
}

// printMonitorStatus renders a MonitorStatusResponse in the requested format.
// The text rendering is intentionally minimal — no live refresh, no terminal
// box drawing — because cli/data cannot import the cli/monitor display
// formatter. Users who want the dashboard should run `loom monitor`.
func printMonitorStatus(w io.Writer, s *gen.MonitorStatusResponse, format string) error {
	if s == nil {
		return fmt.Errorf("monitor status is nil")
	}
	if format == formatJSON {
		return writeJSON(w, s)
	}
	fmt.Fprintf(w, "Workspace: %s\n", monitorWorkspaceName(s))
	fmt.Fprintln(w, "")

	fmt.Fprintln(w, "AGENTS:")
	if len(s.Agents) == 0 {
		fmt.Fprintln(w, "  (no agents)")
	} else {
		for _, a := range s.Agents {
			role := "-"
			if a.Role != nil && *a.Role != "" {
				role = *a.Role
			}
			fmt.Fprintf(w, "  %-20s  %-10s  %-20s  %s\n", a.Name, role, a.Branch, a.Status)
		}
	}
	fmt.Fprintln(w, "")

	fmt.Fprintln(w, "TASKS:")
	fmt.Fprintf(w, "  ready_to_implement: %d\n", s.Tasks.ReadyToImplement)
	fmt.Fprintf(w, "  needs_planning:     %d\n", s.Tasks.NeedsPlanning)
	fmt.Fprintf(w, "  in_progress:        %d\n", s.Tasks.InProgress)
	fmt.Fprintf(w, "  need_review:        %d\n", s.Tasks.NeedReview)
	fmt.Fprintf(w, "  backlog:            %d\n", s.Tasks.Backlog)
	fmt.Fprintf(w, "  epics:              %d\n", s.Tasks.Epics)
	fmt.Fprintln(w, "")

	fmt.Fprintln(w, "STATS:")
	fmt.Fprintf(w, "  open:        %d\n", s.Stats.Open)
	fmt.Fprintf(w, "  in_progress: %d\n", s.Stats.InProgress)
	fmt.Fprintf(w, "  review:      %d\n", s.Stats.Review)
	fmt.Fprintf(w, "  blocked:     %d\n", s.Stats.Blocked)
	fmt.Fprintf(w, "  closed:      %d\n", s.Stats.Closed)
	fmt.Fprintf(w, "  total:       %d\n", s.Stats.Total)
	fmt.Fprintf(w, "  completion:  %.1f%%\n", s.Stats.Completion)
	return nil
}

// monitorWorkspaceName extracts a user-friendly workspace label from the
// monitor response. Returns "(default)" if neither Name nor Mode is set.
func monitorWorkspaceName(s *gen.MonitorStatusResponse) string {
	if s.Workspace.Name != nil && *s.Workspace.Name != "" {
		return *s.Workspace.Name
	}
	if s.Workspace.Mode != "" {
		return string(s.Workspace.Mode)
	}
	return "(default)"
}
