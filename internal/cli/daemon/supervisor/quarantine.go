package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// Task quarantine: a supervisor-scoped, task-ID-keyed ledger of repeated
// no-progress kills. When a backend silently stalls, the watchdog (or the
// ownership/lease path) kills the session, recovery resets the task to open,
// and the picker re-selects the same task — an infinite boomerang that
// agent-level machinery (restart budgets, blocks) cannot break, because the
// task returns to open every cycle and a sibling re-picks it. The ledger
// counts consecutive quarantine-eligible kills per task ACROSS agents; once a
// task accumulates quarantineThreshold() of them with no progress in between,
// the sweep sets it to blocked (status + label + kill-timeline comment).
//
// Eligibility is declared in the policy seat (agentpolicy.QuarantineEligible);
// this file owns the state — mirroring how BlockBudget is declared in the
// policy table but counted agent-side by the supervisor.

const (
	defaultQuarantineThreshold = 3
	quarantineLabel            = "loom:quarantined"
	quarantineWriteTimeout     = 10 * time.Second
	maxTrackedQuarantineTasks  = 512 // defensive cap on ledger size; oldest evicted
	maxKillEventsRetained      = 10  // kill-timeline cap per task

	// The ledger is persisted next to daemon-agents.json so a daemon restart
	// does not reset the counter. That mattered concretely: the failure mode
	// that produces boomeranging tasks (a wedged daemon that PM2 restarts) was
	// exactly the one that wiped the evidence, so the threshold could never be
	// reached on a host where the daemon crash-loops.
	quarantineStateFileName = "daemon-quarantine.json"
	quarantineStateVersion  = 1
	// quarantineRecordTTL drops records not touched within this window at load
	// time. A task still genuinely boomeranging re-accumulates immediately.
	quarantineRecordTTL = 24 * time.Hour

	// quarantineBootGrace is how long after daemon boot (Supervisor.BootedAt)
	// a kill is treated as collateral of the restart itself — resume failures,
	// stale-lock cleanup, adopted runs — rather than evidence about the task it
	// was holding.
	//
	// A ZERO BootedAt DISABLES the grace: it means the construction site never
	// recorded a boot time, not that the daemon booted at the epoch, and it
	// must never suppress a kill. Same trap applyRunDurationKill documents for
	// a zero lastStart. The Supervisor is a cross-package composite literal, so
	// every test literal omits the field and must behave exactly as it did
	// before the field existed.
	quarantineBootGrace = 5 * time.Minute
)

// killEvent is one observed kill of a task-holding agent, captured at exit
// time (after classifyAgentExit, before finalize/recovery clear the session
// and lock state it reads).
type killEvent struct {
	At              time.Time `json:"at"`
	Agent           string    `json:"agent"`             // ap.Entry.Worktree
	StopReason      string    `json:"stop_reason"`       // e.g. "watchdog"; empty for a bare crash / ownership kill
	ErrClass        string    `json:"err_class"`         // classified outcome (Unknown | Timeout | Transient | ContextOverflow)
	ExitCode        int       `json:"exit_code"`         //
	FleetSessionID  string    `json:"fleet_session_id"`  // ap.AgentSessionID — captured before finalize clears it
	ClaudeSessionID string    `json:"claude_session_id"` // lock ClaudeSessionID (best-effort; empty if absent)
	RunID           string    `json:"run_id"`            // lock RunID (best-effort)

	// RunSilent mirrors ap.RunSilentAtStop: for a run_duration_exceeded kill,
	// whether the run was ALSO silent past its output timeout. Meaningless (and
	// false) for every other stop reason.
	RunSilent bool `json:"run_silent,omitempty"`
	// NotCounted is the quarantineCountable reason string when this kill was
	// recorded in the timeline but never charged to the task's counter; empty
	// for a kill that counted.
	NotCounted string `json:"not_counted,omitempty"`
}

// reason renders a compact kill descriptor for status output, e.g.
// "watchdog/Timeout" or "crash/Unknown".
func (ev killEvent) reason() string {
	kind := ev.StopReason
	if kind == "" {
		kind = "crash"
	}
	if ev.ErrClass == "" {
		return kind
	}
	return kind + "/" + ev.ErrClass
}

