package doctor

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// isolateRuntimeDir points LOOM_WORKSPACE_RUNTIME_DIR at a throwaway directory
// for the duration of a test. Agent shells export that variable, so a test that
// ever writes runtime state would otherwise write the PRODUCTION fleet's state.
// None of the tests below is supposed to touch it — this makes that true rather
// than hoped for.
func isolateRuntimeDir(t *testing.T) {
	t.Helper()
	t.Setenv("LOOM_WORKSPACE_RUNTIME_DIR", t.TempDir())
	ResetWorkspaceRuntimeDirCache()
	t.Cleanup(ResetWorkspaceRuntimeDirCache)
}

// loadStateFixture reads one of the testdata daemon-state files.
func loadStateFixture(t *testing.T, name string) *daemonStateView {
	t.Helper()
	state, err := loadDaemonStateView(filepath.Join("testdata", "starvation", name))
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	return state
}

// readyIssues builds n ready, unassigned issues carrying the given labels.
func readyIssues(n int, labels ...string) []backend.IssueData {
	issues := make([]backend.IssueData, 0, n)
	for i := 0; i < n; i++ {
		issues = append(issues, backend.IssueData{
			ID:     fmt.Sprintf("PUPPET-%d", 1000+i),
			Status: "open",
			Labels: append([]string(nil), labels...),
		})
	}
	return issues
}

// metricFor returns the row for a role, failing the test when it is absent —
// a missing row is itself a bug, since every configured role gets one.
func metricFor(t *testing.T, report starvationReport, role string) roleMetric {
	t.Helper()
	for _, m := range report.Roles {
		if m.Role == role {
			return m
		}
	}
	t.Fatalf("no role metric for %q in %+v", role, report.Roles)
	return roleMetric{}
}

func assertMetric(t *testing.T, got roleMetric, wantQ, wantCInt, wantCAct int, wantStarved bool) {
	t.Helper()
	if got.Q != wantQ || got.CInt != wantCInt || got.CAct != wantCAct || got.Starved != wantStarved {
		t.Fatalf("role %s: got Q=%d C_int=%d C_act=%d starved=%t; want Q=%d C_int=%d C_act=%d starved=%t",
			got.Role, got.Q, got.CInt, got.CAct, got.Starved, wantQ, wantCInt, wantCAct, wantStarved)
	}
}

// TestStarvation_IntegratorFatal replays the incident this check exists for: a
// supervised integrator dead on an AuthFailure while 35 approved tickets sat
// ready. Eleven agents read "idle" through the /agents API at the time, which
// is why the check reads the daemon state file instead.
func TestStarvation_IntegratorFatal(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"integrator": {Labels: []string{"approved"}},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "integrator", Role: "integrator"},
		},
	}
	state := loadStateFixture(t, "integrator_fatal.json")

	report := computeStarvation(cfg, nil, state, readyIssues(35, "approved"), starvationReadyLimit)
	assertMetric(t, metricFor(t, report, "integrator"), 35, 1, 0, true)

	result := renderStarvation(report)
	if result.Status != StatusFail {
		t.Fatalf("status = %v, want fail", result.Status)
	}
	if result.Name != "fleet_starvation" {
		t.Fatalf("name = %q", result.Name)
	}
	if !strings.Contains(result.Detail, "AuthFailure") {
		t.Fatalf("detail does not name the error class:\n%s", result.Detail)
	}
	// `loom data agent start` is a proven no-op after an in-memory terminal
	// stop, so the remediation has to be the daemon restart.
	if !strings.Contains(result.Detail, "pm2 restart loom-daemon") {
		t.Fatalf("detail does not name the pm2 remediation:\n%s", result.Detail)
	}
}

