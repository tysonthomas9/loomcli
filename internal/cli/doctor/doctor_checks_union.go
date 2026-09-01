package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/types"
)

const unionCheckName = "union_merged_not_closed"

// maxUnionOffenderDetail caps the per-offender Get fan-out used to name the
// dependents an offender is gating. Offenders are normally zero and were ten
// at the worst measured moment, so the cap only guards a pathological board.
const maxUnionOffenderDetail = 25

// unionBoardListLimit is set explicitly on every board query. Leaving Limit at
// zero sends no limit parameter and takes fleet-db's server-side default, which
// is exactly the silent truncation this check argues against; 200 is the
// server's own clamp, and no non-terminal status has come close to it.
const unionBoardListLimit = 200

// defaultUnionBranch is the last fallback when neither the repo entry nor the
// defaults block names a local integration branch.
const defaultUnionBranch = "local/union"

// unionMergeSubject matches the integrator's union merge commit subject, which
// is the precise "this ticket's branch reached the union branch" signal:
//
//	local union: merge loom/PUPPET-344 (doctor: pin that managed drift ...)
//
// The id pattern is prefix-agnostic so the check is not hardcoded to one
// workspace; the intersection with the board is what removes noise.
var unionMergeSubject = regexp.MustCompile(`^local union: merge loom/([A-Z][A-Z0-9]*-[0-9]+)\b`)

// unionIssueID matches a bare issue id, used to read `Task:` trailer values.
var unionIssueID = regexp.MustCompile(`\b[A-Z][A-Z0-9]*-[0-9]+\b`)

// unionNonTerminalStatuses is every status a ticket can hold while its work is
// still outstanding. `closed` and `tombstone` are terminal and are not queried.
var unionNonTerminalStatuses = []string{
	string(types.StatusOpen),
	string(types.StatusInProgress),
	string(types.StatusBlocked),
	string(types.StatusDeferred),
	string(types.StatusReview),
	string(types.StatusHooked),
	string(types.StatusPinned),
}

// unionRepo is one repo's `local_integration` entry from integration.yaml,
// reduced to what this check needs.
type unionRepo struct {
	Name     string
	Branch   string
	Worktree string
}

// integrationFile is a deliberately minimal view of integration.yaml. Nothing
// else in Go parses that file; decoding only the three fields used here keeps
// the check from taking a dependency on the rest of the contract's shape.
type integrationFile struct {
	Defaults struct {
		LocalIntegration struct {
			Branch string `yaml:"branch"`
		} `yaml:"local_integration"`
	} `yaml:"defaults"`
	Repos map[string]struct {
		LocalIntegration *struct {
			Branch   string `yaml:"branch"`
			Worktree string `yaml:"worktree"`
		} `yaml:"local_integration"`
	} `yaml:"repos"`
}

// unionMerge records where an issue id was seen on a union branch.
type unionMerge struct {
	Repo string
	SHA  string
}

