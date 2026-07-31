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
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	defaultOwnershipRetryInterval = 5 * time.Second
	ownershipOwnerIDDir           = ".loom"
	ownershipOwnerIDFile          = "supervisor-owner-id"
	ownershipOwnerIDPrefix        = "loom-supervisor-owner-"
)

var errOwnershipLeaseCleared = errors.New("ownership lease was cleared locally")

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
		s.ownershipOwnerDurable = false
		return s.ownershipOwnerID
	}

	id, err := loadOrCreateOwnershipOwnerID(s.ProjectDir)
	if err != nil {
		// Fail closed: a process-specific owner cannot accidentally collapse two
		// independent runtimes into one owner. This only forfeits fast restart.
		s.ownershipOwnerID = s.resolveNodeID()
		s.ownershipOwnerDurable = false
		slog.Warn("durable supervisor ownership identity unavailable; restart recovery will wait for lease expiry",
			"project_dir", s.ProjectDir, "err", err)
		return s.ownershipOwnerID
	}
	s.ownershipOwnerID = id
	s.ownershipOwnerDurable = true
	return id
}

// CommandOwnerID returns the stable runtime identity used to recover
// acknowledged lifecycle commands after the daemon's process-scoped NodeID
// changes. Workspace daemon exclusivity and the agent ownership lease fence
// keep this identity scoped to one authoritative local runtime.
func (s *Supervisor) CommandOwnerID() (string, error) {
	s.ownershipOwnerMu.Lock()
	defer s.ownershipOwnerMu.Unlock()

	// Unit tests and legacy in-process harnesses omit ProjectDir. Production
	// daemon construction always supplies it; only that production path is
	// allowed to claim durable commands across process replacement.
	if strings.TrimSpace(s.ProjectDir) == "" {
		if s.ownershipOwnerID == "" {
			s.ownershipOwnerID = s.resolveNodeID()
		}
		return s.ownershipOwnerID, nil
	}
	if s.ownershipOwnerID != "" && s.ownershipOwnerDurable {
		return s.ownershipOwnerID, nil
	}
	id, err := loadOrCreateOwnershipOwnerID(s.ProjectDir)
	if err != nil {
		return "", fmt.Errorf("durable lifecycle command owner unavailable: %w", err)
	}
	s.ownershipOwnerID = id
	s.ownershipOwnerDurable = true
	return id, nil
}

// AgentCommandOwnershipProof is an in-memory, request-only snapshot of the
// logical-agent ownership authority held by one concrete AgentProcess. Token is
// a bearer secret and must never be logged, persisted, or copied into a command
// response. LeaseID, FencingToken, and LifecycleGenerationAt are non-secret
// recovery coordinates.
type AgentCommandOwnershipProof struct {
	AgentID               string
	LeaseID               string
	OwnerID               string
	NodeID                string
	Token                 string
	FencingToken          int64
	LifecycleGenerationAt time.Time
}

func (p AgentCommandOwnershipProof) valid() bool {
	return p.AgentID != "" &&
		p.LeaseID != "" &&
		p.OwnerID != "" &&
		p.NodeID != "" &&
		p.Token != "" &&
		p.FencingToken > 0
}

// CurrentAgentCommandOwnershipProof returns authority only from the actual
// local AgentProcess for agentID. Presence in the supervisor slice, a running
// PID, or the Agent projection alone is never treated as ownership proof.
func (s *Supervisor) CurrentAgentCommandOwnershipProof(agentID string) (AgentCommandOwnershipProof, error) {
	ap := s.findAgentProcess(agentID)
	if ap == nil {
		return AgentCommandOwnershipProof{}, fmt.Errorf("agent %q has no local AgentProcess ownership authority", agentID)
	}
	return s.agentCommandOwnershipProof(ap)
}

