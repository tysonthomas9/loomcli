package data

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// stubBlockedQuerier is a local blockedQuerier. The shared mock backend in
// internal/cli/clitest is out of reach here: the data-isolation depguard rule
// forbids cli/data from importing cli sub-packages.
type stubBlockedQuerier struct {
	blockedResult []backend.IssueData
	blockedErr    error
	blockedOpts   backend.BlockedOpts
	listResult    []backend.IssueData
	listErr       error
	listOpts      backend.ListOpts
	listCalled    bool
}

func (s *stubBlockedQuerier) Blocked(_ context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	s.blockedOpts = opts
	return s.blockedResult, s.blockedErr
}

func (s *stubBlockedQuerier) List(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	s.listOpts = opts
	s.listCalled = true
	return s.listResult, s.listErr
}

// withBlockedFlags sets the command's package-level flag vars for one test and
// restores them afterwards.
func withBlockedFlags(t *testing.T, limit int, issueType, parent string) {
	t.Helper()
	oldLimit, oldType, oldParent := blockedLimit, blockedType, blockedParent
	blockedLimit, blockedType, blockedParent = limit, issueType, parent
	t.Cleanup(func() {
		blockedLimit, blockedType, blockedParent = oldLimit, oldType, oldParent
	})
}

func issueIDs(items []backend.IssueData) []string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// The two views are unioned: an issue parked with status=blocked is invisible
// to the backend's dependency-edge query, which is what hid the tasks behind
// the claim loop from the operator.
func TestFetchBlockedIssues_UnionsBothViews(t *testing.T) {
	withBlockedFlags(t, 0, "", "")
	mock := &stubBlockedQuerier{
		blockedResult: []backend.IssueData{
			{ID: "A", Status: "open", BlockedBy: []string{"dep-1"}, BlockedByCount: 1},
		},
		listResult: []backend.IssueData{{ID: "B", Status: "blocked"}},
	}

	items, err := fetchBlockedIssues(context.Background(), mock)
	if err != nil {
		t.Fatalf("fetchBlockedIssues: %v", err)
	}
	if got := issueIDs(items); strings.Join(got, ",") != "A,B" {
		t.Fatalf("ids = %v, want [A B]", got)
	}
}

func TestFetchBlockedIssues_DeDupesKeepingDependencyMetadata(t *testing.T) {
	withBlockedFlags(t, 0, "", "")
	mock := &stubBlockedQuerier{
		blockedResult: []backend.IssueData{
			{ID: "A", Status: "blocked", BlockedBy: []string{"dep-1"}, BlockedByCount: 1},
		},
		listResult: []backend.IssueData{{ID: "A", Status: "blocked"}},
	}

	items, err := fetchBlockedIssues(context.Background(), mock)
	if err != nil {
		t.Fatalf("fetchBlockedIssues: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %v, want exactly one", issueIDs(items))
	}
	if items[0].BlockedByCount != 1 || len(items[0].BlockedBy) != 1 {
		t.Fatalf("dependency metadata lost: %+v", items[0])
	}
}

func TestFetchBlockedIssues_ForwardsFiltersToBothQueries(t *testing.T) {
	withBlockedFlags(t, 4, "bug", "EPIC-1")
	mock := &stubBlockedQuerier{}

	if _, err := fetchBlockedIssues(context.Background(), mock); err != nil {
		t.Fatalf("fetchBlockedIssues: %v", err)
	}
	if !mock.listCalled {
		t.Fatal("the status=blocked query was never issued")
	}
	if mock.blockedOpts.Limit != 4 || mock.blockedOpts.Type != "bug" || mock.blockedOpts.ParentID != "EPIC-1" {
		t.Errorf("BlockedOpts = %#v", mock.blockedOpts)
	}
	lopts := mock.listOpts
	if lopts.Status != "blocked" || lopts.Limit != 4 || lopts.IssueType != "bug" || lopts.ParentID != "EPIC-1" {
		t.Errorf("ListOpts = %#v", lopts)
	}
}

func TestFetchBlockedIssues_LimitAppliedAfterMerge(t *testing.T) {
	withBlockedFlags(t, 4, "", "")
	mock := &stubBlockedQuerier{
		blockedResult: []backend.IssueData{{ID: "A"}, {ID: "B"}, {ID: "C"}},
		listResult:    []backend.IssueData{{ID: "D"}, {ID: "E"}, {ID: "F"}},
	}

	items, err := fetchBlockedIssues(context.Background(), mock)
	if err != nil {
		t.Fatalf("fetchBlockedIssues: %v", err)
	}
	if got := issueIDs(items); strings.Join(got, ",") != "A,B,C,D" {
		t.Fatalf("ids = %v, want the first 4 after merge", got)
	}
}

func TestFetchBlockedIssues_ErrorFromEitherQueryPropagates(t *testing.T) {
	withBlockedFlags(t, 0, "", "")
	boom := errors.New("boom")

	depFails := &stubBlockedQuerier{blockedErr: boom}
	if _, err := fetchBlockedIssues(context.Background(), depFails); !errors.Is(err, boom) {
		t.Errorf("Blocked error = %v, want it propagated", err)
	}

	listFails := &stubBlockedQuerier{listErr: boom}
	if _, err := fetchBlockedIssues(context.Background(), listFails); !errors.Is(err, boom) {
		t.Errorf("List error = %v, want it propagated", err)
	}
}

func TestFetchBlockedIssues_EmptyUnionRenders(t *testing.T) {
	withBlockedFlags(t, 0, "", "")
	mock := &stubBlockedQuerier{}

	items, err := fetchBlockedIssues(context.Background(), mock)
	if err != nil {
		t.Fatalf("fetchBlockedIssues: %v", err)
	}
	if items == nil {
		t.Fatal("items = nil, want a non-nil empty slice so -o json emits []")
	}

	var text, jsonOut bytes.Buffer
	if err := printIssueList(&text, items, "text"); err != nil {
		t.Fatalf("printIssueList text: %v", err)
	}
	if !strings.Contains(text.String(), "(no issues)") {
		t.Errorf("text output = %q, want (no issues)", text.String())
	}
	if err := printIssueList(&jsonOut, items, formatJSON); err != nil {
		t.Fatalf("printIssueList json: %v", err)
	}
	if strings.TrimSpace(jsonOut.String()) != "[]" {
		t.Errorf("json output = %q, want []", jsonOut.String())
	}
}
