// Package daemonregistry detects daemon liveness from the fleet-db
// Node registry. It is the cwd-independent third source of daemon
// liveness, alongside the lock-file and state-file based detection
// in package cli (DetectDaemonRuntime).
//
// The supervisor publishes itself as a Node on startup and heartbeats
// every 30 s; this package parses the loom.daemon.* labels the
// supervisor attaches and reports whether a live daemon is present
// for a given workspace. The registry is shared with non-supervisor
// local Nodes, so the loom.daemon.pid label is what distinguishes a
// supervisor advertisement from anything else that heartbeats there.
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
// LabelPID is the required one: Supervisor.daemonRuntimeLabels always
// emits it, and Detect treats it as the daemon's identity. LabelCwd
// and LabelSocket are optional metadata — a Node carrying only those
// is not a supervisor.
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
//  3. A valid loom.daemon.pid label (positive integer) must be
//     present — this is the daemon's identity on the wire. Other
//     local Nodes heartbeat into the same registry (notably the
//     `loom serve` task worker, which advertises only
//     loom-driver-executor / loom-task-worker), and without this
//     check any of them would masquerade as a live supervisor.
//     loom.daemon.cwd / loom.daemon.socket are optional metadata and
//     never establish identity on their own.
//  4. If the qualifying Node claims to be a supervisor on this host
//     — NodeID of the form "loom-supervisor-<localhost>-<pid>", i.e.
//     the prefix AND the hostname must both match (see
//     hostnameMatchesLocal) — lockfile.IsProcessRunning must report
//     the PID alive. This guards against ghost Node rows where the
//     daemon crashed mid-shutdown and the TTL hasn't elapsed yet.
//     Every other qualifying Node is trusted on its heartbeat alone:
//     a PID on another machine cannot be probed from here, and a
//     same-host Node using some other NodeID scheme is
//     indistinguishable from a remote one at this layer. Rule 3 is
//     what keeps that fallback narrow.
//
// Candidate filtering happens before most-recent-heartbeat selection,
// so a newer non-daemon Node can never displace or suppress a valid
// supervisor advertisement.
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
		// Daemon identity is mandatory: only a Node advertising a
		// valid supervisor PID counts. parseLabels reports zero for a
		// missing, empty, non-numeric, zero, or negative PID label, so
		// this one check rejects both generic local Nodes (the `loom
		// serve` task worker) and malformed daemon advertisements.
		if pid <= 0 {
			continue
		}
		// If the Node was registered by this host, require the PID to
		// be alive — defends against ghost rows.
		if hostnameMatchesLocal(n.NodeID, localHost) {
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
// slice. Unparseable values (non-int PIDs, zero/negative PIDs) are
// skipped rather than failing the whole detection, so a PID of zero is
// returned only when no label carried a usable one; because Detect
// requires pid > 0, that means the Node is not a supervisor.
//
// Skipping (rather than resetting) makes this last-valid-wins if a Node
// somehow carries several PID labels. daemonRuntimeLabels emits exactly
// one, so that case does not arise in practice.
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