// taskFailureRecord accumulates consecutive no-progress kills for one task.
// Every persisted field is exported and tagged: the record round-trips through
// encoding/json into daemon-quarantine.json with no shadow struct. inFlight is
// deliberately unexported — it is a live-process guard, not durable state, and
// is force-cleared on load.
type taskFailureRecord struct {
	Count int         `json:"count"` // consecutive eligible no-progress kills since last reset/quarantine
	Kills []killEvent `json:"kills"` // capped timeline (last maxKillEventsRetained)

	// QuarantinedAt latches once the record is resolved: the daemon wrote
	// blocked, OR the read-back guard found the task already terminal/
	// blocked/deferred. Count is zeroed at latch time; the first fresh
	// eligible kill clears the latch (re-arm), so N fresh kills are needed
	// to re-quarantine after a human release.
	QuarantinedAt time.Time `json:"quarantined_at,omitzero"`
	DaemonWrote   bool      `json:"daemon_wrote,omitempty"` // true only when WE performed the blocked-write (only these surface in daemon status)
	WriteFailed   bool      `json:"write_failed,omitempty"` // informational: last write attempt failed (retry is driven by the sweep predicate, not this flag)
	inFlight      bool      // an agent's supervise loop is mid-write right now (guards concurrent sweeps); never persisted

	LastUpdated time.Time `json:"last_updated"` // touched on create/increment/latch/write-attempt — the eviction key

	// Field-delta progress baseline (covers plan-role agents, whose artifact
	// is a fleet-db design/notes write rather than a commit). Populated by
	// the first successful issue GET; comparisons apply ONLY when known —
	// "unknown" is never progress, and zero-value hashes are never compared.
	BaselineKnown      bool   `json:"baseline_known,omitempty"`
	BaselineDesignHash uint64 `json:"baseline_design_hash,omitempty"`
	BaselineNotesHash  uint64 `json:"baseline_notes_hash,omitempty"`

	LastKillReason  string `json:"last_kill_reason,omitempty"`
	QuarantineKills int    `json:"quarantine_kills,omitempty"` // Count captured at latch time (display-only; Count itself zeroes as the re-arm baseline)
}

// taskQuarantine is the daemon-wide ledger. One shared map per supervisor:
// kills of the same task from different agents accumulate on one record —
// the exact incident shape (a task boomeranging across siblings).
type taskQuarantine struct {
	mu  sync.Mutex
	rec map[string]*taskFailureRecord

	// persist writes the ledger to disk after a mutation. Wired once in qrec
	// (before the ledger is published), nil when persistence is disabled — an
	// embedded Supervisor with no ProjectDir, which is every unit test that
	// does not opt in. Always invoked with mu released.
	persist func()
}

// persistAfter invokes the save hook if one is wired. MUST be called with
// q.mu released: the hook re-takes the mutex to snapshot the ledger.
func (q *taskQuarantine) persistAfter() {
	if q.persist != nil {
		q.persist()
	}
}

// qrec lazily initializes the quarantine ledger. The Supervisor is built as a
// cross-package composite literal (daemon.go), so lazy init avoids touching
// every construction site.
func (s *Supervisor) qrec() *taskQuarantine {
	s.quarantineOnce.Do(func() {
		if s.quarantineStatePathCache == "" {
			s.quarantineStatePathCache = s.quarantineStatePath()
		}
		q := &taskQuarantine{rec: s.loadQuarantineState()}
		q.persist = func() { s.saveQuarantineState(q) }
		s.quarantine = q
	})
	return s.quarantine
}

// ---------------------------------------------------------------------------
// Persistence: daemon-quarantine.json
// ---------------------------------------------------------------------------

// quarantineStateFile is the on-disk envelope. Version gates forward
// compatibility: an unknown version starts from an empty ledger rather than
// guessing at a shape, so no migration code is ever needed.
type quarantineStateFile struct {
	Version int                           `json:"version"`
	Records map[string]*taskFailureRecord `json:"records"`
}

// quarantineStatePath resolves daemon-quarantine.json next to
// daemon-agents.json. An empty ProjectDir (embedded uses, unit tests) returns
// "", which disables persistence entirely — load and save then no-op.
func (s *Supervisor) quarantineStatePath() string {
	if s.ProjectDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(config.ResolveDaemonStatePath(s.ProjectDir)), quarantineStateFileName)
}

// loadQuarantineState hydrates the ledger from disk. It never fails: a
// missing, corrupt, truncated or future-versioned file yields an empty ledger,
// because a bad ledger must not stop the daemon from supervising agents.
//
// Two load-time-only transforms are applied; in-memory semantics are untouched:
// records not touched within quarantineRecordTTL are dropped, and inFlight is
// force-cleared (a process that died mid-write left it set, and a set flag
// would permanently exclude the record from takeDue).
func (s *Supervisor) loadQuarantineState() map[string]*taskFailureRecord {
	rec := make(map[string]*taskFailureRecord)
	path := s.quarantineStatePathCache
	if path == "" {
		return rec
	}
	data, err := os.ReadFile(path) //nolint:gosec // path derived from the daemon state directory
	if err != nil {
		if os.IsNotExist(err) {
			slog.Debug("no persisted task quarantine ledger, starting empty", "path", path)
		} else {
			slog.Warn("task quarantine ledger unreadable, starting empty", "path", path, "err", err)
		}
		return rec
	}
	var state quarantineStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Warn("task quarantine ledger corrupt, starting empty", "path", path, "err", err)
		return rec
	}
	if state.Version != quarantineStateVersion {
		slog.Warn("task quarantine ledger version unsupported, starting empty",
			"path", path, "version", state.Version, "supported", quarantineStateVersion)
		return rec
	}
	cutoff := time.Now().Add(-quarantineRecordTTL)
	for id, r := range state.Records {
		if id == "" || r == nil || r.LastUpdated.IsZero() || r.LastUpdated.Before(cutoff) {
			continue
		}
		r.inFlight = false
		rec[id] = r
	}
	if len(rec) > 0 {
		slog.Info("restored task quarantine ledger across daemon restart",
			"path", path, "records", len(rec))
	}
	return rec
}