// TestStarvation_CiVerifierNeverFlags is the false-positive guard and the most
// important test in this file. ci-verifier is parked with desired_state=stopped
// — capacity that is intentionally zero. No amount of ready work makes that
// starvation, and a check that says otherwise gets muted and stops working.
func TestStarvation_CiVerifierNeverFlags(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"ci-verifier": {Labels: []string{"ci-check"}},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "ci-verifier", Role: "ci-verifier"},
		},
	}
	state := loadStateFixture(t, "ci_verifier_parked_stopped.json")

	for _, q := range []int{4, 10000} {
		t.Run(fmt.Sprintf("q=%d", q), func(t *testing.T) {
			report := computeStarvation(cfg, nil, state, readyIssues(q, "ci-check"), 0)
			assertMetric(t, metricFor(t, report, "ci-verifier"), q, 0, 0, false)
			if len(report.Starved) != 0 {
				t.Fatalf("starved roles = %v, want none", report.Starved)
			}
			if renderStarvation(report).Status == StatusFail {
				t.Fatal("a deliberately parked role must never produce a FAIL")
			}
		})
	}
}

// TestStarvation_DashboardQaParkedRunning is the other half of the park
// distinction: parked with desired_state=running is capacity that was intended
// and is not there.
func TestStarvation_DashboardQaParkedRunning(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"dashboard-qa": {Labels: []string{"qa"}},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "dashboard-qa", Role: "dashboard-qa"},
		},
	}
	state := loadStateFixture(t, "dashboard_qa_parked_running.json")

	report := computeStarvation(cfg, nil, state, readyIssues(38, "qa"), starvationReadyLimit)
	assertMetric(t, metricFor(t, report, "dashboard-qa"), 38, 1, 0, true)
	if renderStarvation(report).Status != StatusFail {
		t.Fatal("want fail")
	}
}

// TestStarvation_StoppedIsAlive pins the rule most likely to be "simplified"
// into a fleet-wide false alarm: "stopped" is the normal idle state.
func TestStarvation_StoppedIsAlive(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"decomposer": {Labels: []string{"needs-decomposition"}},
			"observer":   {ExcludeLabels: []string{cli.OperatorLabel}},
			"plan":       {Labels: []string{"needs-plan"}},
			"tester":     {Labels: []string{"in-review"}},
			"coder":      {Labels: []string{"ready-to-implement"}},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "decomposer", Role: "decomposer"},
			{Worktree: "observer", Role: "observer"},
			{Worktree: "planner", Role: "plan"},
			{Worktree: "tester", Role: "tester"},
			{Worktree: "worker", Role: "coder"},
			{Worktree: "worker-2", Role: "coder"},
			{Worktree: "worker-3", Role: "coder"},
		},
	}
	state := loadStateFixture(t, "healthy.json")

	report := computeStarvation(cfg, nil, state, readyIssues(38, "ready-to-implement"), starvationReadyLimit)
	for _, m := range report.Roles {
		if m.Starved {
			t.Fatalf("role %s flagged starved with every agent merely idle: %+v", m.Role, m)
		}
	}
	assertMetric(t, metricFor(t, report, "coder"), 38, 3, 3, false)
}

// TestStarvation_BlockedIsAlive pins the second alive-status rule: a blocked
// agent self-resumes, so it is capacity, not a starvation.
func TestStarvation_BlockedIsAlive(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := &cfgpkg.DaemonConfig{
		Roles:  map[string]cfgpkg.RoleConfig{"coder": {Labels: []string{"ready-to-implement"}}},
		Agents: []cfgpkg.AgentEntry{{Worktree: "worker", Role: "coder"}},
	}
	state := &daemonStateView{
		PID:    1,
		Agents: []daemonAgentView{{Worktree: "worker", Role: "coder", Status: "blocked", StopReason: "max_retries_blocked"}},
	}

	report := computeStarvation(cfg, nil, state, readyIssues(7, "ready-to-implement"), starvationReadyLimit)
	assertMetric(t, metricFor(t, report, "coder"), 7, 1, 1, false)
}

