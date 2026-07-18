package epic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/managementapi"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

type epicRunManagementStub struct {
	workspace string
	requests  []managementapi.SubmitDriverRunRequest
	states    []*domain.DriverRun
	getErr    error
}

func (stub *epicRunManagementStub) Workspace() string { return stub.workspace }

func (stub *epicRunManagementStub) SubmitDriverRun(
	_ context.Context,
	request managementapi.SubmitDriverRunRequest,
) (*domain.DriverRun, error) {
	stub.requests = append(stub.requests, request)
	return &domain.DriverRun{
		WorkspaceKey: stub.workspace, RunID: request.RunID,
		DriverID: request.DriverRef, DriverVersionID: "version-1",
		Status: domain.DriverRunQueued, EpicID: request.EpicID,
		Payload: append(json.RawMessage(nil), request.Payload...),
	}, nil
}

func (stub *epicRunManagementStub) GetDriverRun(context.Context, string) (*domain.DriverRun, error) {
	if stub.getErr != nil {
		return nil, stub.getErr
	}
	if len(stub.states) == 0 {
		return nil, nil
	}
	state := stub.states[0]
	stub.states = stub.states[1:]
	return state, nil
}

func TestQueueEpicWorkflowRunUsesServerStampedManagementSubmission(t *testing.T) {
	originalParent := runParent
	runParent = "EPIC-1"
	t.Cleanup(func() { runParent = originalParent })
	management := &epicRunManagementStub{workspace: "TEST"}
	run, err := queueEpicWorkflowRun(
		context.Background(), management, "epic-runner", "run-explicit", json.RawMessage(`{"runner":"daytona"}`),
	)
	if err != nil {
		t.Fatalf("queueEpicWorkflowRun: %v", err)
	}
	if run.RunID != "run-explicit" || len(management.requests) != 1 {
		t.Fatalf("run=%#v requests=%#v", run, management.requests)
	}
	request := management.requests[0]
	if request.CLICommand != "epic-run" || request.DriverRef != "epic-runner" || request.RunID != "run-explicit" ||
		request.Entrypoint != "run" || request.EpicID != "EPIC-1" || string(request.Payload) != `{"runner":"daytona"}` {
		t.Fatalf("management submission = %#v", request)
	}
}

func TestExecuteWorkflowRunObservesTerminalManagementState(t *testing.T) {
	management := &epicRunManagementStub{states: []*domain.DriverRun{{
		RunID: "run-1", Status: domain.DriverRunCompleted, Summary: "done",
	}}}
	if err := executeWorkflowRun(context.Background(), management, "run-1"); err != nil {
		t.Fatalf("executeWorkflowRun: %v", err)
	}
}

func TestExecuteWorkflowRunReturnsFailedStateOrReadError(t *testing.T) {
	failed := &epicRunManagementStub{states: []*domain.DriverRun{{
		RunID: "run-1", Status: domain.DriverRunFailed, Summary: "boom",
	}}}
	if err := executeWorkflowRun(context.Background(), failed, "run-1"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("failed execute error = %v", err)
	}
	want := errors.New("management unavailable")
	unavailable := &epicRunManagementStub{getErr: want}
	if err := executeWorkflowRun(context.Background(), unavailable, "run-1"); !errors.Is(err, want) {
		t.Fatalf("read error = %v, want %v", err, want)
	}
}