// snapshot deep-copies the ledger under the mutex so the marshal and the file
// write run without holding it.
func (q *taskQuarantine) snapshot() quarantineStateFile {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := quarantineStateFile{
		Version: quarantineStateVersion,
		Records: make(map[string]*taskFailureRecord, len(q.rec)),
	}
	for id, rec := range q.rec {
		cp := *rec
		cp.Kills = append([]killEvent(nil), rec.Kills...)
		out.Records[id] = &cp
	}
	return out
}

// saveQuarantineState writes the ledger atomically (PID-tagged temp + rename,
// the pattern writeStateFile already uses). Best-effort throughout: an
// unwritable directory logs one warning and leaves the in-memory ledger
// working exactly as before. Synchronous by design — one small write per agent
// exit, and no spawned goroutines.
func (s *Supervisor) saveQuarantineState(q *taskQuarantine) {
	path := s.quarantineStatePathCache
	if path == "" {
		return
	}
	data, err := json.MarshalIndent(q.snapshot(), "", "  ")
	if err != nil {
		slog.Warn("task quarantine ledger marshal failed", "path", path, "err", err)
		return
	}
	// The daemon state directory may not exist yet on a first run.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		slog.Warn("task quarantine ledger directory unavailable", "path", path, "err", err)
		return
	}
	tempFile := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tempFile, data, 0o600); err != nil {
		slog.Warn("task quarantine ledger write failed", "path", tempFile, "err", err)
		return
	}
	if err := os.Rename(tempFile, path); err != nil {
		slog.Warn("task quarantine ledger rename failed", "path", path, "err", err)
		os.Remove(tempFile)
	}
}

