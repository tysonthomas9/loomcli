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
	// defaultDeadlineQuarantineThreshold is the SEPARATE, higher threshold for
	// the run-turn-deadline bucket. A deadline expiry is loom's own designed
	// clean stop rather than a crash, so it must not be 1-of-3 toward parking a
	// ticket -- but a task that overruns its budget every single time still
	// boomerangs forever, so it is counted, just further out. Override with
	// LOOM_TASK_DEADLINE_QUARANTINE_THRESHOLD.
	defaultDeadlineQuarantineThreshold = 6
	quarantineLabel                    = "loom:quarantined"
	quarantineWriteTimeout             = 10 * time.Second
	maxTrackedQuarantineTasks          = 512 // defensive cap on ledger size; oldest evicted
	maxKillEventsRetained              = 10  // kill-timeline cap per task

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

// killKind renders the kill's shape: the recorded StopReason when there is
// one, else a fallback derived from the class.
//
// "crash" is the historical fallback and is wrong for exactly one class: a
// run-turn deadline expiry is loom's OWN clean stop, so rendering it
// "crash/RunTurnDeadline" reproduces one level down the same misnomer this
// whole path exists to remove. It renders "expiry" instead.
func (ev killEvent) killKind() string {
	if ev.StopReason != "" {
		return ev.StopReason
	}
	if ev.ErrClass == agenterr.RunTurnDeadlineOutcome.String() {
		return "expiry"
	}
	return "crash"
}

// reason renders a compact kill descriptor for status output, e.g.
// "watchdog/Timeout", "crash/Unknown" or "expiry/RunTurnDeadline".
func (ev killEvent) reason() string {
	kind := ev.killKind()
	if ev.ErrClass == "" {
		return kind
	}
	return kind + "/" + ev.ErrClass
}

