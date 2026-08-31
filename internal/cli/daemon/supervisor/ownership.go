package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli/daemonregistry"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const defaultOwnershipRetryInterval = 5 * time.Second

// ownershipAcquireOutcome classifies an ownership acquire attempt so callers
// can distinguish "someone else verifiably holds it" from "could not tell"
// (network/5xx) — the verify-before-kill path branches on exactly that.
type ownershipAcquireOutcome int

const (
	ownershipAcquired ownershipAcquireOutcome = iota
	ownershipHeldByOther
	ownershipAcquireInconclusive
)

// acquireAgentOwnership acquires the agent's ownership lease with no
// takeover: the ordinary path every caller outside the reclaim probe uses.
func (s *Supervisor) acquireAgentOwnership(ap *AgentProcess) ownershipAcquireOutcome {
	return s.acquireAgentOwnershipWithTakeover(ap, "")
}

// acquireAgentOwnershipWithTakeover is acquireAgentOwnership plus an
// optional compare-and-steal: a non-empty takeoverFrom asks the server to
// break a still-live lease, but only while that lease still names exactly
// that owner. The caller (reclaimAbandonedOwnership) is responsible for
// having proved the named owner is gone.
func (s *Supervisor) acquireAgentOwnershipWithTakeover(ap *AgentProcess, takeoverFrom string) ownershipAcquireOutcome {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		// Disabled control plane maps to success so non-control-plane
		// supervision keeps today's behavior exactly.
		return ownershipAcquired
	}
	nodeID := s.NodeID
	if nodeID == "" {
		nodeID = s.resolveNodeID()
	}
	ttl := defaultLeaseTTL
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	sentAt := time.Now()
	lease, err := s.ControlStore.AgentOwnershipLeases().Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey:    s.WorkspaceID,
		AgentID:         ap.Entry.Worktree,
		OwnerID:         nodeID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		NodeID:          nodeID,
		TTL:             ttl,
		// Empty for every ordinary acquire, which keeps the request
		// wire-identical to what an older fleet-db accepts.
		TakeoverFromOwnerID: takeoverFrom,
	})
	if err != nil {
		// ErrAlreadyClaimed is fleet-db's 409 already_claimed — a
		// server-arbitrated "someone else owns this", not an
		// inconclusive transport failure. Classifying it as
		// inconclusive made the verify-before-kill path fail open on a
		// lease it had verifiably lost (split-brain double-run).
		if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrAlreadyClaimed) {
			slog.Info("agent ownership held by another daemon", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID, "err", err)
			return ownershipHeldByOther
		}
		if errors.Is(err, domain.ErrNotFound) {
			clearAgentOwnershipLeaseState(ap)
			slog.Info("agent ownership leases unavailable; continuing without ownership guard", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID, "err", err)
			return ownershipAcquired
		}
		slog.Warn("agent ownership acquire failed", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID, "err", err)
		return ownershipAcquireInconclusive
	}
	ap.Mu.Lock()
	ap.OwnershipLeaseID = lease.LeaseID
	ap.OwnershipLeaseToken = lease.Token
	ap.OwnershipFencingToken = lease.FencingToken
	ap.OwnershipLastHeartbeat = lease.LastHeartbeat // server-derived: display/telemetry only
	ap.OwnershipRenewedAt = sentAt                  // local-clock anchor: drives the fail-open validity window
	ap.Mu.Unlock()
	slog.Debug("agent ownership acquired", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID, "node_id", nodeID, "fencing_token", lease.FencingToken)
	return ownershipAcquired
}

func clearAgentOwnershipLeaseState(ap *AgentProcess) {
	ap.Mu.Lock()
	ap.OwnershipLeaseID = ""
	ap.OwnershipLeaseToken = ""
	ap.OwnershipFencingToken = 0
	ap.OwnershipLastHeartbeat = time.Time{}
	ap.Mu.Unlock()
}