// quarantineThreshold is the consecutive no-progress-kill count at which a
// task is quarantined. LOOM_TASK_QUARANTINE_THRESHOLD wins when set (mirrors
// GetOutputTimeout — fleet-db's wire schema does not persist such daemon
// config fields); <= 0 disables both quarantine hooks (operator kill-switch).
func (s *Supervisor) quarantineThreshold() int {
	if v := os.Getenv("LOOM_TASK_QUARANTINE_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultQuarantineThreshold
}

// recordTaskExitForQuarantine is the ledger hook. It runs in spawnAndWait
// immediately after classifyAgentExit: ap.LastError is set, the lock file is
// still present (recovery has not cleared it), and ap.AgentSessionID has not
// been cleared by finalize — the only point in the exit sequence where all
// three are observable together.
func (s *Supervisor) recordTaskExitForQuarantine(ap *AgentProcess, exitCode int) {
	if s.quarantineThreshold() <= 0 {
		return
	}
	lockInfo, _, _ := cli.CheckLock(ap.WorktreePath)
	taskID := s.taskIDForLifecycle(ap, lockInfo)
	if taskID == "" {
		return // no task attached (idle watchdog kills classify as NoWork anyway)
	}
	snap := snapshotTaskExit(ap, lockInfo, exitCode)
	q := s.qrec()
	// An incomplete run (exit 0, claim never released) is deliberately NOT
	// clean: classifyAgentExit gives it a LastError, so snap.clean is false and
	// the record survives. Evicting on that shape is what made this spiral
	// invisible — a task could alternate real kills with unfinished turns and
	// the ledger reset to zero on every one of them, so the threshold was never
	// reached. It still does not INCREMENT: IncompleteRun is a domain outcome
	// and QuarantineEligible rejects those, which is right — a turn that ran out
	// is a coordination signal, not a task-fault kill, and counting it would
	// quarantine tasks whose agents are progressing without committing.
	if snap.clean || commitProgressed(ap.WorktreePath, snap.beforeRef) {
		q.evict(taskID)
		return
	}
	// A kill that is not evidence about the TASK is recorded for diagnosis and
	// nothing more. This gate keeps the position the narrower lifecycle check it
	// replaced was given: AFTER the clean/commit-progress eviction (a drained
	// agent that DID commit still clears its record) and BEFORE the outcome
	// check, because a drain that escalates to SIGTERM classifies as Timeout,
	// which QuarantineEligible accepts — that is how a config-churn loop
	// manufactured quarantine credit against tasks that were never at fault.
	if countable, why := s.quarantineCountable(snap.event); !countable {
		snap.event.NotCounted = why
		q.recordUncountedKill(taskID, snap.event) // timeline only
		slog.Info("kill not charged to task quarantine", "task", taskID,
			"agent", ap.Entry.Worktree, "kill", snap.event.reason(), "why", why)
		return
	}
	if !agentpolicy.QuarantineEligible(snap.outcome) {
		return
	}
	designHash, notesHash, baselineKnown := s.fetchIssueBaseline(taskID)
	count, progressed := q.recordEligibleKill(taskID, snap.event, designHash, notesHash, baselineKnown)
	if progressed {
		slog.Info("task progressed between kills (design/notes delta), dropping quarantine record",
			"task", taskID, "agent", ap.Entry.Worktree)
		return
	}
	slog.Info("recorded no-progress kill for task",
		"task", taskID, "agent", ap.Entry.Worktree, "kill", snap.event.reason(),
		"count", count, "threshold", s.quarantineThreshold())
}

// stopReasonQuarantineEligible reports whether a kill carrying this stop
// reason counts as evidence that the TASK is stalling.
//
// Lifecycle stops do not: a drain (config change, shutdown, an operator's
// stop) is a decision the daemon made about the AGENT, and the run it
// interrupted carries no evidence in either direction. The gate is therefore a
// skip, not an evict — an accumulated count survives a drain and the next
// genuine kill continues from where it left off.
//
// The behavioral consequence is worth stating plainly: a task that only ever
// dies during drains is never quarantined. That is correct — such a task is
// being killed by the daemon, not by its own stall.
//
// Everything else stays eligible, notably watchdog (the signal the whole
// mechanism was built for), run_duration_exceeded, fatal_error, fast_fail and
// a bare crash (empty reason). backend_unavailable and rate_limited are
// already filtered out by the outcome class.
func stopReasonQuarantineEligible(r StopReason) bool {
	switch r {
	case StopReasonConfigRemoved, StopReasonShutdown, StopReasonManualStop,
		StopReasonYielded, StopReasonEphemeralDone:
		return false
	default:
		return true
	}
}

// lifecycleUncountedReason names the lifecycle stop for the ledger's
// NotCounted field. Kept in step with stopReasonQuarantineEligible: every
// reason that predicate rejects needs a name here.
func lifecycleUncountedReason(r StopReason) string {
	switch r {
	case StopReasonShutdown:
		// The daemon SIGTERMed its own agents mid-run; the task was a bystander.
		return "daemon_shutdown"
	case StopReasonManualStop:
		return "manual_stop"
	case StopReasonConfigRemoved:
		return "config_removed"
	case StopReasonYielded:
		return "yielded"
	case StopReasonEphemeralDone:
		return "ephemeral_done"
	default:
		return "lifecycle_stop"
	}
}

// quarantineCountable reports whether this kill says anything about the TASK.
// Kills that are verdicts about the daemon, the agent, or the account are
// recorded in the timeline for diagnosis but never advance the counter.
//
// The outcome-class seat (agentpolicy.QuarantineEligible) cannot make this
// call: it sees an agenterr.Outcome, which knows nothing of stop reasons or
// daemon lifecycle. A daemon that SIGTERMs its own agents mid-run produces a
// perfectly eligible outcome class, and during the 2026-08-26/27 incident three
// such kills were enough to quarantine a task that had never stalled.
//
// StopReasonWatchdog stays COUNTABLE and deliberately so — the watchdog fires
// because the agent went silent past its output timeout, which is the
// definition of a stall and the breaker's best signal. So does a bare crash
// (empty StopReason), the breaker's base case. Blinding the counter to either
// would leave nothing to count.
func (s *Supervisor) quarantineCountable(ev killEvent) (bool, string) {
	r := StopReason(ev.StopReason)
	// Lifecycle stops keep stopReasonQuarantineEligible as their single
	// authority: it also rejects yielded and ephemeral_done, which the switch
	// below never listed and which must not be charged to a task either.
	if !stopReasonQuarantineEligible(r) {
		return false, lifecycleUncountedReason(r)
	}
	switch r {
	case StopReasonBackendUnavailable:
		// The agent's backend CLI is missing from PATH. Nothing to do with the task.
		return false, "backend_unavailable"
	case StopReasonMaxRetries, StopReasonMaxRetriesBlocked, StopReasonFastFail:
		// Agent-level budgets already escalate agent-side (block, fast-fail).
		// Charging the task too double-counts one failure against two breakers.
		return false, "agent_budget"
	case StopReasonRunDurationExceeded:
		// See applyRunDurationKill: the cap fires regardless of activity, so on
		// its own it is not a no-progress signal. A run that was still talking
		// when the ceiling hit it was working, however slowly; only a run that
		// was ALSO silent is the wedge markRunDurationExceeded argues about.
		if !ev.RunSilent {
			return false, "duration_kill_while_active"
		}
	}
	// Collateral of a daemon restart lands in a burst right after boot and says
	// nothing about any task. Zero BootedAt disables the grace — see the
	// constant.
	if !s.BootedAt.IsZero() && ev.At.Sub(s.BootedAt) < quarantineBootGrace {
		return false, "boot_grace"
	}
	return true, ""
}

// recordUncountedKill files an infrastructure kill in an EXISTING record's
// timeline and nothing more. Every omission here is load-bearing:
//
//   - it never creates a record — an uncounted kill alone does not deserve a
//     ledger slot, and creating one would churn evictOldestLocked;
//   - it never increments Count;
//   - it never clears the latch. recordEligibleKill's re-arm branch exists so a
//     human-released task needs N FRESH kills before re-quarantining; an
//     infrastructure kill must not be allowed to consume that re-arm;
//   - it never evicts. Uncounted is inert, not exculpatory: the task has not
//     been shown to be making progress, only shown not to be at fault here.
func (q *taskQuarantine) recordUncountedKill(taskID string, ev killEvent) {
	q.mu.Lock()
	defer q.mu.Unlock()
	rec := q.rec[taskID]
	if rec == nil {
		return
	}
	rec.Kills = append(rec.Kills, ev)
	if len(rec.Kills) > maxKillEventsRetained {
		rec.Kills = rec.Kills[len(rec.Kills)-maxKillEventsRetained:]
	}
	rec.LastUpdated = time.Now()
}

// taskExitSnapshot is the per-exit state the ledger consumes, read under
// ap.Mu in one critical section.
type taskExitSnapshot struct {
	clean     bool
	outcome   agenterr.Outcome
	beforeRef string
	event     killEvent
}

func snapshotTaskExit(ap *AgentProcess, lockInfo *cli.LockInfo, exitCode int) taskExitSnapshot {
	ap.Mu.Lock()
	lastErr := ap.LastError
	stopReason := ap.StopReason
	beforeRef := ap.BeforeRef
	fleetSessionID := ap.AgentSessionID
	runSilent := ap.RunSilentAtStop
	ap.Mu.Unlock()

	snap := taskExitSnapshot{
		clean:     exitCode == 0 && lastErr == nil,
		beforeRef: beforeRef,
	}
	errClass := ""
	if lastErr != nil {
		snap.outcome = lastErr.Class
		errClass = lastErr.Class.String()
	}
	snap.event = killEvent{
		At:             time.Now(),
		Agent:          ap.Entry.Worktree,
		StopReason:     string(stopReason),
		ErrClass:       errClass,
		ExitCode:       exitCode,
		FleetSessionID: fleetSessionID,
		RunSilent:      runSilent,
	}
	if lockInfo != nil {
		snap.event.ClaudeSessionID = lockInfo.ClaudeSessionID
		snap.event.RunID = lockInfo.RunID
	}
	return snap
}

// commitProgressed reports whether the worktree HEAD moved past the ref
// captured at session creation. An unknown baseline (BeforeRef empty — it is
// set only after session creation succeeds) or an unreadable current HEAD is
// NOT progress: comparing HEAD against "" would fake progress on every
// session-creation-failure exit and suppress quarantine for that failure mode.
func commitProgressed(worktreePath, beforeRef string) bool {
	if beforeRef == "" {
		return false
	}
	head := automode.CaptureHEADRef(worktreePath)
	return head != "" && head != beforeRef
}

// fetchIssueBaseline GETs the issue once per eligible kill and returns
// FNV-1a hashes of its Design and Notes fields. ok=false (no backend, GET
// failed) means "unknown": the increment proceeds regardless, and the caller
// never compares against zero-value hashes.
func (s *Supervisor) fetchIssueBaseline(taskID string) (designHash, notesHash uint64, ok bool) {
	if s.IssueBackend == nil {
		return 0, 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), quarantineWriteTimeout)
	defer cancel()
	issue, err := s.IssueBackend.Get(ctx, taskID)
	if err != nil || issue == nil {
		return 0, 0, false
	}
	return hashIssueField(issue.Design), hashIssueField(issue.Notes), true
}

