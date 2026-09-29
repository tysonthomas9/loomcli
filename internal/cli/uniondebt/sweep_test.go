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
	byStatus map[string][]backend.IssueData
	listed   []listCall
	created  []backend.CreateParams
	added    []string // "<id>:<label>"
	removed  []string // "<id>:<label>"
	comments map[string][]string
	// designs holds the detail-only Design body per issue ID, so a test can
	// exercise the recorded-tip lookup the slim list projection cannot serve.
	designs   map[string]string
	getCalls  []string
	getErr    error
	createErr error
	listErr   error
	nextID    int
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		byStatus: map[string][]backend.IssueData{},
		comments: map[string][]string{},
		designs:  map[string]string{},
	}
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

func (f *fakeBackend) Get(_ context.Context, id string) (*backend.IssueDetailData, error) {
	f.getCalls = append(f.getCalls, id)
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, issues := range f.byStatus {
		for _, iss := range issues {
			if iss.ID == id {
				iss.Design = f.designs[id]
				return &backend.IssueDetailData{IssueData: iss}, nil
			}
		}
	}
	return nil, fmt.Errorf("no such issue %q", id)
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
	// tips records the recordedTip the sweep passed, per task ID.
	tips map[string]string
}

func (s *stubProber) Probe(_, _, taskID, recordedTip string) (ProbeResult, error) {
	s.calls = append(s.calls, taskID)
	if s.tips == nil {
		s.tips = map[string]string{}
	}
	s.tips[taskID] = recordedTip
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
		Labels: []string{defaultLabels.Marker, "delivered"},
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
	wantLabels := []string{defaultLabels.Route, defaultLabels.Debt, defaultLabels.DebtOfPrefix + "PUPPET-103"}
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
	if want := "PUPPET-9:" + defaultLabels.Marker; len(f.removed) != 1 || f.removed[0] != want {
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
	if want := "PUPPET-11:" + defaultLabels.Unreachable; len(f.added) != 1 || f.added[0] != want {
		t.Errorf("added = %v, want [%s]", f.added, want)
	}
	if want := "PUPPET-11:" + defaultLabels.Marker; len(f.removed) != 1 || f.removed[0] != want {
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
		Labels: []string{defaultLabels.Route, defaultLabels.Debt, defaultLabels.DebtOfPrefix + "PUPPET-103"},
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
		Labels: []string{defaultLabels.Debt, defaultLabels.DebtOfPrefix + "PUPPET-103"},
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
		if len(call.labels) != 1 || call.labels[0] != defaultLabels.Marker {
			t.Errorf("List %d labels = %v, want [%s]", i, call.labels, defaultLabels.Marker)
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
	f.add(backend.IssueData{ID: "PUPPET-77", Status: "open", SourceRepo: "loomcli", Labels: []string{defaultLabels.Marker}})
	f.byStatus["review"] = append(f.byStatus["review"], backend.IssueData{ID: "PUPPET-77", Status: "review", SourceRepo: "loomcli", Labels: []string{defaultLabels.Marker}})
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

// TestSweep_UsesContractLabels pins the whole point of the label block: the
// vocabulary is the workspace's, not the binary's. A contract that renames all
// five must be obeyed everywhere — the ledger query, the derived ticket's
// labels, the dedupe lookup and the prose written into the design body.
func TestSweep_UsesContractLabels(t *testing.T) {
	custom := withLabels(`  labels:
    marker: merge-pending
    unreachable: merge-unreachable
    debt: merge-debt
    debt_of_prefix: merge-debt-for/
    route: integrate
`)
	c, err := LoadContract(writeContract(t, custom))
	if err != nil {
		t.Fatalf("LoadContract: %v", err)
	}

	f := newFakeBackend()
	iss := closedIssue("PUPPET-103", "loomcli", 1)
	iss.Labels = []string{"merge-pending", "delivered"}
	f.add(iss)
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-103": {Class: ClassClean, Ref: "origin/loom/PUPPET-103", TipSHA: "abc123"},
	}}

	rep := run(t, f, p, Options{Contract: c})
	if item := onlyItem(t, rep); item.Action != ActionFiled {
		t.Fatalf("item = %+v, want filed", item)
	}

	for i, call := range f.listed {
		for _, label := range call.labels {
			if strings.HasPrefix(label, "union-") {
				t.Errorf("List %d queried the default vocabulary %v, not the contract's", i, call.labels)
				break
			}
		}
	}

	if len(f.created) != 1 {
		t.Fatalf("expected 1 create, got %d", len(f.created))
	}
	got := f.created[0]
	wantLabels := []string{"integrate", "merge-debt", "merge-debt-for/PUPPET-103"}
	if !hasAll(got.Labels, wantLabels) || len(got.Labels) != len(wantLabels) {
		t.Errorf("Labels = %v, want exactly %v", got.Labels, wantLabels)
	}
	if !strings.Contains(got.Design, "merge-pending") {
		t.Errorf("design body should name the configured marker:\n%s", got.Design)
	}
	if strings.Contains(got.Design, "union-pending") {
		t.Errorf("design body still names the default marker:\n%s", got.Design)
	}
	if c := f.comments["PUPPET-103"]; len(c) != 1 || !strings.Contains(c[0], "merge-pending") {
		t.Errorf("comment should name the configured marker, got %v", c)
	}
}

// TestSweep_UnreachableUsesContractLabel covers the other write path: the
// swap-the-marker branch must use the configured replacement too.
func TestSweep_UnreachableUsesContractLabel(t *testing.T) {
	c, err := LoadContract(writeContract(t, withLabels("  labels:\n    marker: merge-pending\n    unreachable: merge-unreachable\n")))
	if err != nil {
		t.Fatalf("LoadContract: %v", err)
	}

	f := newFakeBackend()
	iss := closedIssue("PUPPET-11", "loomcli", 2)
	iss.Labels = []string{"merge-pending"}
	f.add(iss)
	p := &stubProber{results: map[string]ProbeResult{"PUPPET-11": {Class: ClassNoBranch}}}

	rep := run(t, f, p, Options{Contract: c})
	if item := onlyItem(t, rep); item.Action != ActionUnreachable {
		t.Fatalf("item = %+v, want unreachable", item)
	}
	if want := "PUPPET-11:merge-unreachable"; len(f.added) != 1 || f.added[0] != want {
		t.Errorf("added = %v, want [%s]", f.added, want)
	}
	if want := "PUPPET-11:merge-pending"; len(f.removed) != 1 || f.removed[0] != want {
		t.Errorf("removed = %v, want [%s]", f.removed, want)
	}
}

// --- superseded ---

// debtTicket is an existing derived debt ticket for origin, carrying the design
// body the sweep reads the recorded tip out of.
func debtTicket(f *fakeBackend, id, origin, design string) {
	f.add(backend.IssueData{
		ID: id, Status: "open", SourceRepo: "loomcli",
		Labels: []string{defaultLabels.Route, defaultLabels.Debt, defaultLabels.DebtOfPrefix + origin},
	})
	f.designs[id] = design
}

func TestSweep_SupersededRetiresMarkerAndWarnsDebtTicket(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-540", "loomcli", 2))
	debtTicket(f, "PUPPET-560", "PUPPET-540", "    "+tipSHALine+"   f0e9458b6\n")
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-540": {
			Class:  ClassSuperseded,
			Ref:    "origin/loom/PUPPET-415",
			TipSHA: "9d70c75d4",
			Detail: "the recorded tip f0e9458b6 is not an ancestor of origin/loom/PUPPET-415",
		},
	}}

	item := onlyItem(t, run(t, f, p, Options{}))
	if item.Action != ActionSuperseded || item.Class != ClassSuperseded {
		t.Fatalf("item = %+v, want the superseded action and class", item)
	}
	if item.RecordedTip != "f0e9458b6" {
		t.Errorf("RecordedTip = %q, want the sha from the debt ticket design", item.RecordedTip)
	}
	if got := p.tips["PUPPET-540"]; got != "f0e9458b6" {
		t.Errorf("probe got recordedTip %q, want it passed through", got)
	}

	// The marker is swapped, exactly the shape the no-branch path uses.
	if want := "PUPPET-540:" + defaultLabels.Superseded; len(f.added) != 1 || f.added[0] != want {
		t.Errorf("added = %v, want [%s]", f.added, want)
	}
	if want := "PUPPET-540:" + defaultLabels.Marker; len(f.removed) != 1 || f.removed[0] != want {
		t.Errorf("removed = %v, want [%s]", f.removed, want)
	}

	// Both tickets are told why.
	orig := f.comments["PUPPET-540"]
	if len(orig) != 1 {
		t.Fatalf("comments on the original = %v, want exactly one", orig)
	}
	for _, want := range []string{"f0e9458b6", "9d70c75d4", "origin/loom/PUPPET-415", "not an ancestor"} {
		if !strings.Contains(orig[0], want) {
			t.Errorf("original comment missing %q:\n%s", want, orig[0])
		}
	}
	derived := f.comments["PUPPET-560"]
	if len(derived) != 1 {
		t.Fatalf("comments on the debt ticket = %v, want exactly one", derived)
	}
	for _, want := range []string{"PUPPET-540", "f0e9458b6", "origin/loom/PUPPET-415", "Close this"} {
		if !strings.Contains(derived[0], want) {
			t.Errorf("debt-ticket comment missing %q:\n%s", want, derived[0])
		}
	}

	// Files nothing, closes nothing.
	if len(f.created) != 0 {
		t.Errorf("created %+v; a superseded item files no new work", f.created)
	}
	if item.DerivedID != "PUPPET-560" {
		t.Errorf("DerivedID = %q, want the existing debt ticket", item.DerivedID)
	}
}

