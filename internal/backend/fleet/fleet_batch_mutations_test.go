package fleet

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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

func TestFleetBatchAndWorkflowSmallHelperBranches(t *testing.T) {
	if _, err := batchCreateIssueReq(backend.BatchOp{Args: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid create args error = nil")
	}

	results := make([]backend.BatchResult, 3)
	results[1] = backend.BatchResult{Success: false, Error: "local parse error"}
	failPendingBatchCreates([]int{0, 1, 2}, results, "remote failed")
	if results[0].Error != "remote failed" || results[1].Error != "local parse error" || results[2].Error != "remote failed" {
		t.Fatalf("failPendingBatchCreates results = %+v", results)
	}

	results = make([]backend.BatchResult, 3)
	results[1] = backend.BatchResult{Success: false, Error: "skip"}
	assignBatchCreateResults([]int{0, 1, 2}, results, []fleetIssueWire{{ID: "NEW-1", Title: "new", Status: "open", Priority: 2, Type: "task"}})
	if !results[0].Success || len(results[0].Data) == 0 {
		t.Fatalf("first assigned result = %+v", results[0])
	}
	if results[1].Error != "skip" {
		t.Fatalf("existing error was overwritten: %+v", results[1])
	}
	if !results[2].Success || len(results[2].Data) != 0 {
		t.Fatalf("missing response slot result = %+v", results[2])
	}

	fb := &FleetBackend{actor: "default-actor"}
	assignee := "explicit"
	if got := fb.claimActor(&assignee, &backend.IssueDetailData{IssueData: backend.IssueData{Assignee: "current"}}); got != "explicit" {
		t.Fatalf("claimActor explicit = %q", got)
	}
	if got := fb.claimActor(nil, &backend.IssueDetailData{IssueData: backend.IssueData{Assignee: "current"}}); got != "current" {
		t.Fatalf("claimActor current = %q", got)
	}
	empty := ""
	if got := fb.claimActor(&empty, nil); got != "default-actor" {
		t.Fatalf("claimActor backend actor = %q", got)
	}

	blank := "  "
	if got, err := parseOptionalFleetTime(&blank); err != nil || !got.IsZero() {
		t.Fatalf("blank parseOptionalFleetTime = %v err=%v", got, err)
	}
	raw := time.Now().UTC().Format(time.RFC3339Nano)
	if got, err := parseOptionalFleetTime(&raw); err != nil || got.IsZero() {
		t.Fatalf("valid parseOptionalFleetTime = %v err=%v", got, err)
	}
	bad := "tomorrow"
	if _, err := parseOptionalFleetTime(&bad); err == nil || !strings.Contains(err.Error(), "RFC3339") {
		t.Fatalf("invalid parseOptionalFleetTime err = %v", err)
	}
}

func TestFleetBatchSingleOpLocalValidationBranches(t *testing.T) {
	fb := &FleetBackend{}
	if got := fb.runSingleUpdate(t.Context(), backend.BatchOp{Args: json.RawMessage(`{`)}); got.Success || !strings.Contains(got.Error, "unmarshal update args") {
		t.Fatalf("invalid update batch result = %+v", got)
	}
	if got := fb.runSingleUpdate(t.Context(), backend.BatchOp{Args: json.RawMessage(`{"params":{}}`)}); got.Success || !strings.Contains(got.Error, "missing id") {
		t.Fatalf("missing update id result = %+v", got)
	}
	if got := fb.runSingleDelete(t.Context(), backend.BatchOp{Args: json.RawMessage(`{`)}); got.Success || !strings.Contains(got.Error, "unmarshal delete args") {
		t.Fatalf("invalid delete batch result = %+v", got)
	}
	if got := fb.runSingleDelete(t.Context(), backend.BatchOp{Args: json.RawMessage(`{"force":true}`)}); got.Success || !strings.Contains(got.Error, "missing id/ids") {
		t.Fatalf("missing delete id result = %+v", got)
	}

	if normalizeFleetCursor("") != "0" || normalizeFleetCursor("123") != "123-0" || normalizeFleetCursor("bad") != "0" {
		t.Fatalf("normalizeFleetCursor basic branches failed")
	}
	if !isFleetStreamID("123-4") || isFleetStreamID("123") || isFleetStreamID("a-b") {
		t.Fatalf("isFleetStreamID branches failed")
	}
	encoded := normalizeFleetCursorForV2("123-4")
	if !strings.HasPrefix(encoded, fleetOpaqueCursorPrefix) || normalizeFleetCursor(encoded) != "123-4" {
		t.Fatalf("opaque cursor roundtrip = %q", encoded)
	}
}