func hashIssueField(v string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(v))
	return h.Sum64()
}

// evict drops a task's failure record (clean exit or progress observed).
func (q *taskQuarantine) evict(taskID string) {
	q.mu.Lock()
	delete(q.rec, taskID)
	q.mu.Unlock()
	q.persistAfter()
}

// recordEligibleKill folds one quarantine-eligible kill into the ledger and
// returns the record's new count. Field-delta progress (a changed
// Design/Notes hash against a known baseline) evicts the record instead of
// incrementing — the task IS moving, just not via commits.
func (q *taskQuarantine) recordEligibleKill(taskID string, ev killEvent, designHash, notesHash uint64, baselineKnown bool) (count int, progressed bool) {
	// LIFO: the unlock runs first, so the save sees a consistent ledger and
	// never re-enters the mutex while it is held.
	defer q.persistAfter()
	q.mu.Lock()
	defer q.mu.Unlock()

	rec := q.rec[taskID]
	if rec != nil && rec.BaselineKnown && baselineKnown &&
		(designHash != rec.BaselineDesignHash || notesHash != rec.BaselineNotesHash) {
		delete(q.rec, taskID)
		return 0, true
	}
	if rec == nil {
		q.evictOldestLocked()
		rec = &taskFailureRecord{}
		q.rec[taskID] = rec
	}
	if baselineKnown && !rec.BaselineKnown {
		// First successful GET establishes the baseline; never inferred as
		// progress (zero-value hashes are never compared).
		rec.BaselineKnown = true
		rec.BaselineDesignHash = designHash
		rec.BaselineNotesHash = notesHash
	}
	if !rec.QuarantinedAt.IsZero() {
		// Latched record seeing a fresh kill: the task was released (human
		// or undefer) and stalled again. Re-arm — N fresh kills are required
		// before it re-quarantines.
		rec.QuarantinedAt = time.Time{}
		rec.DaemonWrote = false
		rec.WriteFailed = false
		rec.Count = 0
	}
	rec.Count++
	rec.Kills = append(rec.Kills, ev)
	if len(rec.Kills) > maxKillEventsRetained {
		rec.Kills = rec.Kills[len(rec.Kills)-maxKillEventsRetained:]
	}
	rec.LastKillReason = ev.reason()
	rec.LastUpdated = time.Now()
	return rec.Count, false
}

