package fleet

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestGetMutations_ProjectsOwningIssueFromEntitySnapshots(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fb, ts := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		respondOK(w, fleetMutationsResponse{
			Events: []fleetMutationEvent{
				{
					Timestamp:  now,
					Action:     "comment.add",
					EntityType: "comment",
					EntityID:   "issue-live",
				},
				{
					Timestamp:  now,
					Action:     "comment.add",
					EntityType: "comment",
					EntityID:   "comment-1",
					After:      `{"id":"comment-1","issue_id":"issue-1"}`,
				},
				{
					Timestamp:  now,
					Action:     "label.remove",
					EntityType: "label",
					EntityID:   "label-1",
					Before:     `{"id":"label-1","issue_id":"issue-2"}`,
				},
				{
					Timestamp:  now,
					Action:     "dep.add",
					EntityType: "dependency",
					EntityID:   "dependency-1",
					After:      `{"id":"dependency-1","issue_id":"issue-3"}`,
				},
			},
		})
	})
	defer ts.Close()

	got, err := fb.GetMutations(context.Background(), 0)
	if err != nil {
		t.Fatalf("GetMutations: %v", err)
	}
	wantIssueIDs := []string{"issue-live", "issue-1", "issue-2", "issue-3"}
	if len(got) != len(wantIssueIDs) {
		t.Fatalf("got %d mutations, want %d", len(got), len(wantIssueIDs))
	}
	for i, want := range wantIssueIDs {
		if got[i].IssueID != want {
			t.Errorf("got[%d].IssueID = %q, want owning issue %q", i, got[i].IssueID, want)
		}
	}
}
