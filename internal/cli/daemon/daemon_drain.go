package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// defaultYieldTTL bounds a drain that did not ask for a deadline of its own.
// A drain also lapses when the supervisor it was addressed to restarts, so the
// TTL is only the backstop for a supervisor that stays up.
const defaultYieldTTL = 2 * time.Hour

// parkedReportEveryNTicks throttles the "agent parked" warning on the 30s
// config poll to roughly one line per five minutes per agent. Startup logs
// unconditionally, so a park is never silent — only repetitive.
const parkedReportEveryNTicks = 10

// maxDrainReconcileWrites caps the PATCHes one startup reconciliation may
// issue. reconcileStaleDrains sits on the NewDaemon critical path, so its
// total work has to stay bounded regardless of how large the fleet is.
const maxDrainReconcileWrites = 64

// ParkedAgent is an agent the daemon deliberately did not claim. Parked agents
// are not in sup.Agents, so without carrying them explicitly they vanish from
// daemon-agents.json and `loom daemon status` — which is what made a fleet-wide
// park invisible for hours.
type ParkedAgent struct {
	Worktree       string
	Role           string
	DesiredState   domain.AgentDesiredState
	DrainExpiresAt *time.Time
	ResumeCommand  string
}

// resumeCommandFor is the operator's way out of a park, carried in the log
// line and the status output so it never has to be reconstructed by hand.
func resumeCommandFor(name string) string {
	return "loom data agent start " + name
}

func newParkedAgent(entry cfgpkg.AgentEntry) ParkedAgent {
	return ParkedAgent{
		Worktree:       entry.Worktree,
		Role:           entry.Role,
		DesiredState:   entry.DesiredState,
		DrainExpiresAt: entry.DrainExpiresAt,
		ResumeCommand:  resumeCommandFor(entry.Worktree),
	}
}

