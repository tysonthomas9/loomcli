package uniondebt

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// ledgerStatuses is enumerated one at a time on purpose: fleet-db clamps list
// responses (200 rows), so a single unfiltered list-and-filter loses most of
// the board. Each status is queried separately with an explicit limit and the
// results unioned.
var ledgerStatuses = []string{"open", "in_progress", "review", "blocked", "closed"}

// ledgerLimit is the per-status list cap. The whole ledger is single digits
// today; this is headroom, not a target.
const ledgerLimit = 200

// issueClient is the slice of backend.IssueBackend the sweep uses.
type issueClient interface {
	List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error)
	// Get fetches the full projection. It is needed for Design: a collection
	// response may omit a large design body (internal/backend/types.go), and
	// the recorded tip lives in that body.
	Get(ctx context.Context, id string) (*backend.IssueDetailData, error)
	Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error)
	AddLabel(ctx context.Context, id string, label string) error
	RemoveLabel(ctx context.Context, id string, label string) error
	AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error)
}

// prober is the git layer, stubbed in tests.
type prober interface {
	Probe(clone, unionBranch, taskID, recordedTip string) (ProbeResult, error)
}

// Action is what the sweep did about one ledger item.
type Action string

const (
	// ActionFiled means a derived debt ticket was created.
	ActionFiled Action = "filed"
	// ActionRetired means the marker was removed with no replacement.
	ActionRetired Action = "retired"
	// ActionUnreachable means the marker was swapped for union-unreachable.
	ActionUnreachable Action = "unreachable"
	// ActionSuperseded means the marker was swapped for union-superseded: the
	// recorded branch is not the branch to merge any more.
	ActionSuperseded Action = "superseded"
	// ActionSkipped means a debt ticket already exists, or --limit was hit.
	ActionSkipped Action = "skipped"
	// ActionError means the item could not be classified or acted on.
	ActionError Action = "error"
)

// Item is one ledger entry's outcome, and is the JSON output shape.
type Item struct {
	OriginID    string `json:"origin_id"`
	Repo        string `json:"repo"`
	Clone       string `json:"clone,omitempty"`
	Union       string `json:"union_branch,omitempty"`
	Ref         string `json:"ref,omitempty"`
	TipSHA      string `json:"tip_sha,omitempty"`
	RecordedTip string `json:"recorded_tip,omitempty"`
	Class       Class  `json:"class,omitempty"`
	Action      Action `json:"action"`
	DerivedID   string `json:"derived_id,omitempty"`
	Detail      string `json:"detail,omitempty"`
	ProbedAt    string `json:"probed_at"`
	DryRun      bool   `json:"dry_run,omitempty"`
	ErrMessage  string `json:"error,omitempty"`
}

// Report is the whole sweep's outcome.
type Report struct {
	Items  []Item `json:"items"`
	Errors int    `json:"errors"`
}

// Options configure one sweep.
type Options struct {
	Contract *Contract
	// Repos restricts the sweep to these source repos. Empty means all.
	Repos []string
	// DryRun classifies and reports without performing any write.
	DryRun bool
	// Limit caps derived tickets filed per run, so a mis-parse cannot flood
	// the board. Zero or negative means unlimited.
	Limit int
	// Now supplies the probe timestamp; nil uses time.Now.
	Now func() time.Time
}

// Sweeper reads the union-pending ledger, probes each item locally and turns
// real debt into claimable work. It never merges and never claims: the closed
// original is never touched beyond its labels and comments.
type Sweeper struct {
	issues issueClient
	probe  prober
	opts   Options
}

