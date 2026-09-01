package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
)

// unionLog builds the NUL-prefixed record stream `git log --format=%x00...`
// produces, so the tests exercise the real parser rather than a friendlier one.
func unionLog(records ...string) string {
	var b strings.Builder
	for _, r := range records {
		b.WriteString("\x00")
		b.WriteString(r)
		b.WriteString("\n")
	}
	return b.String()
}

// setupUnionWorkspace points the active workspace at a temp dir and writes the
// given integration.yaml body into it. Repo worktree paths in that body must be
// real directories for unionTicketIDs to scan them, so callers create them.
//
// It overrides the unionWorkspacePath seam rather than standing up a workspace
// config: resolving one for real needs a fleet-db binary on PATH, which the
// repo gate's clean environment does not have.
func setupUnionWorkspace(t *testing.T, integrationYAML string) string {
	t.Helper()
	dir := t.TempDir()
	orig := unionWorkspacePath
	unionWorkspacePath = func() string { return dir }
	t.Cleanup(func() { unionWorkspacePath = orig })
	if integrationYAML != "" {
		path := filepath.Join(dir, "integration.yaml")
		if err := os.WriteFile(path, []byte(integrationYAML), 0o600); err != nil {
			t.Fatalf("write integration.yaml: %v", err)
		}
	}
	return dir
}

// assertUnionReported fails when the check skipped. StatusPass is the zero
// CheckStatus, so a skipped CheckResult{} is indistinguishable from a green one
// on Status alone — Name is the field that separates them.
func assertUnionReported(t *testing.T, got CheckResult) {
	t.Helper()
	if got.Name != unionCheckName {
		t.Fatalf("check was skipped, expected a %s result: %+v", unionCheckName, got)
	}
}

// unionYAML renders an integration.yaml naming one local_integration repo per
// entry of worktrees (name -> worktree path).
func unionYAML(worktrees map[string]string) string {
	var b strings.Builder
	b.WriteString("defaults:\n  local_integration:\n    branch: local/union\nrepos:\n")
	names := make([]string, 0, len(worktrees))
	for name := range worktrees {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "  %s:\n    local_integration:\n      worktree: %s\n", name, worktrees[name])
	}
	return b.String()
}

// boardLister returns a ListFn serving issues keyed by status, and records
// which statuses were queried.
func boardLister(byStatus map[string][]backend.IssueData, queried *[]string) func(context.Context, backend.ListOpts) ([]backend.IssueData, error) {
	return func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		if queried != nil {
			*queried = append(*queried, opts.Status)
		}
		return byStatus[opts.Status], nil
	}

}

func TestCheckUnionMergedNotClosed_SkippedCases(t *testing.T) {
	t.Run("nil deps", func(t *testing.T) {
		setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": t.TempDir()}))
		if got := checkUnionMergedNotClosed(nil); got.Name != "" {
			t.Fatalf("expected skipped, got %+v", got)
		}
	})

	t.Run("no issue backend", func(t *testing.T) {
		setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": t.TempDir()}))
		deps, _, _, _, _ := NewTestDeps(t)
		deps.IssueBackend = nil
		if got := checkUnionMergedNotClosed(deps); got.Name != "" {
			t.Fatalf("expected skipped, got %+v", got)
		}
	})

	t.Run("no git runner", func(t *testing.T) {
		setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": t.TempDir()}))
		deps, _, _, _, _ := NewTestDeps(t)
		deps.Git = nil
		if got := checkUnionMergedNotClosed(deps); got.Name != "" {
			t.Fatalf("expected skipped, got %+v", got)
		}
	})

	t.Run("no active workspace", func(t *testing.T) {
		orig := unionWorkspacePath
		unionWorkspacePath = func() string { return "" }
		t.Cleanup(func() { unionWorkspacePath = orig })
		deps, _, _, _, _ := NewTestDeps(t)
		if got := checkUnionMergedNotClosed(deps); got.Name != "" {
			t.Fatalf("expected skipped, got %+v", got)
		}
	})

	t.Run("missing integration.yaml", func(t *testing.T) {
		setupUnionWorkspace(t, "")
		deps, _, _, _, _ := NewTestDeps(t)
		if got := checkUnionMergedNotClosed(deps); got.Name != "" {
			t.Fatalf("expected skipped, got %+v", got)
		}
	})

	t.Run("unparseable integration.yaml", func(t *testing.T) {
		setupUnionWorkspace(t, "repos:\n  loomcli:\n   bad: [unterminated\n")
		deps, _, _, _, _ := NewTestDeps(t)
		if got := checkUnionMergedNotClosed(deps); got.Name != "" {
			t.Fatalf("expected skipped, got %+v", got)
		}
	})

	t.Run("every worktree missing", func(t *testing.T) {
		setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": "/nonexistent/union/loomcli"}))
		deps, _, _, _, _ := NewTestDeps(t)
		if got := checkUnionMergedNotClosed(deps); got.Name != "" {
			t.Fatalf("expected skipped, got %+v", got)
		}
	})

	t.Run("board list error", func(t *testing.T) {
		wt := t.TempDir()
		setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": wt}))
		deps, git, _, _, be := NewTestDeps(t)
		git.RunFunc = func(_ string, _ ...string) cli.CommandResult {
			return cli.CommandResult{Stdout: unionLog("abc1234|local union: merge loom/PUPPET-251 (x)")}
		}
		be.ListErr = errors.New("backend down")
		if got := checkUnionMergedNotClosed(deps); got.Name != "" {
			t.Fatalf("expected skipped, got %+v", got)
		}
	})
}