// parseYieldTTL converts the wire's ttl_seconds into a drain duration.
//
// Empty, unparseable and negative input all fall back to defaultYieldTTL: a
// malformed TTL must not silently become a permanent park, which is the exact
// failure this whole change exists to remove. "0" is meaningful and distinct —
// it means "no expiry, rely on node-ID supersession alone" (--until-restart).
func parseYieldTTL(raw string) time.Duration {
	if raw == "" {
		return defaultYieldTTL
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs < 0 {
		return defaultYieldTTL
	}
	return time.Duration(secs) * time.Second
}

// yieldTTLFromArgs pulls ttl_seconds out of a control-socket args blob.
//
// The value is carried as a string to match the agent-command payload map,
// where every value is a string. Malformed or absent args fall back to the
// default TTL rather than erroring: a yield with a bad TTL is still a yield.
func yieldTTLFromArgs(rawArgs json.RawMessage) time.Duration {
	if len(rawArgs) == 0 {
		return defaultYieldTTL
	}
	var args struct {
		TTLSeconds string `json:"ttl_seconds"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return defaultYieldTTL
	}
	return parseYieldTTL(args.TTLSeconds)
}

// markAgentYieldAccepted stamps the drain in the store: desired_state=draining
// plus the node ID this supervisor answers to and, unless the TTL is zero, an
// expiry.
//
// It is called before any "is the agent actually running" check, so a yield
// aimed at an agent with no live process still records intent. Without that,
// a yield to a stopped agent used to be a no-op that reported success.
func (d *Daemon) markAgentYieldAccepted(name string, ttl time.Duration) {
	desired := domain.AgentDesiredDraining
	nodeID := d.sup.ResolveNodeID()
	patch := store.AgentUpdate{DesiredState: &desired, DrainNodeID: &nodeID}
	var expires *time.Time
	if ttl > 0 {
		t := time.Now().UTC().Add(ttl)
		expires = &t
		patch.DrainExpiresAt = &t
	}

	if d.store != nil && d.sup.WorkspaceID != "" && d.store.Agents() != nil {
		ctx, cancel := context.WithTimeout(context.Background(), agentCommandPollTimeout)
		defer cancel()
		if _, err := d.store.Agents().Update(ctx, d.sup.WorkspaceID, name, patch); err != nil {
			// The local mirror below still parks the agent, so a failed stamp
			// degrades to a drain this supervisor honors until it restarts
			// rather than to no drain at all.
			slog.Warn("agent drain stamp failed", "worktree", name, "err", err)
		}
	}

	d.setConfigAgentDrain(name, nodeID, expires)
}

// setConfigAgentDrain mirrors the drain stamp into the in-memory config so the
// supervision predicate sees it immediately, rather than after the next 30s
// config poll.
func (d *Daemon) setConfigAgentDrain(name, nodeID string, expires *time.Time) {
	d.reconcileMu.Lock()
	defer d.reconcileMu.Unlock()
	if d.config == nil {
		return
	}
	for i := range d.config.Agents {
		if d.config.Agents[i].Worktree == name {
			d.config.Agents[i].DesiredState = domain.AgentDesiredDraining
			d.config.Agents[i].DrainNodeID = nodeID
			d.config.Agents[i].DrainExpiresAt = expires
			d.configHash = computeConfigHash(d.config)
			return
		}
	}
}

// reconcileStaleDrains releases drains that no longer belong to anyone before
// the agent list is built.
//
// It runs once, on the NewDaemon critical path, and only there: clearing on the
// 30s reconcile tick would race a yield issued seconds earlier and undo it.
// Every failure mode here is non-fatal — a store that is nil, absent or erroring
// leaves the agent parked, which is the pre-change behavior and safe. It must
// never fail NewDaemon.
func reconcileStaleDrains(sup *supervisor.Supervisor, st store.Store, config *cfgpkg.DaemonConfig) {
	if config == nil || sup == nil {
		return
	}
	currentNodeID := sup.ResolveNodeID()
	now := time.Now().UTC()
	writes := 0

	for i := range config.Agents {
		entry := &config.Agents[i]
		decision := domain.ResolveDrain(entry.DesiredState, entry.DrainNodeID, entry.DrainExpiresAt, currentNodeID, now)
		if !domain.DrainClearableAtStartup(decision) {
			continue
		}
		slog.Warn("releasing stale agent drain",
			"worktree", entry.Worktree,
			"reason", decision.String(),
			"drain_node_id", entry.DrainNodeID,
			"current_node_id", currentNodeID,
			"drain_expires_at", formatDrainExpiry(entry.DrainExpiresAt))

		// The in-memory clear is the load-bearing half: initSupervisorAgents
		// reads this config, not the store. The PATCH keeps fleet-db honest so
		// the next reader does not see a drain that is already released.
		entry.DesiredState = ""
		entry.DrainNodeID = ""
		entry.DrainExpiresAt = nil

		if writes >= maxDrainReconcileWrites {
			continue
		}
		if clearDrainInStore(sup, st, entry.Worktree) {
			writes++
		}
	}
}

// clearDrainInStore PATCHes one released drain, reporting whether a request was
// actually issued. desired_state=idle is what does the clearing: fleet-db
// derives the drain-field reset from any desired_state that is not "draining".
func clearDrainInStore(sup *supervisor.Supervisor, st store.Store, name string) bool {
	if st == nil || sup.WorkspaceID == "" || st.Agents() == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentCommandPollTimeout)
	defer cancel()
	desired := domain.AgentDesiredIdle
	if _, err := st.Agents().Update(ctx, sup.WorkspaceID, name, store.AgentUpdate{DesiredState: &desired}); err != nil {
		slog.Warn("clearing stale agent drain in store failed", "worktree", name, "err", err)
	}
	return true
}

// ParkedAgents returns the agents this daemon is deliberately not claiming,
// recomputed from the live config so a yield issued after startup shows up.
// Falls back to the startup snapshot when no config is available.
func (d *Daemon) ParkedAgents() []ParkedAgent {
	cfg := d.configSnapshot()
	if cfg == nil {
		return d.parked
	}
	currentNodeID := d.sup.ResolveNodeID()
	now := time.Now().UTC()
	var out []ParkedAgent
	for _, entry := range cfg.Agents {
		if entry.ShouldSuperviseWithRoles(cfg.Roles, currentNodeID, now) {
			continue
		}
		out = append(out, newParkedAgent(entry))
	}
	return out
}

// toDaemonAgentStatus renders a parked agent for the state file. It has no PID,
// no run history and no supervised process — only the reason it is parked and
// the command that ends the park.
func (p ParkedAgent) toDaemonAgentStatus() DaemonAgentStatus {
	return DaemonAgentStatus{
		Worktree:       p.Worktree,
		Role:           p.Role,
		Status:         "parked",
		DesiredState:   string(p.DesiredState),
		DrainExpiresAt: p.DrainExpiresAt,
		ResumeCommand:  p.ResumeCommand,
	}
}

// reportParkedAgents warns about every agent the daemon is deliberately not
// claiming. Called from the existing 30s config poll, it throttles to roughly
// one line per agent per five minutes — throttled, but never silent.
func (d *Daemon) reportParkedAgents() {
	d.parkedTicks++
	if d.parkedTicks%parkedReportEveryNTicks != 1 {
		return
	}
	cfg := d.configSnapshot()
	if cfg == nil {
		return
	}
	currentNodeID := d.sup.ResolveNodeID()
	now := time.Now().UTC()
	// One deadline for the whole cycle, not one per agent: this runs on the
	// 30s config-poll goroutine, and a per-agent timeout would let a slow
	// backend stall the poll loop for timeout x parked-agent-count. Once the
	// budget is spent the remaining lines are logged without a count.
	ctx, cancel := context.WithTimeout(context.Background(), agentCommandPollTimeout)
	defer cancel()
	for _, entry := range cfg.Agents {
		if entry.ShouldSuperviseWithRoles(cfg.Roles, currentNodeID, now) {
			continue
		}
		logParkedAgent(newParkedAgent(entry), d.readyTaskCount(ctx, entry))
	}
}

// readyTaskCount reports how much work the parked agent is sitting on, which is
// what turns "an agent is parked" into "an agent is parked on 12 ready tasks".
// A count of -1 means the query failed; the line is still logged without it.
func (d *Daemon) readyTaskCount(ctx context.Context, entry cfgpkg.AgentEntry) int {
	if d.issueBackend == nil || ctx.Err() != nil {
		return -1
	}
	issues, err := d.issueBackend.Ready(ctx, backend.ReadyOpts{ParentID: entry.Parent})
	if err != nil {
		return -1
	}
	return len(issues)
}

// logParkedAgent emits the one canonical parked line. A failed ready-task query
// drops the count and adds err=, but never drops the line: the whole point is
// that a park leaves evidence.
func logParkedAgent(p ParkedAgent, readyTasks int) {
	attrs := []any{
		"worktree", p.Worktree,
		"desired_state", string(p.DesiredState),
	}
	if readyTasks >= 0 {
		attrs = append(attrs, "ready_tasks", readyTasks)
	} else {
		attrs = append(attrs, "err", "ready task count unavailable")
	}
	if p.DrainExpiresAt != nil {
		attrs = append(attrs, "drain_expires_at", formatDrainExpiry(p.DrainExpiresAt))
	}
	attrs = append(attrs, "resume", p.ResumeCommand)
	slog.Warn("agent parked: not claiming", attrs...)
}

// formatDrainExpiry renders an optional expiry for logs, so a missing deadline
// reads as an empty value rather than a pointer address.
func formatDrainExpiry(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
