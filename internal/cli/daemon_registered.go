package cli

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/lockfile"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// RegisteredDaemonInfo describes a supervisor daemon detected via the
// fleet-db Node registry. It is cwd-independent: the supervisor publishes
// the registration (with PID/cwd/socket as Labels) on startup and refreshes
// the heartbeat on a timer, so diagnose can find the daemon no matter what
// directory it was launched from.
type RegisteredDaemonInfo struct {
	Running bool
	PID     int
	Cwd     string
	Socket  string
	NodeID  string
}

// nodeListing captures just enough of store.NodeStore.List to keep the
// detection logic mockable in unit tests without dragging in a full
// store.Store implementation.
type nodeListing interface {
	List(ctx context.Context, workspaceKey string) ([]*domain.Node, error)
}

// DetectRegisteredDaemonRuntime returns a daemon liveness signal derived
// from the fleet-db Node registry. The supervisor calls
// NodeStore.Create + Heartbeat with RuntimeProvider="local" on startup
// (internal/cli/daemon/supervisor/supervisor.go), so the Node row is the
// authoritative cwd-independent record of "is a supervisor daemon
// running for this workspace?".
//
// The function is best-effort: a nil store, a List error, or unparseable
// labels yield a zero value (Running=false). Callers fall back to the
// path-based DetectDaemonRuntime in that case.
//
// Liveness rule:
//  1. RuntimeProvider == "local" (filter out fleet/CI/Kubernetes nodes).
//  2. ExpiresAt is in the future (heartbeat is fresh).
//  3. If the Node was registered by this host (NodeID encodes hostname),
//     the PID label must also be alive locally — defends against
//     ghost rows where the daemon crashed without un-registering.
//     For Nodes from a different host, heartbeat freshness alone is
//     trusted (we can't probe a remote PID).
func DetectRegisteredDaemonRuntime(ctx context.Context, st store.Store, workspaceKey string) RegisteredDaemonInfo {
	if st == nil || workspaceKey == "" {
		return RegisteredDaemonInfo{}
	}
	return detectRegisteredDaemonRuntime(ctx, st.Nodes(), workspaceKey, time.Now(), lockfile.IsProcessRunning, localHostname())
}

func detectRegisteredDaemonRuntime(
	ctx context.Context,
	nodes nodeListing,
	workspaceKey string,
	now time.Time,
	pidAlive func(int) bool,
	localHost string,
) RegisteredDaemonInfo {
	if nodes == nil {
		return RegisteredDaemonInfo{}
	}
	list, err := nodes.List(ctx, workspaceKey)
	if err != nil {
		slog.Debug("registered daemon detection: node list failed", "workspace", workspaceKey, "err", err)
		return RegisteredDaemonInfo{}
	}

	var best RegisteredDaemonInfo
	var bestHeartbeat time.Time
	for _, node := range list {
		if node == nil {
			continue
		}
		if node.RuntimeProvider != domain.RuntimeProviderLocal {
			continue
		}
		if !node.ExpiresAt.IsZero() && !node.ExpiresAt.After(now) {
			continue
		}
		labels := parseDaemonLabels(node.Labels)
		if labels.pid > 0 && nodeHostMatches(node.NodeID, localHost) {
			if pidAlive != nil && !pidAlive(labels.pid) {
				continue
			}
		}
		info := RegisteredDaemonInfo{
			Running: true,
			PID:     labels.pid,
			Cwd:     labels.cwd,
			Socket:  labels.socket,
			NodeID:  node.NodeID,
		}
		if best.Running && !node.LastHeartbeat.After(bestHeartbeat) {
			continue
		}
		best = info
		bestHeartbeat = node.LastHeartbeat
	}
	return best
}

type daemonLabels struct {
	pid    int
	cwd    string
	socket string
}

func parseDaemonLabels(labels []string) daemonLabels {
	var out daemonLabels
	for _, label := range labels {
		switch {
		case strings.HasPrefix(label, "loom.daemon.pid="):
			value := strings.TrimPrefix(label, "loom.daemon.pid=")
			if pid, err := strconv.Atoi(value); err == nil && pid > 0 {
				out.pid = pid
			}
		case strings.HasPrefix(label, "loom.daemon.cwd="):
			out.cwd = strings.TrimPrefix(label, "loom.daemon.cwd=")
		case strings.HasPrefix(label, "loom.daemon.socket="):
			out.socket = strings.TrimPrefix(label, "loom.daemon.socket=")
		}
	}
	return out
}

// nodeHostMatches reports whether a Node ID of the form
// "loom-supervisor-<host>-<pid>" (the format produced by
// Supervisor.resolveNodeID) was registered on the local hostname.
//
// Returns false for IDs that don't match the supervisor format: we
// can't tell if such a PID is local, so probing
// lockfile.IsProcessRunning against it would be misleading. The
// caller treats false as "trust heartbeat freshness alone".
//
// Returns true when localHost is empty so callers that fail to
// resolve their own hostname don't degrade into "treat every Node as
// remote" behavior.
func nodeHostMatches(nodeID, localHost string) bool {
	if localHost == "" {
		return true
	}
	const prefix = "loom-supervisor-"
	if !strings.HasPrefix(nodeID, prefix) {
		return false
	}
	rest := strings.TrimPrefix(nodeID, prefix)
	idx := strings.LastIndex(rest, "-")
	if idx <= 0 {
		return false
	}
	return rest[:idx] == localHost
}

func localHostname() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return host
}
