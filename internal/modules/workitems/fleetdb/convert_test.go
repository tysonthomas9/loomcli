package fleetdb

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

func TestFleetIssueWire_FieldDriftGuard(t *testing.T) {
	wireType := reflect.TypeOf(fleetIssueWire{})
	wireKeys := make(map[string]bool)
	for index := 0; index < wireType.NumField(); index++ {
		tag := wireType.Field(index).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		key := tag
		if comma := indexComma(tag); comma >= 0 {
			key = tag[:comma]
		}
		switch key {
		case "type":
			wireKeys["kind"] = true
		case "repo", "source_repo":
			wireKeys["repo_canonical"] = true
		case "parent", "parent_id":
			wireKeys["parent"] = true
		default:
			wireKeys[key] = true
		}
	}
	want := map[string]bool{
		"id": true, "title": true, "status": true, "priority": true,
		"kind": true, "assignee": true, "owner": true, "labels": true,
		"repo_canonical": true, "parent": true, "design": true,
		"design_artifact_id": true, "design_format": true, "has_design": true,
		"description": true, "acceptance_criteria": true, "notes": true,
		"external_ref": true, "created_at": true, "created_by": true,
		"updated_at": true, "due_at": true, "defer_until": true,
		"closed_at": true, "close_reason": true, "moved_to": true, "moved_from": true,
	}
	if !reflect.DeepEqual(wireKeys, want) {
		t.Fatalf("fleet issue wire keys drifted\n got: %#v\nwant: %#v", wireKeys, want)
	}
}

func indexComma(value string) int {
	for index := range value {
		if value[index] == ',' {
			return index
		}
	}
	return -1
}

func TestFleetIssueWireProjectsDirectlyToBackend(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	closed := now.Add(2 * time.Hour)
	due := now.Add(24 * time.Hour)
	deferred := now.Add(time.Hour)
	wire := fleetIssueWithCountsWire{
		fleetIssueWire: fleetIssueWire{
			ID: "PARITY-1", Title: "round-trip", Status: "closed", Priority: 1,
			Type: "bug", Assignee: "agent-a", Owner: "owner@example.com",
			Labels: []string{"x", "y"}, Repo: "repo", ParentID: "EPIC-1",
			Design: "design notes", Notes: "BLOCKED: dep missing",
			Description: "desc", CreatedAt: now, CreatedBy: "creator", UpdatedAt: now,
			DueAt: &due, DeferUntil: &deferred, ClosedAt: &closed, CloseReason: "fixed",
			ExternalRef: "https://example.test/pr/1",
		},
		DependencyCount: 3,
		DependentCount:  1,
	}

	got := wire.toIssueSummary()
	if got.ID != "PARITY-1" || got.IssueType != "bug" || got.SourceRepo != "repo" || got.Parent != "EPIC-1" {
		t.Fatalf("identity projection = %#v", got)
	}
	if got.Design != "design notes" || got.Notes != "BLOCKED: dep missing" || got.ExternalRef != "https://example.test/pr/1" {
		t.Fatalf("content projection = %#v", got)
	}
	if got.DependencyCount != 3 || got.DependentCount != 1 || got.ClosedAt == nil || !got.ClosedAt.Equal(closed) {
		t.Fatalf("lifecycle projection = %#v", got)
	}
	if got.DueAt == nil || !got.DueAt.Equal(due) || got.DeferUntil == nil || !got.DeferUntil.Equal(deferred) {
		t.Fatalf("schedule projection = %#v", got)
	}

	detail := wire.fleetIssueWire.toIssueDetail()
	if detail.Description != "desc" || detail.Dependencies == nil || detail.Dependents == nil || detail.Comments == nil {
		t.Fatalf("detail projection = %#v", detail)
	}
}

func TestFleetIssueWireNormalizesCollectionsAndAliases(t *testing.T) {
	got := (fleetIssueWire{Repo: "org/repo"}).toIssueSummary()
	if got.SourceRepo != "org/repo" || got.Labels == nil {
		t.Fatalf("normalized issue = %#v", got)
	}
	if fallback := (fleetIssueWire{SourceRepo: "fallback"}).toIssueSummary(); fallback.SourceRepo != "fallback" {
		t.Fatalf("source repo fallback = %#v", fallback)
	}
}

func TestFleetIssueWireArtifactDesignReference(t *testing.T) {
	var wire fleetIssueWithCountsWire
	if err := json.Unmarshal([]byte(`{"id":"FLEET-1","title":"Artifact design","type":"task","has_design":true,"design_artifact_id":"design-fleet-1-hash","design_format":"html"}`), &wire); err != nil {
		t.Fatal(err)
	}
	got := wire.toIssueSummary()
	if got.Design != "" || !got.HasDesign || got.DesignArtifactID != "design-fleet-1-hash" || got.DesignFormat != "html" {
		t.Fatalf("artifact design projection = %#v", got)
	}
}

func TestCountIssuesResponse_ZeroValueGroups(t *testing.T) {
	var response countIssuesResponse
	if err := json.Unmarshal([]byte(`{"total":5,"groups":{"open":3,"closed":2}}`), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 5 || response.Groups["open"] != 3 || response.Groups["in_progress"] != 0 {
		t.Fatalf("count response = %#v", response)
	}
}

func TestReadyAndBlockedIssueProjections(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	parent := "EPIC-1"
	ready := availabilityIssuesToSummaries([]*readyIssueWithParent{
		{fleetIssueWire: fleetIssueWire{ID: "TASK-1", Status: string(workitems.StatusOpen), CreatedAt: now}, Parent: &parent},
		nil,
	})
	if len(ready) != 1 || ready[0].Parent != parent {
		t.Fatalf("ready projection = %#v", ready)
	}
	blocked := blockedIssueResponsesToSummaries([]blockedIssueResponseWire{
		{Issue: fleetIssueWire{ID: "TASK-2", Status: string(workitems.StatusBlocked)}, Blockers: []blockedBlockerWire{{ID: "TASK-1"}}},
	})
	if len(blocked) != 1 || blocked[0].BlockedByCount != 1 || blocked[0].BlockedBy[0] != "TASK-1" {
		t.Fatalf("blocked projection = %#v", blocked)
	}
}

func TestCloseResultProjectsFleetWire(t *testing.T) {
	result := closeResultJSONToResult(&closeResultJSON{
		Closed:    &fleetIssueWire{ID: "TASK-1", Status: string(workitems.StatusClosed)},
		Unblocked: []*fleetIssueWire{{ID: "TASK-2", Status: string(workitems.StatusOpen)}},
	})
	if result.Closed == nil || result.Closed.ID != "TASK-1" || len(result.Unblocked) != 1 || result.Unblocked[0].ID != "TASK-2" {
		t.Fatalf("close result = %#v", result)
	}
	empty := closeResultJSONToResult(&closeResultJSON{})
	if empty.Closed != nil || empty.Unblocked == nil {
		t.Fatalf("empty close result = %#v", empty)
	}
}
