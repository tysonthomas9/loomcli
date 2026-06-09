package supervisor

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/agentpolicy"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

const primaryBackendRetryCooldown = time.Minute

// backendUnavailableRecheckInterval is the fixed delay between retries when the
// backend CLI binary is missing. The agent recovers automatically once the
// binary is installed or PATH is fixed, so we poll on a fixed interval rather
// than an exponential backoff, and never count these retries toward max_retries.
const backendUnavailableRecheckInterval = 30 * time.Second

type maxRetriesExhausted struct {
	AgentName    string
	Role         string
	TaskID       string
	Backend      string
	MaxRetries   int
	RestartCount int
	ErrorClass   string
	ErrorMessage string
}

// newMaxRetriesExhaustedLocked snapshots the signals needed after the restart
// decision releases ap.Mu. Caller holds ap.Mu.
func newMaxRetriesExhaustedLocked(ap *AgentProcess, maxRetries int) *maxRetriesExhausted {
	info := &maxRetriesExhausted{
		AgentName:    ap.Entry.Worktree,
		Role:         ap.Entry.Role,
		TaskID:       ap.AssignedTaskID,
		MaxRetries:   maxRetries,
		RestartCount: ap.RestartCount,
		ErrorClass:   "unknown",
	}
	if ap.LastError != nil {
		info.ErrorClass = ap.LastError.Class.String()
		info.ErrorMessage = strings.TrimSpace(ap.LastError.Message)
	}
	if info.TaskID == "" {
		info.TaskID = strings.TrimSpace(ap.RequestedTaskID)
	}
	return info
}

func (s *Supervisor) handleMaxRetriesExhausted(ap *AgentProcess, info maxRetriesExhausted) {
	slog.Warn("agent restart budget exhausted; entering error state",
		"worktree", info.AgentName,
		"max_retries", info.MaxRetries,
		"restart_count", info.RestartCount)
	s.markControlPlaneAgentState(ap, domain.AgentStateError)
	s.markAgentStoppedForExplicitResume(info.AgentName)
	if info.TaskID == "" || s.IssueBackend == nil {
		return
	}
	s.blockTaskAfterMaxRetries(info)
}

func (s *Supervisor) markAgentStoppedForExplicitResume(agentName string) {
	if agentName == "" {
		return
	}
	s.AgentsMu.Lock()
	if s.StoppedAgents == nil {
		s.StoppedAgents = make(map[string]struct{})
	}
	s.StoppedAgents[agentName] = struct{}{}
	s.AgentsMu.Unlock()
}

func (s *Supervisor) blockTaskAfterMaxRetries(info maxRetriesExhausted) {
	status := "blocked"
	assignee := info.AgentName

	ctx, cancel := s.operationContext()
	defer cancel()
	if err := s.IssueBackend.Update(ctx, info.TaskID, backend.UpdateParams{
		Status:   &status,
		Assignee: &assignee,
	}); err != nil {
		slog.Warn("failed to block task after agent retry budget exhausted",
			"worktree", info.AgentName, "task_id", info.TaskID, "err", err)
		return
	}

	if _, err := s.IssueBackend.AddComment(ctx, backend.CommentAddParams{
		IssueID: info.TaskID,
		Author:  "loom-daemon",
		Text:    maxRetriesExhaustedComment(info),
	}); err != nil {
		slog.Warn("failed to comment after agent retry budget exhausted",
			"worktree", info.AgentName, "task_id", info.TaskID, "err", err)
	}
}

func maxRetriesExhaustedComment(info maxRetriesExhausted) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Agent %s stopped with error after exhausting its retry budget (%d failed attempt(s), max_retries=%d). Automatic retries are stopped; start or restart the agent to resume.",
		info.AgentName, info.RestartCount, info.MaxRetries)
	if info.Backend != "" {
		fmt.Fprintf(&b, " Backend: %s.", info.Backend)
	}
	if info.ErrorClass != "" {
		fmt.Fprintf(&b, " Last error: %s", info.ErrorClass)
		if info.ErrorMessage != "" {
			fmt.Fprintf(&b, ": %s", info.ErrorMessage)
		}
		b.WriteString(".")
	}
	return b.String()
}

