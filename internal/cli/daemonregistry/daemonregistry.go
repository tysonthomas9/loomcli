// Package daemonregistry detects daemon liveness from the fleet-db
// Node registry. It is the cwd-independent third source of daemon
// liveness, alongside the lock-file and state-file based detection
// in package cli (DetectDaemonRuntime).
//
// The supervisor publishes itself as a Node on startup and heartbeats
// every 30 s; this package parses the loom.daemon.* labels the
// supervisor attaches and reports whether a live daemon is present
// for a given workspace.
package daemonregistry

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

// Info describes the liveness of a daemon as advertised via the
// fleet-db Node registry. It is the third, cwd-independent source of
// daemon-liveness truth alongside the lock file and state file checked
// by cli.DetectDaemonRuntime.
//
// The supervisor publishes itself as a Node with
// RuntimeProvider=domain.RuntimeProviderLocal on startup and
// heartbeats every 30 s (TTL 2 min). It also attaches structured
// labels — see LabelPID / LabelCwd / LabelSocket — which this package
// parses to populate PID / Cwd / Socket below.
//
// The Running field is true when *any* Node satisfies the liveness
// rule documented in Detect. The remaining fields describe the
// most-recently-heartbeat'd qualifying Node.
type Info struct {
	Running bool
	PID     int
	Cwd     string
	Socket  string
}

// Label key prefixes used to round-trip PID / Cwd / Socket through
// domain.Node.Labels. The fleet-db server treats Labels as opaque, so
// no schema change is required — only this prefix contract. The
// supervisor writes these labels when registering its Node; diagnose
// parses them back out via Detect.
//
// "cwd" is the daemon's project directory (its workspace working
// tree), not strictly the shell cwd at launch time — these usually
// match, but when they don't, the project directory is the more
// useful value for diagnosing "where does this daemon think its
// runtime files live?".
const (
	LabelPID    = "loom.daemon.pid="
	LabelCwd    = "loom.daemon.cwd="
	LabelSocket = "loom.daemon.socket="
)

// Detect checks whether a daemon has registered itself in the fleet-db
// Node registry for the given workspace. The supervisor publishes its
// presence via store.NodeStore.Create on startup; this lookup is
// cwd-independent and reports correctly even when the daemon was
// launched from an arbitrary directory.
//
// Liveness rule (a Node must satisfy ALL of the following):
//
//  1. RuntimeProvider == domain.RuntimeProviderLocal — filter out
//     fleet/CI/Kubernetes-provisioned nodes that are not the local
//     supervisor.
//  2. ExpiresAt.After(now) — the supervisor heartbeat is fresh.
//  3. If a loom.daemon.pid label is present AND the Node was
//     registered by this host (NodeID contains the local hostname),
//     lockfile.IsProcessRunning must report the PID alive. This
//     guards against ghost Node rows where the daemon crashed
//     mid-shutdown and the TTL hasn't elapsed yet. For Nodes from
//     other hosts we trust the heartbeat alone — we cannot probe a
//     PID on a different machine.
//
// On nil store, store errors, or no matching nodes, Running=false is
// returned with no error surfaced. Diagnose ORs Running across three
// sources (AppData / WorkspaceLocal / Registered); a false here is
// not a verdict — it just means "this source can't confirm". The
// LOOM-3 false positives are fixed because a registered daemon now
// turns Running=true regardless of cwd, even when the path-based
// sources see nothing.
func Detect(ctx context.Context, st store.Store, workspaceKey string) Info {
	if st == nil || workspaceKey == "" {
		return Info{}
	}
	nodes, err := st.Nodes().List(ctx, workspaceKey)
	if err != nil {
		slog.Debug("daemonregistry.Detect: node list failed", "workspace", workspaceKey, "err", err)
		return Info{}
	}
	now := time.Now()
	localHost, _ := os.Hostname()

	var best *domain.Node
	var bestInfo Info
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if n.RuntimeProvider != domain.RuntimeProviderLocal {
			continue
		}
		if !n.ExpiresAt.After(now) {
			continue
		}
		pid, cwd, socket := parseLabels(n.Labels)
		// If the Node was registered by this host and has a PID label,
		// require the PID to be alive — defends against ghost rows.
		if pid > 0 && hostnameMatchesLocal(n.NodeID, localHost) {
			if !lockfile.IsProcessRunning(pid) {
				continue
			}
		}
		info := Info{
			Running: true,
			PID:     pid,
			Cwd:     cwd,
			Socket:  socket,
		}
		if best == nil || n.LastHeartbeat.After(best.LastHeartbeat) {
			best = n
			bestInfo = info
		}
	}
	return bestInfo
}

// parseLabels extracts the loom.daemon.* labels from a Node's Labels
// slice. Unparseable values (non-int PIDs, negative PIDs) are treated
// as absent rather than failing the whole detection.
func parseLabels(labels []string) (pid int, cwd, socket string) {
	for _, label := range labels {
		switch {
		case strings.HasPrefix(label, LabelPID):
			raw := strings.TrimPrefix(label, LabelPID)
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				continue
			}
			pid = n
		case strings.HasPrefix(label, LabelCwd):
			cwd = strings.TrimPrefix(label, LabelCwd)
		case strings.HasPrefix(label, LabelSocket):
			socket = strings.TrimPrefix(label, LabelSocket)
		}
	}
	return pid, cwd, socket
}

// hostnameMatchesLocal returns true when the NodeID
// (form: "loom-supervisor-<host>-<pid>") was registered on this host.
// If the NodeID cannot be parsed or the local hostname is unknown we
// return false, meaning the caller should trust the heartbeat alone
// without probing the PID locally.
func hostnameMatchesLocal(nodeID, localHost string) bool {
	if localHost == "" || nodeID == "" {
		return false
	}
	const prefix = "loom-supervisor-"
	if !strings.HasPrefix(nodeID, prefix) {
		return false
	}
	rest := strings.TrimPrefix(nodeID, prefix)
	// rest is "<host>-<pid>"; the host itself may contain hyphens,
	// so trim the final "-<digits>" suffix to recover the host part.
	idx := strings.LastIndex(rest, "-")
	if idx < 0 {
		return false
	}
	host := rest[:idx]
	return host == localHost
}
