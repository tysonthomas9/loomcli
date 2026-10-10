package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/apply"
	"github.com/tysonthomas9/loomcli/internal/loomgit/publish"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
)

func setLeadMayApprove(t *testing.T, enabled bool) {
	t.Helper()
	if _, _, err := publish.SetGitSettingsLocal(context.Background(), "W", publish.GitSettingsChange{LeadMayApprovePublish: &enabled},
		review.Actor{Kind: "human", ID: "tyson"}, nil); err != nil {
		t.Fatal(err)
	}
}

func taskVerdict(t *testing.T) string {
	t.Helper()
	store, err := review.OpenLocal()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	revisions, err := store.TaskRevisions(context.Background(), "W", "T")
	if err != nil || len(revisions) != 1 {
		t.Fatalf("revisions=%v err=%v", revisions, err)
	}
	return revisions[0].Verdict
}

// The verdict API's 409 names the reason, not only Loom's code.
func TestVerdictRefusalCarriesTheReason(t *testing.T) {
	change, head, number := freezeTaskRevision(t)
	setLeadMayApprove(t, false)
	body, _ := json.Marshal(map[string]any{"head_sha": head, "verdict": "approve", "actor": map[string]string{"kind": "lead", "id": "lead"}})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/workspaces/W/changes/"+change+"/revisions/"+strconv.Itoa(number)+"/verdict", bytes.NewReader(body))
	request.SetPathValue("ws", "W")
	request.SetPathValue("change", change)
	request.SetPathValue("r", strconv.Itoa(number))
	handleVerdict(recorder, request)
	var response map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	if recorder.Code != http.StatusConflict || response["error"] != "review_required" || response["message"] != "lead approval policy is off" {
		t.Fatalf("lead approval with the policy off = %d %s", recorder.Code, recorder.Body.String())
	}
	if verdict := taskVerdict(t); verdict != "" {
		t.Fatalf("a refused lead approval recorded verdict %q", verdict)
	}
}

// The CLI's pinned approval (S4) keeps Lead may approve and records the lead
// as a policy verdict, never a human one (D42).
func TestApproveLocalPinnedAppliesTheActorsPolicy(t *testing.T) {
	change, head, number := freezeTaskRevision(t)
	ctx := context.Background()
	lead := review.Actor{Kind: "lead", ID: "lead"}
	setLeadMayApprove(t, false)
	_, err := apply.ApproveLocalPinned(ctx, "W", "lead", change, number, head, lead, true)
	if !errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) || !strings.Contains(err.Error(), "lead approval policy is off") {
		t.Fatalf("lead approval with the policy off: %v", err)
	}
	if verdict := taskVerdict(t); verdict != "" {
		t.Fatalf("a refused lead approval recorded verdict %q", verdict)
	}
	_, err = apply.ApproveLocalPinned(ctx, "W", "lead", change, number, strings.Repeat("0", 40), review.Actor{Kind: "human", ID: "tyson"}, true)
	if !errors.Is(err, loomgit.NewError(loomgit.StaleSubject, "", nil)) || taskVerdict(t) != "" {
		t.Fatalf("approval of code other than the shown head was not refused as stale: %v", err)
	}
	_, err = apply.ApproveLocalPinned(ctx, "W", "lead", change, number, head, review.Actor{Kind: "agent", ID: "worker"}, true)
	if !errors.Is(err, loomgit.NewError(loomgit.ReviewRequired, "", nil)) || taskVerdict(t) != "" {
		t.Fatalf("the authoring agent approved its own revision: %v", err)
	}
	setLeadMayApprove(t, true)
	// Following needs a working area; the verdict is recorded before it.
	_, _ = apply.ApproveLocalPinned(ctx, "W", "lead", change, number, head, lead, true)
	if verdict := taskVerdict(t); verdict != "policy" {
		t.Fatalf("lead approval with the policy on recorded %q, want policy", verdict)
	}
}