// ReacquireAgentCommandOwnership performs the same-owner server arbitration
// used by replacement-daemon recovery. It intentionally returns a fresh raw
// proof rather than trying to reconstruct the bearer token from the durable
// command row. If no supervised generation exists (for example a Stop was
// projected before the daemon crashed), a short-lived AgentProcess derived
// from the current daemon configuration owns only this recovery operation.
func (s *Supervisor) ReacquireAgentCommandOwnership(
	agentID string,
) (AgentCommandOwnershipProof, func(), error) {
	ap := s.findAgentProcess(agentID)
	transient := false
	if ap == nil {
		// Aggregate recovery needs only the stable agent identity. The agent may
		// have been removed from the latest config after its command was Acked;
		// the durable command owner must still be able to fence and terminally
		// converge that row.
		entry := cfgpkg.AgentEntry{Worktree: agentID}
		if s.ConfigSnapshot != nil {
			if cfg := s.ConfigSnapshot(); cfg != nil {
				for _, candidate := range cfg.Agents {
					if candidate.Worktree == agentID {
						entry = candidate
						break
					}
				}
			}
		}
		ap = &AgentProcess{
			Entry:                 entry,
			LifecycleGenerationAt: time.Now().UTC(),
			OwnershipAcquired:     make(chan struct{}),
		}
		transient = true
	}

	if s.acquireAgentOwnership(ap) != ownershipAcquired {
		return AgentCommandOwnershipProof{}, nil, fmt.Errorf("agent %q command recovery could not reacquire ownership", agentID)
	}
	proof, err := s.agentCommandOwnershipProof(ap)
	if err != nil {
		if transient {
			s.releaseAgentOwnership(ap)
		}
		return AgentCommandOwnershipProof{}, nil, err
	}
	var release func()
	if transient {
		release = func() { s.releaseAgentOwnership(ap) }
	}
	return proof, release, nil
}

// WaitForAgentCommandOwnership waits for the explicit first-acquire signal of
// a newly added lifecycle generation. AddAgentWithOwnershipReservation keeps
// the signaled lease live until ReleaseAgentCommandOwnershipReservation.
func (s *Supervisor) WaitForAgentCommandOwnership(
	ctx context.Context,
	ap *AgentProcess,
	afterGeneration time.Time,
	afterFence int64,
) (AgentCommandOwnershipProof, error) {
	if ap == nil {
		return AgentCommandOwnershipProof{}, errors.New("replacement AgentProcess is required")
	}
	ap.Mu.Lock()
	ready := ap.OwnershipAcquired
	ap.Mu.Unlock()
	if ready == nil {
		return AgentCommandOwnershipProof{}, fmt.Errorf("agent %q replacement has no ownership-acquired signal", ap.Entry.Worktree)
	}
	select {
	case <-ctx.Done():
		return AgentCommandOwnershipProof{}, fmt.Errorf("wait for agent %q replacement ownership: %w", ap.Entry.Worktree, ctx.Err())
	case <-ready:
	}
	proof, err := s.agentCommandOwnershipProof(ap)
	if err != nil {
		return AgentCommandOwnershipProof{}, err
	}
	if !proof.LifecycleGenerationAt.After(afterGeneration) {
		return AgentCommandOwnershipProof{}, fmt.Errorf(
			"agent %q replacement generation %s is not newer than %s",
			ap.Entry.Worktree,
			proof.LifecycleGenerationAt,
			afterGeneration,
		)
	}
	if proof.FencingToken <= afterFence {
		return AgentCommandOwnershipProof{}, fmt.Errorf(
			"agent %q replacement ownership fence %d is not newer than %d",
			ap.Entry.Worktree,
			proof.FencingToken,
			afterFence,
		)
	}
	return proof, nil
}

// ReleaseAgentCommandOwnershipReservation lets normal ownership retirement
// proceed after a Restart command has reached a durable terminal state.
func (s *Supervisor) ReleaseAgentCommandOwnershipReservation(ap *AgentProcess) {
	if ap == nil {
		return
	}
	ap.Mu.Lock()
	ap.ownershipCommandReserved = false
	pending := ap.ownershipReleasePending
	ap.ownershipReleasePending = false
	ap.Mu.Unlock()
	if pending {
		s.releaseAgentOwnership(ap)
	}
}