// NewSweeper wires a Sweeper. Pass nil for p to use the real git prober.
func NewSweeper(issues issueClient, p prober, opts Options) *Sweeper {
	if p == nil {
		p = NewProber()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Sweeper{issues: issues, probe: p, opts: opts}
}

// labels is the workspace's label vocabulary, read from the contract on every
// use so the sweep never carries a second, stale copy of it.
func (s *Sweeper) labels() LabelSet { return s.opts.Contract.Labels() }

// Run performs the sweep and returns a report. A single item's failure never
// aborts the run: errors are accumulated into the report and counted.
func (s *Sweeper) Run(ctx context.Context) (*Report, error) {
	ledger, err := s.ledger(ctx)
	if err != nil {
		return nil, err
	}

	rep := &Report{}
	filed := 0
	for _, iss := range ledger {
		item := s.handle(ctx, iss, &filed)
		if item.Action == ActionError {
			rep.Errors++
		}
		rep.Items = append(rep.Items, item)
	}
	return rep, nil
}

// ledger enumerates every issue still carrying the marker, one status at a
// time, deduplicated by ID and ordered for stable output.
func (s *Sweeper) ledger(ctx context.Context) ([]backend.IssueData, error) {
	marker := s.labels().Marker
	seen := map[string]backend.IssueData{}
	for _, status := range ledgerStatuses {
		found, err := s.issues.List(ctx, backend.ListOpts{
			Status: status,
			Labels: []string{marker},
			Limit:  ledgerLimit,
		})
		if err != nil {
			return nil, fmt.Errorf("list %s issues labeled %s: %w", status, marker, err)
		}
		for _, iss := range found {
			if _, dup := seen[iss.ID]; !dup {
				seen[iss.ID] = iss
			}
		}
	}

	out := make([]backend.IssueData, 0, len(seen))
	for _, iss := range seen {
		if s.wantRepo(iss.SourceRepo) {
			out = append(out, iss)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Sweeper) wantRepo(repo string) bool {
	if len(s.opts.Repos) == 0 {
		return true
	}
	for _, want := range s.opts.Repos {
		if want == repo {
			return true
		}
	}
	return false
}

// handle probes one ledger item and performs the write its class calls for.
// filed is the running count of derived tickets, checked against --limit.
func (s *Sweeper) handle(ctx context.Context, iss backend.IssueData, filed *int) Item {
	now := s.opts.Now().UTC()
	item := Item{
		OriginID: iss.ID,
		Repo:     iss.SourceRepo,
		ProbedAt: now.Format(time.RFC3339),
		DryRun:   s.opts.DryRun,
	}

	li, ok := s.opts.Contract.Lookup(iss.SourceRepo)
	if !ok {
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("no local_integration clone for source repo %q in the contract", iss.SourceRepo)
		return item
	}
	item.Clone, item.Union = li.Clone, li.Branch

	// The derived ticket is looked up BEFORE the probe, not inside file(): the
	// probe needs the tip recorded in its design body to tell a rebuilt branch
	// from ordinary conflict work.
	debtOf := s.labels().DebtOfPrefix + iss.ID
	existing, err := s.existingDebt(ctx, debtOf)
	if err != nil {
		item.Action = ActionError
		item.ErrMessage = err.Error()
		return item
	}
	item.RecordedTip = s.recordedTip(ctx, existing)

	res, err := s.probe.Probe(li.Clone, li.Branch, iss.ID, item.RecordedTip)
	if err != nil {
		item.Action = ActionError
		item.ErrMessage = err.Error()
		return item
	}
	item.Class, item.Ref, item.TipSHA = res.Class, res.Ref, res.TipSHA
	return s.act(ctx, iss, res, li, item, debtOf, existing, filed)
}

// act performs the write one probe class calls for. It is split out of handle
// so each half stays readable: handle gathers facts, act decides.
func (s *Sweeper) act(ctx context.Context, iss backend.IssueData, res ProbeResult, li LocalIntegration, item Item, debtOf, existing string, filed *int) Item {
	switch res.Class {
	case ClassNoUnion:
		// Never create the union branch and never touch the ticket — the
		// integrator prompt owns branch creation. Report and move on.
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("union branch %s not found in clone %s", li.Branch, li.Clone)
		return item

	case ClassInUnion:
		item.Action = ActionRetired
		item.Detail = fmt.Sprintf("%s is already an ancestor of %s", res.Ref, li.Branch)
		s.apply(ctx, &item, func() error { return s.retire(ctx, iss.ID, "", item) })
		return item

	case ClassNoBranch:
		item.Action = ActionUnreachable
		item.Detail = fmt.Sprintf("neither origin/loom/%s nor loom/%s exists in %s", iss.ID, iss.ID, li.Clone)
		s.apply(ctx, &item, func() error { return s.retire(ctx, iss.ID, s.labels().Unreachable, item) })
		return item

	case ClassSuperseded:
		item.Action = ActionSuperseded
		item.Detail = res.Detail
		item.DerivedID = existing
		s.apply(ctx, &item, func() error { return s.supersede(ctx, iss.ID, existing, item) })
		return item

	case ClassClean, ClassConflict:
		return s.file(ctx, iss, res, li, item, debtOf, existing, filed)

	default:
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("unknown probe class %q", res.Class)
		return item
	}
}

// apply runs a write unless this is a dry run, folding any failure into item.
func (s *Sweeper) apply(_ context.Context, item *Item, write func() error) {
	if s.opts.DryRun {
		return
	}
	if err := write(); err != nil {
		item.Action = ActionError
		item.ErrMessage = err.Error()
	}
}

// retire comments on the original, removes the marker and — when replacement
// is non-empty — stamps the replacement label. The comment goes first so the
// evidence exists even if a later call fails: a marker removed with no comment
// would erase the only record of why.
func (s *Sweeper) retire(ctx context.Context, id, replacement string, item Item) error {
	if _, err := s.issues.AddComment(ctx, backend.CommentAddParams{
		IssueID: id,
		Text:    retireComment(item, replacement, s.labels()),
	}); err != nil {
		return fmt.Errorf("comment on %s: %w", id, err)
	}
	if replacement != "" {
		if err := s.issues.AddLabel(ctx, id, replacement); err != nil {
			return fmt.Errorf("add %s to %s: %w", replacement, id, err)
		}
	}
	marker := s.labels().Marker
	if err := s.issues.RemoveLabel(ctx, id, marker); err != nil {
		return fmt.Errorf("remove %s from %s: %w", marker, id, err)
	}
	return nil
}

// supersede retires the marker in favor of the superseded label and, when a
// derived debt ticket exists, tells that ticket its instructions are stale.
//
// It files nothing and closes nothing. The sweeper has no status write today
// and gains none here: closing is the integrator's act, per the abandon path in
// prompts/integrator.md, and a status write would widen this command's blast
// radius well past labels and comments.
func (s *Sweeper) supersede(ctx context.Context, id, derivedID string, item Item) error {
	if err := s.retire(ctx, id, s.labels().Superseded, item); err != nil {
		return err
	}
	if derivedID == "" {
		return nil
	}
	if _, err := s.issues.AddComment(ctx, backend.CommentAddParams{
		IssueID: derivedID,
		Text:    staleDebtComment(id, item, s.labels()),
	}); err != nil {
		return fmt.Errorf("comment on %s: %w", derivedID, err)
	}
	return nil
}

// recordedTip recovers the tip SHA the derived debt ticket was filed against.
//
// Every failure — no debt ticket, an unreadable one, a design body with no
// parsable tip line — yields "", which switches the ref-moved signal off. That
// is deliberate: a guessed tip could produce a false "superseded" and retire
// real debt.
func (s *Sweeper) recordedTip(ctx context.Context, derivedID string) string {
	if derivedID == "" {
		return ""
	}
	detail, err := s.issues.Get(ctx, derivedID)
	if err != nil || detail == nil {
		return ""
	}
	return parseRecordedTip(detail.Design)
}

// tipSHALine labels the recorded tip in a derived ticket's design body. The
// writer and the parser share this one constant so they cannot drift apart.
const tipSHALine = "Tip SHA:"

// recordedTipPattern is what a value after tipSHALine must look like to be
// believed: an abbreviated or full hex object name and nothing else.
var recordedTipPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// parseRecordedTip pulls the tip SHA back out of a design body written by
// designBody, or returns "" when there is nothing trustworthy to read.
func parseRecordedTip(design string) string {
	for _, line := range strings.Split(design, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, tipSHALine) {
			continue
		}
		sha := strings.TrimSpace(strings.TrimPrefix(trimmed, tipSHALine))
		if recordedTipPattern.MatchString(sha) {
			return sha
		}
	}
	return ""
}

// file creates the derived, claimable debt ticket for one real debt item and
// records it on the original. The original KEEPS its marker: it is retired by
// the integrator once the merge actually lands, which is what makes the ledger
// drain reflect union content rather than sweeper activity.
func (s *Sweeper) file(ctx context.Context, iss backend.IssueData, res ProbeResult, li LocalIntegration, item Item, debtOf, existing string, filed *int) Item {
	lbl := s.labels()

	if existing != "" {
		item.Action = ActionSkipped
		item.DerivedID = existing
		item.Detail = "a debt ticket already exists for this original"
		return item
	}

	if s.opts.Limit > 0 && *filed >= s.opts.Limit {
		item.Action = ActionSkipped
		item.Detail = fmt.Sprintf("--limit %d reached", s.opts.Limit)
		return item
	}

	item.Action = ActionFiled
	if s.opts.DryRun {
		return item
	}

	created, err := s.issues.Create(ctx, debtParams(iss, res, li, item.ProbedAt, debtOf, lbl))
	if err != nil {
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("create debt ticket for %s: %v", iss.ID, err)
		return item
	}
	*filed++
	item.DerivedID = created.ID

	if _, err := s.issues.AddComment(ctx, backend.CommentAddParams{
		IssueID: iss.ID,
		Text:    filedComment(item, li, lbl),
	}); err != nil {
		item.Action = ActionError
		item.ErrMessage = fmt.Sprintf("comment on %s after filing %s: %v", iss.ID, created.ID, err)
	}
	return item
}

// debtParams builds the derived ticket. Every field here is chosen to clear the
// four gates that make the closed original unreachable: open status, no
// assignee, a plain task type, a design body, and the contract's route label
// with none of the excluded labels.
func debtParams(iss backend.IssueData, res ProbeResult, li LocalIntegration, probedAt, debtOf string, lbl LabelSet) backend.CreateParams {
	return backend.CreateParams{
		Title:     fmt.Sprintf("union debt: merge %s %s into %s (from %s)", iss.SourceRepo, res.Ref, li.Branch, iss.ID),
		IssueType: "task",
		Status:    "open",
		Priority:  debtPriority(iss.Priority),
		// No Assignee: /ready is unassigned-only, so a stamped assignee hides
		// the ticket from the very queue this routes it through.
		Labels:     []string{lbl.Route, lbl.Debt, debtOf},
		SourceRepo: iss.SourceRepo,
		Design:     designBody(iss, res, li, probedAt, lbl),
		// No Dependencies on the original: AddDependency rejects a closed
		// target ("dependency target is closed"), and every original here is
		// closed. The link lives in the design body and in the debtOf label —
		// do not "fix" this by adding one.
		IdempotencyKey: fmt.Sprintf("union-debt|%s|%s", iss.ID, res.TipSHA),
	}
}

// existingDebt returns the ID of a debt ticket already filed for this original,
// or "". Closed debt tickets count: refiling one that was deliberately
// abandoned would loop forever.
func (s *Sweeper) existingDebt(ctx context.Context, debtOf string) (string, error) {
	debt := s.labels().Debt
	for _, status := range ledgerStatuses {
		found, err := s.issues.List(ctx, backend.ListOpts{
			Status: status,
			Labels: []string{debt, debtOf},
			Limit:  ledgerLimit,
		})
		if err != nil {
			return "", fmt.Errorf("list %s issues labeled %s: %w", status, debtOf, err)
		}
		if len(found) > 0 {
			return found[0].ID, nil
		}
	}
	return "", nil
}

// debtPriority inherits the original's priority with a floor of 2, so a P0/P1
// original cannot make a merge chore outrank live work.
func debtPriority(orig int) int {
	if orig < 2 {
		return 2
	}
	return orig
}

func retireComment(item Item, replacement string, lbl LabelSet) string {
	var b strings.Builder
	if replacement == "" {
		fmt.Fprintf(&b, "union-debt sweep: %s marker retired — already in union.\n\n", lbl.Marker)
	} else {
		fmt.Fprintf(&b, "union-debt sweep: %s marker replaced with %s.\n\n", lbl.Marker, replacement)
	}
	writeProbeFacts(&b, item)
	if item.Detail != "" {
		fmt.Fprintf(&b, "Reason: %s\n", item.Detail)
	}
	return b.String()
}

func filedComment(item Item, li LocalIntegration, lbl LabelSet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "union-debt sweep: filed %s to merge this branch into %s.\n\n", item.DerivedID, li.Branch)
	writeProbeFacts(&b, item)
	fmt.Fprintf(&b, "\nThe %s marker stays until the merge lands; the integrator removes it.\n", lbl.Marker)
	return b.String()
}

// staleDebtComment tells an existing debt ticket that the branch its design
// names is not the branch to merge any more. It says close, never merge: the
// recorded ref holds work that was rebuilt or has already arrived by another
// route.
func staleDebtComment(originID string, item Item, lbl LabelSet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "union-debt sweep: this debt ticket is STALE — do not merge it.\n\n")
	fmt.Fprintf(&b, "Original: %s\n", originID)
	if item.RecordedTip != "" {
		fmt.Fprintf(&b, "Filed at: %s\n", item.RecordedTip)
	}
	fmt.Fprintf(&b, "Ref now:  %s (%s)\n", item.Ref, item.TipSHA)
	if item.Detail != "" {
		fmt.Fprintf(&b, "Reason:   %s\n", item.Detail)
	}
	fmt.Fprintf(&b, "\nMerging the ref this ticket names would re-apply abandoned code. Close this\n")
	fmt.Fprintf(&b, "ticket rather than merging it; %s now carries `%s`.\n", originID, lbl.Superseded)
	return b.String()
}