// ---------------------------------------------------------------------------
// Sweep: the quarantine write
// ---------------------------------------------------------------------------

// sweepQuarantineDue scans the ledger and quarantines every record meeting
// the sweep predicate: Count >= threshold && QuarantinedAt.IsZero() &&
// !inFlight. This predicate is the ONLY trigger — a failed write leaves
// Count >= threshold with a zero latch, so it re-qualifies naturally.
//
// Scanning (rather than acting only on this agent's task) is deliberate:
// worker-self-picked tasks have an empty AssignedTaskID and a cleared lock by
// this point in the exit sequence, and write-failure retries heal on ANY
// agent's next cycle, not just the same task's next kill. Runs after
// postMortemRecovery reset the task to open, so the write transitions
// open→blocked.
func (s *Supervisor) sweepQuarantineDue(ap *AgentProcess) {
	threshold := s.quarantineThreshold()
	if threshold <= 0 || s.IssueBackend == nil {
		return
	}
	for _, due := range s.qrec().takeDue(threshold) {
		s.quarantineTask(ap, due)
	}
}

// dueTask is the snapshot of a record meeting the sweep predicate, taken
// under the ledger mutex so the network calls run without holding it.
type dueTask struct {
	taskID             string
	count              int
	kills              []killEvent
	baselineKnown      bool
	baselineDesignHash uint64
	baselineNotesHash  uint64
}

// takeDue collects every record meeting the sweep predicate and marks it
// inFlight so a concurrently-exiting agent's sweep cannot double-write. The
// caller MUST resolve each returned task (latch / release / evict).
func (q *taskQuarantine) takeDue(threshold int) []dueTask {
	defer q.persistAfter()
	q.mu.Lock()
	defer q.mu.Unlock()
	var due []dueTask
	for id, rec := range q.rec {
		if rec.Count < threshold || !rec.QuarantinedAt.IsZero() || rec.inFlight {
			continue
		}
		rec.inFlight = true
		kills := make([]killEvent, len(rec.Kills))
		copy(kills, rec.Kills)
		due = append(due, dueTask{
			taskID:             id,
			count:              rec.Count,
			kills:              kills,
			baselineKnown:      rec.BaselineKnown,
			baselineDesignHash: rec.BaselineDesignHash,
			baselineNotesHash:  rec.BaselineNotesHash,
		})
	}
	return due
}

// quarantineVerdict is the read-back guard's decision for one due task.
type quarantineVerdict int

const (
	quarantineProceed         quarantineVerdict = iota
	quarantineLatchResolved                     // already terminal/blocked/deferred: latch without writing
	quarantineStayDue                           // actively in_progress: never block mid-run; stay due
	quarantineRetryFailed                       // GET failed: stay due, flag the failed attempt
	quarantineEvictProgressed                   // open but the field baseline moved: release from the spiral
)

// quarantineTask performs the read-back guard plus the load-bearing blocked
// write for one due task. All calls are synchronous within the exiting
// agent's supervise loop, bounded by quarantineWriteTimeout — no spawned
// goroutines (keeps daemon shutdown, test determinism, and state-file
// visibility simple). Never fatal; never blocks the supervise loop beyond
// the timeout.
func (s *Supervisor) quarantineTask(ap *AgentProcess, due dueTask) {
	ctx, cancel := context.WithTimeout(context.Background(), quarantineWriteTimeout)
	defer cancel()
	q := s.qrec()

	switch s.checkQuarantineTarget(ctx, due) {
	case quarantineProceed:
		s.writeQuarantine(ctx, ap, due)
	case quarantineLatchResolved:
		q.latch(due.taskID, false)
	case quarantineEvictProgressed:
		slog.Info("task progressed since its kill spiral was recorded, releasing instead of quarantining",
			"task", due.taskID)
		q.evict(due.taskID)
	case quarantineStayDue:
		q.release(due.taskID)
	case quarantineRetryFailed:
		q.markWriteFailed(due.taskID)
	}
}

