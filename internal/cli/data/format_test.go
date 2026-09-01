package data

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/backend/api/gen"
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

func TestPrintIssueDetailTextIncludesOptionalFields(t *testing.T) {
	var out bytes.Buffer
	createdAt := time.Date(2026, 5, 13, 9, 30, 0, 0, time.UTC)
	detail := &backend.IssueDetailData{
		IssueData: backend.IssueData{
			ID:        "TASK-2",
			Title:     "Cover optional fields",
			Status:    "review",
			Priority:  2,
			IssueType: "task",
			Assignee:  "builder",
			Owner:     "planner",
			Parent:    "EPIC-1",
			Labels:    []string{"coverage", "cli"},
		},
		AcceptanceCriteria: "All optional fields render",
		Comments: []backend.CommentData{{
			Author:    "reviewer",
			Text:      "Looks covered",
			CreatedAt: createdAt,
		}},
	}

	if err := printIssueDetail(&out, detail, formatText); err != nil {
		t.Fatalf("printIssueDetail: %v", err)
	}

	got := out.String()
	for _, need := range []string{
		"Type:     task",
		"Assignee: builder",
		"Owner:    planner",
		"Parent:   EPIC-1",
		"Labels:   [coverage cli]",
		"Acceptance Criteria:\nAll optional fields render",
		"Comments (1):",
		"[2026-05-13 09:30] reviewer: Looks covered",
	} {
		if !strings.Contains(got, need) {
			t.Fatalf("output missing %q:\n%s", need, got)
		}
	}
}

