package doctor

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// testNow is the injected clock every test below computes ages against, so a
// fixture's age never depends on when the suite runs.
var testNow = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

// agedIssues builds n ready, unassigned issues carrying the given labels, all
// created `age` before testNow.
func agedIssues(n int, age time.Duration, labels ...string) []backend.IssueData {
	issues := make([]backend.IssueData, 0, n)
	for i := 0; i < n; i++ {
		issues = append(issues, backend.IssueData{
			ID:        fmt.Sprintf("PUPPET-%d", 300+i),
			Status:    "open",
			Labels:    append([]string(nil), labels...),
			CreatedAt: testNow.Add(-age),
		})
	}
	return issues
}

// fleet20260907Config is the fleet as it stood during the stall: a ci-verifier
// that owns `delivered`, and planner/coder that exclude it.
func fleet20260907Config() *cfgpkg.DaemonConfig {
	return &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"ci-verifier": {Labels: []string{"delivered"}},
			"planner":     {Labels: []string{"needs-plan"}, ExcludeLabels: []string{"delivered"}},
			"coder":       {Labels: []string{"approved"}, ExcludeLabels: []string{"delivered"}},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "ci-verifier", Role: "ci-verifier"},
			{Worktree: "planner", Role: "planner"},
			{Worktree: "worker", Role: "coder"},
		},
	}
}

func groupFor(t *testing.T, report reachabilityReport, key string) orphanGroup {
	t.Helper()
	for _, g := range report.Groups {
		if g.Key == key {
			return g
		}
	}
	t.Fatalf("no group keyed %q in %+v", key, report.Groups)
	return orphanGroup{}
}

// TestReachability_2026_09_07Shape is the whole ticket in one test. On
// 2026-09-07 the ci-verifier was parked with desired_state=stopped while 22
// `delivered` tickets sat ready. Every ENABLED role had Q = 0, so
// fleet_starvation never failed — it reported the stranded work as a secondary
// line beside its own passing verdict, contributing nothing to the exit code,
// all night. fleet_reachability must FAIL on exactly the same inputs.
func TestReachability_2026_09_07Shape(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := fleet20260907Config()
	state := loadStateFixtureFrom(t, "reachability", "fleet_2026_09_07.json")
	ready := agedIssues(22, 18*24*time.Hour, "delivered")

	// This is the hole the new check closes. fleet_starvation does not FAIL on
	// these inputs — no ENABLED role is starved, every one of them has Q = 0 —
	// so it contributes nothing to doctor's exit code and reports the 22
	// stranded tickets as a secondary WARN line beside its own verdict.
	starvationReport := computeStarvation(cfg, nil, state, ready, reachabilityReadyLimit)
	if len(starvationReport.Starved) != 0 {
		t.Fatalf("starved roles = %v, want none", starvationReport.Starved)
	}
	starvation := renderStarvation(starvationReport)
	if starvation.Status == StatusFail {
		t.Fatalf("fleet_starvation must not fail here; that is why this ticket exists\n%s", starvation.Detail)
	}

	report := computeReachability(cfg, nil, state, ready, reachabilityReadyLimit, testNow)
	result := renderReachability(report)
	if result.Status != StatusFail {
		t.Fatalf("fleet_reachability status = %v, want fail\n%s", result.Status, result.Detail)
	}
	if result.Name != reachabilityCheckName {
		t.Fatalf("name = %q, want %q", result.Name, reachabilityCheckName)
	}
	if len(report.Unreachable) != 22 {
		t.Fatalf("unreachable = %d, want 22", len(report.Unreachable))
	}
	// Branch 2 of culpritKey produces this: removing `delivered` does NOT make
	// the issue reachable, because planner requires `needs-plan` and coder
	// requires `approved`. The single-label joint key renders bare.
	g := groupFor(t, report, "delivered")
	if g.Count != 22 || g.OldestAgeHuman != "18d" {
		t.Fatalf("group = %+v, want count 22 and oldest 18d", g)
	}
	for _, want := range []string{"delivered", "22", "18d"} {
		if !strings.Contains(result.Summary, want) {
			t.Fatalf("summary %q does not contain %q", result.Summary, want)
		}
	}
	if !strings.Contains(result.Detail, "no enabled role claims: delivered (22 issues") {
		t.Fatalf("detail does not name the orphaning label:\n%s", result.Detail)
	}
	if !strings.Contains(result.Detail, "disabled roles") || !strings.Contains(result.Detail, "ci-verifier") {
		t.Fatalf("detail does not name the disabled role:\n%s", result.Detail)
	}
}

