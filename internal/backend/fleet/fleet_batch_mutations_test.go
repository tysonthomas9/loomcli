package fleet

import (
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

func TestBatchCreateIssueReq_MapsSourceRepoToRepo(t *testing.T) {
	args, err := json.Marshal(backend.CreateParams{
		Title:      "Repo scoped",
		IssueType:  "task",
		Priority:   2,
		SourceRepo: "repo-a",
	})
	if err != nil {
		t.Fatalf("Marshal CreateParams: %v", err)
	}

	req, err := batchCreateIssueReq(backend.BatchOp{
		Operation: "create",
		Args:      args,
	})
	if err != nil {
		t.Fatalf("batchCreateIssueReq: %v", err)
	}

	if req.Repo != "repo-a" {
		t.Fatalf("Repo = %q, want repo-a", req.Repo)
	}
}

// TestBatchCreateIssueReq_CarriesAcceptanceCriteria is the batch half of
// PUPPET-522: fleetBatchCreateIssueReq is a hand-written body struct that
// bypasses CreateParams.FleetCreateBody, so it dropped the field independently
// of the single-create path.
func TestBatchCreateIssueReq_CarriesAcceptanceCriteria(t *testing.T) {
	args, err := json.Marshal(backend.CreateParams{
		Title:              "With AC",
		IssueType:          "task",
		AcceptanceCriteria: "AC-1",
	})
	if err != nil {
		t.Fatalf("Marshal CreateParams: %v", err)
	}

	req, err := batchCreateIssueReq(backend.BatchOp{Operation: "create", Args: args})
	if err != nil {
		t.Fatalf("batchCreateIssueReq: %v", err)
	}
	if req.AcceptanceCriteria != "AC-1" {
		t.Fatalf("AcceptanceCriteria = %q, want AC-1", req.AcceptanceCriteria)
	}
}

// The unset case must marshal to a body with no acceptance_criteria key at
// all: a fleet-db whose create schema predates the field rejects the *whole*
// batch on an unknown one, and there is no per-item retry.
func TestBatchCreateIssueReq_OmitsUnsetAcceptanceCriteria(t *testing.T) {
	args, err := json.Marshal(backend.CreateParams{Title: "No AC", IssueType: "task"})
	if err != nil {
		t.Fatalf("Marshal CreateParams: %v", err)
	}

	req, err := batchCreateIssueReq(backend.BatchOp{Operation: "create", Args: args})
	if err != nil {
		t.Fatalf("batchCreateIssueReq: %v", err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal request: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("Unmarshal request: %v", err)
	}
	if _, ok := decoded["acceptance_criteria"]; ok {
		t.Fatalf("body carries acceptance_criteria for an unset value: %s", body)
	}
}