// TestStarvation_LabelFilters covers the four ways an issue can fail to be
// this role's work: a missing required label, a present excluded label, a repo
// outside the agent's affinity, and an epic outside its Parent scoping.
func TestStarvation_LabelFilters(t *testing.T) {
	isolateRuntimeDir(t)

	state := &daemonStateView{
		PID: 1,
		Agents: []daemonAgentView{
			{Worktree: "worker", Role: "coder", Status: "running"},
			{Worktree: "observer", Role: "observer", Status: "running"},
			{Worktree: "epic-worker", Role: "epic-coder", Status: "running"},
		},
	}
	repos := []cfgpkg.RepoConfig{
		{Name: "loomcli", SourceRepoID: "loomcli"},
		{Name: "fleet-db", SourceRepoID: "fleet-db"},
	}
	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			// Include labels, plus an exclusion that must win over them.
			"coder": {Labels: []string{"ready-to-implement"}, ExcludeLabels: []string{cli.OperatorLabel}},
			// Empty include set: vacuously true, matches everything.
			"observer":   {ExcludeLabels: []string{cli.OperatorLabel}},
			"epic-coder": {Labels: []string{"ready-to-implement"}},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "worker", Role: "coder", Repos: []string{"loomcli"}},
			{Worktree: "observer", Role: "observer"},
			{Worktree: "epic-worker", Role: "epic-coder", Parent: "PUPPET-423"},
		},
	}

	ready := []backend.IssueData{
		{ID: "A", Labels: []string{"ready-to-implement"}, SourceRepo: "loomcli", Parent: "PUPPET-423"},
		{ID: "B", Labels: []string{"ready-to-implement"}, SourceRepo: "fleet-db"},                     // wrong repo for coder
		{ID: "C", Labels: []string{"ready-to-implement", cli.OperatorLabel}, SourceRepo: "loomcli"},   // excluded label
		{ID: "D", Labels: []string{"needs-plan"}, SourceRepo: "loomcli"},                              // missing required label
		{ID: "E", Labels: []string{"ready-to-implement"}, SourceRepo: "loomcli", Assignee: "someone"}, // already claimed
	}

	report := computeStarvation(cfg, repos, state, ready, starvationReadyLimit)

	// coder: only A. B is the wrong repo, C is excluded, D lacks the label,
	// E is assigned.
	if got := metricFor(t, report, "coder").Q; got != 1 {
		t.Fatalf("coder Q = %d, want 1", got)
	}
	// observer has no include labels, so everything unassigned and not
	// operator-labeled counts: A, B, D.
	if got := metricFor(t, report, "observer").Q; got != 3 {
		t.Fatalf("observer Q = %d, want 3 (empty include set matches everything)", got)
	}
	// epic-coder is scoped to PUPPET-423, so only A.
	if got := metricFor(t, report, "epic-coder").Q; got != 1 {
		t.Fatalf("epic-coder Q = %d, want 1 (Parent scoping)", got)
	}
}

// TestStarvation_FilterlessRoleExcludedFromReachability covers both halves of
// the filterless-role rule: such a role is dropped from the reachability
// question (it would otherwise make all work look claimable) AND is itself
// reported as a config defect.
func TestStarvation_FilterlessRoleExcludedFromReachability(t *testing.T) {
	isolateRuntimeDir(t)

	state := &daemonStateView{
		PID: 1,
		Agents: []daemonAgentView{
			{Worktree: "worker", Role: "coder", Status: "running"},
			{Worktree: "dashboard-qa", Role: "dashboard-qa", Status: "running"},
		},
	}
	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{
			"coder": {Labels: []string{"ready-to-implement"}},
			// No labels and no exclude_labels: matches everything.
			"dashboard-qa": {},
		},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "worker", Role: "coder"},
			{Worktree: "dashboard-qa", Role: "dashboard-qa"},
		},
	}
	ready := []backend.IssueData{
		{ID: "REACHABLE", Labels: []string{"ready-to-implement"}},
		{ID: "ORPHAN", Labels: []string{"needs-triage"}},
	}

	report := computeStarvation(cfg, nil, state, ready, starvationReadyLimit)

	if !report.UnreachableComputed {
		t.Fatal("unreachable_computed = false, want true (coder carries a filter)")
	}
	if len(report.Unreachable) != 1 || report.Unreachable[0] != "ORPHAN" {
		t.Fatalf("unreachable = %v, want [ORPHAN]", report.Unreachable)
	}
	if !hasDefect(report, "filterless_role", "dashboard-qa") {
		t.Fatalf("no filterless_role defect for dashboard-qa: %+v", report.ConfigDefects)
	}
	if renderStarvation(report).Status != StatusWarn {
		t.Fatal("unreachable work plus a config defect should warn, not fail")
	}
}

