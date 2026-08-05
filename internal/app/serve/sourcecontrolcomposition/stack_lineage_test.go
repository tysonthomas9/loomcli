package sourcecontrolcomposition

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/driver/taskworktree"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/stacklineage"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

func TestRecordTaskOutcomeOwnsFinalizeBarrier(t *testing.T) {
	ctx := t.Context()
	lineage := stackstore.New(t.TempDir())
	const workspace, repository = "WS", "acme/widgets"
	if err := lineage.EnsureStack(ctx, stacklineage.Stack{ID: "epic:E", WorkspaceKey: workspace, RepoName: repository, RootBase: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lineage.AddNode(ctx, workspace, "epic:E", "A", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := lineage.AddNode(ctx, workspace, "epic:E", "B", "A", ""); err != nil {
		t.Fatal(err)
	}
	capability := &SourceControlCapability{lineage: lineage}
	recorded, err := capability.RecordTaskOutcome(ctx, sourcecontrol.TaskOutcomeCommand{
		WorkspaceKey: workspace, Repository: repository, TaskID: "A",
		Metadata: map[string]string{"delivery": "pull_request", "github_head_sha": "deadbeef"},
	})
	if err != nil || !recorded {
		t.Fatalf("RecordTaskOutcome = %v, %v", recorded, err)
	}
	nodes, err := lineage.ListNodes(ctx, workspace, "epic:E")
	if err != nil {
		t.Fatal(err)
	}
	if nodes[0].State != stacklineage.NodeStatePublished || nodes[0].OutputSHA != "deadbeef" || nodes[0].LastPublishedAt == nil {
		t.Fatalf("published node = %+v", nodes[0])
	}
	lookup := taskworktree.StackLineageLookup{Store: lineage}
	want := stacklineage.OutputBranchName("epic:E", "A")
	if got, ok, err := lookup.BaseRefForTask(ctx, workspace, repository, "B"); err != nil || !ok || got != want {
		t.Fatalf("dependent base = %q, %v, %v; want %q", got, ok, err, want)
	}

	recorded, err = capability.RecordTaskOutcome(ctx, sourcecontrol.TaskOutcomeCommand{
		WorkspaceKey: workspace, Repository: repository, TaskID: "A",
		Metadata: map[string]string{"delivery": "pull_request_skipped_no_changes"},
	})
	if err != nil || !recorded {
		t.Fatalf("RecordTaskOutcome empty = %v, %v", recorded, err)
	}
	if got, ok, err := lookup.BaseRefForTask(ctx, workspace, repository, "B"); err != nil || !ok || got != "main" {
		t.Fatalf("empty predecessor base = %q, %v, %v", got, ok, err)
	}
}

func TestRecordTaskOutcomeIgnoresNonDeliveryEvidence(t *testing.T) {
	capability := &SourceControlCapability{}
	for _, metadata := range []map[string]string{nil, {"delivery": "patch_back"}} {
		recorded, err := capability.RecordTaskOutcome(t.Context(), sourcecontrol.TaskOutcomeCommand{Metadata: metadata})
		if err != nil || recorded {
			t.Fatalf("RecordTaskOutcome = %v, %v", recorded, err)
		}
	}
}