// TestReachability_IndependentOfStarvation is the acceptance clause spelled
// out: a fleet can fail both checks at once, on different subjects.
func TestReachability_IndependentOfStarvation(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			// integrator is supervised but fatally dead: starved.
			"integrator": {Labels: []string{"approved"}},
			// ci-verifier is parked: its work is unreachable, not starved.
			"ci-verifier": {Labels: []string{"delivered"}},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "integrator", Role: "integrator"},
			{Worktree: "ci-verifier", Role: "ci-verifier"},
		},
	}
	state := &daemonStateView{PID: 4242, Agents: []daemonAgentView{
		{Worktree: "integrator", Role: "integrator", Status: "failed", LastErrorClass: "AuthFailure"},
		{Worktree: "ci-verifier", Role: "ci-verifier", Status: "parked", DesiredState: desiredStateStopped},
	}}
	ready := append(agedIssues(3, time.Hour, "approved"), agedIssues(5, 2*24*time.Hour, "delivered")...)

	starvation := renderStarvation(computeStarvation(cfg, nil, state, ready, reachabilityReadyLimit))
	reach := renderReachability(computeReachability(cfg, nil, state, ready, reachabilityReadyLimit, testNow))
	if starvation.Status != StatusFail || reach.Status != StatusFail {
		t.Fatalf("want both fail; starvation=%v reachability=%v", starvation.Status, reach.Status)
	}
	if starvation.Name == reach.Name {
		t.Fatal("the two checks must report under different names")
	}
	if !strings.Contains(starvation.Summary, "integrator") {
		t.Fatalf("starvation summary lost its subject: %q", starvation.Summary)
	}
	if !strings.Contains(reach.Summary, "delivered") || strings.Contains(reach.Summary, "approved") {
		t.Fatalf("reachability summary should name only the unclaimable label: %q", reach.Summary)
	}
}

// TestReachability_OperatorQueueIsNamedNotDropped covers work deliberately
// reserved for a human: never in the FAIL set, always in the summary.
func TestReachability_OperatorQueueIsNamedNotDropped(t *testing.T) {
	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"coder": {Labels: []string{"approved"}},
		},
		Agents: []cfgpkg.AgentEntry{{Worktree: "worker", Role: "coder"}},
	}
	state := &daemonStateView{PID: 4242, Agents: []daemonAgentView{
		{Worktree: "worker", Role: "coder", Status: "stopped"},
	}}

	t.Run("stale human queue warns", func(t *testing.T) {
		isolateRuntimeDir(t)
		ready := agedIssues(3, 5*24*time.Hour, cli.OperatorLabel)
		report := computeReachability(cfg, nil, state, ready, reachabilityReadyLimit, testNow)
		result := renderReachability(report)
		if result.Status != StatusWarn {
			t.Fatalf("status = %v, want warn (oldest human item is 5d)", result.Status)
		}
		if len(report.Unreachable) != 0 || len(report.Groups) != 0 {
			t.Fatalf("operator work must never enter the FAIL set: %+v", report)
		}
		for _, want := range []string{"awaiting a human", "3", "5d"} {
			if !strings.Contains(result.Summary, want) {
				t.Fatalf("summary %q does not contain %q", result.Summary, want)
			}
		}
	})

	t.Run("fresh human queue passes and is still named", func(t *testing.T) {
		isolateRuntimeDir(t)
		ready := agedIssues(1, 10*time.Minute, cli.OperatorLabel)
		result := renderReachability(computeReachability(cfg, nil, state, ready, reachabilityReadyLimit, testNow))
		if result.Status != StatusPass {
			t.Fatalf("status = %v, want pass (a 10-minute-old operator ticket is normal)", result.Status)
		}
		if !strings.Contains(result.Summary, "awaiting a human") {
			t.Fatalf("summary drops the human queue: %q", result.Summary)
		}
	})
}

