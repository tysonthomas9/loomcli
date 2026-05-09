package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
)

// startAgentLeaseHeartbeat launches a periodic heartbeat goroutine for the
// supervisor-owned agent-lease (the per-session lease attached to
// ap.AgentLeaseID / ap.AgentLeaseToken). Without this, the lease created in
// createControlPlaneAgentSession expires after defaultLeaseTTL (2 minutes) and
// every subsequent loom data update from the agent fails with
// ErrInvalidTransition. Mirrors startOwnershipHeartbeat.
//
// Returns a stop function the caller invokes when the lease is released.
// Returns a no-op if there is no control store, no workspace, or no live lease
// token at the moment of invocation.
func (s *Supervisor) startAgentLeaseHeartbeat(ap *AgentProcess) func() {
	if s.ControlStore == nil || s.WorkspaceID == "" {
		return func() {}
	}
	ap.Mu.Lock()
	token := ap.AgentLeaseToken
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
				if !s.heartbeatAgentLease(ap, ttl) {
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

func (s *Supervisor) heartbeatAgentLease(ap *AgentProcess, ttl time.Duration) bool {
	ap.Mu.Lock()
	leaseID := ap.AgentLeaseID
	token := ap.AgentLeaseToken
	ap.Mu.Unlock()
	if token == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	lease, err := s.ControlStore.AgentLeases().Heartbeat(ctx, s.WorkspaceID, leaseID, token, ttl)
	cancel()
	if err != nil {
		slog.Warn("agent lease heartbeat failed; stopping agent process", "worktree", ap.Entry.Worktree, "workspace", s.WorkspaceID, "lease_id", leaseID, "err", err)
		backend := s.GetEffectiveBackend(ap)
		ap.Mu.Lock()
		ap.LastError = &agenterr.AgentError{
			Class:     agenterr.Unknown,
			ExitCode:  -1,
			Message:   fmt.Sprintf("agent lease heartbeat failed: %v", err),
			Backend:   backend,
			Timestamp: time.Now(),
		}
		ap.Mu.Unlock()
		s.StopAgent(ap, s.GetSigtermTimeout())
		return false
	}
	if lease != nil {
		ap.Mu.Lock()
		ap.AgentLeaseLastHeartbeat = lease.LastHeartbeat
		ap.Mu.Unlock()
	}
	return true
}

// stopAgentLeaseHeartbeat invokes and clears any heartbeat-stop function
// stored on ap. Also resets AgentLeaseLastHeartbeat so a stale timestamp
// is not surfaced to the UI after the agent has exited. Safe to call
// multiple times.
func stopAgentLeaseHeartbeat(ap *AgentProcess) {
	ap.Mu.Lock()
	stop := ap.AgentLeaseHeartbeatStop
	ap.AgentLeaseHeartbeatStop = nil
	ap.AgentLeaseLastHeartbeat = time.Time{}
	ap.Mu.Unlock()
	if stop != nil {
		stop()
	}
}