// checkQuarantineTarget is the read-back guard + stale-retry revalidation:
// between the kills and this sweep (or between a failed write and its retry)
// the task may have been re-picked, completed, or human-handled.
func (s *Supervisor) checkQuarantineTarget(ctx context.Context, due dueTask) quarantineVerdict {
	issue, err := s.IssueBackend.Get(ctx, due.taskID)
	if err != nil || issue == nil {
		return quarantineRetryFailed
	}
	switch issue.Status {
	case "open":
		if due.baselineKnown &&
			(hashIssueField(issue.Design) != due.baselineDesignHash ||
				hashIssueField(issue.Notes) != due.baselineNotesHash) {
			// Progressed since the spiral was recorded (a stale retry after
			// a failed write): release it instead of blocking. Commit
			// progress cannot be stale here — every run's exit passes the
			// record hook before any sweep in that cycle, and an in-flight
			// run is caught by the in_progress skip below.
			return quarantineEvictProgressed
		}
		return quarantineProceed
	case "in_progress":
		// Actively being worked (stale retry after the task was re-picked):
		// never block a task mid-run, and don't latch — the deciding
		// evidence arrives at that run's exit, whose record hook evicts or
		// increments before the next sweep acts.
		return quarantineStayDue
	default:
		// closed/tombstone: done. review: completed work awaiting approval.
		// blocked: already quarantined or human-blocked. deferred: a human
		// or scheduler deferred it — defer to that decision. Latch without
		// writing: no label, no comment, excluded from daemon status.
		return quarantineLatchResolved
	}
}

// writeQuarantine is the one load-bearing write: a single Update the fleet
// client decomposes in a verified-safe order (labels → release claim lock as
// current assignee → PATCH status=blocked → assign ""). The kill-timeline
// comment is best-effort after the status write lands.
func (s *Supervisor) writeQuarantine(ctx context.Context, ap *AgentProcess, due dueTask) {
	q := s.qrec()
	blocked := "blocked"
	unassigned := ""
	err := s.IssueBackend.Update(ctx, due.taskID, backend.UpdateParams{
		Status:    &blocked,
		Assignee:  &unassigned,
		AddLabels: []string{quarantineLabel},
	})
	if err != nil {
		slog.Warn("task quarantine write failed, will retry on a later sweep",
			"task", due.taskID, "err", err)
		q.markWriteFailed(due.taskID)
		return
	}
	// Message text is load-bearing: TestScenarioTaskQuarantine greps the
	// daemon log for "quarantined after repeated no-progress kills".
	slog.Info("task quarantined after repeated no-progress kills",
		"task", due.taskID, "kills", due.count, "threshold", s.quarantineThreshold(),
		"status", "blocked", "label", quarantineLabel)
	s.postQuarantineComment(ctx, ap, due)
	q.latch(due.taskID, true)
}

// postQuarantineComment posts the kill timeline. Best-effort: the status
// write already landed, so a comment failure logs and does NOT unlatch.
// fleet-db drops the Author param on the wire; attribution lives in the text.
func (s *Supervisor) postQuarantineComment(ctx context.Context, ap *AgentProcess, due dueTask) {
	text := formatKillTimeline(due.taskID, s.quarantineThreshold(), due.count, due.kills)
	if _, err := s.IssueBackend.AddComment(ctx, backend.CommentAddParams{
		IssueID: due.taskID,
		Author:  ap.Entry.Worktree,
		Text:    text,
	}); err != nil {
		slog.Warn("quarantine kill-timeline comment failed (status write already landed)",
			"task", due.taskID, "err", err)
	}
}

// formatKillTimeline renders the quarantine comment: an ASCII-only markdown
// kill table plus release instructions. Daemon-generated operational text —
// no emoji or non-ASCII; session ids truncate to short prefixes; an empty
// StopReason renders as "crash".
func formatKillTimeline(taskID string, threshold, count int, kills []killEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Task quarantined by loom daemon** -- %d consecutive no-progress kills.\n\n", count)
	fmt.Fprintf(&b, "Claimed and killed %dx with no commit or design/notes progress between attempts\n", count)
	b.WriteString("(backend stall -> watchdog/ownership kill -> reset -> re-pick -> identical freeze).\n")
	b.WriteString("Set to **blocked** and unassigned to stop the boomerang.\n\n")
	b.WriteString("| # | time (UTC) | agent | kill | class | exit | fleet session | claude session | note |\n")
	b.WriteString("|---|-----------|-------|------|-------|------|---------------|----------------|------|\n")
	for i, ev := range kills {
		kind := ev.StopReason
		if kind == "" {
			kind = "crash"
		}
		class := ev.ErrClass
		if class == "" {
			class = "-"
		}
		// The note column is why a reader can trust the count: kills the
		// ledger discounted are still listed, marked with the reason they
		// were not charged to the task.
		note := ev.NotCounted
		if note == "" {
			note = "-"
		} else {
			note = "not counted: " + note
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s | %s | %d | %s | %s | %s |\n",
			i+1, ev.At.UTC().Format(time.RFC3339), ev.Agent, kind, class, ev.ExitCode,
			shortSessionID(ev.FleetSessionID), shortSessionID(ev.ClaudeSessionID), note)
	}
	fmt.Fprintf(&b, "\nTo release: investigate the stall, then `loom data update %s --status open`\n", taskID)
	fmt.Fprintf(&b, "(the %s label stays as an audit marker; clear it via the fleet-db API\n", quarantineLabel)
	fmt.Fprintf(&b, "`DELETE /issues/%s/labels/%s` if desired). Manual `loom claim %s` also\n", taskID, quarantineLabel, taskID)
	fmt.Fprintf(&b, "works (blocked is claimable) -- it will re-quarantine after %d fresh no-progress kills.\n", threshold)
	return b.String()
}