func isReplaceableTerminalAgent(ap *AgentProcess) bool {
	done, ok := pendingTerminalAgentDone(ap)
	if !ok {
		return false
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func pendingTerminalAgentDone(ap *AgentProcess) (<-chan struct{}, bool) {
	if ap == nil || ap.Done == nil {
		return nil, false
	}
	ap.Mu.Lock()
	reason := ap.StopReason
	ap.Mu.Unlock()
	if reason != StopReasonMaxRetries && reason != StopReasonFatalError {
		return nil, false
	}
	return ap.Done, true
}

// shouldRestart determines if the agent should restart by consulting the
// policy disposition for the classified outcome of the most recent exit
// (agentpolicy.Decide). The table owns the per-class verdict; this layer
// owns its counters (RestartCount/RateRetryCount/NoWorkCount) and its
// configured budgets.
func (s *Supervisor) shouldRestart(ap *AgentProcess) bool {
	maxRetries := s.getMaxRetries()

	ap.Mu.Lock()
	shouldRestart, exhausted := s.shouldRestartLocked(ap, maxRetries)
	ap.Mu.Unlock()

	if exhausted != nil {
		exhausted.Backend = s.GetEffectiveBackend(ap)
		s.handleMaxRetriesExhausted(ap, *exhausted)
	}
	return shouldRestart
}

// shouldRestartLocked returns the restart decision and optional max-retry
// exhaustion snapshot. Caller holds ap.Mu.
func (s *Supervisor) shouldRestartLocked(ap *AgentProcess, maxRetries int) (bool, *maxRetriesExhausted) {
	// Clean success (exit 0, no error): always restart, reset counters —
	// including legacy park counters. Long runs (>1 minute) also reset primary
	// backend.
	if ap.LastExitCode == 0 && ap.LastError == nil {
		s.applyCleanSuccessRestart(ap)
		return true, nil
	}

	var outcome agenterr.Outcome
	if ap.LastError != nil {
		outcome = ap.LastError.Class
	}

	switch d := agentpolicy.Decide(outcome); d.Decision {
	case agentpolicy.StopFatal:
		s.applyFatalStop(ap, outcome)
		return false, nil

	case agentpolicy.FastFail:
		s.applyFastFailStop(ap, outcome)
		return false, nil

	case agentpolicy.Park:
		// BackendUnavailable: fixed recheck without eroding the restart
		// budget — recoverable once the binary returns.
		s.applyBackendUnavailableRestart(ap)
		return true, nil

	case agentpolicy.Failover:
		s.applyFailoverExhaustedStop(ap, outcome)
		return false, nil

	case agentpolicy.RetryUncounted:
		if outcome.Is(agenterr.NoWorkOutcome) {
			s.applyNoWorkRestart(ap)
			return true, nil
		}
		// Rate limits: unlimited uncounted retries by default; the
		// rate_limit_no_count config opt-out routes them through the
		// counted budget instead (the layer's config wins, pt7).
		if s.getRateLimitNoCount() {
			s.applyRateLimitedRestart(ap)
			return true, nil
		}
		return s.applyCountedRestart(ap, d, maxRetries)

	default: // Retry
		return s.applyCountedRestart(ap, d, maxRetries)
	}
}

// applyFatalStop stops for auth/billing errors that need human intervention.
// Caller holds ap.Mu.
func (s *Supervisor) applyFatalStop(ap *AgentProcess, outcome agenterr.Outcome) {
	log.Printf("[daemon] Agent %s: fatal error (%s), stopping supervisor",
		ap.Entry.Worktree, outcome)
	ap.NoWorkCount = 0
	ap.StopReason = StopReasonFatalError
}

// applyFastFailStop stops for deterministic errors retrying cannot fix.
// Caller holds ap.Mu.
func (s *Supervisor) applyFastFailStop(ap *AgentProcess, outcome agenterr.Outcome) {
	log.Printf("[daemon] Agent %s: deterministic failure (%s), stopping supervisor (fast-fail)",
		ap.Entry.Worktree, outcome)
	ap.NoWorkCount = 0
	ap.StopReason = StopReasonFastFail
}

// applyFailoverExhaustedStop handles a failover-only error after the caller's
// fallback attempt has already failed because no fallback exists or all
// fallbacks are exhausted. Caller holds ap.Mu.
func (s *Supervisor) applyFailoverExhaustedStop(ap *AgentProcess, outcome agenterr.Outcome) {
	log.Printf("[daemon] Agent %s: failover-only error (%s) with no fallback remaining, stopping supervisor (fast-fail)",
		ap.Entry.Worktree, outcome)
	ap.NoWorkCount = 0
	ap.StopReason = StopReasonFastFail
}

// applyCleanSuccessRestart resets every retry counter after a clean exit —
// including the park-escalation budget ("progress" ends a park spiral). Long
// runs (>1 minute) also reset to the primary backend. Caller holds ap.Mu.
func (s *Supervisor) applyCleanSuccessRestart(ap *AgentProcess) {
	ap.RestartCount = 0
	ap.RateRetryCount = 0
	ap.NoWorkCount = 0
	ap.ParkCount = 0
	ap.StopReason = ""
	if time.Since(ap.LastStart) > time.Minute {
		ap.CurrentBackendIdx = 0 // reset to primary backend
	}
}

// applyRateLimitedRestart handles an uncounted rate-limit retry. Caller holds ap.Mu.
func (s *Supervisor) applyRateLimitedRestart(ap *AgentProcess) {
	ap.RateRetryCount++
	ap.NoWorkCount = 0
	ap.StopReason = ""
	log.Printf("[daemon] Agent %s: rate limited (retry %d, not counted toward max_retries)",
		ap.Entry.Worktree, ap.RateRetryCount)
}

// applyCountedRestart handles a counted retry (policy Decision Retry): the
// failure erodes max_retries, and once the budget is spent the agent enters
// #134's terminal error state until explicit resume.
// max_retries == 0 stays an explicit fail-fast opt-out; the guard sits after
// the increment so its counter side effect (RestartCount lands at 1) is
// unchanged. Caller holds ap.Mu.
func (s *Supervisor) applyCountedRestart(ap *AgentProcess, _ agentpolicy.Disposition, maxRetries int) (bool, *maxRetriesExhausted) {
	ap.RestartCount++
	ap.RateRetryCount = 0 // reset rate counter on non-rate error
	ap.NoWorkCount = 0
	if ap.RestartCount <= maxRetries {
		ap.StopReason = ""
		return true, nil
	}

	// The guard sits after the increment so the max_retries==0 counter side
	// effect (RestartCount lands at 1) is preserved.
	log.Printf("[daemon] Agent %s: restart budget exhausted, entering error state",
		ap.Entry.Worktree)
	ap.StopReason = StopReasonMaxRetries
	return false, newMaxRetriesExhaustedLocked(ap, maxRetries)
}

// applyNoWorkRestart resets retry counters for a NoWork exit and, if the
// agent has failed over to a fallback backend, periodically returns to the
// primary to test recovery. Caller holds ap.Mu. NoWork never counts toward
// max_retries — task availability is not a backend-health signal.
func (s *Supervisor) applyNoWorkRestart(ap *AgentProcess) {
	ap.RestartCount = 0
	ap.RateRetryCount = 0
	ap.NoWorkCount++
	if ap.CurrentBackendIdx > 0 && shouldRetryPrimaryAfterNoWork(ap.NoWorkCount, s.getNoWorkBackoff()) {
		ap.CurrentBackendIdx = 0
	}
	ap.StopReason = ""
}

// applyBackendUnavailableRestart keeps an agent retrying when the backend CLI
// binary is missing (CLI binary not on PATH): recoverable once the binary is
// installed or PATH is fixed, so we keep retrying on a fixed recheck interval
// without eroding the restart budget. Caller holds ap.Mu. Mirrors the pre-spawn
// backend gate for the case where the missing backend is only detected at
// runtime, after the process has already been spawned.
func (s *Supervisor) applyBackendUnavailableRestart(ap *AgentProcess) {
	ap.RateRetryCount = 0
	ap.NoWorkCount = 0
	ap.StopReason = StopReasonBackendUnavailable
	log.Printf("[daemon] Agent %s: backend unavailable, will recheck in %s (not counted toward max_retries)",
		ap.Entry.Worktree, s.backendRecheckBackoff())
}

// backendRecheckBackoff is the fixed delay between BackendUnavailable re-checks
// (configurable via backendRecheckInterval; package default otherwise).
func (s *Supervisor) backendRecheckBackoff() time.Duration {
	if s.backendRecheckInterval > 0 {
		return s.backendRecheckInterval
	}
	return backendUnavailableRecheckInterval
}

// computeBackoff returns the sleep duration before the next restart. The
// policy disposition names the configured bucket (BackoffProfile); this
// layer applies its restart_policy values and counters.
func (s *Supervisor) computeBackoff(ap *AgentProcess) time.Duration {
	maxBackoff := s.getBackoffMax()

	ap.Mu.Lock()
	lastErr := ap.LastError
	count := ap.RestartCount
	rateCount := ap.RateRetryCount
	ap.Mu.Unlock()

	var outcome agenterr.Outcome
	if lastErr != nil {
		outcome = lastErr.Class
	}
	d := agentpolicy.Decide(outcome)

	var initial int
	var retryN int
	switch d.Backoff {
	case agentpolicy.BPNoWork:
		// Fixed poll: task availability is not a backend-health signal.
		return time.Duration(s.getNoWorkBackoff()) * time.Second
	case agentpolicy.BPBackendUnavailable:
		// Fixed recheck: waiting for the backend CLI to reappear, not
		// backing off a flaky run.
		return s.backendRecheckBackoff()
	case agentpolicy.BPRateLimit:
		initial = s.getRateLimitBackoff()
		retryN = rateCount
		maxBackoff = s.getRateLimitMaxWait()
	case agentpolicy.BPTimeout:
		initial = s.getTimeoutBackoff()
		retryN = count
	default:
		initial = s.getBackoffInitial()
		retryN = count
	}

	var hint time.Duration
	if d.HonorHint && lastErr != nil {
		hint = lastErr.RetryAfter
	}
	return exponentialBackoff(initial, retryN, maxBackoff, hint)
}

// exponentialBackoff computes initial * 2^retryN seconds capped at maxBackoff
// (both in seconds), preferring a larger harness Retry-After hint when one
// was carried through (hint is zero when the policy says not to honor it).
func exponentialBackoff(initial, retryN, maxBackoff int, hint time.Duration) time.Duration {
	// Cap count to prevent integer overflow in bit shift
	if retryN > 30 {
		retryN = 30
	}
	backoffSec := initial * (1 << retryN)
	if backoffSec > maxBackoff || backoffSec < 0 {
		backoffSec = maxBackoff
	}
	backoff := time.Duration(backoffSec) * time.Second
	if hint > backoff {
		backoff = hint
		if backoff > time.Duration(maxBackoff)*time.Second {
			backoff = time.Duration(maxBackoff) * time.Second
		}
	}
	return backoff
}

func shouldRetryPrimaryAfterNoWork(noWorkCount, noWorkBackoffSeconds int) bool {
	if noWorkCount <= 0 || noWorkBackoffSeconds <= 0 {
		return false
	}
	return time.Duration(noWorkCount)*time.Duration(noWorkBackoffSeconds)*time.Second >= primaryBackendRetryCooldown
}

// Helper functions to safely access config.RestartPolicy fields with defaults.
// All use ConfigSnapshot() to avoid data races with hot-reload.
func (s *Supervisor) getMaxRetries() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.MaxRetries != nil {
		return *cfg.Daemon.RestartPolicy.MaxRetries
	}
	return 3 // default
}

func (s *Supervisor) getBackoffInitial() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.BackoffInitial != nil {
		return *cfg.Daemon.RestartPolicy.BackoffInitial
	}
	return 2 // default seconds
}