func TestCheckUnionMergedNotClosed_Pass(t *testing.T) {
	wt := t.TempDir()
	setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": wt}))
	deps, git, _, _, be := NewTestDeps(t)
	git.RunFunc = func(_ string, _ ...string) cli.CommandResult {
		return cli.CommandResult{Stdout: unionLog("abc1234|local union: merge loom/PUPPET-251 (delivered)")}
	}
	be.ListFn = boardLister(map[string][]backend.IssueData{
		"open": {{ID: "PUPPET-900", Status: "open"}},
	}, nil)

	got := checkUnionMergedNotClosed(deps)
	assertUnionReported(t, got)
	if got.Status != StatusPass {
		t.Fatalf("expected pass, got %v: %s", got.Status, got.Summary)
	}
	if !strings.Contains(got.Summary, "no union-merged issues left open") {
		t.Errorf("unexpected summary: %s", got.Summary)
	}
	if !strings.Contains(got.Summary, "1 id(s) across 1 repo(s) vs 1 non-closed") {
		t.Errorf("summary should report both counts, got: %s", got.Summary)
	}
}

func TestCheckUnionMergedNotClosed_WarnsWithGatedDependents(t *testing.T) {
	wt := t.TempDir()
	setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": wt}))
	deps, git, _, _, be := NewTestDeps(t)
	git.RunFunc = func(_ string, args ...string) cli.CommandResult {
		if unionArgsHaveFirstParent(args) {
			return cli.CommandResult{Stdout: unionLog(
				"db0e364a0|local union: merge loom/PUPPET-251 (fleet lock TTL)")}
		}
		return cli.CommandResult{Stdout: unionLog("db0e364a0|")}
	}
	be.ListFn = boardLister(map[string][]backend.IssueData{
		"open": {
			{ID: "PUPPET-251", Status: "open"},
			{ID: "PUPPET-252", Status: "open"},
			{ID: "PUPPET-253", Status: "open"},
		},
	}, nil)
	be.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		if id != "PUPPET-251" {
			t.Fatalf("unexpected Get for %s", id)
		}
		return &backend.IssueDetailData{Dependents: []backend.DependencyData{
			{IssueID: "PUPPET-253"},
			{IssueID: "PUPPET-252"},
		}}, nil
	}

	got := checkUnionMergedNotClosed(deps)
	assertUnionReported(t, got)
	if got.Status != StatusWarn {
		t.Fatalf("expected warn, got %v: %s", got.Status, got.Summary)
	}
	want := "issue=PUPPET-251 status=open repo=loomcli merge=db0e364a0 gating=PUPPET-252,PUPPET-253"
	if !strings.Contains(got.Detail, want) {
		t.Errorf("detail missing %q, got:\n%s", want, got.Detail)
	}
	if !strings.Contains(got.Detail, "loom data close") {
		t.Errorf("detail should carry the remediation, got:\n%s", got.Detail)
	}
}