func TestPrintAgentListTextJSONAndEmpty(t *testing.T) {
	entries := []agentEntry{
		{Name: "falcon", Role: "task", Status: "idle"},
		{Name: "nova", Role: "plan", Status: "running"},
	}

	t.Run("text", func(t *testing.T) {
		var out bytes.Buffer
		if err := printAgentList(&out, entries, formatText); err != nil {
			t.Fatalf("printAgentList text: %v", err)
		}
		got := out.String()
		for _, need := range []string{"NAME", "ROLE", "STATUS", "falcon", "nova"} {
			if !strings.Contains(got, need) {
				t.Fatalf("text output missing %q:\n%s", need, got)
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		var out bytes.Buffer
		if err := printAgentList(&out, entries, formatJSON); err != nil {
			t.Fatalf("printAgentList json: %v", err)
		}
		var decoded []agentEntry
		if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
			t.Fatalf("decode agent list JSON: %v", err)
		}
		if len(decoded) != 2 || decoded[1].Name != "nova" {
			t.Fatalf("decoded agents = %#v", decoded)
		}
	})

	t.Run("empty", func(t *testing.T) {
		var out bytes.Buffer
		if err := printAgentList(&out, nil, formatText); err != nil {
			t.Fatalf("printAgentList empty: %v", err)
		}
		if strings.TrimSpace(out.String()) != "(no agents)" {
			t.Fatalf("empty output = %q, want no agents sentinel", out.String())
		}
	})
}

func TestPrintNilErrors(t *testing.T) {
	var out bytes.Buffer
	if err := printIssueDetail(&out, nil, formatText); err == nil {
		t.Fatal("printIssueDetail nil: expected error")
	}
	if err := printCreatedIssue(&out, &out, nil, formatText); err == nil {
		t.Fatal("printCreatedIssue nil: expected error")
	}
	if err := printMonitorStatus(&out, nil, formatText); err == nil {
		t.Fatal("printMonitorStatus nil: expected error")
	}
}

func TestPrintMessageResultJSON(t *testing.T) {
	var out bytes.Buffer
	if err := printMessageResult(&out, "claimed TASK-1", formatJSON); err != nil {
		t.Fatalf("printMessageResult: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decode message JSON: %v", err)
	}
	if decoded["message"] != "claimed TASK-1" {
		t.Fatalf("decoded message = %#v", decoded)
	}
}

func TestMonitorWorkspaceNameFallbacks(t *testing.T) {
	name := "PUPPET"
	if got := monitorWorkspaceName(&gen.MonitorStatusResponse{
		Workspace: gen.MonitorWorkspaceInfo{Mode: "workspace", Name: &name, Resolved: true},
	}); got != name {
		t.Fatalf("resolved name = %q, want %q", got, name)
	}
	// Mode is the literal string "workspace"; it must never stand in for a name.
	mode := gen.MonitorWorkspaceInfoMode("workspace")
	if got := monitorWorkspaceName(&gen.MonitorStatusResponse{
		Workspace: gen.MonitorWorkspaceInfo{Mode: mode},
	}); got != "(unresolved)" {
		t.Fatalf("mode-only = %q, want (unresolved)", got)
	}
	if got := monitorWorkspaceName(&gen.MonitorStatusResponse{}); got != "(unresolved)" {
		t.Fatalf("empty = %q, want (unresolved)", got)
	}
	empty := ""
	if got := monitorWorkspaceName(&gen.MonitorStatusResponse{
		Workspace: gen.MonitorWorkspaceInfo{Mode: mode, Name: &empty},
	}); got != "(unresolved)" {
		t.Fatalf("empty name = %q, want (unresolved)", got)
	}
}

// TestPrintAgentListStoreBackedColumns covers the regression that left the
// STATUS (and ROLE) columns blank: the store-backed server marshals
// domain.Agent directly, so it sends role_name/live_status/state and no
// "role" or "status" key at all.
func TestPrintAgentListStoreBackedColumns(t *testing.T) {
	entries := []agentEntry{
		{Name: "falcon", RoleName: "task", State: "idle", DesiredState: "draining", LiveStatus: "draining"},
		{Name: "nova", RoleName: "plan", State: "active", LiveStatus: "idle"},
		{Name: "ghost", RoleName: "task", State: "stopped"},
	}

	var out bytes.Buffer
	if err := printAgentList(&out, entries, formatText); err != nil {
		t.Fatalf("printAgentList: %v", err)
	}
	got := out.String()

	for _, line := range strings.Split(strings.TrimSpace(got), "\n")[1:] {
		if strings.HasSuffix(strings.TrimRight(line, " "), " ") {
			t.Errorf("row has a trailing blank column: %q", line)
		}
	}
	for _, need := range []string{"falcon", "draining", "nova", "idle", "ghost", "stopped", "task", "plan"} {
		if !strings.Contains(got, need) {
			t.Errorf("output missing %q:\n%s", need, got)
		}
	}
}

// TestAgentDisplayStatusPrecedence pins the fallback order, including the
// socket-mode payload staying exactly as it was.
func TestAgentDisplayStatusPrecedence(t *testing.T) {
	tests := []struct {
		name  string
		entry agentEntry
		want  string
	}{
		{"live status wins", agentEntry{Status: "s", State: "st", LiveStatus: "draining"}, "draining"},
		{"socket mode status is unchanged", agentEntry{Status: "running", State: "st"}, "running"},
		{"state is the last resort", agentEntry{State: "stopped"}, "stopped"},
		{"nothing renders a sentinel, never a blank", agentEntry{}, "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentDisplayStatus(tc.entry); got != tc.want {
				t.Errorf("agentDisplayStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPrintMonitorStatusWorkspaceHeader is the regression test for the exact
// string in PUPPET-258: `Workspace: workspace`, the server's MODE rendered as
// if it were a workspace NAME, over an all-zero dashboard.
func TestPrintMonitorStatusWorkspaceHeader(t *testing.T) {
	const warning = "warning: server did not resolve a workspace; counts below are not scoped and may be zero"

	t.Run("resolved prints the name and no warning", func(t *testing.T) {
		name := "PUPPET"
		var out bytes.Buffer
		s := &gen.MonitorStatusResponse{
			Workspace: gen.MonitorWorkspaceInfo{Mode: "workspace", Name: &name, Resolved: true},
			Tasks:     gen.MonitorTaskSummary{ReadyToImplement: 16, InProgress: 7},
			Stats:     gen.MonitorStats{Open: 23, Total: 41},
		}
		if err := printMonitorStatus(&out, s, formatText); err != nil {
			t.Fatalf("printMonitorStatus: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "Workspace: PUPPET\n") {
			t.Errorf("output missing the workspace name header; got:\n%s", got)
		}
		if strings.Contains(got, warning) {
			t.Errorf("unexpected unresolved warning; got:\n%s", got)
		}
		if !strings.Contains(got, "ready_to_implement: 16") {
			t.Errorf("counts not rendered from the payload; got:\n%s", got)
		}
	})

	t.Run("mode-only prints (unresolved) and warns", func(t *testing.T) {
		var out bytes.Buffer
		s := &gen.MonitorStatusResponse{
			Workspace: gen.MonitorWorkspaceInfo{Mode: "workspace"},
		}
		if err := printMonitorStatus(&out, s, formatText); err != nil {
			t.Fatalf("printMonitorStatus: %v", err)
		}
		got := out.String()
		if strings.Contains(got, "Workspace: workspace") {
			t.Errorf("regression: mode printed as a workspace name; got:\n%s", got)
		}
		if !strings.Contains(got, "Workspace: (unresolved)\n") {
			t.Errorf("output missing the (unresolved) header; got:\n%s", got)
		}
		if !strings.Contains(got, warning) {
			t.Errorf("output missing the unresolved warning; got:\n%s", got)
		}
	})
}