func (s *Supervisor) releaseAgentOwnership(ap *AgentProcess) {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return
	}
	ap.Mu.Lock()
	agentID := ap.Entry.Worktree
	leaseID := ap.OwnershipLeaseID
	token := ap.OwnershipLeaseToken
	ap.OwnershipLeaseID = ""
	ap.OwnershipLeaseToken = ""
	ap.OwnershipFencingToken = 0
	ap.OwnershipLastHeartbeat = time.Time{}
	ap.Mu.Unlock()
	if token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	if _, err := s.ControlStore.AgentOwnershipLeases().Release(ctx, s.WorkspaceID, agentID, token); err != nil {
		slog.Warn("agent ownership release failed", "worktree", agentID, "workspace", s.WorkspaceID, "lease_id", leaseID, "err", err)
	}
}

func (s *Supervisor) startOwnershipHeartbeat(ap *AgentProcess) func() {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return func() {}
	}
	ap.Mu.Lock()
	token := ap.OwnershipLeaseToken
	ap.Mu.Unlock()
	if token == "" {
		return func() {}
	}

	ttl := defaultLeaseTTL
	interval := ownershipHeartbeatBaseInterval(ttl)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(nextOwnershipHeartbeatDelay(ap, interval, ttl))
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-s.Shutdown:
				return
			case <-timer.C:
				if !s.heartbeatAgentOwnership(ap, ttl) {
					return
				}
				timer.Reset(nextOwnershipHeartbeatDelay(ap, interval, ttl))
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func (s *Supervisor) heartbeatAgentOwnership(ap *AgentProcess, ttl time.Duration) bool {
	ap.Mu.Lock()
	token := ap.OwnershipLeaseToken
	ap.Mu.Unlock()
	if token == "" {
		return false
	}
	if err := s.doOwnershipHeartbeat(ap, ttl); err != nil {
		return s.verifyAgentOwnershipAfterHeartbeatFailure(ap, ttl, err)
	}
	return true
}

func ownershipHeartbeatBaseInterval(ttl time.Duration) time.Duration {
	interval := ttl / 4
	if interval <= 0 || interval > defaultNodeInterval {
		return defaultNodeInterval
	}
	return interval
}

func nextOwnershipHeartbeatDelay(ap *AgentProcess, interval, ttl time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	ap.Mu.Lock()
	renewedAt := ap.OwnershipRenewedAt
	ap.Mu.Unlock()
	remaining := ownershipRemainingValidity(renewedAt, ttl)
	if remaining <= 0 {
		return 0
	}
	if remaining < interval {
		return remaining
	}
	return interval
}