func writeProbeFacts(b *strings.Builder, item Item) {
	fmt.Fprintf(b, "Repo:     %s\n", item.Repo)
	fmt.Fprintf(b, "Clone:    %s\n", item.Clone)
	fmt.Fprintf(b, "Union:    %s\n", item.Union)
	if item.Ref != "" {
		fmt.Fprintf(b, "Ref:      %s\n", item.Ref)
		fmt.Fprintf(b, "Tip SHA:  %s\n", item.TipSHA)
	}
	if item.RecordedTip != "" {
		fmt.Fprintf(b, "Filed at: %s (the tip the debt was recorded against)\n", item.RecordedTip)
	}
	fmt.Fprintf(b, "Class:    %s\n", item.Class)
	fmt.Fprintf(b, "Probed:   %s (no fetch — refs as they stood in the clone)\n", item.ProbedAt)
}

// designBody is the derived ticket's design: everything the integrator needs to
// do the merge without re-deriving any of it.
func designBody(iss backend.IssueData, res ProbeResult, li LocalIntegration, probedAt string, lbl LabelSet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Original: %s\n\n", iss.ID)
	fmt.Fprintf(&b, "# Union debt: %s %s -> %s\n\n", iss.SourceRepo, res.Ref, li.Branch)
	fmt.Fprintf(&b, "This branch is NOT in `%s`, so its code is absent from the build this\n", li.Branch)
	fmt.Fprintf(&b, "machine runs. There is no new code to write and no PR to open: the whole\n")
	fmt.Fprintf(&b, "task is to merge an existing branch into the union branch.\n\n")

	fmt.Fprintf(&b, "## Facts (probed %s, no fetch)\n\n", probedAt)
	fmt.Fprintf(&b, "    Original:  %s (%s)\n", iss.ID, iss.Status)
	fmt.Fprintf(&b, "    Repo:      %s\n", iss.SourceRepo)
	fmt.Fprintf(&b, "    Clone:     %s\n", li.Clone)
	fmt.Fprintf(&b, "    Union:     %s\n", li.Branch)
	fmt.Fprintf(&b, "    Ref:       %s\n", res.Ref)
	fmt.Fprintf(&b, "    %s   %s\n", tipSHALine, res.TipSHA)
	fmt.Fprintf(&b, "    Probe:     %s\n\n", res.Class)
	if res.Class == ClassClean {
		fmt.Fprintf(&b, "`git merge-tree` reported no conflict at probe time. That is a hint, not a\n")
		fmt.Fprintf(&b, "guarantee — the union tip may have moved since.\n\n")
	} else {
		fmt.Fprintf(&b, "`git merge-tree` reported a conflict. Conflicts are expected on this kind of\n")
		fmt.Fprintf(&b, "task, not exceptional. Verbatim summary:\n\n```\n%s\n```\n\n", res.Conflict)
	}

	fmt.Fprintf(&b, "## Do this\n\n```\ncd %s\ngit checkout %s\ngit merge --no-ff %s\n```\n\n", li.Clone, li.Branch, res.Ref)
	fmt.Fprintf(&b, "Resolve conflicts per the union-merge section of your prompt (the oracle rule\n")
	fmt.Fprintf(&b, "and the staleness-vs-disagreement check apply unchanged). Do NOT push: the\n")
	fmt.Fprintf(&b, "union branch is local-only.\n\n")

	fmt.Fprintf(&b, "## Completion contract\n\n")
	fmt.Fprintf(&b, "On success:\n")
	fmt.Fprintf(&b, "  1. Remove `%s` from %s and comment there with the repo, ref and merge SHA.\n", lbl.Marker, iss.ID)
	fmt.Fprintf(&b, "  2. Close THIS ticket.\n")
	fmt.Fprintf(&b, "  3. Never stamp `delivered` on this ticket — ci-verifier claims `delivered`\n")
	fmt.Fprintf(&b, "     and there is no PR for it to verify.\n\n")
	fmt.Fprintf(&b, "If the conflict is genuinely unresolvable (the union/trunk-conflict case your\n")
	fmt.Fprintf(&b, "prompt says belongs to a human): add `integration-blocked` to THIS ticket, swap\n")
	fmt.Fprintf(&b, "%s's `%s` for `union-abandoned` with a comment saying why, and stop.\n", iss.ID, lbl.Marker)
	return b.String()
}
