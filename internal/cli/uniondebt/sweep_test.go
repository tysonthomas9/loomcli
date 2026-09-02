package uniondebt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// --- fakes ---

type listCall struct {
	status string
	labels []string
}

type fakeBackend struct {
	// issues keyed by status, as the backend's List filters see them.
	byStatus  map[string][]backend.IssueData
	listed    []listCall
	created   []backend.CreateParams
	added     []string // "<id>:<label>"
	removed   []string // "<id>:<label>"
	comments  map[string][]string
	createErr error
	listErr   error
	nextID    int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{byStatus: map[string][]backend.IssueData{}, comments: map[string][]string{}}
}

func (f *fakeBackend) add(iss backend.IssueData) {
	f.byStatus[iss.Status] = append(f.byStatus[iss.Status], iss)
}

func (f *fakeBackend) List(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	f.listed = append(f.listed, listCall{status: opts.Status, labels: append([]string(nil), opts.Labels...)})
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []backend.IssueData
	for _, iss := range f.byStatus[opts.Status] {
		if hasAll(iss.Labels, opts.Labels) {
			out = append(out, iss)
		}
	}
	return out, nil
}

func hasAll(have, want []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (f *fakeBackend) Create(_ context.Context, p backend.CreateParams) (*backend.IssueData, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, p)
	f.nextID++
	id := fmt.Sprintf("NEW-%d", f.nextID)
	created := backend.IssueData{ID: id, Title: p.Title, Status: p.Status, Labels: p.Labels, SourceRepo: p.SourceRepo}
	f.add(created)
	return &created, nil
}

func (f *fakeBackend) AddLabel(_ context.Context, id, label string) error {
	f.added = append(f.added, id+":"+label)
	return nil
}

func (f *fakeBackend) RemoveLabel(_ context.Context, id, label string) error {
	f.removed = append(f.removed, id+":"+label)
	return nil
}

func (f *fakeBackend) AddComment(_ context.Context, p backend.CommentAddParams) (*backend.CommentData, error) {
	f.comments[p.IssueID] = append(f.comments[p.IssueID], p.Text)
	return &backend.CommentData{}, nil
}

// stubProber returns a canned result per task ID.
type stubProber struct {
	results map[string]ProbeResult
	errs    map[string]error
	calls   []string
}

func (s *stubProber) Probe(_, _, taskID string) (ProbeResult, error) {
	s.calls = append(s.calls, taskID)
	if err := s.errs[taskID]; err != nil {
		return ProbeResult{}, err
	}
	return s.results[taskID], nil
}

// --- helpers ---

func testContract(t *testing.T) *Contract {
	t.Helper()
	c, err := LoadContract(writeContract(t, contractFixture))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func closedIssue(id, repo string, priority int) backend.IssueData {
	return backend.IssueData{
		ID: id, Status: "closed", SourceRepo: repo, Priority: priority,
		Labels: []string{MarkerLabel, "delivered"},
	}
}

func run(t *testing.T, f *fakeBackend, p prober, opts Options) *Report {
	t.Helper()
	if opts.Contract == nil {
		opts.Contract = testContract(t)
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC) }
	}
	rep, err := NewSweeper(f, p, opts).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

func onlyItem(t *testing.T, rep *Report) Item {
	t.Helper()
	if len(rep.Items) != 1 {
		t.Fatalf("expected exactly 1 item, got %d: %+v", len(rep.Items), rep.Items)
	}
	return rep.Items[0]
}

// --- tests ---

