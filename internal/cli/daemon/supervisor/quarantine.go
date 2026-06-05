package supervisor

import (
	"context"
	"hash/fnv"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/automode"
)

// Task quarantine: a supervisor-scoped, task-ID-keyed ledger of repeated
// no-progress kills. When a backend silently stalls, the watchdog (or the
// ownership/lease path) kills the session, recovery resets the task to open,
// and the picker re-selects the same task — an infinite boomerang that
// agent-level machinery (restart budgets, parks) cannot break, because the
// task returns to open every cycle and a sibling re-picks it. The ledger
// counts consecutive quarantine-eligible kills per task ACROSS agents; once a
// task accumulates quarantineThreshold() of them with no progress in between,
// the sweep sets it to blocked (status + label + kill-timeline comment).
//
// Eligibility is declared in the policy seat (agentpolicy.QuarantineEligible);
// this file owns the state — mirroring how ParkBudget is declared in the
// policy table but counted agent-side by the supervisor.

const (
	defaultQuarantineThreshold = 3
	quarantineLabel            = "loom:quarantined"
	quarantineWriteTimeout     = 10 * time.Second
	maxTrackedQuarantineTasks  = 512 // defensive cap on ledger size; oldest evicted
	maxKillEventsRetained      = 10  // kill-timeline cap per task
)

// killEvent is one observed kill of a task-holding agent, captured at exit
// time (after classifyAgentExit, before finalize/recovery clear the session
// and lock state it reads).
type killEvent struct {
	At              time.Time
	Agent           string // ap.Entry.Worktree
	StopReason      string // e.g. "watchdog"; empty for a bare crash / ownership kill
	ErrClass        string // classified outcome (Unknown | Timeout | Transient | ContextOverflow)
	ExitCode        int
	FleetSessionID  string // ap.AgentSessionID — captured before finalize clears it
	ClaudeSessionID string // lock ClaudeSessionID (best-effort; empty if absent)
	RunID           string // lock RunID (best-effort)
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
type taskFailureRecord struct {
	Count int         // consecutive eligible no-progress kills since last reset/quarantine
	Kills []killEvent // capped timeline (last maxKillEventsRetained)

	// QuarantinedAt latches once the record is resolved: the daemon wrote
	// blocked, OR the read-back guard found the task already terminal/
	// blocked/deferred. Count is zeroed at latch time; the first fresh
	// eligible kill clears the latch (re-arm), so N fresh kills are needed
	// to re-quarantine after a human release.
	QuarantinedAt time.Time
	DaemonWrote   bool // true only when WE performed the blocked-write (only these surface in daemon status)
	WriteFailed   bool // informational: last write attempt failed (retry is driven by the sweep predicate, not this flag)
	inFlight      bool // an agent's supervise loop is mid-write right now (guards concurrent sweeps)

	LastUpdated time.Time // touched on create/increment/latch/write-attempt — the eviction key

	// Field-delta progress baseline (covers plan-role agents, whose artifact
	// is a fleet-db design/notes write rather than a commit). Populated by
	// the first successful issue GET; comparisons apply ONLY when known —
	// "unknown" is never progress, and zero-value hashes are never compared.
	BaselineKnown      bool
	BaselineDesignHash uint64
	BaselineNotesHash  uint64

	LastKillReason string
}

// taskQuarantine is the daemon-wide ledger. One shared map per supervisor:
// kills of the same task from different agents accumulate on one record —
// the exact incident shape (a task boomeranging across siblings).
type taskQuarantine struct {
	mu  sync.Mutex
	rec map[string]*taskFailureRecord
}

// qrec lazily initializes the quarantine ledger. The Supervisor is built as a
// cross-package composite literal (daemon.go), so lazy init avoids touching
// every construction site.
func (s *Supervisor) qrec() *taskQuarantine {
	s.quarantineOnce.Do(func() {
		s.quarantine = &taskQuarantine{rec: make(map[string]*taskFailureRecord)}
	})
	return s.quarantine
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
	if snap.clean || commitProgressed(ap.WorktreePath, snap.beforeRef) {
		q.evict(taskID)
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
}

// recordEligibleKill folds one quarantine-eligible kill into the ledger and
// returns the record's new count. Field-delta progress (a changed
// Design/Notes hash against a known baseline) evicts the record instead of
// incrementing — the task IS moving, just not via commits.
func (q *taskQuarantine) recordEligibleKill(taskID string, ev killEvent, designHash, notesHash uint64, baselineKnown bool) (count int, progressed bool) {
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