func TestCheckUnionMergedNotClosed_TrailerOnlyIDIsDetected(t *testing.T) {
	wt := t.TempDir()
	setupUnionWorkspace(t, unionYAML(map[string]string{"fleet-db": wt}))
	deps, git, _, _, be := NewTestDeps(t)
	// Work that reached the union branch through the trunk has a Task trailer
	// and no union merge subject.
	git.RunFunc = func(_ string, args ...string) cli.CommandResult {
		if unionArgsHaveFirstParent(args) {
			return cli.CommandResult{Stdout: unionLog("aff2474|chore: unrelated")}
		}
		return cli.CommandResult{Stdout: unionLog("aff2474|PUPPET-88\n")}
	}
	be.ListFn = boardLister(map[string][]backend.IssueData{
		"open": {{ID: "PUPPET-88", Status: "open"}},
	}, nil)
	be.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{}, nil
	}

	got := checkUnionMergedNotClosed(deps)
	assertUnionReported(t, got)
	if got.Status != StatusWarn {
		t.Fatalf("expected warn, got %v: %s", got.Status, got.Summary)
	}
	if !strings.Contains(got.Detail, "issue=PUPPET-88") || !strings.Contains(got.Detail, "merge=aff2474") {
		t.Errorf("trailer-only id not reported, got:\n%s", got.Detail)
	}
	if !strings.Contains(got.Detail, "gating=none") {
		t.Errorf("expected gating=none for an offender with no dependents, got:\n%s", got.Detail)
	}
}

func TestCheckUnionMergedNotClosed_BodyMentionIsNotAMerge(t *testing.T) {
	wt := t.TempDir()
	setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": wt}))
	deps, git, _, _, be := NewTestDeps(t)
	// A commit whose body merely mentions a ticket is not evidence that the
	// ticket's work was merged.
	git.RunFunc = func(_ string, args ...string) cli.CommandResult {
		if unionArgsHaveFirstParent(args) {
			return cli.CommandResult{Stdout: unionLog("cafe123|fix: something (supersedes PUPPET-999)")}
		}
		return cli.CommandResult{Stdout: unionLog("cafe123|")}
	}
	be.ListFn = boardLister(map[string][]backend.IssueData{
		"open": {{ID: "PUPPET-999", Status: "open"}},
	}, nil)

	got := checkUnionMergedNotClosed(deps)
	assertUnionReported(t, got)
	if got.Status != StatusPass {
		t.Fatalf("expected pass, got %v: %s\n%s", got.Status, got.Summary, got.Detail)
	}
}

// TestCheckUnionMergedNotClosed_QueriesEveryNonTerminalStatus is the regression
// test for the list-clamp bug: a naive list-all-and-filter implementation
// passes every other test here while silently dropping most of the board.
func TestCheckUnionMergedNotClosed_QueriesEveryNonTerminalStatus(t *testing.T) {
	wt := t.TempDir()
	setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": wt}))
	deps, git, _, _, be := NewTestDeps(t)
	git.RunFunc = func(_ string, _ ...string) cli.CommandResult {
		return cli.CommandResult{Stdout: unionLog("beef001|local union: merge loom/PUPPET-77 (x)")}
	}
	var queried []string
	var limits []int
	inner := boardLister(map[string][]backend.IssueData{
		"blocked": {{ID: "PUPPET-77", Status: "blocked"}},
	}, &queried)
	be.ListFn = func(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		limits = append(limits, opts.Limit)
		return inner(ctx, opts)
	}
	be.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{}, nil
	}

	got := checkUnionMergedNotClosed(deps)
	assertUnionReported(t, got)
	if got.Status != StatusWarn {
		t.Fatalf("expected warn for a blocked offender, got %v: %s", got.Status, got.Summary)
	}
	if !strings.Contains(got.Detail, "issue=PUPPET-77 status=blocked") {
		t.Errorf("detail should carry the blocked offender, got:\n%s", got.Detail)
	}
	for _, want := range []string{"open", "in_progress", "blocked", "deferred", "review", "hooked", "pinned"} {
		if !containsString(queried, want) {
			t.Errorf("status %q was never queried (queried: %v)", want, queried)
		}
	}
	for _, unwanted := range []string{"closed", "tombstone"} {
		if containsString(queried, unwanted) {
			t.Errorf("terminal status %q should not be queried", unwanted)
		}
	}
	for _, limit := range limits {
		if limit != unionBoardListLimit {
			t.Errorf("board query used limit %d, want the explicit %d: a zero Limit takes the "+
				"server default, which is the truncation this check exists to avoid", limit, unionBoardListLimit)
		}
	}
}

