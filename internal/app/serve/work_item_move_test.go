package serve

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/workitemmove"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

type workItemMoveTransportStub struct {
	sourceWorkspace string
	sourceIssueID   string
	input           infrafleetdb.WorkItemMoveInput
	result          *infrafleetdb.WorkItemMoveResult
	err             error
}

func (stub *workItemMoveTransportStub) MoveWorkItem(
	_ context.Context,
	workspace,
	issueID string,
	input infrafleetdb.WorkItemMoveInput,
) (*infrafleetdb.WorkItemMoveResult, error) {
	stub.sourceWorkspace, stub.sourceIssueID, stub.input = workspace, issueID, input
	return stub.result, stub.err
}

func TestWorkItemMoveFleetDBAdapterMapsAtomicCommandAndCanonicalReferences(t *testing.T) {
	revision := time.Now().UTC()
	stub := &workItemMoveTransportStub{result: &infrafleetdb.WorkItemMoveResult{
		Source: &infrafleetdb.WorkItemMoveIssue{ID: "SOURCE-1", Workspace: "SOURCE"},
		Target: &infrafleetdb.WorkItemMoveIssue{ID: "TARGET-2", Workspace: "TARGET"}, Replayed: true,
	}}
	adapter, err := newWorkItemMoveFleetDBAdapter(stub)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.MoveAtomic(t.Context(), workitemmove.AtomicCommand{
		SourceWorkspace: "SOURCE", SourceIssueID: "SOURCE-1", TargetWorkspace: "TARGET",
		ExpectedSourceRevision: revision, RequestID: "move-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if stub.sourceWorkspace != "SOURCE" || stub.sourceIssueID != "SOURCE-1" ||
		stub.input.TargetWorkspace != "TARGET" || stub.input.RequestID != "move-1" ||
		!stub.input.ExpectedSourceRevision.Equal(revision) || result.Target.IssueID != "TARGET-2" || !result.Replayed {
		t.Fatalf("input=%+v result=%+v", stub.input, result)
	}
}

func TestWorkItemMoveFleetDBAdapterMapsFailuresToWorkflowVocabulary(t *testing.T) {
	for _, test := range []struct {
		err  error
		want error
	}{
		{infrafleetdb.ErrWorkItemMoveRevisionConflict, workitemmove.ErrConflict},
		{infrafleetdb.ErrWorkItemMoveIdempotencyConflict, workitemmove.ErrConflict},
		{infrafleetdb.ErrWorkItemMoveIneligible, workitemmove.ErrConflict},
		{infrafleetdb.ErrWorkItemMoveForbidden, workitemmove.ErrForbidden},
		{persistence.ErrConflict, workitemmove.ErrConflict},
		{errors.New("network down"), workitemmove.ErrUnavailable},
	} {
		adapter, err := newWorkItemMoveFleetDBAdapter(&workItemMoveTransportStub{err: test.err})
		if err != nil {
			t.Fatal(err)
		}
		_, err = adapter.MoveAtomic(t.Context(), workitemmove.AtomicCommand{})
		if !errors.Is(err, test.want) {
			t.Fatalf("error=%v want=%v", err, test.want)
		}
	}
}
