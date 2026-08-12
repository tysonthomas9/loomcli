package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	defaultOwnershipRetryInterval = 5 * time.Second
	ownershipOwnerIDDir           = ".loom"
	ownershipOwnerIDFile          = "supervisor-owner-id"
	ownershipOwnerIDPrefix        = "loom-supervisor-owner-"
)

// resolveOwnershipOwnerID returns the durable identity used as OwnerID on
// agent-ownership leases. NodeID deliberately includes the daemon PID and is
// therefore an execution-instance identity; using it as OwnerID strands every
// lease until TTL after a daemon or container restart. The owner id lives in
// the workspace runtime, next to the daemon's already-exclusive .loom state,
// so a replacement daemon re-acquires as the same logical owner while a
// different runtime remains fenced as a different owner.
func (s *Supervisor) resolveOwnershipOwnerID() string {
	s.ownershipOwnerMu.Lock()
	defer s.ownershipOwnerMu.Unlock()
	if s.ownershipOwnerID != "" {
		return s.ownershipOwnerID
	}

	// Unit-test and legacy single-process callers without a project directory
	// retain the prior safe identity. Production daemon construction always
	// supplies ProjectDir.
	if strings.TrimSpace(s.ProjectDir) == "" {
		s.ownershipOwnerID = s.resolveNodeID()
		return s.ownershipOwnerID
	}

	id, err := loadOrCreateOwnershipOwnerID(s.ProjectDir)
	if err != nil {
		// Fail closed: a process-specific owner cannot accidentally collapse two
		// independent runtimes into one owner. This only forfeits fast restart.
		s.ownershipOwnerID = s.resolveNodeID()
		slog.Warn("durable supervisor ownership identity unavailable; restart recovery will wait for lease expiry",
			"project_dir", s.ProjectDir, "err", err)
		return s.ownershipOwnerID
	}
	s.ownershipOwnerID = id
	return id
}

func loadOrCreateOwnershipOwnerID(projectDir string) (string, error) {
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		return "", fmt.Errorf("open project root: %w", err)
	}
	defer func() { _ = root.Close() }()

	path := ownershipOwnerIDDir + "/" + ownershipOwnerIDFile
	if data, err := root.ReadFile(path); err == nil {
		return parseOwnershipOwnerID(data)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	if err := root.MkdirAll(ownershipOwnerIDDir, 0o700); err != nil {
		return "", fmt.Errorf("create ownership identity dir: %w", err)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate ownership identity: %w", err)
	}
	id := ownershipOwnerIDPrefix + hex.EncodeToString(random)

	// Atomic rename avoids leaving a partial identity after a crash. Daemon
	// workspace/cwd flocking serializes writers across processes; the mutex in
	// resolveOwnershipOwnerID serializes all agent goroutines in this process.
	tmpPath := ownershipOwnerIDDir + "/.supervisor-owner-id-" + hex.EncodeToString(random)
	tmp, err := root.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create ownership identity temp file: %w", err)
	}
	defer func() { _ = root.Remove(tmpPath) }()
	if _, err := tmp.WriteString(id + "\n"); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write ownership identity: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("sync ownership identity: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close ownership identity: %w", err)
	}
	if err := root.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("publish ownership identity: %w", err)
	}
	return id, nil
}

func parseOwnershipOwnerID(data []byte) (string, error) {
	id := strings.TrimSpace(string(data))
	hexPart := strings.TrimPrefix(id, ownershipOwnerIDPrefix)
	if hexPart == id || len(hexPart) != 32 {
		return "", fmt.Errorf("invalid durable supervisor ownership identity")
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", fmt.Errorf("invalid durable supervisor ownership identity: %w", err)
	}
	return id, nil
}

// ownershipAcquireOutcome classifies an ownership acquire attempt so callers
// can distinguish "someone else verifiably holds it" from "could not tell"
// (network/5xx) — the verify-before-kill path branches on exactly that.
type ownershipAcquireOutcome int

const (
	ownershipAcquired ownershipAcquireOutcome = iota
	ownershipHeldByOther
	ownershipAcquireInconclusive
)

func (s *Supervisor) acquireAgentOwnership(ap *AgentProcess) ownershipAcquireOutcome {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		// Disabled control plane maps to success so non-control-plane
		// supervision keeps today's behavior exactly.
		return ownershipAcquired
	}
	nodeID := s.NodeID
	if nodeID == "" {
		nodeID = s.resolveNodeID()
	}
	ownerID := s.resolveOwnershipOwnerID()
	ttl := s.ownershipTTL()
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	sentAt := time.Now()
	lease, err := s.ControlStore.AgentOwnershipLeases().Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey:    s.WorkspaceID,
		AgentID:         ap.Entry.Worktree,
		OwnerID:         ownerID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		NodeID:          nodeID,
		TTL:             ttl,
	})
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyClaimed) || errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
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
	slog.Debug("agent ownership acquired", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID,
		"owner_id", ownerID, "node_id", nodeID, "fencing_token", lease.FencingToken)
	return ownershipAcquired
}

// ownershipTTL intentionally follows node liveness rather than the much
// longer task-session lease. If durable owner state is unavailable, a crashed
// daemon therefore blocks a replacement only until its node would be declared
// stale, not for the full duration of a paid agent turn.
func (s *Supervisor) ownershipTTL() time.Duration {
	if s.NodeTTL > 0 {
		return s.NodeTTL
	}
	return defaultNodeTTL
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

	ttl := s.ownershipTTL()
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

// refreshAgentOwnershipBeforeSpawn closes the potentially nontrivial preflight
// window with a synchronous server arbitration. The old heartbeat is stopped
// first so a concurrent heartbeat failure cannot silently exit after this
// acquire and leave the new subprocess unguarded. A non-nil returned stopper
// means ownership is current and a replacement heartbeat is live; nil means
// the caller must either retry (keepRunning=true) or exit.
func (s *Supervisor) refreshAgentOwnershipBeforeSpawn(ap *AgentProcess, stopHeartbeat func()) (stopper func(), keepRunning bool) {
	stopHeartbeat()
	if s.acquireAgentOwnership(ap) == ownershipAcquired {
		return s.startOwnershipHeartbeat(ap), true
	}
	s.completePreSpawnCleanup(ap, "ownership_lost")
	s.Concurrency.Release(ap.Entry.Role)
	s.releaseAgentOwnership(ap)
	s.postExitCleanup(ap)
	return nil, s.sleepBeforeOwnershipRetry(ap)
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
		errors.Is(err, domain.ErrAlreadyClaimed) ||
		errors.Is(err, domain.ErrAlreadyExists) ||
		errors.Is(err, domain.ErrConflict) ||
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
