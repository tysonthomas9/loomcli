package doctor

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// spawnHealthWindow is the period over which spawn health and fleet progress
// are judged. One hour is long enough that a healthy fleet has finished
// several runs in it, and short enough that an outage is named while it is
// still happening rather than after the operator has already noticed.
const spawnHealthWindow = time.Hour

// spawnHealthMinRuns is the minimum number of terminal runs in the window
// before a success rate means anything. Below this a single unlucky failure
// reads as a 0% success rate, and doctor cries wolf on a fleet that has
// barely started.
const spawnHealthMinRuns = 5

// spawnHealthWarnRate is the success rate below which the fleet is degraded
// but not yet dark. Calibrated against PUPPET's 2026-08-28 outage: healthy
// hours run well above 90%, while the two hours preceding the total outage
// ran 40% and 17%.
const spawnHealthWarnRate = 0.5

// spawnLastErrorRunes bounds how much of the supervisor's recorded error is
// quoted, so one pathological message cannot take over the report.
const spawnLastErrorRunes = 300

// outcomeTally is the pure summary of the session ledger that both spawn
// checks reason about. It carries no filesystem state, so every interesting
// case can be exercised without staging a workspace.
type outcomeTally struct {
	Completed  int
	Failed     int
	Aborted    int
	ErrorClass map[string]int          // normalized class -> count, failed+aborted only
	ClassLabel map[string]string       // normalized class -> most-seen raw spelling
	LatestFail *sessions.SessionRecord // newest terminal non-completed run
}

// terminal counts the runs that actually reached an outcome. Records still
// `running` are excluded deliberately: they are not evidence either way, and
// counting them would dilute the rate exactly when the fleet is stuck.
func (t outcomeTally) terminal() int { return t.Completed + t.Failed + t.Aborted }

// successRate is completions over terminal runs; zero terminal runs is 0.
func (t outcomeTally) successRate() float64 {
	if t.terminal() == 0 {
		return 0
	}
	return float64(t.Completed) / float64(t.terminal())
}

// dominantErrorClass returns the most frequent error class and its count,
// using the raw spelling the operator will find in their own ledger. Ties
// break by label so doctor output is stable between runs.
func (t outcomeTally) dominantErrorClass() (string, int) {
	type entry struct {
		label string
		count int
	}
	entries := make([]entry, 0, len(t.ErrorClass))
	for key, count := range t.ErrorClass {
		label := t.ClassLabel[key]
		if label == "" {
			label = key
		}
		entries = append(entries, entry{label: label, count: count})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].label < entries[j].label
	})
	if len(entries) == 0 {
		return "", 0
	}
	return entries[0].label, entries[0].count
}

// normalizeErrorClass folds the two disjoint spellings of error_class into one
// bucket. The supervisor writes snake_case literals ("spawn_failure") while
// the normal-exit path writes Outcome.String() values ("SpawnFailure"); both
// are present in a live ledger and they name the same thing.
func normalizeErrorClass(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	return strings.ReplaceAll(strings.ToLower(s), "_", "")
}

// tallyOutcomes buckets terminal records inside the window. The `since`
// comparison is repeated here even though the reader's Filter already applies
// it, so the pure core is self-contained and its tests need no Filter.
func tallyOutcomes(recs []sessions.SessionRecord, since time.Time) outcomeTally {
	t := outcomeTally{
		ErrorClass: make(map[string]int),
		ClassLabel: make(map[string]string),
	}
	rawCount := make(map[string]map[string]int)

	for i := range recs {
		rec := recs[i]
		if !since.IsZero() && rec.StartedAt.Before(since) {
			continue
		}
		switch rec.Status {
		case sessions.StatusCompleted:
			t.Completed++
			continue
		case sessions.StatusFailed:
			t.Failed++
		case sessions.StatusAborted:
			t.Aborted++
		default:
			continue // still running: not terminal, not evidence
		}

		key := normalizeErrorClass(rec.ErrorClass)
		raw := strings.TrimSpace(rec.ErrorClass)
		if raw == "" {
			raw = "unknown"
		}
		t.ErrorClass[key]++
		if rawCount[key] == nil {
			rawCount[key] = make(map[string]int)
		}
		rawCount[key][raw]++

		if t.LatestFail == nil || rec.StartedAt.After(t.LatestFail.StartedAt) {
			latest := rec
			t.LatestFail = &latest
		}
	}

	// The displayed label is the spelling most often seen in the operator's
	// own file, so grepping the report against the ledger works.
	for key, spellings := range rawCount {
		best, bestN := "", -1
		for raw, n := range spellings {
			if n > bestN || (n == bestN && raw < best) {
				best, bestN = raw, n
			}
		}
		t.ClassLabel[key] = best
	}
	return t
}

// loadOutcomeTally reads the session ledger read-only and tallies the window.
// A nil store means the sessions store itself was unavailable, which is not
// something to diagnose; a non-nil error means the ledger could not be read.
func loadOutcomeTally(now time.Time) (outcomeTally, *sessions.Store, error) {
	since := now.Add(-spawnHealthWindow)
	store, err := sessions.NewStore(cli.GetWorkspaceRuntimeDir())
	if err != nil {
		return outcomeTally{}, nil, err
	}
	recs, err := store.Snapshot(sessions.Filter{Since: since})
	if err != nil {
		return outcomeTally{}, store, err
	}
	return tallyOutcomes(recs, since), store, nil
}

