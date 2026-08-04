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
	entries := []gen.AgentControlEntry{
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
		var decoded []gen.AgentControlEntry
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
	mode := gen.MonitorWorkspaceInfoMode("workspace")
	if got := monitorWorkspaceName(&gen.MonitorStatusResponse{
		Workspace: gen.MonitorWorkspaceInfo{Mode: mode},
	}); got != "workspace" {
		t.Fatalf("mode fallback = %q, want workspace", got)
	}
	if got := monitorWorkspaceName(&gen.MonitorStatusResponse{}); got != "(default)" {
		t.Fatalf("empty fallback = %q, want default", got)
	}
}