func (s *Supervisor) getBackoffMax() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.BackoffMax != nil {
		return *cfg.Daemon.RestartPolicy.BackoffMax
	}
	return 300 // default seconds
}

// GetOutputTimeout returns the configured output timeout in seconds.
// LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS env var is honored when set — useful
// for integration tests that need to trip the watchdog quickly (e.g.
// test/playground/scenarios/). The env var wins over fleet-db config
// because fleet-db's wire schema does not currently persist this field
// (see internal/infra/fleetdb/daemon.go).
func (s *Supervisor) GetOutputTimeout() int {
	if v := os.Getenv("LOOM_DAEMON_OUTPUT_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.OutputTimeout != nil {
		return *cfg.Daemon.RestartPolicy.OutputTimeout
	}
	return 900 // default: 15 minutes
}

func (s *Supervisor) getRateLimitBackoff() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.RateLimitBackoff != nil {
		return *cfg.Daemon.RestartPolicy.RateLimitBackoff
	}
	return 30 // default seconds
}

func (s *Supervisor) getRateLimitMaxWait() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.RateLimitMaxWait != nil {
		return *cfg.Daemon.RestartPolicy.RateLimitMaxWait
	}
	return 300 // default seconds
}

func (s *Supervisor) getRateLimitNoCount() bool {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.RateLimitNoCount != nil {
		return *cfg.Daemon.RestartPolicy.RateLimitNoCount
	}
	return true // default: rate-limit retries don't count toward max_retries
}