func TestCheckUnionMergedNotClosed_ClosedDependentIsNotGated(t *testing.T) {
	wt := t.TempDir()
	setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": wt}))
	deps, git, _, _, be := NewTestDeps(t)
	git.RunFunc = func(_ string, _ ...string) cli.CommandResult {
		return cli.CommandResult{Stdout: unionLog("aaa1111|local union: merge loom/PUPPET-10 (x)")}
	}
	be.ListFn = boardLister(map[string][]backend.IssueData{
		"open": {{ID: "PUPPET-10", Status: "open"}},
	}, nil)
	be.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) {
		// PUPPET-500 is closed, so it is absent from the non-terminal board.
		return &backend.IssueDetailData{Dependents: []backend.DependencyData{{IssueID: "PUPPET-500"}}}, nil
	}

	got := checkUnionMergedNotClosed(deps)
	assertUnionReported(t, got)
	if !strings.Contains(got.Detail, "gating=none") {
		t.Errorf("a closed dependent must not count as gated, got:\n%s", got.Detail)
	}
}

func TestCheckUnionMergedNotClosed_GetFailureReportsUnknown(t *testing.T) {
	wt := t.TempDir()
	setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": wt}))
	deps, git, _, _, be := NewTestDeps(t)
	git.RunFunc = func(_ string, _ ...string) cli.CommandResult {
		return cli.CommandResult{Stdout: unionLog("aaa2222|local union: merge loom/PUPPET-11 (x)")}
	}
	be.ListFn = boardLister(map[string][]backend.IssueData{
		"open": {{ID: "PUPPET-11", Status: "open"}},
	}, nil)
	be.GetErr = errors.New("boom")

	got := checkUnionMergedNotClosed(deps)
	assertUnionReported(t, got)
	if got.Status != StatusWarn {
		t.Fatalf("a failing Get must not suppress the offender, got %v", got.Status)
	}
	if !strings.Contains(got.Detail, "gating=unknown") {
		t.Errorf("expected gating=unknown, got:\n%s", got.Detail)
	}
}

func TestCheckUnionMergedNotClosed_MultiRepoSkipsOnlyTheBrokenOne(t *testing.T) {
	healthy := t.TempDir()
	setupUnionWorkspace(t, unionYAML(map[string]string{
		"loomcli":  healthy,
		"fleet-db": "/nonexistent/union/fleet-db",
	}))
	deps, git, _, _, be := NewTestDeps(t)
	git.RunFunc = func(dir string, args ...string) cli.CommandResult {
		if dir != healthy {
			t.Fatalf("git must not run against a missing worktree (%s)", dir)
		}
		if unionArgsHaveFirstParent(args) {
			return cli.CommandResult{Stdout: unionLog("ccc3333|local union: merge loom/PUPPET-12 (x)")}
		}
		return cli.CommandResult{Stdout: unionLog("ccc3333|")}
	}
	be.ListFn = boardLister(map[string][]backend.IssueData{
		"open": {{ID: "PUPPET-12", Status: "open"}},
	}, nil)
	be.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{}, nil
	}

	got := checkUnionMergedNotClosed(deps)
	assertUnionReported(t, got)
	if got.Status != StatusWarn {
		t.Fatalf("expected warn from the healthy repo, got %v: %s", got.Status, got.Summary)
	}
	if !strings.Contains(got.Detail, "issue=PUPPET-12") {
		t.Errorf("healthy repo was not scanned, got:\n%s", got.Detail)
	}
	if !strings.Contains(got.Detail, "skipped repos: fleet-db") {
		t.Errorf("skipped repo should be named, got:\n%s", got.Detail)
	}
}

func TestCheckUnionMergedNotClosed_GitLogFailureSkipsRepo(t *testing.T) {
	wt := t.TempDir()
	setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": wt}))
	deps, git, _, _, be := NewTestDeps(t)
	git.RunFunc = func(_ string, _ ...string) cli.CommandResult {
		return cli.CommandResult{Err: errors.New("unknown revision local/union")}
	}
	be.ListFn = boardLister(nil, nil)

	if got := checkUnionMergedNotClosed(deps); got.Name != "" {
		t.Fatalf("no usable repo must skip the check, got %+v", got)
	}
}