// doOwnershipHeartbeat performs one heartbeat round-trip and, on success,
// advances the server-derived display fields and the local-clock renewal
// anchor (captured immediately before the request was sent, monotonic
// reading preserved).
func (s *Supervisor) doOwnershipHeartbeat(ap *AgentProcess, ttl time.Duration) error {
	ap.Mu.Lock()
	agentID := ap.Entry.Worktree
	token := ap.OwnershipLeaseToken
	ap.Mu.Unlock()
	if token == "" {
		return errors.New("ownership heartbeat: lease token cleared")
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	sentAt := time.Now()
	lease, err := s.ControlStore.AgentOwnershipLeases().Heartbeat(ctx, s.WorkspaceID, agentID, token, ttl)
	if err != nil {
		return err
	}
	ap.Mu.Lock()
	ap.OwnershipLastHeartbeat = lease.LastHeartbeat // server-derived: display/telemetry only, never feeds validity
	ap.OwnershipFencingToken = lease.FencingToken
	ap.OwnershipRenewedAt = sentAt // local-clock anchor: drives the fail-open validity window
	ap.Mu.Unlock()
	return nil
}

// verifyAgentOwnershipAfterHeartbeatFailure decides whether a heartbeat
// failure means ownership is verifiably lost (kill) or recoverable
// (re-acquire / ride out). Every decision derives from a server-arbitrated
// operation — a heartbeat retry or a re-acquire — never from comparing
// server timestamps against the local clock. Returns true to keep the
// heartbeat loop (and agent) running.
func (s *Supervisor) verifyAgentOwnershipAfterHeartbeatFailure(ap *AgentProcess, ttl time.Duration, hbErr error) bool {
	agentID := ap.Entry.Worktree
	slog.Warn("agent ownership heartbeat failed; verifying before any kill", "worktree", agentID, "workspace", s.WorkspaceID, "err", hbErr)
	if !isTypedDomainError(hbErr) {
		// Untyped failure (timeout, connection error, 5xx): one immediate
		// retry IS the false-alarm verification, and it is server-side
		// proof (no client clock involved). It goes through the same
		// heartbeat plumbing, so a success is a genuine renewal and
		// OwnershipRenewedAt advances naturally.
		retryErr := s.doOwnershipHeartbeat(ap, ttl)
		if retryErr == nil {
			slog.Info("ownership heartbeat retry succeeded; continuing", "worktree", agentID)
			return true
		}
		if !isTypedDomainError(retryErr) {
			// Inconclusive twice — bounded fail-open.
			return s.continueOwnershipIfWithinValidity(ap, ttl, retryErr)
		}
		hbErr = retryErr
	}
	// The server itself ruled the lease non-renewable by us (410 gone,
	// 404, legacy 409, 403, 4xx): re-acquire arbitration.
	return s.arbitrateOwnershipByReacquire(ap, ttl, hbErr)
}

// arbitrateOwnershipByReacquire resolves an ownership dispute through one
// tri-state re-acquire — fleet-db's acquire op is the arbiter (a
// same-owner-live re-acquire preserves the token; an expired/released one
// issues a fresh token).
func (s *Supervisor) arbitrateOwnershipByReacquire(ap *AgentProcess, ttl time.Duration, hbErr error) bool {
	agentID := ap.Entry.Worktree
	ap.Mu.Lock()
	// Dead-process guard from supervisor state, not signal-0: the
	// supervisor clears Cmd/Pid only after cmd.Wait() returns, so an
	// exited-but-unreaped process still passes kill(pid, 0), and PID reuse
	// makes signal-0 worse. (Narrow race — exited, Wait not yet returned,
	// ProcessState still nil — is acceptable: superviseAgent unwinds
	// moments later and releases/re-acquires normally. This guard is
	// best-effort anti-resurrection; the loop is the corrector.)
	dead := ap.Cmd == nil || ap.Pid == 0 || ap.Cmd.ProcessState != nil
	ap.Mu.Unlock()
	if dead {
		slog.Info("ownership verification: agent process not running; not re-acquiring", "worktree", agentID)
		return false
	}
	switch s.acquireAgentOwnership(ap) {
	case ownershipAcquired:
		ap.Mu.Lock()
		newFence := ap.OwnershipFencingToken
		ap.Mu.Unlock()
		slog.Info("ownership re-acquired after heartbeat failure", "worktree", agentID, "fencing_token", newFence, "heartbeat_err", hbErr)
		return true
	case ownershipHeldByOther:
		s.killAgentForOwnership(ap, "verifiably_lost", hbErr)
		return false
	default: // ownershipAcquireInconclusive
		return s.continueOwnershipIfWithinValidity(ap, ttl, hbErr)
	}
}

// continueOwnershipIfWithinValidity is the bounded fail-open: ride out an
// inconclusive verification only while the last confirmed renewal proves
// the lease is still live server-side; past that bound ownership is
// genuinely unknown AND acquirable by others, so fail closed.
func (s *Supervisor) continueOwnershipIfWithinValidity(ap *AgentProcess, ttl time.Duration, hbErr error) bool {
	ap.Mu.Lock()
	renewedAt := ap.OwnershipRenewedAt
	ap.Mu.Unlock()
	if ownershipWithinValidity(renewedAt, ttl) {
		slog.Warn("ownership verification inconclusive; continuing within lease validity window", "worktree", ap.Entry.Worktree, "err", hbErr)
		return true
	}
	s.killAgentForOwnership(ap, "ownership_unverifiable", hbErr)
	return false
}

func ownershipRemainingValidity(renewedAt time.Time, ttl time.Duration) time.Duration {
	if renewedAt.IsZero() || ttl <= 0 {
		return 0
	}
	now := time.Now()
	monoRemaining := ttl - now.Sub(renewedAt)
	wallRemaining := ttl - now.Round(0).Sub(renewedAt.Round(0))
	if monoRemaining <= 0 || wallRemaining <= 0 {
		return 0
	}
	if monoRemaining < wallRemaining {
		return monoRemaining
	}
	return wallRemaining
}

// ownershipWithinValidity: fail-open is permitted only while BOTH clocks
// agree less than ttl has elapsed since the last confirmed renewal. The
// server stamps expires_at = server_processing_time + ttl, and processing
// necessarily happens after the client sent, so "elapsed-since-send < ttl"
// is a conservative lower bound on server-side validity that involves no
// cross-host clock comparison (never use the server-returned ExpiresAt
// here). The wall clause counts suspend gaps even where the monotonic clock
// freezes across sleep; the monotonic clause counts backward wall-clock
// steps. Either clause expiring ends fail-open — over-counting is safe.
// Residual assumption: the monotonic clock does not freeze during the same
// window in which the wall clock steps backward.
func ownershipWithinValidity(renewedAt time.Time, ttl time.Duration) bool {
	return ownershipRemainingValidity(renewedAt, ttl) > 0
}

// isTypedDomainError reports whether the error carries a domain sentinel —
// i.e. the server itself classified the outcome (vs a transport-level
// failure where the server's verdict is unknown).
func isTypedDomainError(err error) bool {
	return errors.Is(err, domain.ErrGone) ||
		errors.Is(err, domain.ErrNotFound) ||
		errors.Is(err, domain.ErrAlreadyExists) ||
		errors.Is(err, domain.ErrConflict) ||
		errors.Is(err, domain.ErrAlreadyClaimed) ||
		errors.Is(err, domain.ErrInvalid)
}

// killAgentForOwnership stops the agent for an ownership-loss reason,
// preserving the existing LastError shape.
func (s *Supervisor) killAgentForOwnership(ap *AgentProcess, reason string, hbErr error) {
	agentID := ap.Entry.Worktree
	slog.Warn("agent ownership heartbeat failed; stopping agent process", "worktree", agentID, "workspace", s.WorkspaceID, "reason", reason, "err", hbErr)
	backend := s.GetEffectiveBackend(ap)
	ap.Mu.Lock()
	ap.LastError = &agenterr.AgentError{
		Class:     agenterr.OutcomeFromHarness(wrapper.ErrUnknown),
		ExitCode:  -1,
		Message:   fmt.Sprintf("ownership heartbeat failed (%s): %v", reason, hbErr),
		Backend:   backend,
		Timestamp: time.Now(),
	}
	ap.Mu.Unlock()
	s.StopAgent(ap, s.GetSigtermTimeout())
}

func (s *Supervisor) sleepBeforeOwnershipRetry(ap *AgentProcess) bool {
	ap.Mu.Lock()
	ap.BackoffUntil = time.Now().Add(defaultOwnershipRetryInterval)
	ap.Mu.Unlock()
	defer func() {
		ap.Mu.Lock()
		ap.BackoffUntil = time.Time{}
		ap.Mu.Unlock()
	}()
	select {
	case <-time.After(defaultOwnershipRetryInterval):
		return true
	case <-s.Shutdown:
		s.setShutdownStopReason(ap)
		return false
	case <-ap.StopCh:
		s.setStopReasonDefault(ap, StopReasonConfigRemoved)
		return false
	}
}

// ─── abandonment probe ───────────────────────────────────────────────────────
//
// Kept in this file rather than an ownership_reclaim.go of its own: the
// supervisor package sits at its recorded package-size ceiling
// (scripts/package-size-allow.txt), and a 29th file would raise a ratchet
// whose stated end state is sub-splitting the package, not growing it.

// ownershipPIDIsRunning is the liveness probe used by the abandonment
// decision, indirected so tests can pin it without spawning processes.
var ownershipPIDIsRunning = lockfile.IsProcessRunning

// acquireAgentOwnershipWithReclaim is the supervision loop's entry point:
// an ordinary acquire, and — only when the server says the lease is
// verifiably held by someone else — one abandonment probe that may reclaim
// a lease orphaned by a dead local supervisor. Returns true when the agent
// may run; false means the caller falls back to the retry sleep.
//
// Deliberately NOT used by arbitrateOwnershipByReacquire: a supervisor
// resolving its own heartbeat failure must never steal the lease back, or
// verify-before-kill stops meaning anything.
func (s *Supervisor) acquireAgentOwnershipWithReclaim(ap *AgentProcess) bool {
	switch s.acquireAgentOwnership(ap) {
	case ownershipAcquired:
		return true
	case ownershipHeldByOther:
		return s.reclaimAbandonedOwnership(ap)
	default: // ownershipAcquireInconclusive
		return false
	}
}

// reclaimAbandonedOwnership answers one question: is the owner currently
// holding this agent's lease provably gone? It answers only from evidence
// it can verify locally — the lease row, this machine's hostname, and the
// liveness of a PID on this host — and EVERY unproven case returns false,
// which is today's behavior (wait out the retry interval, and ultimately
// the lease TTL).
//
// PID reuse fails safe: a recycled PID reads as alive, so we decline to
// steal and wait instead. There is no path where reuse causes a wrongful
// steal.
func (s *Supervisor) reclaimAbandonedOwnership(ap *AgentProcess) bool {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return false
	}
	agentID := ap.Entry.Worktree
	nodeID := s.NodeID
	if nodeID == "" {
		nodeID = s.resolveNodeID()
	}
	// The 409 does not carry the current owner_id, so read the lease.
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	lease, err := s.ControlStore.AgentOwnershipLeases().Get(ctx, s.WorkspaceID, agentID)
	cancel()
	if err != nil || lease == nil {
		// No evidence (transport failure, 404, already released): no steal.
		slog.Debug("ownership reclaim: lease unreadable; not reclaiming", "worktree", agentID, "workspace", s.WorkspaceID, "err", err)
		return false
	}
	localHost, hostErr := os.Hostname()
	if hostErr != nil {
		localHost = ""
	}
	deadOwner, ok := abandonedOwnerID(lease, nodeID, os.Getpid(), localHost, ownershipPIDIsRunning, time.Now())
	if !ok {
		return false
	}
	// A steal must be loud and greppable: it names both owners.
	slog.Warn("reclaiming abandoned agent ownership lease",
		"worktree", agentID, "workspace", s.WorkspaceID,
		"dead_owner_id", deadOwner, "new_owner_id", nodeID)
	if outcome := s.acquireAgentOwnershipWithTakeover(ap, deadOwner); outcome != ownershipAcquired {
		// Lost the compare-and-swap race to another reclaimer, or the
		// server does not understand takeovers yet (a pre-takeover
		// fleet-db rejects the body as invalid). Either way: no steal,
		// fall back to the retry loop, which is today's behavior.
		slog.Info("ownership reclaim did not take effect; falling back to retry",
			"worktree", agentID, "workspace", s.WorkspaceID, "dead_owner_id", deadOwner, "outcome", outcome)
		return false
	}
	return true
}