// taskFailureRecord accumulates consecutive eligible kills for one task, in
// two independent buckets (see agentpolicy.QuarantineBucket). Either counter
// reaching ITS OWN threshold quarantines the task; progress and the latch zero
// both. Every persisted field is exported and tagged: the record round-trips
// through encoding/json into daemon-quarantine.json with no shadow struct.
// inFlight is deliberately unexported — it is a live-process guard, not
// durable state, and is force-cleared on load.
type taskFailureRecord struct {
	Count         int         `json:"count"`                    // consecutive no-progress kills (watchdog/ownership) since last reset/quarantine
	DeadlineCount int         `json:"deadline_count,omitempty"` // consecutive run-turn-deadline expiries; separate, higher threshold
	Kills         []killEvent `json:"kills"`                    // capped timeline (last maxKillEventsRetained), both buckets interleaved

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

	// Field-delta progress baseline (covers agents whose artifact is not a
	// worktree commit: a plan role writes design/notes, and the critic /
	// integrator / tester / decomposer roles write a COMMENT, a LABEL or a
	// PR). Populated by the first successful issue GET; comparisons apply
	// ONLY when known — "unknown" is never progress, and zero-value
	// baselines are never compared.
	//
	// Status and UpdatedAt are deliberately NOT tracked: the daemon itself
	// drives open -> in_progress -> open on every pick and recovery, and any
	// write bumps UpdatedAt, so either would report progress on every cycle
	// and disable the breaker outright.
	BaselineKnown      bool   `json:"baseline_known,omitempty"`
	BaselineDesignHash uint64 `json:"baseline_design_hash,omitempty"`
	BaselineNotesHash  uint64 `json:"baseline_notes_hash,omitempty"`
	// BaselineMaxCommentID is monotone (ids are assigned increasing), so it
	// is compared with > and never !=: a DELETED comment lowers the max and
	// must not read as progress.
	BaselineMaxCommentID int64  `json:"baseline_max_comment_id,omitempty"`
	BaselineLabelsHash   uint64 `json:"baseline_labels_hash,omitempty"` // FNV-1a over the sorted label set

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

// deadlineQuarantineThreshold is the consecutive run-turn-deadline-expiry
// count at which a task is quarantined. LOOM_TASK_DEADLINE_QUARANTINE_THRESHOLD
// wins when set; <= 0 disables THAT BUCKET ONLY, leaving no-progress kills
// counting as before. The whole-feature kill-switch stays
// LOOM_TASK_QUARANTINE_THRESHOLD <= 0, which disables both.
func (s *Supervisor) deadlineQuarantineThreshold() int {
	if v := os.Getenv("LOOM_TASK_DEADLINE_QUARANTINE_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultDeadlineQuarantineThreshold
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
	lockInfo, _, _ := cli.CheckLock(ap.WorkDir())
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
	if snap.clean || commitProgressed(ap.WorkDir(), snap.beforeRef) {
		q.evict(taskID)
		return
	}
	// The outcome class picks the bucket; quarantineBucketForKill routes an
	// active run killed at its time cap to the deadline bucket too, so one
	// policy (PUPPET-611) governs every turn-deadline kill.
	bucket := quarantineBucketForKill(snap.event, agentpolicy.QuarantineBucketFor(snap.outcome))
	if bucket == agentpolicy.QuarantineNone {
		return
	}
	if countable, why := s.quarantineCountable(snap.event); !countable {
		snap.event.NotCounted = why
		q.recordUncountedKill(taskID, snap.event) // timeline only
		slog.Info("kill not charged to task quarantine", "task", taskID,
			"agent", ap.Entry.Worktree, "kill", snap.event.reason(), "why", why)
		return
	}
	threshold := s.quarantineThreshold()
	if bucket == agentpolicy.QuarantineDeadline {
		// Its own threshold, and its own kill-switch: a non-positive value
		// disables this bucket while leaving no-progress counting intact.
		threshold = s.deadlineQuarantineThreshold()
		if threshold <= 0 {
			return
		}
	}
	base, baselineKnown := s.fetchIssueBaseline(taskID)
	count, progressed := q.recordEligibleKill(taskID, bucket, snap.event, base, baselineKnown)
	if progressed {
		slog.Info("task progressed between kills (design/notes/comment/label delta), dropping quarantine record",
			"task", taskID, "agent", ap.Entry.Worktree)
		return
	}
	// "bucket" is what keeps this line honest: a deadline expiry is not a
	// no-progress kill, and the threshold it is measured against is not the
	// no-progress one either.
	slog.Info("recorded eligible kill for task",
		"task", taskID, "agent", ap.Entry.Worktree, "kill", snap.event.reason(),
		"bucket", quarantineBucketName(bucket), "count", count, "threshold", threshold)
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
	switch StopReason(ev.StopReason) {
	case StopReasonShutdown:
		// The daemon SIGTERMed its own agents mid-run; the task was a bystander.
		return false, "daemon_shutdown"
	case StopReasonManualStop:
		return false, "manual_stop"
	case StopReasonConfigRemoved:
		return false, "config_removed"
	case StopReasonYielded, StopReasonEphemeralDone:
		// Lifecycle stops the daemon decided about the AGENT (folded in from
		// PUPPET-108's stopReasonQuarantineEligible when stacking with PUPPET-198).
		return false, ev.StopReason
	case StopReasonBackendUnavailable:
		// The agent's backend CLI is missing from PATH. Nothing to do with the task.
		return false, "backend_unavailable"
	case StopReasonMaxRetries, StopReasonMaxRetriesBlocked, StopReasonFastFail:
		// Agent-level budgets already escalate agent-side (block, fast-fail).
		// Charging the task too double-counts one failure against two breakers.
		return false, "agent_budget"
	case StopReasonRunDurationExceeded:
		// Counted, but not as a no-progress kill unless the run was ALSO
		// silent: see quarantineBucketForKill. PUPPET-198 exempted an active
		// cap-kill outright; PUPPET-611's deadline bucket is now the single
		// policy for a run that hit its time budget while still working.
	}
	// Collateral of a daemon restart lands in a burst right after boot and says
	// nothing about any task. Zero BootedAt disables the grace — see the
	// constant.
	if !s.BootedAt.IsZero() && ev.At.Sub(s.BootedAt) < quarantineBootGrace {
		return false, "boot_grace"
	}
	return true, ""
}

// quarantineBucketForKill picks the counter a kill advances. The outcome class
// decides (agentpolicy.QuarantineBucketFor) with one exception: a run the
// duration cap stopped while it was still producing output was working, just
// past its time budget. That is the same event as a RunTurnDeadline expiry
// the child reports itself, so it advances the same, higher deadline bucket
// instead of being exempted (PUPPET-198) or charged as a no-progress kill. A
// run that was ALSO silent at the cap stays no-progress: that is the wedge
// markRunDurationExceeded argues about.
func quarantineBucketForKill(ev killEvent, b agentpolicy.QuarantineBucket) agentpolicy.QuarantineBucket {
	if b != agentpolicy.QuarantineNone && StopReason(ev.StopReason) == StopReasonRunDurationExceeded && !ev.RunSilent {
		return agentpolicy.QuarantineDeadline
	}
	return b
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
	// Persist like every other ledger mutation (LIFO: runs after the unlock).
	defer q.persistAfter()
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

// issueBaseline is the field-delta progress fingerprint of one issue, read
// off a single Get response — every component comes from data the GET already
// returned, so widening it costs no extra network call.
type issueBaseline struct {
	designHash   uint64
	notesHash    uint64
	maxCommentID int64
	labelsHash   uint64
}

// progressedFrom reports whether this (freshly read) baseline shows movement
// past the recorded one. Hashes compare by inequality; the comment id compares
// by > because it is monotone and a deletion must not read as progress.
func (b issueBaseline) progressedFrom(prev issueBaseline) bool {
	return b.designHash != prev.designHash ||
		b.notesHash != prev.notesHash ||
		b.maxCommentID > prev.maxCommentID ||
		b.labelsHash != prev.labelsHash
}

// issueBaselineOf fingerprints a Get response. Comments and Labels ride on the
// same IssueDetailData the design/notes hashes already came from.
func issueBaselineOf(issue *backend.IssueDetailData) issueBaseline {
	return issueBaseline{
		designHash:   hashIssueField(issue.Design),
		notesHash:    hashIssueField(issue.Notes),
		maxCommentID: maxCommentID(issue.Comments),
		labelsHash:   hashLabelSet(issue.Labels),
	}
}

// fetchIssueBaseline GETs the issue once per eligible kill and fingerprints
// it. ok=false (no backend, GET failed) means "unknown": the increment
// proceeds regardless, and the caller never compares against a zero baseline.
func (s *Supervisor) fetchIssueBaseline(taskID string) (base issueBaseline, ok bool) {
	if s.IssueBackend == nil {
		return issueBaseline{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), quarantineWriteTimeout)
	defer cancel()
	issue, err := s.IssueBackend.Get(ctx, taskID)
	if err != nil || issue == nil {
		return issueBaseline{}, false
	}
	return issueBaselineOf(issue), true
}

func hashIssueField(v string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(v))
	return h.Sum64()
}

// maxCommentID is the highest comment id on the issue (0 when there are
// none). fleet-db assigns comment ids monotonically, so this is edit-proof:
// editing a comment leaves the max alone, and only a NEW comment raises it.
func maxCommentID(comments []backend.CommentData) int64 {
	var highest int64
	for _, c := range comments {
		if c.ID > highest {
			highest = c.ID
		}
	}
	return highest
}

// hashLabelSet is FNV-1a over the sorted, NUL-delimited label set — order
// independent (label order is not meaningful) and unambiguous across
// concatenations.
func hashLabelSet(labels []string) uint64 {
	sorted := make([]string, len(labels))
	copy(sorted, labels)
	sort.Strings(sorted)
	h := fnv.New64a()
	for _, l := range sorted {
		_, _ = h.Write([]byte(l))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

// evict drops a task's failure record (clean exit or progress observed).
func (q *taskQuarantine) evict(taskID string) {
	q.mu.Lock()
	delete(q.rec, taskID)
	q.mu.Unlock()
	q.persistAfter()
}

// quarantineBucketName renders a bucket for logs.
func quarantineBucketName(b agentpolicy.QuarantineBucket) string {
	if b == agentpolicy.QuarantineDeadline {
		return "deadline"
	}
	return "no-progress"
}

// recordEligibleKill folds one quarantine-eligible kill into the ledger and
// returns the new count OF THE BUCKET IT ADVANCED. Field-delta progress
// against a known baseline — a changed Design/Notes hash, a NEW comment, or a
// changed label set — evicts the record instead of incrementing: the task IS
// moving, just not via commits. The comment and label arms are what make the
// review roles visible; a critic that posted its verdict and was then reaped
// used to register as a no-progress kill on the task it had just advanced.
func (q *taskQuarantine) recordEligibleKill(taskID string, bucket agentpolicy.QuarantineBucket, ev killEvent, base issueBaseline, baselineKnown bool) (count int, progressed bool) {
	// LIFO: the unlock runs first, so the save sees a consistent ledger and
	// never re-enters the mutex while it is held.
	defer q.persistAfter()
	q.mu.Lock()
	defer q.mu.Unlock()

	rec := q.rec[taskID]
	if rec != nil && rec.BaselineKnown && baselineKnown && base.progressedFrom(rec.baseline()) {
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
		// progress (zero-value baselines are never compared).
		rec.setBaseline(base)
	}
	if !rec.QuarantinedAt.IsZero() {
		// Latched record seeing a fresh kill: the task was released (human
		// or undefer) and stalled again. Re-arm — N fresh kills are required
		// before it re-quarantines.
		rec.QuarantinedAt = time.Time{}
		rec.DaemonWrote = false
		rec.WriteFailed = false
		rec.Count = 0
		rec.DeadlineCount = 0
	}
	if bucket == agentpolicy.QuarantineDeadline {
		rec.DeadlineCount++
		count = rec.DeadlineCount
	} else {
		rec.Count++
		count = rec.Count
	}
	rec.Kills = append(rec.Kills, ev)
	if len(rec.Kills) > maxKillEventsRetained {
		rec.Kills = rec.Kills[len(rec.Kills)-maxKillEventsRetained:]
	}
	rec.LastKillReason = ev.reason()
	rec.LastUpdated = time.Now()
	return count, false
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
		if rec.Count == 0 {
			// The deadline bucket is what topped out; report ITS count.
			rec.QuarantineKills = rec.DeadlineCount
		}
		rec.Count = 0
		rec.DeadlineCount = 0
		rec.QuarantinedAt = time.Now()
		rec.DaemonWrote = daemonWrote
		rec.WriteFailed = false
		rec.LastUpdated = time.Now()
		// Clear the baseline, do not refresh it. writeQuarantine posts the
		// kill-timeline comment (and adds the quarantine label) right before
		// this latch, so a FROZEN baseline would let the daemon read its own
		// write as task progress on the next kill. Clearing is strictly safer
		// than refreshing: unknown is never progress, so the next kill simply
		// re-establishes it.
		rec.clearBaseline()
	}
	q.mu.Unlock()
	q.persistAfter()
}

// baseline reads the record's four baseline components back out. Caller holds
// q.mu.
func (rec *taskFailureRecord) baseline() issueBaseline {
	return issueBaseline{
		designHash:   rec.BaselineDesignHash,
		notesHash:    rec.BaselineNotesHash,
		maxCommentID: rec.BaselineMaxCommentID,
		labelsHash:   rec.BaselineLabelsHash,
	}
}

// setBaseline anchors all four components together — they are only ever
// known or unknown as a set. Caller holds q.mu.
func (rec *taskFailureRecord) setBaseline(b issueBaseline) {
	rec.BaselineKnown = true
	rec.BaselineDesignHash = b.designHash
	rec.BaselineNotesHash = b.notesHash
	rec.BaselineMaxCommentID = b.maxCommentID
	rec.BaselineLabelsHash = b.labelsHash
}

// clearBaseline returns the record to "unknown", so the next successful GET
// re-establishes it. Caller holds q.mu.
func (rec *taskFailureRecord) clearBaseline() {
	rec.BaselineKnown = false
	rec.BaselineDesignHash = 0
	rec.BaselineNotesHash = 0
	rec.BaselineMaxCommentID = 0
	rec.BaselineLabelsHash = 0
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
