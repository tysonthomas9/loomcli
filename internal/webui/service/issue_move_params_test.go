package service

import (
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestBuildMoveCreateBackendParamsCopiesFieldsAndFormatsDates(t *testing.T) {
	due := time.Date(2026, 5, 19, 14, 30, 0, 0, time.UTC)
	deferUntil := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	estimated := 45
	source := &backend.IssueDetailData{
		IssueData: backend.IssueData{
			Title:      "Move me",
			IssueType:  "task",
			Priority:   2,
			Design:     "design",
			Assignee:   "worker",
			Owner:      "owner",
			Labels:     []string{"api", "ui"},
			DueAt:      &due,
			DeferUntil: &deferUntil,
		},
		Description:        "details",
		AcceptanceCriteria: "criteria",
		Notes:              "notes",
		ExternalRef:        "GH-1",
		EstimatedMinutes:   &estimated,
	}

	got := buildMoveCreateBackendParams(source, "SRC-1")
	if got.Title != source.Title || got.IssueType != source.IssueType || got.Priority != source.Priority {
		t.Fatalf("basic fields not copied: %+v", got)
	}
	if got.CreatedBy != "web-ui" || got.Owner != "owner" || got.Assignee != "worker" {
		t.Fatalf("ownership fields = %+v", got)
	}
	if !strings.Contains(got.Description, "details\n\n(Moved from SRC-1)") {
		t.Fatalf("description = %q", got.Description)
	}
	if got.DueAt != due.Format(time.RFC3339) || got.DeferUntil != deferUntil.Format(time.RFC3339) {
		t.Fatalf("dates = due %q defer %q", got.DueAt, got.DeferUntil)
	}
	if got.EstimatedMinutes == nil || *got.EstimatedMinutes != estimated || got.ExternalRef != "GH-1" {
		t.Fatalf("detail fields = %+v", got)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "api" || got.Labels[1] != "ui" {
		t.Fatalf("labels = %#v", got.Labels)
	}

	source.Description = ""
	got = buildMoveCreateBackendParams(source, "SRC-2")
	if got.Description != "(Moved from SRC-2)" {
		t.Fatalf("empty description marker = %q", got.Description)
	}
}