// TestReachability_OrphaningLabelIsNamed checks the line that prevents a
// recurrence: the label, its count and its age, with reachable work absent.
func TestReachability_OrphaningLabelIsNamed(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"coder":       {Labels: []string{"approved"}, ExcludeLabels: []string{"integration-blocked"}},
			"tester":      {Labels: []string{"in-review"}, ExcludeLabels: []string{"integration-blocked"}},
			"ci-verifier": {Labels: []string{"delivered"}},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "worker", Role: "coder"},
			{Worktree: "tester", Role: "tester"},
			{Worktree: "ci-verifier", Role: "ci-verifier"},
		},
	}
	state := &daemonStateView{PID: 4242, Agents: []daemonAgentView{
		{Worktree: "worker", Role: "coder", Status: "stopped"},
		{Worktree: "tester", Role: "tester", Status: "running"},
		{Worktree: "ci-verifier", Role: "ci-verifier", Status: "parked", DesiredState: desiredStateStopped},
	}}

	blocked := agedIssues(5, 3*24*time.Hour, "integration-blocked")
	reachable := agedIssues(4, time.Hour, "approved")
	for i := range reachable {
		reachable[i].ID = fmt.Sprintf("OK-%d", i)
	}
	report := computeReachability(cfg, nil, state, append(blocked, reachable...), reachabilityReadyLimit, testNow)
	result := renderReachability(report)

	if result.Status != StatusFail {
		t.Fatalf("status = %v, want fail\n%s", result.Status, result.Detail)
	}
	if !strings.Contains(result.Detail, "no enabled role claims: integration-blocked (5 issues, oldest 3d") {
		t.Fatalf("detail does not name the label group as expected:\n%s", result.Detail)
	}
	if len(report.Groups) != 1 {
		t.Fatalf("groups = %+v, want exactly one", report.Groups)
	}
	for _, id := range report.Unreachable {
		if strings.HasPrefix(id, "OK-") {
			t.Fatalf("reachable issue %s landed in the unreachable set", id)
		}
	}
	if report.Evaluated != 9 {
		t.Fatalf("evaluated = %d, want 9", report.Evaluated)
	}
}

// TestReachability_HealthyFleetPasses is the false-positive guard. A check that
// fires on a healthy fleet gets muted within a day, and then it is worth less
// than nothing.
func TestReachability_HealthyFleetPasses(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"coder":   {Labels: []string{"approved"}},
			"planner": {Labels: []string{"needs-plan"}},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "worker", Role: "coder"},
			{Worktree: "planner", Role: "planner"},
		},
	}
	state := &daemonStateView{PID: 4242, Agents: []daemonAgentView{
		{Worktree: "worker", Role: "coder", Status: "stopped"},
		{Worktree: "planner", Role: "planner", Status: "running"},
	}}
	ready := append(agedIssues(6, time.Hour, "approved"), agedIssues(2, time.Minute, "needs-plan")...)

	report := computeReachability(cfg, nil, state, ready, reachabilityReadyLimit, testNow)
	result := renderReachability(report)
	if result.Status != StatusPass {
		t.Fatalf("status = %v, want pass\n%s", result.Status, result.Detail)
	}
	if len(report.Groups) != 0 || len(report.Unreachable) != 0 {
		t.Fatalf("healthy fleet produced findings: %+v", report)
	}
	if !report.Computed {
		t.Fatal("a healthy fleet with label filters must report computed")
	}
}

// TestReachability_NoFilteredRolesIsNotComputed guards the one distinction a
// dashboard cannot recover from: "everything is claimable" versus "the question
// was never asked".
func TestReachability_NoFilteredRolesIsNotComputed(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"coder": {},
		},
		Agents: []cfgpkg.AgentEntry{{Worktree: "worker", Role: "coder"}},
	}
	state := &daemonStateView{PID: 4242, Agents: []daemonAgentView{
		{Worktree: "worker", Role: "coder", Status: "stopped"},
	}}

	report := computeReachability(cfg, nil, state, agedIssues(4, time.Hour, "delivered"), reachabilityReadyLimit, testNow)
	if report.Computed {
		t.Fatal("computed must be false when no role carries a label filter")
	}
	result := renderReachability(report)
	if result.Status != StatusWarn {
		t.Fatalf("status = %v, want warn (never pass, never fail)", result.Status)
	}
	if !strings.Contains(result.Summary, "not computed") {
		t.Fatalf("summary %q must say the question was not asked", result.Summary)
	}
}

