package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const defaultOwnershipRetryInterval = 5 * time.Second

func (s *Supervisor) acquireAgentOwnership(ap *AgentProcess) bool {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return true
	}
	nodeID := s.NodeID
	if nodeID == "" {
		nodeID = s.resolveNodeID()
	}
	ttl := defaultLeaseTTL
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	lease, err := s.ControlStore.AgentOwnershipLeases().Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey:    s.WorkspaceID,
		AgentID:         ap.Entry.Worktree,
		OwnerID:         nodeID,
		RuntimeProvider: domain.RuntimeProviderLocal,
		NodeID:          nodeID,
		TTL:             ttl,
	})
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) || errors.Is(err, domain.ErrConflict) {
			slog.Info("agent ownership held by another daemon", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID, "err", err)
			return false
		}
		if errors.Is(err, domain.ErrNotFound) {
			clearAgentOwnershipLeaseState(ap)
			slog.Info("agent ownership leases unavailable; continuing without ownership guard", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID, "err", err)
			return true
		}
		slog.Warn("agent ownership acquire failed", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID, "err", err)
		return false
	}
	ap.Mu.Lock()
	ap.OwnershipLeaseID = lease.LeaseID
	ap.OwnershipLeaseToken = lease.Token
	ap.OwnershipFencingToken = lease.FencingToken
	ap.OwnershipLastHeartbeat = lease.LastHeartbeat
	ap.Mu.Unlock()
	slog.Debug("agent ownership acquired", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID, "node_id", nodeID, "fencing_token", lease.FencingToken)
	return true
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
	interval := ttl / 4
	if interval <= 0 || interval > defaultNodeInterval {
		interval = defaultNodeInterval
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-s.Shutdown:
				return
			case <-ticker.C:
				if !s.heartbeatAgentOwnership(ap, ttl) {
					return
				}
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
	agentID := ap.Entry.Worktree
	token := ap.OwnershipLeaseToken
	ap.Mu.Unlock()
	if token == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	lease, err := s.ControlStore.AgentOwnershipLeases().Heartbeat(ctx, s.WorkspaceID, agentID, token, ttl)
	cancel()
	if err != nil {
		slog.Warn("agent ownership heartbeat failed; stopping agent process", "worktree", agentID, "workspace", s.WorkspaceID, "err", err)
		backend := s.GetEffectiveBackend(ap)
		ap.Mu.Lock()
		ap.LastError = &agenterr.AgentError{
			Class:     agenterr.Unknown,
			ExitCode:  -1,
			Message:   fmt.Sprintf("ownership heartbeat failed: %v", err),
			Backend:   backend,
			Timestamp: time.Now(),
		}
		ap.Mu.Unlock()
		s.StopAgent(ap, s.GetSigtermTimeout())
		return false
	}
	ap.Mu.Lock()
	ap.OwnershipLastHeartbeat = lease.LastHeartbeat
	ap.OwnershipFencingToken = lease.FencingToken
	ap.Mu.Unlock()
	return true
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
		s.setStopReason(ap, StopReasonShutdown)
		return false
	case <-ap.StopCh:
		s.setStopReasonDefault(ap, StopReasonConfigRemoved)
		return false
	}
}