func (s *Supervisor) findAgentProcess(agentID string) *AgentProcess {
	s.AgentsMu.RLock()
	defer s.AgentsMu.RUnlock()
	for _, ap := range s.Agents {
		if ap != nil && ap.Entry.Worktree == agentID {
			return ap
		}
	}
	return nil
}

func (s *Supervisor) agentCommandOwnershipProof(ap *AgentProcess) (AgentCommandOwnershipProof, error) {
	if ap == nil {
		return AgentCommandOwnershipProof{}, errors.New("agent ownership proof requires an AgentProcess")
	}
	ap.Mu.Lock()
	proof := AgentCommandOwnershipProof{
		AgentID:               ap.Entry.Worktree,
		LeaseID:               ap.OwnershipLeaseID,
		OwnerID:               ap.OwnershipOwnerID,
		NodeID:                ap.OwnershipNodeID,
		Token:                 ap.OwnershipLeaseToken,
		FencingToken:          ap.OwnershipFencingToken,
		LifecycleGenerationAt: ap.LifecycleGenerationAt,
	}
	ap.Mu.Unlock()
	if !proof.valid() {
		return AgentCommandOwnershipProof{}, fmt.Errorf("agent %q has no complete active ownership proof", ap.Entry.Worktree)
	}
	return proof, nil
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
	ap.ownershipOpMu.Lock()
	defer ap.ownershipOpMu.Unlock()
	nodeID, ownerID := s.ownershipAcquireIdentity()
	lease, sentAt, err := s.requestAgentOwnershipLease(ap, nodeID, ownerID)
	if err != nil {
		return s.classifyOwnershipAcquireError(ap, err)
	}
	if lease == nil {
		slog.Warn("agent ownership acquire returned no lease", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID)
		return ownershipAcquireInconclusive
	}
	if !s.installAgentOwnershipLease(ap, lease, nodeID, ownerID, sentAt) {
		return ownershipAcquireInconclusive
	}
	slog.Debug("agent ownership acquired", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID,
		"owner_id", ownerID, "node_id", nodeID, "fencing_token", lease.FencingToken)
	return ownershipAcquired
}

func (s *Supervisor) ownershipAcquireIdentity() (string, string) {
	nodeID := s.NodeID
	if nodeID == "" {
		nodeID = s.resolveNodeID()
	}
	return nodeID, s.resolveOwnershipOwnerID()
}

func (s *Supervisor) requestAgentOwnershipLease(
	ap *AgentProcess,
	nodeID,
	ownerID string,
) (*domain.AgentOwnershipLease, time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	sentAt := time.Now()
	lease, err := s.ControlStore.AgentOwnershipLeases().Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey:    s.WorkspaceID,
		AgentID:         ap.Entry.Worktree,
		OwnerID:         ownerID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		NodeID:          nodeID,
		TTL:             s.ownershipTTL(),
	})
	return lease, sentAt, err
}

