package git

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskreview"
)

// TestMain keeps verdict tests off any real issue backend: settling a task's
// review status (P1.26) is stubbed unless a test records it.
func TestMain(m *testing.M) {
	settleTask = func(context.Context, string, string) (taskreview.Decision, error) { return taskreview.Wait, nil }
	os.Exit(m.Run())
}

// D29 / P1.26: a verdict settles its task at once (Approve once applied
// closes it, Reject reopens it) so the task list shows the outcome.
func TestVerdictSettlesItsTaskReview(t *testing.T) {
	change, head, number := freezeTaskRevision(t)
	previousFollow, previousArea, previousPublish, previousSettle := followApproved, hasWorkingArea, publishApproved, settleTask
	t.Cleanup(func() {
		followApproved, hasWorkingArea, publishApproved, settleTask = previousFollow, previousArea, previousPublish, previousSettle
	})
	hasWorkingArea = func(context.Context, *review.Local, string, string) (bool, error) { return true, nil }
	followApproved = func(context.Context, string, string) (apply.FollowResult, error) {
		return apply.FollowResult{Applied: []string{change}}, nil
	}
	publishApproved = func(context.Context, string, string, publish.DeclaredStacks) ([]publish.ApprovalOutcome, error) {
		return []publish.ApprovalOutcome{{Change: change, Status: "published", PRNumber: 3}}, nil
	}
	var settled []string
	settleTask = func(_ context.Context, workspace, got string) (taskreview.Decision, error) {
		if got != change {
			t.Fatalf("settled change %s, want %s", got, change)
		}
		settled = append(settled, workspace)
		return taskreview.Wait, nil
	}
	post := func(verdict string) int {
		data, _ := json.Marshal(map[string]any{"head_sha": head, "verdict": verdict, "reason": "r",
			"actor": map[string]string{"kind": "human", "id": "user"}})
		recorder := httptest.NewRecorder()
		handleVerdict(recorder, withPath(httptest.NewRequest("POST", "/", bytes.NewReader(data)), change, number))
		return recorder.Code
	}
	if code := post("approve"); code != http.StatusOK || len(settled) != 1 {
		t.Fatalf("approve = %d, settled %v, want one settle", code, settled)
	}
	if code := post("reject"); code != http.StatusOK || len(settled) != 2 || settled[1] != "W" {
		t.Fatalf("reject = %d, settled %v, want a second settle for W", code, settled)
	}
}

func withPath(req *http.Request, change string, number int) *http.Request {
	req.SetPathValue("ws", "W")
	req.SetPathValue("change", change)
	req.SetPathValue("r", strconv.Itoa(number))
	return req
}
