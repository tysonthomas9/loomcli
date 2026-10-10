package taskrunapi

import (
	"context"
	"net/http"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type reviewIssueBackend struct {
	fakeIssueBackend
	updates []backend.UpdateParams
}

func (r *reviewIssueBackend) Update(_ context.Context, _ string, params backend.UpdateParams) error {
	r.updates = append(r.updates, params)
	return nil
}

type closeCapturingRuns struct {
	store.TaskRunStore
	closeTask []bool
}

func (c *closeCapturingRuns) Complete(ctx context.Context, ws, id string, complete store.TaskRunComplete) (*domain.TaskRun, error) {
	c.closeTask = append(c.closeTask, complete.CloseTask)
	return c.TaskRunStore.Complete(ctx, ws, id, complete)
}

type closeCapturingStore struct {
	store.Store
	runs *closeCapturingRuns
}

func (s closeCapturingStore) TaskRuns() store.TaskRunStore { return s.runs }

func stubReviewJournal(t *testing.T, awaits bool) *[]string {
	t.Helper()
	var attempts []string
	prior := attemptAwaitsReview
	attemptAwaitsReview = func(_ context.Context, _, _, attempt string) (bool, error) {
		attempts = append(attempts, attempt)
		return awaits, nil
	}
	t.Cleanup(func() { attemptAwaitsReview = prior })
	return &attempts
}

// D29 / P1.26 (cloud runners): a runner's completion asks to close its task,
// but a run that froze code awaiting review leaves the task open in review.
func TestRunnerCompleteKeepsTaskWithCodeOpenForReview(t *testing.T) {
	for _, tc := range []struct {
		name      string
		awaits    bool
		wantClose bool
	}{{"code awaits review", true, false}, {"no changes", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := stubReviewJournal(t, tc.awaits)
			h := newHarness(t)
			runs := &closeCapturingRuns{TaskRunStore: h.store.TaskRuns()}
			issues := &reviewIssueBackend{}
			module := NewModule(Config{Store: closeCapturingStore{Store: h.store, runs: runs},
				IssueBackends: func(string, string) (backend.IssueBackend, error) { return issues, nil }})
			_, err := module.complete(context.Background(), "WS", leaseIdentity{TaskRunID: h.taskRunID, NodeID: h.nodeID,
				LeaseID: h.leaseID, LeaseToken: h.token, FencingToken: h.fence},
				[]byte(`{"completionId":"c1","status":"completed","closeTask":true,"runtimeMetadata":{"attempt_id":"att-1"}}`))
			if err != nil {
				t.Fatalf("complete: %v", err)
			}
			if len(runs.closeTask) != 1 || runs.closeTask[0] != tc.wantClose {
				t.Fatalf("CloseTask = %v, want %v", runs.closeTask, tc.wantClose)
			}
			marked := len(issues.updates) == 1 && *issues.updates[0].Status == "review" && backend.HasCodeReviewLabel(issues.updates[0].AddLabels)
			if marked == tc.wantClose || len(*attempts) != 1 || (*attempts)[0] != "att-1" {
				t.Fatalf("updates = %+v attempts = %v, want review mark %v for att-1", issues.updates, *attempts, !tc.wantClose)
			}
		})
	}
}

// A completion that doesn't close its task never consults the journal.
func TestRunnerCompleteWithoutCloseLeavesTaskAlone(t *testing.T) {
	attempts := stubReviewJournal(t, true)
	h := newHarness(t)
	resp, out := h.postOp(t, "complete", map[string]any{"completionId": "c1", "status": "completed"}, identity{})
	if resp.StatusCode != http.StatusOK || len(*attempts) != 0 {
		t.Fatalf("complete = %d %v attempts %v, want no review check", resp.StatusCode, out, *attempts)
	}
}