func TestSweep_SupersededWithNoDebtTicketStillRetires(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-541", "loomcli", 2))
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-541": {Class: ClassSuperseded, Ref: "loom/PUPPET-541", TipSHA: "aaa", Detail: "rebuilt"},
	}}

	item := onlyItem(t, run(t, f, p, Options{}))
	if item.Action != ActionSuperseded {
		t.Fatalf("Action = %s, want superseded", item.Action)
	}
	if got := p.tips["PUPPET-541"]; got != "" {
		t.Errorf("recordedTip = %q, want empty with no debt ticket to read it from", got)
	}
	if want := "PUPPET-541:" + defaultLabels.Superseded; len(f.added) != 1 || f.added[0] != want {
		t.Errorf("added = %v, want [%s]", f.added, want)
	}
	if want := "PUPPET-541:" + defaultLabels.Marker; len(f.removed) != 1 || f.removed[0] != want {
		t.Errorf("removed = %v, want [%s]", f.removed, want)
	}
	if len(f.comments) != 1 || len(f.comments["PUPPET-541"]) != 1 {
		t.Errorf("comments = %v, want one on the original only", f.comments)
	}
}

func TestSweep_SupersededDryRunWritesNothing(t *testing.T) {
	f := newFakeBackend()
	f.add(closedIssue("PUPPET-540", "loomcli", 2))
	debtTicket(f, "PUPPET-560", "PUPPET-540", "    "+tipSHALine+"   f0e9458b6\n")
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-540": {Class: ClassSuperseded, Ref: "origin/loom/PUPPET-415", TipSHA: "9d70c75d4", Detail: "rebuilt"},
	}}

	item := onlyItem(t, run(t, f, p, Options{DryRun: true}))
	if item.Action != ActionSuperseded || !item.DryRun {
		t.Fatalf("item = %+v, want a dry-run superseded item", item)
	}
	if len(f.added) != 0 || len(f.removed) != 0 || len(f.comments) != 0 || len(f.created) != 0 {
		t.Errorf("dry run wrote: added=%v removed=%v comments=%v created=%v",
			f.added, f.removed, f.comments, f.created)
	}
}

