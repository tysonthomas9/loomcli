package daemon

import (
	"log/slog"
	"strings"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
)

// unavailableReportInterval is how many config-poll ticks pass between ERROR
// summaries while agents are still unavailable. At the 30s poll interval this
// is roughly five minutes, which keeps a permanently broken entry visible in
// the log without drowning it.
const unavailableReportInterval = 10

// UnavailableAgent is a configured agent the daemon could not construct at boot:
// its worktree does not resolve, or its role config does not. It is NOT in
// sup.Agents, so it is carried here to reach the state file and `loom daemon
// status` instead of disappearing.
type UnavailableAgent struct {
	Worktree string
	Role     string
	Repo     string
	Reason   string // the error text from NewAgent
	Hint     string // operator next step
	Since    time.Time
}

// newUnavailableAgent records a per-agent construction failure. The hint is
// chosen from the error text because NewAgent's two failure modes need
// different recovery steps and neither is distinguishable by type: worktree
// resolution wraps the entry as `agent[N] worktree "name": ...`, while role
// resolution wraps as `agent[N]: ...`.
func newUnavailableAgent(entry cfgpkg.AgentEntry, err error) UnavailableAgent {
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	return UnavailableAgent{
		Worktree: entry.Worktree,
		Role:     entry.Role,
		Repo:     entry.Repo,
		Reason:   reason,
		Hint:     unavailableHint(entry, reason),
		Since:    time.Now(),
	}
}

// unavailableHint is the one-line recovery command printed under every
// unavailable row. Keep it to one line — it renders per agent.
func unavailableHint(entry cfgpkg.AgentEntry, reason string) string {
	if strings.Contains(reason, "worktree") {
		repos := entry.Repo
		if repos == "" {
			repos = strings.Join(entry.Repos, ",")
		}
		return "create the worktree, or re-register it: loom agentdef add " +
			entry.Worktree + " --role " + entry.Role + " --repos " + repos
	}
	return "role " + entry.Role + " does not resolve: check its prompt file and the roles block in the daemon config"
}

// toDaemonAgentStatus projects an unavailable agent into the state-file shape.
// There is no process behind it, so PID stays 0 and every run-history field is
// left zero; the reason and hint carry the whole story.
func (u UnavailableAgent) toDaemonAgentStatus() DaemonAgentStatus {
	return DaemonAgentStatus{
		Worktree: u.Worktree,
		Role:     u.Role,
		Repo:     u.Repo,
		PID:      0,
		Status:   "unavailable",
		Detail:   u.Reason,
		Hint:     u.Hint,
	}
}

// UnavailableAgents returns a copy of the agents the daemon could not construct.
func (d *Daemon) UnavailableAgents() []UnavailableAgent {
	d.unavailableMu.Lock()
	defer d.unavailableMu.Unlock()
	return append([]UnavailableAgent(nil), d.unavailable...)
}

// tickUnavailableAgents is the per-config-poll work on the unavailable list.
// It runs on EVERY tick, including the unchanged-config early return: a
// worktree appearing on disk changes no config hash, and that is exactly the
// recovery this is here to catch. Fleet mode suppresses local supervision, so
// only the reporting half runs there.
func (d *Daemon) tickUnavailableAgents(fleetMode bool) {
	if !fleetMode {
		d.retryUnavailableAgents()
	}
	d.reportUnavailableAgents()
}

// retryUnavailableAgents re-attempts construction of every still-unavailable
// agent that is present in the current config snapshot. This is the self-healing
// path: a worktree created after the daemon booted changes no config hash, so
// nothing else would ever notice it appeared.
//
// Takes drainAddMu — the same lock applyAgentChanges uses — so a retry cannot
// race a drain or an add.
func (d *Daemon) retryUnavailableAgents() {
	pending := d.UnavailableAgents()
	if len(pending) == 0 {
		return
	}

	entries, roles := d.supervisableEntries()

	d.drainAddMu.Lock()
	defer d.drainAddMu.Unlock()

	kept := make([]UnavailableAgent, 0, len(pending))
	for _, u := range pending {
		entry, ok := entries[u.Worktree]
		if !ok || !entry.ShouldSuperviseWithRoles(roles) {
			// Gone from the config (or parked) since boot: stop reporting it.
			continue
		}
		if d.isAgentStopped(u.Worktree) {
			kept = append(kept, u)
			continue
		}
		if err := d.sup.AddAgent(entry); err != nil {
			u.Reason = err.Error()
			kept = append(kept, u)
			continue
		}
		slog.Info("agent recovered, now supervised", "worktree", u.Worktree, "role", u.Role)
	}

	d.unavailableMu.Lock()
	d.unavailable = kept
	d.unavailableMu.Unlock()
}

// supervisableEntries snapshots the current config's agent entries by name,
// alongside the roles map needed to evaluate desired state.
func (d *Daemon) supervisableEntries() (map[string]cfgpkg.AgentEntry, map[string]cfgpkg.RoleConfig) {
	cfg := d.configSnapshot()
	if cfg == nil {
		return map[string]cfgpkg.AgentEntry{}, nil
	}
	entries := make(map[string]cfgpkg.AgentEntry, len(cfg.Agents))
	for _, e := range cfg.Agents {
		entries[e.Worktree] = e
	}
	return entries, cfg.Roles
}

// reportUnavailableAgents logs the throttled ERROR summary. Self-throttling via
// a tick counter: the first call after agents go unavailable reports, then one
// in every unavailableReportInterval calls until the list empties.
func (d *Daemon) reportUnavailableAgents() {
	d.unavailableMu.Lock()
	if len(d.unavailable) == 0 {
		d.unavailableReportTick = 0
		d.unavailableMu.Unlock()
		return
	}
	tick := d.unavailableReportTick
	d.unavailableReportTick++
	count := len(d.unavailable)
	names := make([]string, 0, count)
	for _, u := range d.unavailable {
		names = append(names, u.Worktree)
	}
	d.unavailableMu.Unlock()

	if tick%unavailableReportInterval != 0 {
		return
	}
	slog.Error("agents unavailable: not supervised",
		"count", count, "worktrees", strings.Join(names, ","))
}

// recordUnavailableAgent adds or refreshes an entry in the unavailable list.
func (d *Daemon) recordUnavailableAgent(entry cfgpkg.AgentEntry, err error) {
	u := newUnavailableAgent(entry, err)
	d.unavailableMu.Lock()
	defer d.unavailableMu.Unlock()
	for i := range d.unavailable {
		if d.unavailable[i].Worktree == u.Worktree {
			d.unavailable[i].Reason = u.Reason
			d.unavailable[i].Hint = u.Hint
			return
		}
	}
	d.unavailable = append(d.unavailable, u)
}
