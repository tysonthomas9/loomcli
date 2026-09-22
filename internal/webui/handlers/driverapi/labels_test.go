package driverapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

func (f *fakeIssueBackend) AddLabel(_ context.Context, id string, label string) error {
	if f.labelErr != nil {
		return f.labelErr
	}
	f.labelAdds = append(f.labelAdds, id+"/"+label)
	return nil
}

func (f *fakeIssueBackend) RemoveLabel(_ context.Context, id string, label string) error {
	if f.labelErr != nil {
		return f.labelErr
	}
	f.labelRemoves = append(f.labelRemoves, id+"/"+label)
	return nil
}

func TestAddLabel(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{
		op:      "add-label",
		body:    map[string]any{"issueId": "ISSUE-7", "label": "needs-review"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, decoded)
	}
	if len(h.backend.labelAdds) != 1 || h.backend.labelAdds[0] != "ISSUE-7/needs-review" {
		t.Fatalf("labelAdds = %v, want [ISSUE-7/needs-review]", h.backend.labelAdds)
	}
	if len(h.backend.labelRemoves) != 0 {
		t.Fatalf("labelRemoves = %v, want none", h.backend.labelRemoves)
	}
	if decoded["issueId"] != "ISSUE-7" || decoded["label"] != "needs-review" {
		t.Fatalf("response = %v, want the applied issueId/label echoed", decoded)
	}
}

func TestRemoveLabel(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{
		op:      "remove-label",
		body:    map[string]any{"issueId": "ISSUE-7", "label": "needs-review"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %v)", resp.StatusCode, decoded)
	}
	if len(h.backend.labelRemoves) != 1 || h.backend.labelRemoves[0] != "ISSUE-7/needs-review" {
		t.Fatalf("labelRemoves = %v, want [ISSUE-7/needs-review]", h.backend.labelRemoves)
	}
	if len(h.backend.labelAdds) != 0 {
		t.Fatalf("labelAdds = %v, want none", h.backend.labelAdds)
	}
}

// Label ops are run-scoped: without a verified RUNNING parent DriverRun the
// mutation must not reach the backend at all.
func TestLabelOpsRequireVerifiedParent(t *testing.T) {
	for _, op := range []string{"add-label", "remove-label"} {
		t.Run(op, func(t *testing.T) {
			h := newTestHarness(t, "")
			resp, _ := h.do(t, opRequest{
				op:   op,
				body: map[string]any{"issueId": "ISSUE-7", "label": "x"},
				headers: map[string]string{
					HeaderDriverRunID:        h.runID,
					HeaderDriverNodeID:       h.nodeID,
					HeaderDriverLeaseID:      "not-the-lease",
					HeaderDriverFencingToken: "1",
				},
			})
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("status = 200, want a rejection for a non-owning caller")
			}
			if len(h.backend.labelAdds)+len(h.backend.labelRemoves) != 0 {
				t.Fatalf("backend mutated despite failed parent verification")
			}
		})
	}
}

func TestLabelOpsValidation(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing issueId", map[string]any{"label": "x"}},
		{"blank issueId", map[string]any{"issueId": "   ", "label": "x"}},
		{"missing label", map[string]any{"issueId": "ISSUE-7"}},
		{"blank label", map[string]any{"issueId": "ISSUE-7", "label": "  "}},
	}
	for _, op := range []string{"add-label", "remove-label"} {
		for _, tc := range cases {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				h := newTestHarness(t, "")
				resp, decoded := h.do(t, opRequest{op: op, body: tc.body, headers: h.ownerHeaders()})
				if resp.StatusCode != http.StatusBadRequest {
					t.Fatalf("status = %d, want 400", resp.StatusCode)
				}
				if code := errorCode(t, decoded); code != "invalid" {
					t.Fatalf("code = %q, want invalid", code)
				}
				if len(h.backend.labelAdds)+len(h.backend.labelRemoves) != 0 {
					t.Fatalf("backend mutated on an invalid request")
				}
			})
		}
	}
}

func TestLabelOpsBackendErrorSurfaces(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.labelErr = errors.New("fleet exploded")
	resp, decoded := h.do(t, opRequest{
		op:      "add-label",
		body:    map[string]any{"issueId": "ISSUE-7", "label": "x"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("status = 200, want the backend failure surfaced (body %v)", decoded)
	}
}

// TestLabelOpsAttribution pins the CURRENT, verified-against-fleet-db
// behavior: loomcli hands the issue backend a driver-run:<id> actor, but
// fleet-db journals the AUTHENTICATED identity instead — it strips the
// client-supplied X-Actor header as anti-spoofing
// (internal/auth/middleware.go), resolves the actor from the authenticated
// context (internal/api/labels.go) and journals that
// (internal/service/issue_service.go).
//
// So this asserts only the half loomcli controls: the actor reaches the
// backend factory. It deliberately does NOT assert that the actor survives
// into the journal, because it does not. The consequence is recorded on the
// op itself: a trigger binding cannot use an actor filter for self-trigger
// safety on journal-sourced label bindings.
//
// If fleet-db ever grows a delegated-actor mechanism, the constant below stops
// being merely advisory and this test is the place to tighten.
func TestLabelOpsAttribution(t *testing.T) {
	h := newTestHarness(t, "")
	resp, _ := h.do(t, opRequest{
		op:      "add-label",
		body:    map[string]any{"issueId": "ISSUE-7", "label": "x"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	want := driverpkg.DriverRunActor(h.runID)
	if h.backend.actor != want {
		t.Fatalf("actor handed to the issue backend = %q, want %q", h.backend.actor, want)
	}
}