// shortSessionID truncates a session id to an 8-char prefix for table
// readability; empty ids render as "-".
func shortSessionID(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// release clears inFlight leaving the record due (skipped this round): it
// re-qualifies on any agent's next sweep.
func (q *taskQuarantine) release(taskID string) {
	q.mu.Lock()
	if rec := q.rec[taskID]; rec != nil {
		rec.inFlight = false
		rec.LastUpdated = time.Now()
	}
	q.mu.Unlock()
	q.persistAfter()
}

// markWriteFailed clears inFlight and flags the failed attempt. Informational
// only — the retry is driven by the sweep predicate (Count still >= threshold,
// latch still zero), not by this flag.
func (q *taskQuarantine) markWriteFailed(taskID string) {
	q.mu.Lock()
	if rec := q.rec[taskID]; rec != nil {
		rec.inFlight = false
		rec.WriteFailed = true
		rec.LastUpdated = time.Now()
	}
	q.mu.Unlock()
	q.persistAfter()
}

// latch marks a record resolved: Count zeroed (the re-arm baseline) and
// QuarantinedAt stamped — the latch can never satisfy the sweep predicate by
// itself. daemonWrote records whether WE performed the blocked-write (only
// those surface in daemon status; a guard-latched human-blocked/deferred/
// closed task is tracked internally but never presented as quarantined).
func (q *taskQuarantine) latch(taskID string, daemonWrote bool) {
	q.mu.Lock()
	if rec := q.rec[taskID]; rec != nil {
		rec.inFlight = false
		rec.QuarantineKills = rec.Count
		rec.Count = 0
		rec.QuarantinedAt = time.Now()
		rec.DaemonWrote = daemonWrote
		rec.WriteFailed = false
		rec.LastUpdated = time.Now()
	}
	q.mu.Unlock()
	q.persistAfter()
}

// evictOldestLocked makes room when the ledger is at capacity by dropping the
// non-inFlight record with the oldest LastUpdated. Hot spirals (touched on
// every kill) are never evicted in favor of stale residue. Caller holds q.mu.
func (q *taskQuarantine) evictOldestLocked() {
	if len(q.rec) < maxTrackedQuarantineTasks {
		return
	}
	var oldestID string
	var oldestAt time.Time
	for id, r := range q.rec {
		if r.inFlight {
			continue
		}
		if oldestID == "" || r.LastUpdated.Before(oldestAt) {
			oldestID, oldestAt = id, r.LastUpdated
		}
	}
	if oldestID != "" {
		delete(q.rec, oldestID)
	}
}

// ---------------------------------------------------------------------------
// Daemon-status surfacing
// ---------------------------------------------------------------------------

// QuarantinedTaskInfo is the JSON-serializable snapshot of one quarantined
// (or quarantine-pending) task, surfaced in daemon-agents.json and
// `loom daemon status` — mirroring how agent blocks surface daemon-status-only.
type QuarantinedTaskInfo struct {
	TaskID string `json:"task_id"`
	// Count is the number of no-progress kills behind the quarantine: the
	// count captured when the write landed, or the live count for a
	// pending (write-failed, retrying) record.
	Count          int       `json:"count"`
	QuarantinedAt  time.Time `json:"quarantined_at,omitzero"`
	LastKillReason string    `json:"last_kill_reason,omitempty"`
	WriteFailed    bool      `json:"write_failed,omitempty"`
}

// QuarantinedTasks returns the daemon-status snapshot: tasks the daemon
// actually quarantined (DaemonWrote) plus due tasks whose blocked-write is
// failing and retrying (WriteFailed). Guard-latched records — tasks the
// read-back found already human-blocked/deferred/closed — are tracked
// internally but never surfaced as quarantined; the loom:quarantined label
// is the on-issue discriminator.
func (s *Supervisor) QuarantinedTasks() []QuarantinedTaskInfo {
	q := s.qrec()
	q.mu.Lock()
	defer q.mu.Unlock()
	out := []QuarantinedTaskInfo{}
	for id, rec := range q.rec {
		switch {
		case rec.DaemonWrote:
			out = append(out, QuarantinedTaskInfo{
				TaskID:         id,
				Count:          rec.QuarantineKills,
				QuarantinedAt:  rec.QuarantinedAt,
				LastKillReason: rec.LastKillReason,
			})
		case rec.WriteFailed && rec.QuarantinedAt.IsZero():
			out = append(out, QuarantinedTaskInfo{
				TaskID:         id,
				Count:          rec.Count,
				LastKillReason: rec.LastKillReason,
				WriteFailed:    true,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}
