package driverapi

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

type reviewIssues struct {
	backend.IssueBackend
	updates map[string]backend.UpdateParams
}

func (r *reviewIssues) Update(_ context.Context, id string, params backend.UpdateParams) error {
	r.updates[id] = params
	return nil
}

// D29 / P1.26: a driver completing a task whose code awaits review keeps it
// in review with the code-review label, in the run's workspace, as the run.
func TestCompleteTaskMarksCodeReviewAsTheDriverRun(t *testing.T) {
	issues := &reviewIssues{updates: map[string]backend.UpdateParams{}}
	var gotWS, gotActor string
	m := &Module{issueBackends: func(ws, actor string) (backend.IssueBackend, error) {
		gotWS, gotActor = ws, actor
		return issues, nil
	}}
	if err := m.codeReviewMarker("driver-run:R1")(context.Background(), "WS", "T-1"); err != nil {
		t.Fatal(err)
	}
	params, ok := issues.updates["T-1"]
	if gotWS != "WS" || gotActor != "driver-run:R1" || !ok || params.Status == nil || *params.Status != "review" ||
		!backend.HasCodeReviewLabel(params.AddLabels) {
		t.Fatalf("ws %q actor %q update %+v, want T-1 in review with the code-review label", gotWS, gotActor, params)
	}
}
