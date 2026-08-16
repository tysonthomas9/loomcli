package workitemmove

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/modules/workspace"
)

type atomicMoverStub struct {
	command AtomicCommand
	result  *AtomicResult
	err     error
	calls   int
}

func (stub *atomicMoverStub) MoveAtomic(_ context.Context, command AtomicCommand) (*AtomicResult, error) {
	stub.calls++
	stub.command = command
	return stub.result, stub.err
}

type workspaceCatalogStub struct {
	refs map[string]*workspace.Reference
}

func (stub workspaceCatalogStub) Resolve(_ context.Context, query workspace.ResolveQuery) (*workspace.Reference, error) {
	if value := stub.refs[query.Reference]; value != nil {
		return value, nil
	}
	return nil, workspace.ErrNotFound
}

func TestCoordinatorResolvesCanonicalKeysAndCallsOneAtomicPort(t *testing.T) {
	revision := time.Date(2026, 8, 16, 12, 0, 0, 123000000, time.UTC)
	mover := &atomicMoverStub{result: &AtomicResult{
		Source: Reference{Workspace: "SOURCE", IssueID: "SOURCE-7"},
		Target: Reference{Workspace: "TARGET", IssueID: "TARGET-9"},
	}}
	coordinator, err := New(mover, workspaceCatalogStub{refs: map[string]*workspace.Reference{
		"Source Name": {Key: "SOURCE", Name: "Source Name"},
		"Target Name": {Key: "TARGET", Name: "Target Name"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := coordinator.Move(t.Context(), Command{
		IssueID: " SOURCE-7 ", SourceWorkspace: " Source Name ", TargetWorkspace: " Target Name ",
		ExpectedSourceRevision: revision, RequestID: " move-1 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if mover.calls != 1 || mover.command.SourceWorkspace != "SOURCE" ||
		mover.command.TargetWorkspace != "TARGET" || mover.command.SourceIssueID != "SOURCE-7" ||
		mover.command.RequestID != "move-1" || !mover.command.ExpectedSourceRevision.Equal(revision) {
		t.Fatalf("calls=%d command=%+v", mover.calls, mover.command)
	}
	if result.TargetID != "TARGET-9" {
		t.Fatalf("result=%+v", result)
	}
}

func TestCoordinatorFailsBeforeAtomicPortForInvalidIntent(t *testing.T) {
	revision := time.Now().UTC()
	for _, command := range []Command{
		{},
		{IssueID: "SOURCE-1", SourceWorkspace: "source", TargetWorkspace: "target", ExpectedSourceRevision: revision},
		{IssueID: "SOURCE-1", SourceWorkspace: "source", TargetWorkspace: "source", ExpectedSourceRevision: revision, RequestID: "move"},
	} {
		mover := &atomicMoverStub{}
		coordinator, err := New(mover, workspaceCatalogStub{refs: map[string]*workspace.Reference{
			"source": {Key: "SOURCE", Name: "source"}, "target": {Key: "TARGET", Name: "target"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := coordinator.Move(t.Context(), command); !errors.Is(err, workitems.ErrInvalid) {
			t.Fatalf("command=%+v error=%v", command, err)
		}
		if mover.calls != 0 {
			t.Fatalf("invalid command reached atomic port: %+v", command)
		}
	}
}

func TestCoordinatorRejectsDivergentOwnerResult(t *testing.T) {
	mover := &atomicMoverStub{result: &AtomicResult{
		Source: Reference{Workspace: "SOURCE", IssueID: "SOURCE-1"},
		Target: Reference{Workspace: "OTHER", IssueID: "OTHER-1"},
	}}
	coordinator, err := New(mover, workspaceCatalogStub{refs: map[string]*workspace.Reference{
		"source": {Key: "SOURCE"}, "target": {Key: "TARGET"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.Move(t.Context(), Command{
		IssueID: "SOURCE-1", SourceWorkspace: "source", TargetWorkspace: "target",
		ExpectedSourceRevision: time.Now().UTC(), RequestID: "move-1",
	})
	if !errors.Is(err, workitems.ErrInvalidPersistedState) {
		t.Fatalf("error=%v", err)
	}
}