// TestStarvation_NoFilteredRoles pins the n/a case. Reporting 0 unreachable
// when the question was never asked is a lie a dashboard cannot recover from.
func TestStarvation_NoFilteredRoles(t *testing.T) {
	isolateRuntimeDir(t)

	state := &daemonStateView{
		PID:    1,
		Agents: []daemonAgentView{{Worktree: "dashboard-qa", Role: "dashboard-qa", Status: "running"}},
	}
	cfg := &cfgpkg.DaemonConfig{
		Roles:  map[string]cfgpkg.RoleConfig{"dashboard-qa": {}},
		Agents: []cfgpkg.AgentEntry{{Worktree: "dashboard-qa", Role: "dashboard-qa"}},
	}

	report := computeStarvation(cfg, nil, state, readyIssues(3, "anything"), starvationReadyLimit)
	if report.UnreachableComputed {
		t.Fatal("unreachable_computed = true with no filtered role")
	}
	if len(report.Unreachable) != 0 {
		t.Fatalf("unreachable = %v, want empty when not computed", report.Unreachable)
	}
	detail := renderStarvationDetail(report)
	if !strings.Contains(detail, "unreachable: n/a (no role carries a label filter)") {
		t.Fatalf("detail does not report n/a:\n%s", detail)
	}
}

// TestStarvation_StaleDaemon covers the frozen-state-file case: fail loudly,
// and compute nothing.
func TestStarvation_StaleDaemon(t *testing.T) {
	isolateRuntimeDir(t)

	state := loadStateFixture(t, "stale_daemon.json")
	result, isStale := staleStateResult(state)
	if !isStale {
		t.Fatal("pid -1 not treated as stale")
	}
	if result.Status != StatusFail {
		t.Fatalf("status = %v, want fail", result.Status)
	}
	if !strings.Contains(result.Summary, "stale") {
		t.Fatalf("summary = %q", result.Summary)
	}
	if result.Data != nil {
		t.Fatalf("stale state must publish no metric, got %+v", result.Data)
	}
}

// TestStarvation_QClamped covers the row-clamp case. A clamped Q is still >= 1,
// so it must be reported as a floor and must still alert.
func TestStarvation_QClamped(t *testing.T) {
	isolateRuntimeDir(t)

	const limit = 5
	cfg := &cfgpkg.DaemonConfig{
		Roles:  map[string]cfgpkg.RoleConfig{"integrator": {Labels: []string{"approved"}}},
		Agents: []cfgpkg.AgentEntry{{Worktree: "integrator", Role: "integrator"}},
	}
	state := loadStateFixture(t, "integrator_fatal.json")

	report := computeStarvation(cfg, nil, state, readyIssues(limit, "approved"), limit)
	if !report.QClamped {
		t.Fatal("q_clamped = false when the ready query came back full")
	}
	m := metricFor(t, report, "integrator")
	assertMetric(t, m, limit, 1, 0, true)
	if !m.QFloor {
		t.Fatal("role metric does not mark Q as a floor")
	}
	if got := renderStarvation(report).Status; got != StatusFail {
		t.Fatalf("status = %v, want fail: a clamped Q is still >= 1", got)
	}
	if !strings.Contains(renderStarvationDetail(report), "Q=>=5") {
		t.Fatalf("detail does not render Q as a floor:\n%s", renderStarvationDetail(report))
	}
}