func (s *Supervisor) getTimeoutBackoff() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.TimeoutBackoff != nil {
		return *cfg.Daemon.RestartPolicy.TimeoutBackoff
	}
	return 5 // default seconds
}

func (s *Supervisor) getNoWorkBackoff() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.NoWorkBackoff != nil {
		return *cfg.Daemon.RestartPolicy.NoWorkBackoff
	}
	return 30 // default seconds
}

// GetIdlePollInterval returns the configured idle poll interval in seconds.
func (s *Supervisor) GetIdlePollInterval() int {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.IdlePollInterval != nil {
		return *cfg.Daemon.RestartPolicy.IdlePollInterval
	}
	return 30 // default seconds
}

// DefaultSigtermTimeout is the default SIGTERM-to-SIGKILL window in seconds.
const DefaultSigtermTimeout = 300

// GetSigtermTimeout returns the configured SIGTERM timeout duration.
func (s *Supervisor) GetSigtermTimeout() time.Duration {
	cfg := s.ConfigSnapshot()
	if cfg.Daemon.RestartPolicy.SigtermTimeout != nil && *cfg.Daemon.RestartPolicy.SigtermTimeout > 0 {
		return time.Duration(*cfg.Daemon.RestartPolicy.SigtermTimeout) * time.Second
	}
	return time.Duration(DefaultSigtermTimeout) * time.Second
}
