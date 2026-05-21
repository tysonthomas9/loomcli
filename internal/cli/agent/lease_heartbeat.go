package agent

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// leaseHeartbeatInterval is how often the agent supervisor pings the daemon
// to refresh its lease. Must be comfortably under the daemon-side TTL
// (currently 2 minutes — see internal/cli/daemon/daemon_ipc.go validateIPCLease).
const leaseHeartbeatInterval = 60 * time.Second

// startBackgroundLeaseHeartbeat launches a goroutine that periodically pings
// the daemon's IPC heartbeat endpoint to keep the agent's lease alive during
// long, IPC-silent steps in the workflow (notably `make gate`, which can
// run for many minutes of `go test ./...`). Without it the lease ages out
// and `loom data close` / `loom data update --status` from the next step
// fail with 'lease expired' 409s, leaving the task stuck in_progress and
// looping under daemon respawns. See LOOM-12.
//
// Returns a stop function that cancels the goroutine. If the required env
// vars aren't set (e.g. running outside a daemon-spawned context), the
// returned function is a no-op and no goroutine is started.
func startBackgroundLeaseHeartbeat() func() {
	socket := os.Getenv("LOOM_DAEMON_SOCKET")
	leaseID := os.Getenv("LOOM_AGENT_LEASE_ID")
	leaseToken := os.Getenv("LOOM_AGENT_LEASE_TOKEN")
	agentName := os.Getenv("LOOM_AGENT_NAME")
	sessionID := os.Getenv("LOOM_SESSION_ID")
	if socket == "" || leaseID == "" || leaseToken == "" || agentName == "" || sessionID == "" {
		return func() {}
	}

	client := cli.NewAgentIPCClient(socket, agentName)
	client.SessionID = sessionID
	client.LeaseID = leaseID
	client.LeaseToken = leaseToken

	ctx, cancel := context.WithCancel(context.Background())
	go runLeaseHeartbeatLoop(ctx, client.Heartbeat, leaseHeartbeatInterval)
	return cancel
}

// runLeaseHeartbeatLoop is the goroutine body. Exposed package-private and
// taking a function rather than a concrete client so tests can drive it
// without spinning up a unix socket. heartbeat is invoked once per tick;
// errors are logged at Debug and otherwise swallowed (see comment below).
func runLeaseHeartbeatLoop(ctx context.Context, heartbeat func() error, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := heartbeat(); err != nil {
				// Failure here is informational. We intentionally do NOT
				// abort the agent: a transient unix-socket hiccup or a
				// late daemon restart should not kill an active worker
				// session. The next IPC call from the agent will surface
				// any real lease problem through the normal error path.
				slog.Debug("background lease heartbeat failed", "err", err)
			}
		}
	}
}