// TestStarvation_ForeignNodeAgent covers ResolveNodeID scoping: another node's
// agent appears in the shared config but never in this node's state file, so it
// is this node's capacity for neither metric.
func TestStarvation_ForeignNodeAgent(t *testing.T) {
	isolateRuntimeDir(t)

	state := &daemonStateView{
		PID:    1,
		Agents: []daemonAgentView{{Worktree: "worker", Role: "coder", Status: "running"}},
	}
	cfg := &cfgpkg.DaemonConfig{
		Roles: map[string]cfgpkg.RoleConfig{"coder": {Labels: []string{"ready-to-implement"}}},
		Agents: []cfgpkg.AgentEntry{
			{Worktree: "worker", Role: "coder"},
			{Worktree: "worker-on-another-node", Role: "coder"},
		},
	}

	report := computeStarvation(cfg, nil, state, readyIssues(2, "ready-to-implement"), starvationReadyLimit)
	assertMetric(t, metricFor(t, report, "coder"), 2, 1, 1, false)
	if !hasDefect(report, "agent_absent_from_state", "worker-on-another-node") {
		t.Fatalf("no agent_absent_from_state defect: %+v", report.ConfigDefects)
	}
}

// TestStarvation_UndefinedRole covers an agent entry pointing at a role with no
// role config: its filters are treated as empty and the mismatch is reported.
func TestStarvation_UndefinedRole(t *testing.T) {
	isolateRuntimeDir(t)

	state := &daemonStateView{
		PID:    1,
		Agents: []daemonAgentView{{Worktree: "mystery", Role: "mystery-role", Status: "running"}},
	}
	cfg := &cfgpkg.DaemonConfig{
		Roles:  map[string]cfgpkg.RoleConfig{},
		Agents: []cfgpkg.AgentEntry{{Worktree: "mystery", Role: "mystery-role"}},
	}

	report := computeStarvation(cfg, nil, state, readyIssues(3, "anything"), starvationReadyLimit)
	if !hasDefect(report, "undefined_role", "mystery") {
		t.Fatalf("no undefined_role defect: %+v", report.ConfigDefects)
	}
	// Empty filters, so every ready issue counts.
	assertMetric(t, metricFor(t, report, "mystery-role"), 3, 1, 1, false)
}

// TestStarvation_IdleFleetIsHealthy pins the case a plain "no agents running"
// alarm could not tell apart from an outage: nothing ready, nothing running.
func TestStarvation_IdleFleetIsHealthy(t *testing.T) {
	isolateRuntimeDir(t)

	cfg := &cfgpkg.DaemonConfig{
		Roles:  map[string]cfgpkg.RoleConfig{"integrator": {Labels: []string{"approved"}}},
		Agents: []cfgpkg.AgentEntry{{Worktree: "integrator", Role: "integrator"}},
	}
	state := loadStateFixture(t, "integrator_fatal.json")

	report := computeStarvation(cfg, nil, state, nil, starvationReadyLimit)
	assertMetric(t, metricFor(t, report, "integrator"), 0, 1, 0, false)
	if renderStarvation(report).Status != StatusPass {
		t.Fatal("a dead agent with no work waiting is not starvation")
	}
}

// TestCheckResultDataOmitted protects every existing doctor golden: a check
// that sets no Data must marshal without the key.
func TestCheckResultDataOmitted(t *testing.T) {
	raw, err := json.Marshal(CheckResult{Name: "x", Status: StatusPass, Summary: "y"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "data") {
		t.Fatalf("CheckResult with no Data marshaled a data key: %s", raw)
	}
	withData, err := json.Marshal(CheckResult{Name: "x", Status: StatusPass, Data: starvationReport{}})
	if err != nil {
		t.Fatalf("marshal with data: %v", err)
	}
	if !strings.Contains(string(withData), `"data"`) {
		t.Fatalf("Data was set but did not marshal: %s", withData)
	}
}

// TestCheckFleetStarvationNoBackend covers the unknown-Q case: without a ready
// queue the check warns rather than asserting anything about starvation.
func TestCheckFleetStarvationNoBackend(t *testing.T) {
	isolateRuntimeDir(t)
	setupWorkspaceConfig(t, &LoomConfig{})

	result := checkFleetStarvation(&cli.Deps{})
	if result.Status == StatusFail {
		t.Fatalf("an unknown Q must never produce a FAIL: %+v", result)
	}
	if result.Data != nil {
		t.Fatalf("no metric should be published without a ready queue: %+v", result.Data)
	}
}

func hasDefect(report starvationReport, kind, subject string) bool {
	for _, d := range report.ConfigDefects {
		if d.Kind == kind && d.Subject == subject {
			return true
		}
	}
	return false
}
