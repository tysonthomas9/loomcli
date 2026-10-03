package git

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

// The verdict response reports applied, and publishes, only when the change
// was applied; a spent follow reports its reason and is never published.
func TestVerdictFollowOutcomes(t *testing.T) {
	const reason = "it was applied and later unapplied from this lead; approve again to apply it"
	for _, tc := range []struct {
		name          string
		followed      apply.FollowResult
		state, stated string
		status        string
		wantReason    string
		published     bool
	}{
		{name: "applied now", followed: apply.FollowResult{Applied: []string{"C1"}}, status: "applied", published: true},
		{name: "spent now", followed: apply.FollowResult{Spent: []apply.SpentApproval{{Change: "C1", Revision: 1, Reason: reason}}},
			status: "spent", wantReason: reason},
		{name: "nothing followed and not applied", status: "approved"},
		{name: "another change applied", followed: apply.FollowResult{Applied: []string{"C2"}}, status: "approved"},
		{name: "follow still applied", state: "applied", status: "applied", published: true},
		{name: "follow already spent", state: "spent", stated: reason, status: "spent", wantReason: reason},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousFollow, previousArea, previousState := followApproved, hasWorkingArea, approvalFollowState
			t.Cleanup(func() {
				followApproved, hasWorkingArea, approvalFollowState = previousFollow, previousArea, previousState
			})
			hasWorkingArea = func(context.Context, *review.Local, string, string) (bool, error) { return true, nil }
			followApproved = func(context.Context, string, string) (apply.FollowResult, error) { return tc.followed, nil }
			approvalFollowState = func(context.Context, *review.Local, string, string, string, int) (string, string, error) {
				return tc.state, tc.stated, nil
			}
			published := false
			publisher := func(context.Context, string, string) error { published = true; return nil }
			response := httptest.NewRecorder()
			followVerdict(response, httptest.NewRequest("POST", "/", nil), nil,
				loomgit.Verdict{Workspace: "W", Change: "C1", Number: 1}, "L", publisher)
			var body struct {
				Success bool   `json:"success"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 200 || !body.Success {
				t.Fatalf("response: %d %s, %v", response.Code, response.Body.String(), err)
			}
			if body.Status != tc.status || body.Reason != tc.wantReason || published != tc.published {
				t.Fatalf("status %q reason %q published %t; want %q %q %t", body.Status, body.Reason, published,
					tc.status, tc.wantReason, tc.published)
			}
		})
	}
}