func TestSweep_FilesDebtTicketForConflict(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-103", "loomcli", 1))
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-103": {Class: ClassConflict, Ref: "origin/loom/PUPPET-103", TipSHA: "abc123", Conflict: "CONFLICT (content): shared.go"},
	}}

	rep := run(t, f, p, Options{})
	item := onlyItem(t, rep)

	if item.Action != ActionFiled || item.DerivedID != "NEW-1" {
		t.Fatalf("item = %+v, want filed -> NEW-1", item)
	}
	if len(f.created) != 1 {
		t.Fatalf("expected 1 create, got %d", len(f.created))
	}
	got := f.created[0]

	// The derived ticket must clear all four claim gates: open, unassigned,
	// non-epic, carrying `approved` and no excluded label.
	if got.Status != "open" {
		t.Errorf("Status = %q, want open", got.Status)
	}
	if got.Assignee != "" {
		t.Errorf("Assignee = %q; /ready is unassigned-only, an assignee hides the ticket", got.Assignee)
	}
	if got.IssueType != "task" {
		t.Errorf("IssueType = %q, want task", got.IssueType)
	}
	if got.SourceRepo != "loomcli" {
		t.Errorf("SourceRepo = %q, want loomcli", got.SourceRepo)
	}
	// Priority 1 original, floor 2.
	if got.Priority != 2 {
		t.Errorf("Priority = %d, want the floor of 2", got.Priority)
	}
	wantLabels := []string{ApprovedLabel, DebtLabel, DebtOfPrefix + "PUPPET-103"}
	if !hasAll(got.Labels, wantLabels) || len(got.Labels) != len(wantLabels) {
		t.Errorf("Labels = %v, want exactly %v", got.Labels, wantLabels)
	}
	for _, bad := range []string{"delivered", "ci-green", "ci-blocked"} {
		if hasAll(got.Labels, []string{bad}) {
			t.Errorf("derived ticket must not inherit %q", bad)
		}
	}
	if len(got.Dependencies) != 0 {
		t.Errorf("Dependencies = %v; a closed original cannot be a dependency target", got.Dependencies)
	}
	if got.IdempotencyKey != "union-debt|PUPPET-103|abc123" {
		t.Errorf("IdempotencyKey = %q, want it derived from origin and tip SHA", got.IdempotencyKey)
	}

	for _, want := range []string{"Original: PUPPET-103", "origin/loom/PUPPET-103", "abc123", "/clones/loomcli", "local/union", "CONFLICT (content): shared.go", "union-pending", "delivered"} {
		if !strings.Contains(got.Design, want) {
			t.Errorf("design body missing %q:\n%s", want, got.Design)
		}
	}

	// The original keeps its marker: the integrator retires it once the merge
	// actually lands.
	if len(f.removed) != 0 {
		t.Errorf("marker must survive filing, got removals %v", f.removed)
	}
	if c := f.comments["PUPPET-103"]; len(c) != 1 || !strings.Contains(c[0], "NEW-1") {
		t.Errorf("original should get one comment naming the derived ticket, got %v", c)
	}
}

func TestSweep_CleanIsFiledLikeConflict(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-1", "loomcli", 3))
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-1": {Class: ClassClean, Ref: "origin/loom/PUPPET-1", TipSHA: "def"},
	}}

	item := onlyItem(t, run(t, f, p, Options{}))
	if item.Action != ActionFiled {
		t.Fatalf("Action = %s, want filed — the sweeper never merges, so clean is filed too", item.Action)
	}
	if f.created[0].Priority != 3 {
		t.Errorf("Priority = %d, want the original's 3", f.created[0].Priority)
	}
}

func TestSweep_InUnionRetiresMarker(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-9", "loomcli", 2))
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-9": {Class: ClassInUnion, Ref: "origin/loom/PUPPET-9", TipSHA: "aaa"},
	}}

	item := onlyItem(t, run(t, f, p, Options{}))
	if item.Action != ActionRetired {
		t.Fatalf("Action = %s, want retired", item.Action)
	}
	if len(f.created) != 0 {
		t.Errorf("in-union debt is illusory; nothing should be filed, got %+v", f.created)
	}
	if want := "PUPPET-9:" + MarkerLabel; len(f.removed) != 1 || f.removed[0] != want {
		t.Errorf("removed = %v, want [%s]", f.removed, want)
	}
	if len(f.added) != 0 {
		t.Errorf("no replacement label for in-union, got %v", f.added)
	}
	if len(f.comments["PUPPET-9"]) != 1 {
		t.Errorf("retiring must leave a comment; got %v", f.comments["PUPPET-9"])
	}
}

func TestSweep_NoBranchStampsUnreachable(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-11", "loomcli", 2))
	p := &stubProber{results: map[string]ProbeResult{"PUPPET-11": {Class: ClassNoBranch}}}

	item := onlyItem(t, run(t, f, p, Options{}))
	if item.Action != ActionUnreachable {
		t.Fatalf("Action = %s, want unreachable", item.Action)
	}
	if want := "PUPPET-11:" + UnreachableLabel; len(f.added) != 1 || f.added[0] != want {
		t.Errorf("added = %v, want [%s]", f.added, want)
	}
	if want := "PUPPET-11:" + MarkerLabel; len(f.removed) != 1 || f.removed[0] != want {
		t.Errorf("removed = %v, want [%s]", f.removed, want)
	}
	c := f.comments["PUPPET-11"]
	if len(c) != 1 || !strings.Contains(c[0], "/clones/loomcli") {
		t.Errorf("comment must name the clone probed, got %v", c)
	}
}