func (s *Supervisor) classifyOwnershipAcquireError(
	ap *AgentProcess,
	err error,
) ownershipAcquireOutcome {
	if errors.Is(err, domain.ErrAlreadyClaimed) ||
		errors.Is(err, domain.ErrAlreadyExists) ||
		errors.Is(err, domain.ErrConflict) {
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

func (s *Supervisor) installAgentOwnershipLease(
	ap *AgentProcess,
	lease *domain.AgentOwnershipLease,
	nodeID,
	ownerID string,
	sentAt time.Time,
) bool {
	ap.Mu.Lock()
	if ap.OwnershipLeaseToken != "" && lease.FencingToken <= ap.OwnershipFencingToken {
		currentFence := ap.OwnershipFencingToken
		ap.Mu.Unlock()
		slog.Warn("agent ownership acquire returned a non-advancing fence; refusing stale authority",
			"worktree", ap.Entry.Worktree,
			"workspace", s.WorkspaceID,
			"current_fencing_token", currentFence,
			"returned_fencing_token", lease.FencingToken)
		return false
	}
	ap.OwnershipLeaseID = lease.LeaseID
	ap.OwnershipOwnerID = lease.OwnerID
	if ap.OwnershipOwnerID == "" {
		ap.OwnershipOwnerID = ownerID
	}
	ap.OwnershipNodeID = lease.NodeID
	if ap.OwnershipNodeID == "" {
		ap.OwnershipNodeID = nodeID
	}
	ap.OwnershipLeaseToken = lease.Token
	ap.OwnershipFencingToken = lease.FencingToken
	ap.OwnershipLastHeartbeat = lease.LastHeartbeat // server-derived: display/telemetry only
	ap.OwnershipRenewedAt = sentAt                  // local-clock anchor: drives the fail-open validity window
	if ap.OwnershipAcquired == nil {
		ap.OwnershipAcquired = make(chan struct{})
	}
	ready := ap.OwnershipAcquired
	ap.Mu.Unlock()
	ap.ownershipReady.Do(func() { close(ready) })
	return true
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
	ap.OwnershipOwnerID = ""
	ap.OwnershipNodeID = ""
	ap.OwnershipLeaseToken = ""
	ap.OwnershipFencingToken = 0
	ap.OwnershipLastHeartbeat = time.Time{}
	ap.OwnershipRenewedAt = time.Time{}
	ap.Mu.Unlock()
}

func (s *Supervisor) releaseAgentOwnership(ap *AgentProcess) {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return
	}
	ap.ownershipOpMu.Lock()
	defer ap.ownershipOpMu.Unlock()
	ap.Mu.Lock()
	agentID := ap.Entry.Worktree
	leaseID := ap.OwnershipLeaseID
	token := ap.OwnershipLeaseToken
	if ap.ownershipCommandReserved && token != "" {
		ap.ownershipReleasePending = true
		ap.Mu.Unlock()
		return
	}
	ap.OwnershipLeaseID = ""
	ap.OwnershipOwnerID = ""
	ap.OwnershipNodeID = ""
	ap.OwnershipLeaseToken = ""
	ap.OwnershipFencingToken = 0
	ap.OwnershipLastHeartbeat = time.Time{}
	ap.OwnershipRenewedAt = time.Time{}
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

type agentStartDecision uint8

const (
	agentStartReady agentStartDecision = iota
	agentStartStopped
	agentStartPreflightRetry
	agentStartOwnershipRetry
)

// prepareAgentStartTransition linearizes Stop against the whole
// preflight/claim/spawn transition. Only agentStartReady leaves StartStopMu
// held; the caller must release it immediately after spawnAgent publishes
// Cmd/Pid.
func (s *Supervisor) prepareAgentStartTransition(
	ap *AgentProcess,
	stopOwnershipHeartbeat func(),
) (func(), agentStartDecision) {
	if !s.beginAgentStartTransition(ap) {
		return stopOwnershipHeartbeat, agentStartStopped
	}

	if !s.preFlightSetup(ap) {
		s.endAgentStartTransition(ap)
		return stopOwnershipHeartbeat, agentStartPreflightRetry
	}

	replacementHeartbeat := s.refreshAgentOwnershipBeforeSpawn(ap, stopOwnershipHeartbeat)
	if replacementHeartbeat == nil {
		s.endAgentStartTransition(ap)
		return nil, agentStartOwnershipRetry
	}
	return replacementHeartbeat, agentStartReady
}

func (s *Supervisor) retryAfterPreflightFailure(ap *AgentProcess, releaseOwnership func()) bool {
	s.Concurrency.Release(ap.Entry.Role)
	releaseOwnership()
	s.postExitCleanup(ap)
	return s.shouldRestart(ap) && s.sleepBeforeRestart(ap)
}

// beginAgentStartTransition acquires the per-agent start/stop barrier and
// performs the final stop check before preflight can claim work. A true return
// leaves StartStopMu held until endAgentStartTransition.
func (s *Supervisor) beginAgentStartTransition(ap *AgentProcess) bool {
	ap.StartStopMu.Lock()
	if s.checkAgentStopSignals(ap) {
		ap.StartStopMu.Unlock()
		return false
	}
	return true
}

func (s *Supervisor) endAgentStartTransition(ap *AgentProcess) {
	ap.StartStopMu.Unlock()
}

// clearAgentSessionState resets session state between supervision cycles.
func (s *Supervisor) clearAgentSessionState(ap *AgentProcess) {
	ap.Mu.Lock()
	ap.Session = nil
	ap.AgentSessionID = ""
	ap.AgentIPCAuthToken = ""
	ap.TranscriptPath = ""
	ap.BeforeRef = ""
	ap.AssignedTaskID = ""
	ap.ResumeTaskID = ""          // per-cycle; re-detected in preFlightSetup (ResumeFailures persists)
	ap.RecoveryMode = recoverCold // per-cycle; re-classified in preFlightSetup
	ap.LastActivity = time.Time{}
	ap.Mu.Unlock()
}

// refreshAgentOwnershipBeforeSpawn closes the potentially nontrivial preflight
// window with a synchronous server arbitration. The old heartbeat is stopped
// first so a concurrent heartbeat failure cannot silently exit after this
// acquire and leave the new subprocess unguarded. A non-nil returned stopper
// means ownership is current and a replacement heartbeat is live; nil means
// the caller must release its start/stop barrier and retry.
func (s *Supervisor) refreshAgentOwnershipBeforeSpawn(ap *AgentProcess, stopHeartbeat func()) func() {
	stopHeartbeat()
	if s.acquireAgentOwnership(ap) == ownershipAcquired {
		return s.startOwnershipHeartbeat(ap)
	}
	s.completePreSpawnCleanup(ap, "ownership_lost")
	s.Concurrency.Release(ap.Entry.Role)
	s.releaseAgentOwnership(ap)
	s.postExitCleanup(ap)
	// The caller may be holding AgentProcess.StartStopMu across the
	// claim-to-spawn transition. It must release that barrier before sleeping
	// so a concurrent Stop can close StopCh and wake the retry wait.
	return nil
}

func (s *Supervisor) heartbeatAgentOwnership(ap *AgentProcess, ttl time.Duration) bool {
	ap.Mu.Lock()
	token := ap.OwnershipLeaseToken
	ap.Mu.Unlock()
	if token == "" {
		return false
	}
	if err := s.doOwnershipHeartbeat(ap, ttl); err != nil {
		// A serialized Release may clear the lease after the optimistic
		// pre-check above but before this heartbeat acquires ownershipOpMu.
		// That is an intentional local retirement, not an ownership failure
		// that should enter retry/re-acquire verification.
		if errors.Is(err, errOwnershipLeaseCleared) {
			return false
		}
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
	ap.ownershipOpMu.Lock()
	defer ap.ownershipOpMu.Unlock()
	ap.Mu.Lock()
	agentID := ap.Entry.Worktree
	token := ap.OwnershipLeaseToken
	ap.Mu.Unlock()
	if token == "" {
		return errOwnershipLeaseCleared
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	sentAt := time.Now()
	lease, err := s.ControlStore.AgentOwnershipLeases().Heartbeat(ctx, s.WorkspaceID, agentID, token, ttl)
	if err != nil {
		return err
	}
	if lease == nil {
		return errors.New("ownership heartbeat returned no lease")
	}
	ap.Mu.Lock()
	if lease.FencingToken < ap.OwnershipFencingToken {
		currentFence := ap.OwnershipFencingToken
		ap.Mu.Unlock()
		return fmt.Errorf(
			"ownership heartbeat returned stale fence %d behind installed fence %d: %w",
			lease.FencingToken,
			currentFence,
			domain.ErrConflict,
		)
	}
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
// tri-state re-acquire — fleet-db's acquire op is the arbiter. Every
// successful Acquire rotates the bearer token and fencing generation, so
// delayed operations from an older generation fail closed.
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