func TestCheckUnionMergedNotClosed_CapSkipsDependentLookups(t *testing.T) {
	wt := t.TempDir()
	setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": wt}))
	deps, git, _, _, be := NewTestDeps(t)

	count := maxUnionOffenderDetail + 5
	records := make([]string, 0, count)
	open := make([]backend.IssueData, 0, count)
	for i := 1; i <= count; i++ {
		id := fmt.Sprintf("PUPPET-%d", i)
		records = append(records, fmt.Sprintf("sha%04d|local union: merge loom/%s (x)", i, id))
		open = append(open, backend.IssueData{ID: id, Status: "open"})
	}
	git.RunFunc = func(_ string, args ...string) cli.CommandResult {
		if unionArgsHaveFirstParent(args) {
			return cli.CommandResult{Stdout: unionLog(records...)}
		}
		return cli.CommandResult{Stdout: ""}
	}
	be.ListFn = boardLister(map[string][]backend.IssueData{"open": open}, nil)
	be.GetFn = func(_ context.Context, id string) (*backend.IssueDetailData, error) {
		t.Fatalf("Get must not be called past the cap (called for %s)", id)
		return nil, nil
	}

	got := checkUnionMergedNotClosed(deps)
	assertUnionReported(t, got)
	if got.Status != StatusWarn {
		t.Fatalf("expected warn, got %v", got.Status)
	}
	if !strings.Contains(got.Detail, "gating=skipped") {
		t.Errorf("expected gating=skipped past the cap, got:\n%s", got.Detail)
	}
	if !strings.Contains(got.Detail, "exceeds the") {
		t.Errorf("expected a truncation line, got:\n%s", got.Detail)
	}
	for _, call := range be.Calls {
		if call.Method == "Get" {
			t.Fatalf("Get was called past the cap")
		}
	}
}

func TestCheckUnionMergedNotClosed_OffendersSortNumerically(t *testing.T) {
	wt := t.TempDir()
	setupUnionWorkspace(t, unionYAML(map[string]string{"loomcli": wt}))
	deps, git, _, _, be := NewTestDeps(t)
	git.RunFunc = func(_ string, args ...string) cli.CommandResult {
		if unionArgsHaveFirstParent(args) {
			return cli.CommandResult{Stdout: unionLog(
				"sha10|local union: merge loom/PUPPET-10 (x)",
				"sha9|local union: merge loom/PUPPET-9 (x)")}
		}
		return cli.CommandResult{Stdout: ""}
	}
	be.ListFn = boardLister(map[string][]backend.IssueData{
		"open": {{ID: "PUPPET-9", Status: "open"}, {ID: "PUPPET-10", Status: "open"}},
	}, nil)
	be.GetFn = func(context.Context, string) (*backend.IssueDetailData, error) {
		return &backend.IssueDetailData{}, nil
	}

	got := checkUnionMergedNotClosed(deps)
	assertUnionReported(t, got)
	nine := strings.Index(got.Detail, "issue=PUPPET-9 ")
	ten := strings.Index(got.Detail, "issue=PUPPET-10 ")
	if nine < 0 || ten < 0 {
		t.Fatalf("both offenders should be reported, got:\n%s", got.Detail)
	}
	if nine > ten {
		t.Errorf("PUPPET-9 should sort before PUPPET-10, got:\n%s", got.Detail)
	}
}

func TestLoadUnionRepos(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "integration.yaml")
	body := `defaults:
  local_integration:
    branch: local/union
repos:
  loomcli:
    target_branch: v5
    local_integration:
      worktree: /union/loomcli
  harness-wrapper:
    local_integration:
      branch: local/other
      worktree: /union/harness-wrapper
  no-worktree:
    local_integration:
      branch: local/union
  local-stack:
    target_branch: main
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	repos, err := loadUnionRepos(path)
	if err != nil {
		t.Fatalf("loadUnionRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos with a usable worktree, got %d: %+v", len(repos), repos)
	}
	// Sorted by name: harness-wrapper, loomcli.
	if repos[0].Name != "harness-wrapper" || repos[0].Branch != "local/other" {
		t.Errorf("per-repo branch should win, got %+v", repos[0])
	}
	if repos[1].Name != "loomcli" || repos[1].Branch != "local/union" {
		t.Errorf("branch should fall back to the defaults block, got %+v", repos[1])
	}

	if _, err := loadUnionRepos(filepath.Join(dir, "absent.yaml")); err == nil {
		t.Error("a missing file must error so the check skips")
	}
}

func TestSortIssueIDsMixedPrefixes(t *testing.T) {
	ids := []string{"WEB-2", "PUPPET-10", "PUPPET-2", "PUPPET-9", "WEB-1", "NOTANID"}
	sortIssueIDs(ids)
	want := []string{"NOTANID", "PUPPET-2", "PUPPET-9", "PUPPET-10", "WEB-1", "WEB-2"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("sortIssueIDs = %v, want %v", ids, want)
		}
	}
}

func unionArgsHaveFirstParent(args []string) bool {
	return containsString(args, "--first-parent")
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