// TestReachability_JointLabelSetAndNoLabels covers culpritKey's two fallbacks.
func TestReachability_JointLabelSetAndNoLabels(t *testing.T) {
	isolateRuntimeDir(t)

	// Every role requires `ready-to-implement` on top of its own routing label,
	// so an issue carrying `approved` and `in-review` but not `ready-to-implement`
	// is served by nobody — and removing either label leaves it just as
	// unreachable, which is what drives culpritKey to its joint branch.
	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"coder":  {Labels: []string{"approved", "ready-to-implement"}},
			"tester": {Labels: []string{"in-review", "ready-to-implement"}},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "worker", Role: "coder"},
			{Worktree: "tester", Role: "tester"},
		},
	}
	state := &daemonStateView{PID: 4242, Agents: []daemonAgentView{
		{Worktree: "worker", Role: "coder", Status: "stopped"},
		{Worktree: "tester", Role: "tester", Status: "stopped"},
	}}

	joint := backend.IssueData{ID: "JOINT-1", Labels: []string{"in-review", "approved", "approved"}, CreatedAt: testNow.Add(-2 * time.Hour)}
	bare := backend.IssueData{ID: "BARE-1", CreatedAt: testNow.Add(-30 * time.Minute)}

	report := computeReachability(cfg, nil, state, []backend.IssueData{joint, bare}, reachabilityReadyLimit, testNow)
	if renderReachability(report).Status != StatusFail {
		t.Fatalf("want fail, got %+v", report)
	}
	// Sorted, deduped, joined with '+' — the duplicate `approved` collapses.
	g := groupFor(t, report, "approved+in-review")
	if g.Count != 1 || g.OldestAgeHuman != "2h" {
		t.Fatalf("joint group = %+v", g)
	}
	if b := groupFor(t, report, noLabelsKey); b.Count != 1 {
		t.Fatalf("no-labels group = %+v", b)
	}
	// The single-label joint key must render bare — no brackets, no "AND".
	single := culpritKeyOf(t, cfg, state, backend.IssueData{ID: "S-1", Labels: []string{"delivered"}})
	if single != "delivered" {
		t.Fatalf("single-label joint key = %q, want %q", single, "delivered")
	}
}

// culpritKeyOf runs one issue through the same accumulation the check uses.
func culpritKeyOf(t *testing.T, cfg *cfgpkg.DaemonConfig, state *daemonStateView, issue backend.IssueData) string {
	t.Helper()
	acc, _ := accumulateRoles(cfg, nil, indexStateByWorktree(state))
	filtered, _ := filteredRoles(cfg)
	enabled, _ := splitFilteredRoles(filtered, acc)
	key, _, _ := culpritKey(cfg, acc, enabled, issue)
	return key
}

// TestReachability_ScopeConstrainedIsLabelled keeps a repo-affinity cause from
// being reported as a label verdict, which would send the operator to the wrong
// file.
func TestReachability_ScopeConstrainedIsLabelled(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"coder": {Labels: []string{"approved"}},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "worker", Role: "coder", Repo: "loomcli"},
		},
	}
	state := &daemonStateView{PID: 4242, Agents: []daemonAgentView{
		{Worktree: "worker", Role: "coder", Status: "stopped"},
	}}
	issues := agedIssues(2, 6*time.Hour, "approved")
	for i := range issues {
		issues[i].SourceRepo = "fleet-db"
	}

	report := computeReachability(cfg, nil, state, issues, reachabilityReadyLimit, testNow)
	result := renderReachability(report)
	if result.Status != StatusFail {
		t.Fatalf("status = %v, want fail\n%s", result.Status, result.Detail)
	}
	g := groupFor(t, report, "approved")
	if !g.ScopeConstrained {
		t.Fatalf("group %+v must be marked scope-constrained", g)
	}
	if !strings.Contains(result.Detail, "scope-constrained") {
		t.Fatalf("detail does not say scope-constrained:\n%s", result.Detail)
	}
}

