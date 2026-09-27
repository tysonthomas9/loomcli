//go:build daemon_bugreplay

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// Bug-replay fault tests, group "claims" (claim/ownership/lease, bucket C).
// Catalogue row #786; no model invariant (bucket C). The local invariant is
// "a ready child is claimable": an open parent (epic) is containment, not a
// blocker, so ClaimIssue must reach the backend claim.
//
// v5 @ 1c6dabfc8: internal/webui/service/issue_impl.go:406 ensureClaimable
// passes every dependency to firstOpenClaimBlocker, and parent-child
// AffectsReadyWork, so the open epic rejects the claim with 409.
// Expected: FAIL on v5, PASS on the #786 head (3adb9b68c).

// claimsBugreplayBackend answers Get per issue ID (the #786 fix walks the
// ancestor chain) and delegates everything else to the package fake.
type claimsBugreplayBackend struct {
	*fakeIssueBackend
	getMu sync.Mutex
	byID  map[string]*backend.IssueDetailData
}

func (b *claimsBugreplayBackend) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	b.getMu.Lock()
	defer b.getMu.Unlock()
	if d, ok := b.byID[id]; ok {
		return d, nil
	}
	return nil, backend.ErrNotFound("Get", "issue "+id+" not found")
}

func claimsIssue(id, parent string, deps ...backend.DependencyData) *backend.IssueDetailData {
	now := time.Now().UTC()
	return &backend.IssueDetailData{
		IssueData:    backend.IssueData{ID: id, Title: id, Status: "open", Priority: 1, Parent: parent, CreatedAt: now, UpdatedAt: now},
		Dependencies: deps,
	}
}

func claimsDep(issueID, dependsOnID, depType, status string) backend.DependencyData {
	return backend.DependencyData{IssueID: issueID, DependsOnID: dependsOnID, Type: depType, Status: status}
}

func claimsService(be *claimsBugreplayBackend) IssueService {
	return NewIssueServiceWithBackend(nil, nil, nil, func(context.Context) backend.IssueBackend { return be })
}

func TestBugReplay_PR786_ReadyChildOfOpenEpicIsClaimable(t *testing.T) {
	edge := claimsDep("i-1", "epic-1", "parent-child", "open")
	be := &claimsBugreplayBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		byID: map[string]*backend.IssueDetailData{
			"i-1":    claimsIssue("i-1", "epic-1", edge),
			"epic-1": claimsIssue("epic-1", "", edge),
		},
	}
	_, err := claimsService(be).ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "i-1"})
	if err != nil {
		var sErr *ServiceError
		if errors.As(err, &sErr) {
			t.Fatalf("ClaimIssue rejected a ready child of an open epic: kind=%v message=%q", sErr.Kind, sErr.Message)
		}
		t.Fatalf("ClaimIssue: %v", err)
	}
	if len(be.claimCalls) != 1 || be.claimCalls[0].id != "i-1" {
		t.Fatalf("backend claim calls = %+v, want one claim of i-1", be.claimCalls)
	}
}

// TestBugReplay_PR786_OpenBlockerStillRejects is the guard half: the fix must
// not turn off real blockers. Passes on v5 and on the #786 head.
func TestBugReplay_PR786_OpenBlockerStillRejects(t *testing.T) {
	be := &claimsBugreplayBackend{
		fakeIssueBackend: &fakeIssueBackend{},
		byID: map[string]*backend.IssueDetailData{
			"i-1": claimsIssue("i-1", "epic-1",
				claimsDep("i-1", "epic-1", "parent-child", "open"),
				claimsDep("i-1", "blocker-1", "blocks", "open")),
			"epic-1": claimsIssue("epic-1", ""),
		},
	}
	_, err := claimsService(be).ClaimIssue(context.Background(), ClaimIssueParams{IssueID: "i-1"})
	var sErr *ServiceError
	if !errors.As(err, &sErr) || sErr.Kind != KindConflict {
		t.Fatalf("ClaimIssue err = %v, want a conflict for open blocker blocker-1", err)
	}
	if len(be.claimCalls) != 0 {
		t.Fatalf("blocked issue was claimed: %+v", be.claimCalls)
	}
}