func TestSweep_NoUnionTouchesNothing(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-12", "loomcli", 2))
	p := &stubProber{results: map[string]ProbeResult{"PUPPET-12": {Class: ClassNoUnion}}}

	rep := run(t, f, p, Options{})
	item := onlyItem(t, rep)
	if item.Action != ActionError || rep.Errors != 1 {
		t.Fatalf("item = %+v, errors = %d; a missing union branch is an error, not a classification to act on", item, rep.Errors)
	}
	if len(f.created)+len(f.added)+len(f.removed) != 0 {
		t.Error("a missing union branch must leave the ticket untouched")
	}
}

func TestSweep_UnknownRepoIsAnError(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-13", "local-stack", 2))
	p := &stubProber{}

	rep := run(t, f, p, Options{})
	if item := onlyItem(t, rep); item.Action != ActionError {
		t.Fatalf("Action = %s, want error for a repo with no local_integration", item.Action)
	}
	if len(p.calls) != 0 {
		t.Error("a repo with no clone must not be probed")
	}
}

func TestSweep_DedupesExistingDebtTicket(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-103", "loomcli", 2))
	f.add(backend.IssueData{
		ID: "PUPPET-500", Status: "in_progress", SourceRepo: "loomcli",
		Labels: []string{ApprovedLabel, DebtLabel, DebtOfPrefix + "PUPPET-103"},
	})
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-103": {Class: ClassConflict, Ref: "origin/loom/PUPPET-103", TipSHA: "abc"},
	}}

	item := onlyItem(t, run(t, f, p, Options{}))
	if item.Action != ActionSkipped || item.DerivedID != "PUPPET-500" {
		t.Fatalf("item = %+v, want skipped -> PUPPET-500", item)
	}
	if len(f.created) != 0 {
		t.Errorf("must not refile, got %+v", f.created)
	}
}

func TestSweep_DedupesAgainstClosedDebtTicket(t *testing.T) {
	// A debt ticket closed as abandoned must not be refiled forever.
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-103", "loomcli", 2))
	f.add(backend.IssueData{
		ID: "PUPPET-501", Status: "closed", SourceRepo: "loomcli",
		Labels: []string{DebtLabel, DebtOfPrefix + "PUPPET-103"},
	})
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-103": {Class: ClassConflict, Ref: "origin/loom/PUPPET-103", TipSHA: "abc"},
	}}

	if item := onlyItem(t, run(t, f, p, Options{})); item.Action != ActionSkipped {
		t.Fatalf("Action = %s, want skipped", item.Action)
	}
}

func TestSweep_DryRunWritesNothing(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-103", "loomcli", 2))
	f.add(closedIssue("PUPPET-9", "loomcli", 2))
	f.add(closedIssue("PUPPET-11", "loomcli", 2))
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-103": {Class: ClassConflict, Ref: "origin/loom/PUPPET-103", TipSHA: "abc"},
		"PUPPET-9":   {Class: ClassInUnion, Ref: "origin/loom/PUPPET-9", TipSHA: "bbb"},
		"PUPPET-11":  {Class: ClassNoBranch},
	}}

	rep := run(t, f, p, Options{DryRun: true})
	if len(rep.Items) != 3 || rep.Errors != 0 {
		t.Fatalf("report = %+v, want 3 clean items", rep)
	}
	if len(f.created) != 0 || len(f.added) != 0 || len(f.removed) != 0 || len(f.comments) != 0 {
		t.Errorf("dry run wrote something: created=%v added=%v removed=%v comments=%v", f.created, f.added, f.removed, f.comments)
	}
	for _, it := range rep.Items {
		if !it.DryRun {
			t.Errorf("item %s not marked dry-run", it.OriginID)
		}
	}
}

func TestSweep_LimitCapsFiling(t *testing.T) {
	f := newFakeBackend()
	results := map[string]ProbeResult{}
	for _, id := range []string{"PUPPET-1", "PUPPET-2", "PUPPET-3"} {
		f.add(closedIssue(id, "loomcli", 2))
		results[id] = ProbeResult{Class: ClassConflict, Ref: "origin/loom/" + id, TipSHA: "sha-" + id}
	}

	rep := run(t, f, &stubProber{results: results}, Options{Limit: 2})
	if len(f.created) != 2 {
		t.Fatalf("created %d tickets, want the --limit of 2", len(f.created))
	}
	var skipped int
	for _, it := range rep.Items {
		if it.Action == ActionSkipped {
			skipped++
			if !strings.Contains(it.Detail, "limit") {
				t.Errorf("skip reason = %q, want it to name the limit", it.Detail)
			}
		}
	}
	if skipped != 1 {
		t.Errorf("skipped %d, want 1", skipped)
	}
	if rep.Errors != 0 {
		t.Errorf("hitting the limit is not an error, got %d", rep.Errors)
	}
}