// abandonedOwnerID returns the owner_id of a lease whose owner is provably
// a dead supervisor process on THIS host, and ok=false for every case that
// falls short of that proof. It is pure so the whole steal policy is
// table-testable.
func abandonedOwnerID(
	lease *domain.AgentOwnershipLease,
	selfNodeID string,
	selfPID int,
	localHost string,
	isRunning func(int) bool,
	now time.Time,
) (string, bool) {
	if lease == nil || lease.OwnerID == "" {
		return "", false
	}
	// Only a live lease is in anyone's way; an expired or released one is
	// acquired by the ordinary retry without a steal.
	if lease.Status != domain.AgentLeaseActive || !lease.ExpiresAt.After(now) {
		return "", false
	}
	// A remotely-run agent's owner cannot be probed from here.
	if lease.RuntimeProvider != domain.RuntimeProviderLocal {
		return "", false
	}
	// Our own node ID: a same-owner re-acquire already succeeds, so a
	// takeover would be both unnecessary and self-directed.
	if lease.OwnerID == selfNodeID {
		return "", false
	}
	host, pid, ok := daemonregistry.ParseSupervisorNodeID(lease.OwnerID)
	if !ok {
		// A fleet/K8s runner or a future naming scheme: not ours to judge.
		return "", false
	}
	// Cross-host recovery is out of scope: it needs node-registry evidence,
	// not a local PID probe, and a wrong answer double-runs the agent.
	if localHost == "" || host != localHost {
		return "", false
	}
	if pid == selfPID {
		return "", false
	}
	if isRunning(pid) {
		return "", false
	}
	return lease.OwnerID, true
}
