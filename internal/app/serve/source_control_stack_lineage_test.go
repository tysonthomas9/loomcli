package serve

import (
	"testing"
	"time"

	stackstore "github.com/tysonthomas9/loomcli/internal/infra/sourcecontrolstackstore"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol/stacklineage"
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
	adapter, err := stackstore.NewAdapter(lineage)
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := sourcecontrol.NewTaskOutcomes(adapter, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	stacks, err := sourcecontrol.NewStackLifecycle(adapter, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	capability := &SourceControlCapability{outcomes: outcomes, stacks: stacks}
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
	want := stacklineage.OutputBranchName("epic:E", "A")
	if got, ok, err := capability.ResolveTaskStackBinding(ctx, workspace, repository, "B"); err != nil || !ok || got.BaseRef != want {
		t.Fatalf("dependent binding = %+v, %v, %v; want base %q", got, ok, err, want)
	}

	recorded, err = capability.RecordTaskOutcome(ctx, sourcecontrol.TaskOutcomeCommand{
		WorkspaceKey: workspace, Repository: repository, TaskID: "A",
		Metadata: map[string]string{"delivery": "pull_request_skipped_no_changes"},
	})
	if err != nil || !recorded {
		t.Fatalf("RecordTaskOutcome empty = %v, %v", recorded, err)
	}
	if got, ok, err := capability.ResolveTaskStackBinding(ctx, workspace, repository, "B"); err != nil || !ok || got.BaseRef != "main" {
		t.Fatalf("empty predecessor binding = %+v, %v, %v", got, ok, err)
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