func TestSweep_OneFailureDoesNotStopTheRest(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-1", "loomcli", 2))
	f.add(closedIssue("PUPPET-2", "loomcli", 2))
	f.add(closedIssue("PUPPET-3", "loomcli", 2))
	p := &stubProber{
		results: map[string]ProbeResult{
			"PUPPET-1": {Class: ClassConflict, Ref: "origin/loom/PUPPET-1", TipSHA: "a"},
			"PUPPET-3": {Class: ClassConflict, Ref: "origin/loom/PUPPET-3", TipSHA: "c"},
		},
		errs: map[string]error{"PUPPET-2": errors.New("git exploded")},
	}

	rep := run(t, f, p, Options{})
	if len(rep.Items) != 3 || rep.Errors != 1 {
		t.Fatalf("report = %+v, want 3 items and 1 error", rep)
	}
	if len(f.created) != 2 {
		t.Errorf("created %d, want the 2 healthy items still filed", len(f.created))
	}
	if len(p.calls) != 3 {
		t.Errorf("probed %v, want all three attempted", p.calls)
	}
}

func TestSweep_EnumeratesLedgerPerStatus(t *testing.T) {
	// fleet-db clamps list responses, so the ledger must be read one status at
	// a time rather than in a single unfiltered call.
	f := newFakeBackend()
	rep := run(t, f, &stubProber{}, Options{})
	if len(rep.Items) != 0 {
		t.Fatalf("empty ledger should yield no items, got %+v", rep.Items)
	}
	if len(f.listed) != len(ledgerStatuses) {
		t.Fatalf("made %d List calls, want one per status (%d)", len(f.listed), len(ledgerStatuses))
	}
	for i, call := range f.listed {
		if call.status != ledgerStatuses[i] {
			t.Errorf("List %d status = %q, want %q", i, call.status, ledgerStatuses[i])
		}
		if len(call.labels) != 1 || call.labels[0] != MarkerLabel {
			t.Errorf("List %d labels = %v, want [%s]", i, call.labels, MarkerLabel)
		}
	}
}

func TestSweep_RepoFilter(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-103", "loomcli", 2))
	f.add(closedIssue("PUPPET-308", "meta-harness", 2))
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-308": {Class: ClassConflict, Ref: "loom/PUPPET-308", TipSHA: "zzz"},
	}}

	rep := run(t, f, p, Options{Repos: []string{"meta-harness"}})
	item := onlyItem(t, rep)
	if item.OriginID != "PUPPET-308" || item.Action != ActionFiled {
		t.Fatalf("item = %+v, want PUPPET-308 filed", item)
	}
	if item.Clone != "/clones/meta-harness" {
		t.Errorf("Clone = %q, want the meta-harness clone", item.Clone)
	}
}

func TestSweep_DeduplicatesAcrossStatuses(t *testing.T) {
	// The same ID surfacing under two status queries must be swept once.
	f := newFakeBackend()
	f.add(backend.IssueData{ID: "PUPPET-77", Status: "open", SourceRepo: "loomcli", Labels: []string{MarkerLabel}})
	f.byStatus["review"] = append(f.byStatus["review"], backend.IssueData{ID: "PUPPET-77", Status: "review", SourceRepo: "loomcli", Labels: []string{MarkerLabel}})
	p := &stubProber{results: map[string]ProbeResult{"PUPPET-77": {Class: ClassNoBranch}}}

	rep := run(t, f, p, Options{})
	if len(rep.Items) != 1 {
		t.Fatalf("expected 1 item after dedupe, got %+v", rep.Items)
	}
}

func TestSweep_CreateFailureIsReported(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-103", "loomcli", 2))
	f.createErr = errors.New("repo is required in this workspace")
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-103": {Class: ClassConflict, Ref: "origin/loom/PUPPET-103", TipSHA: "abc"},
	}}

	rep := run(t, f, p, Options{})
	item := onlyItem(t, rep)
	if item.Action != ActionError || rep.Errors != 1 {
		t.Fatalf("item = %+v, errors = %d, want a reported error", item, rep.Errors)
	}
	if !strings.Contains(item.ErrMessage, "repo is required") {
		t.Errorf("ErrMessage = %q, want the backend's message preserved", item.ErrMessage)
	}
}

func TestSweep_ListFailureAbortsRun(t *testing.T) {
	f := newFakeBackend()
	f.listErr = errors.New("backend down")
	_, err := NewSweeper(f, &stubProber{}, Options{Contract: testContract(t)}).Run(context.Background())
	if err == nil {
		t.Fatal("a ledger read failure must abort the run rather than report an empty ledger")
	}
}

func TestDebtPriority(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, 2}, {1, 2}, {2, 2}, {3, 3}, {5, 5}} {
		if got := debtPriority(tc.in); got != tc.want {
			t.Errorf("debtPriority(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