// TestSweep_UnparsableDesignYieldsNoRecordedTip: a guessed tip could retire real
// debt, so anything that is not a plain hex object name is refused.
func TestSweep_UnparsableDesignYieldsNoRecordedTip(t *testing.T) {
	for name, design := range map[string]string{
		"no tip line": "Original: PUPPET-540\n\nnothing recorded here\n",
		"not a sha":   "    " + tipSHALine + "   see the branch\n",
		"empty value": "    " + tipSHALine + "   \n",
		"too short":   "    " + tipSHALine + "   f0e94\n",
		"not hex":     "    " + tipSHALine + "   zzzzzzzz\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeBackend()
			f.add(closedIssue("PUPPET-540", "loomcli", 2))
			debtTicket(f, "PUPPET-560", "PUPPET-540", design)
			p := &stubProber{results: map[string]ProbeResult{
				"PUPPET-540": {Class: ClassConflict, Ref: "origin/loom/PUPPET-540", TipSHA: "abc"},
			}}

			run(t, f, p, Options{})
			if got := p.tips["PUPPET-540"]; got != "" {
				t.Errorf("recordedTip = %q, want empty for %s", got, name)
			}
		})
	}
}

// TestDesignBody_RecordedTipRoundTrip pins the writer and the parser together:
// they share tipSHALine, and this is what stops them drifting apart.
func TestDesignBody_RecordedTipRoundTrip(t *testing.T) {
	const sha = "f0e9458b6c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f"
	iss := closedIssue("PUPPET-540", "loomcli", 2)
	res := ProbeResult{Class: ClassConflict, Ref: "origin/loom/PUPPET-540", TipSHA: sha, Conflict: "CONFLICT: x"}
	li := LocalIntegration{Branch: "local/union", Clone: "/clones/loomcli"}

	body := designBody(iss, res, li, "2026-09-08T00:00:00Z", defaultLabels)
	if got := parseRecordedTip(body); got != sha {
		t.Fatalf("parseRecordedTip(designBody(...)) = %q, want %q\n%s", got, sha, body)
	}
}

// TestSweep_SupersededUsesContractLabel: the replacement is configuration, like
// every other label the sweep writes.
func TestSweep_SupersededUsesContractLabel(t *testing.T) {
	c, err := LoadContract(writeContract(t, withLabels("  labels:\n    marker: merge-pending\n    superseded: merge-superseded\n")))
	if err != nil {
		t.Fatalf("LoadContract: %v", err)
	}
	f := newFakeBackend()
	iss := closedIssue("PUPPET-540", "loomcli", 2)
	iss.Labels = []string{"merge-pending"}
	f.add(iss)
	p := &stubProber{results: map[string]ProbeResult{
		"PUPPET-540": {Class: ClassSuperseded, Ref: "loom/PUPPET-540", TipSHA: "aaa", Detail: "rebuilt"},
	}}

	if item := onlyItem(t, run(t, f, p, Options{Contract: c})); item.Action != ActionSuperseded {
		t.Fatalf("item = %+v, want superseded", item)
	}
	if want := "PUPPET-540:merge-superseded"; len(f.added) != 1 || f.added[0] != want {
		t.Errorf("added = %v, want [%s]", f.added, want)
	}
	if want := "PUPPET-540:merge-pending"; len(f.removed) != 1 || f.removed[0] != want {
		t.Errorf("removed = %v, want [%s]", f.removed, want)
	}
}
