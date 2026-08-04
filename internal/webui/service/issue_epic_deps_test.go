package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// TestGetIssue_DependentsWireUsesChildIDNotSelf covers the service-layer half
// of the "BLOCKED BY self-reference" bug (issue_backend_helpers.go depsToWire).
//
// Each dependent row (IssueID=child, DependsOnID=epic) must surface the child
// id on the wire. Before the fix depsToWire always projected DependsOnID, so
// the FE received N rows all pointing back at the epic instead of the N
// distinct children.
func TestGetIssue_DependentsWireUsesChildIDNotSelf(t *testing.T) {
	now := time.Now().UTC()
	const epic = "WEB-EXTRACTOR-1"

	fb := &fakeIssueBackend{
		getResult: &backend.IssueDetailData{
			IssueData: backend.IssueData{
				ID: epic, Title: "Web extractor epic", Status: "open",
				Priority: 1, IssueType: "epic", CreatedAt: now, UpdatedAt: now,
			},
			Dependents: []backend.DependencyData{
				{IssueID: "ML-1", DependsOnID: epic, Type: "parent-child", Title: "Child one", Status: "open", CreatedAt: now},
				{IssueID: "ML-2", DependsOnID: epic, Type: "parent-child", Title: "Child two", Status: "open", CreatedAt: now},
			},
		},
	}
	svc := newServiceWithFake(fb)
	raw, err := svc.GetIssue(context.Background(), epic)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	deps, ok := got["dependents"].([]any)
	if !ok || len(deps) != 2 {
		t.Fatalf("dependents = %v, want array of 2", got["dependents"])
	}
	want := map[string]bool{"ML-1": true, "ML-2": true}
	for _, d := range deps {
		entry := d.(map[string]any)
		id, _ := entry["id"].(string)
		if id == epic {
			t.Errorf("dependent wire id = %q (the epic itself) — self-reference bug", epic)
		}
		if !want[id] {
			t.Errorf("dependent wire id = %q, want one of ML-1/ML-2", id)
		}
	}
}

// TestDepsToWire_DependencyUsesDependsOnID guards the non-dependent path: a
// genuine dependency (the viewed issue depends on another) must still project
// DependsOnID, not selfID.
func TestDepsToWire_DependencyUsesDependsOnID(t *testing.T) {
	const self = "i-1"
	wire := depsToWire([]backend.DependencyData{
		{IssueID: self, DependsOnID: "i-0", Type: "blocks", Title: "Blocker"},
	}, self)
	if len(wire) != 1 {
		t.Fatalf("len = %d, want 1", len(wire))
	}
	if wire[0]["id"] != "i-0" {
		t.Errorf("dependency id = %v, want i-0", wire[0]["id"])
	}
}