// TestReachability_QueueAtLimitIsAFloor is the regression guard for the false
// "fleet-db clamps /ready at 200" premise: only a queue that comes back at
// loomcli's own 1000-row limit is a floor.
func TestReachability_QueueAtLimitIsAFloor(t *testing.T) {
	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"coder": {Labels: []string{"approved"}},
		},
		Agents: []cfgpkg.AgentEntry{{Worktree: "worker", Role: "coder"}},
	}
	state := &daemonStateView{PID: 4242, Agents: []daemonAgentView{
		{Worktree: "worker", Role: "coder", Status: "stopped"},
	}}

	cases := []struct {
		name          string
		n             int
		limit         int
		wantTruncated bool
	}{
		{"below the limit is exact", reachabilityReadyLimit - 1, reachabilityReadyLimit, false},
		{"at the limit is a floor", reachabilityReadyLimit, reachabilityReadyLimit, true},
		{"an unbounded query is never a floor", 12, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateRuntimeDir(t)
			ready := agedIssues(tc.n, 4*time.Hour, "delivered")
			report := computeReachability(cfg, nil, state, ready, tc.limit, testNow)
			result := renderReachability(report)
			if result.Status != StatusFail {
				t.Fatalf("status = %v, want fail", result.Status)
			}
			if report.Truncated != tc.wantTruncated {
				t.Fatalf("truncated = %t, want %t", report.Truncated, tc.wantTruncated)
			}
			hasTruncationLine := strings.Contains(result.Detail, "row limit")
			if hasTruncationLine != tc.wantTruncated {
				t.Fatalf("truncation line present = %t, want %t\n%s", hasTruncationLine, tc.wantTruncated, result.Detail)
			}
			hasFloor := strings.Contains(result.Summary, ">=")
			if hasFloor != tc.wantTruncated {
				t.Fatalf("floor rendering = %t, want %t (summary %q)", hasFloor, tc.wantTruncated, result.Summary)
			}
		})
	}
}

// TestReachability_DegradedInputs: a verdict is never fabricated from inputs
// that were not read.
func TestReachability_DegradedInputs(t *testing.T) {
	cfg := &cfgpkg.DaemonConfig{
		Roles:  map[string]cfgpkg.RoleConfig{"coder": {Labels: []string{"approved"}}},
		Agents: []cfgpkg.AgentEntry{{Worktree: "worker", Role: "coder"}},
	}
	state := &daemonStateView{PID: 4242, Agents: []daemonAgentView{
		{Worktree: "worker", Role: "coder", Status: "stopped"},
	}}

	cases := []struct {
		name  string
		cfg   *cfgpkg.DaemonConfig
		state *daemonStateView
	}{
		{"no daemon config", nil, state},
		{"no daemon state", cfg, nil},
		{"neither", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateRuntimeDir(t)
			report := computeReachability(tc.cfg, nil, tc.state, agedIssues(3, time.Hour, "delivered"), reachabilityReadyLimit, testNow)
			result := renderReachability(report)
			if result.Status != StatusWarn {
				t.Fatalf("status = %v, want warn", result.Status)
			}
			if report.Computed {
				t.Fatal("computed must be false for a degraded input")
			}
		})
	}

	t.Run("no issue backend", func(t *testing.T) {
		isolateRuntimeDir(t)
		result := checkFleetReachability(&cli.Deps{})
		if result.Status != StatusWarn {
			t.Fatalf("status = %v, want warn", result.Status)
		}
		if result.Name != reachabilityCheckName {
			t.Fatalf("name = %q", result.Name)
		}
	})
}

// TestReachability_AgeRendering pins the humaniser: one coarse unit, and an
// unusable CreatedAt reported as unknown rather than as a 56-year age.
func TestReachability_AgeRendering(t *testing.T) {
	isolateRuntimeDir(t)

	cases := []struct {
		in   time.Duration
		want string
	}{
		{90 * time.Second, "1m"},
		{45 * time.Minute, "45m"},
		{5 * time.Hour, "5h"},
		{18 * 24 * time.Hour, "18d"},
		{0, "0s"},
	}
	for _, tc := range cases {
		if got := humanAge(tc.in); got != tc.want {
			t.Fatalf("humanAge(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}

	if _, known := issueAge(backend.IssueData{ID: "Z"}, testNow); known {
		t.Fatal("a zero CreatedAt must not report a known age")
	}
	entry := humanEntry(backend.IssueData{ID: "Z"}, testNow)
	if entry.Age != unknownAge || entry.AgeSeconds != 0 {
		t.Fatalf("entry = %+v, want an unknown age", entry)
	}
}