// checkSpawnHealth reports whether agent runs are succeeding at all. It reads
// the session ledger, which is the only durable record of the supervisor
// actually producing work — daemon liveness says nothing about whether the
// runs it schedules survive their first second.
func checkSpawnHealth() CheckResult {
	now := time.Now()
	tally, store, err := loadOutcomeTally(now)
	if store == nil {
		return CheckResult{} // sessions store unavailable — nothing to diagnose
	}
	if err != nil {
		// An unreadable ledger is not evidence of an outage; never fail on it.
		return CheckResult{
			Name:    "spawn_health",
			Status:  StatusWarn,
			Summary: "could not read the session ledger",
			Detail:  err.Error(),
		}
	}

	var lastErr string
	if tally.LatestFail != nil {
		if md, mdErr := store.LoadMetadata(tally.LatestFail.SessionID); mdErr == nil && md != nil {
			lastErr = md.LastError
		}
	}
	return evaluateSpawnHealth(tally, lastErr)
}

// evaluateSpawnHealth is the pure verdict behind checkSpawnHealth. The
// thresholds are package constants rather than parameters: they are calibrated
// against a measured outage, so a test that varied them would be asserting
// against numbers the fleet never runs with.
func evaluateSpawnHealth(t outcomeTally, lastErr string) CheckResult {
	total := t.terminal()
	if total < spawnHealthMinRuns {
		return CheckResult{} // too little evidence to say anything
	}

	switch {
	case t.Completed == 0:
		return CheckResult{
			Name:   "spawn_health",
			Status: StatusFail,
			Summary: fmt.Sprintf("no agent run has succeeded in the last %s (%d failed, %d aborted)",
				spawnHealthWindow, t.Failed, t.Aborted),
			Detail: strings.Join(spawnFailureDetail(t, lastErr), "\n"),
		}
	case t.successRate() < spawnHealthWarnRate:
		return CheckResult{
			Name:   "spawn_health",
			Status: StatusWarn,
			Summary: fmt.Sprintf("agent run success rate is %.0f%% over the last %s (%d of %d succeeded)",
				t.successRate()*100, spawnHealthWindow, t.Completed, total),
			Detail: strings.Join(spawnFailureDetail(t, lastErr), "\n"),
		}
	default:
		return CheckResult{
			Name:   "spawn_health",
			Status: StatusPass,
			Summary: fmt.Sprintf("%d of %d agent run(s) succeeded in the last %s",
				t.Completed, total, spawnHealthWindow),
		}
	}
}

// spawnFailureDetail renders one fact per line: what is failing, the newest
// example, and the supervisor's own words for why.
func spawnFailureDetail(t outcomeTally, lastErr string) []string {
	var lines []string
	if class, count := t.dominantErrorClass(); class != "" {
		lines = append(lines, fmt.Sprintf("dominant error_class: %s (%d of %d failed run(s))",
			class, count, t.Failed+t.Aborted))
	}
	if t.LatestFail != nil {
		lines = append(lines, fmt.Sprintf("most recent failure: %s (%s, %s)",
			t.LatestFail.SessionID, t.LatestFail.AgentName,
			t.LatestFail.StartedAt.UTC().Format(time.RFC3339)))
		if trimmed := condenseLastError(lastErr); trimmed != "" {
			lines = append(lines, "  "+trimmed)
		}
	}
	lines = append(lines, "remediation: inspect `loom daemon status` and the daemon log; "+
		"this is the supervisor refusing to start agents, not a task-level failure.")
	return lines
}

// condenseLastError makes the supervisor's recorded message safe to print as
// one Detail line: newlines collapsed, length bounded on a rune boundary.
func condenseLastError(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " / ")), " "))
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) > spawnLastErrorRunes {
		return string(runes[:spawnLastErrorRunes]) + "…"
	}
	return s
}

// checkFleetProgress is the dead-man's switch: ready work is waiting and
// nothing has finished. It fires for causes nobody enumerated, including the
// ones that produce no failed runs for checkSpawnHealth to look at because no
// run was ever attempted.
func checkFleetProgress(deps *cli.Deps) CheckResult {
	if deps == nil || deps.IssueBackend == nil {
		return CheckResult{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ready, err := deps.IssueBackend.Ready(ctx, backend.ReadyOpts{Limit: 1})
	if err != nil {
		return CheckResult{} // checkIssueBackend owns backend reachability
	}

	tally, store, tallyErr := loadOutcomeTally(time.Now())
	if store == nil || tallyErr != nil {
		return CheckResult{} // spawn_health already reports an unreadable ledger
	}
	return evaluateFleetProgress(tally, len(ready))
}

// evaluateFleetProgress is the pure verdict behind checkFleetProgress.
func evaluateFleetProgress(t outcomeTally, readyCount int) CheckResult {
	switch {
	case readyCount == 0:
		// A quiet fleet is not an outage. This arm must never fail.
		return CheckResult{
			Name:    "fleet_progress",
			Status:  StatusPass,
			Summary: "fleet idle: no ready work to pick up",
		}
	case t.Completed > 0:
		return CheckResult{
			Name:   "fleet_progress",
			Status: StatusPass,
			Summary: fmt.Sprintf("fleet is making progress (%d run(s) completed in the last %s)",
				t.Completed, spawnHealthWindow),
		}
	default:
		var detail []string
		if class, count := t.dominantErrorClass(); class != "" {
			detail = append(detail, fmt.Sprintf("dominant error_class: %s (%d of %d terminal run(s))",
				class, count, t.terminal()))
		} else {
			detail = append(detail, "no runs were even attempted — check that the daemon is "+
				"claiming work (loom daemon status)")
		}
		return CheckResult{
			Name:   "fleet_progress",
			Status: StatusFail,
			Summary: fmt.Sprintf("ready work is waiting and nothing has completed in %s (%d ready, %d terminal run(s))",
				spawnHealthWindow, readyCount, t.terminal()),
			Detail: strings.Join(detail, "\n"),
		}
	}
}
