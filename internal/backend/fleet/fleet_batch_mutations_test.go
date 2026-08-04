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