// checkUnionMergedNotClosed reconciles "the code is merged into local/union"
// against "the ticket is closed". Nothing else on the machine does: the
// integrator closes only its own deliveries, so every other path into the union
// branch — a hand merge, work that arrived via the trunk — leaves the ticket
// open and silently gates whatever declared a dependency on it.
//
// Report-only, and warn rather than fail: runDoctor returns an error when any
// check fails, and a reconciliation gap must not break callers that gate on
// `loom doctor` exiting zero.
//
// Returns an empty CheckResult (silently skipped) on any environmental absence
// — no backend, no workspace, no integration.yaml, no usable union worktree, a
// board query that errors. A half-scanned board is worse than no scan.
func checkUnionMergedNotClosed(deps *cli.Deps) CheckResult {
	if deps == nil || deps.IssueBackend == nil || deps.Git == nil {
		return CheckResult{}
	}

	wsPath := unionWorkspacePath()
	if wsPath == "" {
		return CheckResult{}
	}

	repos, err := loadUnionRepos(filepath.Join(wsPath, "integration.yaml"))
	if err != nil || len(repos) == 0 {
		return CheckResult{}
	}

	merged, skipped, scanned := scanUnionRepos(deps, repos)
	// One repo without a union branch must not blind the check for the others,
	// but zero usable repos means there is nothing to compare against.
	if scanned == 0 {
		return CheckResult{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	board, err := nonTerminalBoard(ctx, deps.IssueBackend)
	if err != nil {
		return CheckResult{}
	}

	var offenders []string
	for id := range merged {
		if _, open := board[id]; open {
			offenders = append(offenders, id)
		}
	}

	if len(offenders) == 0 {
		return unionPassResult(len(merged), scanned, len(board), skipped)
	}
	return unionWarnResult(ctx, deps.IssueBackend, offenders, merged, board, skipped)
}

// unionWarnResult renders the amber result: one line per offender, then the
// repos that could not be scanned, then the remediation.
func unionWarnResult(ctx context.Context, be backend.IssueBackend, offenders []string,
	merged map[string]unionMerge, board map[string]backend.IssueData, skipped []string) CheckResult {
	sortIssueIDs(offenders)
	detail := unionOffenderDetail(ctx, be, offenders, merged, board)
	if len(skipped) > 0 {
		detail = append(detail, "skipped repos: "+strings.Join(skipped, ", "))
	}
	detail = append(detail,
		"remediation: verify the merge is real (`git -C <worktree> log --oneline <branch> | grep <id>`), "+
			"then `loom data close <id>` — the merge, the delivery comment and the labels were done; "+
			"only the close was missed. Dependents stay unclaimable until it lands.")

	return CheckResult{
		Name:    unionCheckName,
		Status:  StatusWarn,
		Summary: fmt.Sprintf("%d issue(s) merged into local/union but not closed", len(offenders)),
		Detail:  strings.Join(detail, "\n"),
	}
}

// scanUnionRepos reads every repo's union branch, returning the merged ids
// (first repo scanned wins for an id merged into two), the names of the repos
// that could not be scanned, and how many were scanned successfully.
func scanUnionRepos(deps *cli.Deps, repos []unionRepo) (map[string]unionMerge, []string, int) {
	merged := make(map[string]unionMerge)
	var skipped []string
	scanned := 0
	for _, r := range repos {
		ids, err := unionTicketIDs(deps, r)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s (%v)", r.Name, err))
			continue
		}
		scanned++
		for id, sha := range ids {
			if _, seen := merged[id]; !seen {
				merged[id] = unionMerge{Repo: r.Name, SHA: sha}
			}
		}
	}
	return merged, skipped, scanned
}

// unionPassResult renders the green result, which reports both sides of the
// comparison so an operator can see the check actually looked at something.
func unionPassResult(ids, repos, board int, skipped []string) CheckResult {
	result := CheckResult{
		Name:   unionCheckName,
		Status: StatusPass,
		Summary: fmt.Sprintf("no union-merged issues left open (%d id(s) across %d repo(s) vs %d non-closed)",
			ids, repos, board),
	}
	if len(skipped) > 0 {
		result.Detail = "skipped repos: " + strings.Join(skipped, ", ")
	}
	return result
}

// unionWorkspacePath returns the active workspace directory, or "" when there
// is none. It is a var so tests can supply a directory without a live fleet-db
// binary on PATH, the same seam fleetHealthProbe uses.
var unionWorkspacePath = func() string {
	ws, err := cfgpkg.ResolveActiveWorkspace()
	if err != nil || ws == nil {
		return ""
	}
	return ws.Path
}

// loadUnionRepos reads integration.yaml and returns the repos that declare a
// usable local_integration worktree. Repos without a local_integration block,
// or with an empty worktree, are skipped rather than reported: not every repo
// in the contract has a union branch.
func loadUnionRepos(path string) ([]unionRepo, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304 — path is derived from the resolved workspace
	if err != nil {
		return nil, err
	}
	var parsed integrationFile
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(parsed.Repos))
	for name := range parsed.Repos {
		names = append(names, name)
	}
	sort.Strings(names)

	var repos []unionRepo
	for _, name := range names {
		li := parsed.Repos[name].LocalIntegration
		if li == nil || li.Worktree == "" {
			continue
		}
		branch := li.Branch
		if branch == "" {
			branch = parsed.Defaults.LocalIntegration.Branch
		}
		if branch == "" {
			branch = defaultUnionBranch
		}
		repos = append(repos, unionRepo{Name: name, Branch: branch, Worktree: li.Worktree})
	}
	return repos, nil
}

// unionTicketIDs returns every issue id that has reached r.Branch, mapped to
// the sha it was seen at (newest wins — git log is newest-first).
//
// Two structured sources, both needed. The union merge subject covers the
// integrator's own merges and the hand merges that followed its convention;
// the `Task:` trailer covers work that reached the union branch through the
// trunk's own history and so has no union merge commit. A bare grep over full
// messages would additionally match a commit that merely mentions another
// ticket ("supersedes PUPPET-x") and report it as merged.
func unionTicketIDs(deps *cli.Deps, r unionRepo) (map[string]string, error) {
	if _, err := os.Stat(r.Worktree); err != nil {
		return nil, fmt.Errorf("worktree unavailable")
	}

	ids := make(map[string]string)

	subjects := deps.Git.Run(r.Worktree, "log", "--format=%x00%h|%s", "--first-parent", r.Branch)
	if subjects.Err != nil {
		return nil, fmt.Errorf("git log --first-parent %s failed", r.Branch)
	}
	for _, rec := range splitUnionRecords(subjects.Stdout) {
		sha, body, ok := strings.Cut(rec, "|")
		if !ok {
			continue
		}
		if m := unionMergeSubject.FindStringSubmatch(body); m != nil {
			if _, seen := ids[m[1]]; !seen {
				ids[m[1]] = sha
			}
		}
	}

	trailers := deps.Git.Run(r.Worktree, "log", "--format=%x00%h|%(trailers:key=Task,valueonly)", r.Branch)
	if trailers.Err != nil {
		return nil, fmt.Errorf("git log trailers on %s failed", r.Branch)
	}
	for _, rec := range splitUnionRecords(trailers.Stdout) {
		sha, body, ok := strings.Cut(rec, "|")
		if !ok {
			continue
		}
		for _, id := range unionIssueID.FindAllString(body, -1) {
			if _, seen := ids[id]; !seen {
				ids[id] = sha
			}
		}
	}

	return ids, nil
}

// splitUnionRecords splits `git log` output whose format starts each commit
// with a NUL. A trailer value can span lines, so line-splitting would mis-pair
// shas with values.
func splitUnionRecords(out string) []string {
	var records []string
	for _, rec := range strings.Split(out, "\x00") {
		rec = strings.TrimSpace(rec)
		if rec != "" {
			records = append(records, rec)
		}
	}
	return records
}

// nonTerminalBoard returns every non-closed issue, keyed by id.
//
// One List per status, never list-everything-and-filter: fleet-db clamps list
// responses (measured 2026-09-01 — a clamped list-all reported 15 non-closed
// issues where the per-status union found 43), and the tickets a clamp drops
// are exactly the ones this check exists to find. Any status query erroring
// fails the whole read rather than reporting a partial board.
func nonTerminalBoard(ctx context.Context, be backend.IssueBackend) (map[string]backend.IssueData, error) {
	board := make(map[string]backend.IssueData)
	for _, status := range unionNonTerminalStatuses {
		issues, err := be.List(ctx, backend.ListOpts{Status: status, Limit: unionBoardListLimit})
		if err != nil {
			return nil, err
		}
		for _, issue := range issues {
			board[issue.ID] = issue
		}
	}
	return board, nil
}

// unionOffenderDetail renders one line per offender, naming the dependents it
// is gating — which are the actual cost of the missed close. Dependents come
// from a per-offender Get (the list projection carries only a count), so the
// fan-out is capped.
func unionOffenderDetail(ctx context.Context, be backend.IssueBackend, offenders []string,
	merged map[string]unionMerge, board map[string]backend.IssueData) []string {
	overCap := len(offenders) > maxUnionOffenderDetail

	lines := make([]string, 0, len(offenders)+1)
	for _, id := range offenders {
		lines = append(lines, fmt.Sprintf("issue=%s status=%s repo=%s merge=%s gating=%s",
			id, board[id].Status, merged[id].Repo, merged[id].SHA, unionGating(ctx, be, id, board, overCap)))
	}
	if overCap {
		lines = append(lines, fmt.Sprintf("dependents not resolved: %d offender(s) exceeds the %d-offender cap",
			len(offenders), maxUnionOffenderDetail))
	}
	return lines
}

// unionGating names the non-terminal dependents of id. A closed dependent is
// not being gated, so it is not counted.
func unionGating(ctx context.Context, be backend.IssueBackend, id string,
	board map[string]backend.IssueData, overCap bool) string {
	if overCap {
		return "skipped"
	}
	detail, err := be.Get(ctx, id)
	if err != nil || detail == nil {
		return "unknown"
	}
	var gated []string
	for _, dep := range detail.Dependents {
		if dep.IssueID == "" {
			continue
		}
		if _, open := board[dep.IssueID]; open {
			gated = append(gated, dep.IssueID)
		}
	}
	if len(gated) == 0 {
		return "none"
	}
	sortIssueIDs(gated)
	return strings.Join(gated, ",")
}

// sortIssueIDs orders ids by prefix then numeric suffix, so PUPPET-9 precedes
// PUPPET-10 instead of following it.
func sortIssueIDs(ids []string) {
	sort.Slice(ids, func(i, j int) bool {
		pi, ni := splitIssueID(ids[i])
		pj, nj := splitIssueID(ids[j])
		if pi != pj {
			return pi < pj
		}
		if ni != nj {
			return ni < nj
		}
		return ids[i] < ids[j]
	})
}

func splitIssueID(id string) (string, int) {
	idx := strings.LastIndex(id, "-")
	if idx < 0 {
		return id, 0
	}
	n, err := strconv.Atoi(id[idx+1:])
	if err != nil {
		return id, 0
	}
	return id[:idx], n
}
