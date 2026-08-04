package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/notify"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Daemon coordinates multiple supervised agents.
type Daemon struct {
	config     *cfgpkg.DaemonConfig
	projectDir string // workspace runtime directory

	// sup is the agent supervisor — owns the agent list, supervision goroutines,
	// health checking, restart policy, and drain logic.
	sup *supervisor.Supervisor

	configHash  string       // SHA-256 hash of current running config for no-op detection
	reconcileMu sync.RWMutex // serializes config writes; readers hold RLock when accessing d.config

	// drainAddMu serializes the drain/add phase of reconciliation.
	// Separate from reconcileMu because drainAgent blocks on <-target.Done,
	// which requires superviseAgent to call configSnapshot() under
	// reconcileMu.RLock — holding reconcileMu.Lock through drain would deadlock.
	// Lock ordering: reconcileMu (released) -> drainAddMu -> agentsMu.
	drainAddMu sync.Mutex

	// controlListener is the Unix domain socket listener for the control server.
	// Set by startControlServer, closed on Stop.
	controlListener net.Listener

	// ipcListener is the Unix domain socket listener for the agent IPC server.
	// Set by startIPCServer, closed on Stop.
	ipcListener net.Listener

	// notifyBus publishes IPC mutation events for real-time consumers.
	// Defaults to NopPublisher; set to a real Bus in runDaemon().
	notifyBus notify.Publisher

	// mutBuf accumulates IPC mutations from notifyBus for control socket queries.
	// Nil when no real Bus is wired (e.g., tests, non-daemon mode).
	mutBuf *MutationBuffer

	issueBackend backend.IssueBackend // pluggable issue data access

	// store is the fleet-db backed source of agent assignments and daemon
	// profile data.
	store       store.Store
	storeHandle *bootstrap.StoreHandle
}

// configSnapshot returns a snapshot of the current config pointer under RLock.
// Safe for concurrent use with reloadAndReconcile which swaps d.config under Lock.
func (d *Daemon) configSnapshot() *cfgpkg.DaemonConfig {
	d.reconcileMu.RLock()
	cfg := d.config
	d.reconcileMu.RUnlock()
	return cfg
}

// emitEvent is a convenience helper that emits an event via the supervisor's event bus.
func (d *Daemon) emitEvent(evt events.Event) {
	d.sup.EmitEvent(evt)
}

// NewDaemon creates a daemon from the loaded config.
// If eventBus is nil, a NopBus is used (events are silently discarded).
// issueBackend provides issue data access for epic transition checks.
func NewDaemon(config *cfgpkg.DaemonConfig, projectDir string, eventBus events.Emitter, issueBackend backend.IssueBackend, st store.Store) (*Daemon, error) {
	if config == nil {
		return nil, fmt.Errorf("daemon config is nil")
	}

	if eventBus == nil {
		eventBus = events.NopBus{}
	}

	d := &Daemon{
		config:       config,
		projectDir:   projectDir,
		notifyBus:    notify.NopPublisher{},
		issueBackend: issueBackend,
		store:        st,
	}

	// Build the supervisor
	sup := &supervisor.Supervisor{
		ConfigSnapshot: d.configSnapshot,
		ProjectDir:     projectDir,
		Concurrency:    supervisor.NewConcurrencyTracker(config.Roles),
		EventBus:       eventBus,
		StoppedAgents:  make(map[string]struct{}),
		Agents:         make([]*supervisor.AgentProcess, 0, len(config.Agents)),
		ControlStore:   st,
		IssueBackend:   issueBackend,
	}

	wireSupervisorCallbacks(sup, issueBackend)
	loadSupervisorWorkspace(sup)

	if err := initSupervisorAgents(sup, config.Agents, config.Roles); err != nil {
		return nil, err
	}

	d.sup = sup

	return d, nil
}

// Start launches the supervisor.
func (d *Daemon) Start() error {
	// Compute initial config hash for reconciler no-op detection.
	d.reconcileMu.Lock()
	d.configHash = computeConfigHash(d.config)
	d.reconcileMu.Unlock()

	// Supervisor.Start() initializes Shutdown and FatalCh; we need both before
	// launching the daemon-owned reconciler goroutine. Start the supervisor
	// first, then attach the reconciler under the same crash-loud harness.
	if err := d.sup.Start(); err != nil {
		return err
	}
	d.sup.RegisterTick(supervisor.GoroutineConfigReconciler)
	d.sup.RunCritical(supervisor.GoroutineConfigReconciler, d.configReconciler)

	d.startAgentCommandPoller()
	return nil
}

// Stop gracefully shuts down the daemon.
func (d *Daemon) Stop() {
	// Close the control socket listener (if running)
	if d.controlListener != nil {
		_ = d.controlListener.Close()
	}

	// Close the agent IPC socket listener (if running)
	if d.ipcListener != nil {
		_ = d.ipcListener.Close()
	}

	// Stop mutation buffer (drains subscription goroutine)
	if d.mutBuf != nil {
		d.mutBuf.Stop()
	}

	// Close notification bus (closes subscriber channels, no-ops if NopPublisher)
	if bus, ok := d.notifyBus.(*notify.Bus); ok {
		bus.Close()
	}

	d.sup.Stop()

	if d.storeHandle != nil {
		_ = d.storeHandle.Close()
		d.storeHandle = nil
	}
}

// Agents returns a snapshot of all agent statuses for inspection.
func (d *Daemon) Agents() []supervisor.SupervisedAgentStatus {
	return d.sup.GetAgents()
}

// AgentCount returns the number of configured agents.
func (d *Daemon) AgentCount() int {
	return d.sup.AgentCount()
}

// QuarantinedTasks returns the supervisor's quarantined-task snapshot for
// state-file and status surfacing.
func (d *Daemon) QuarantinedTasks() []supervisor.QuarantinedTaskInfo {
	return d.sup.QuarantinedTasks()
}

// isAgentStopped returns true if the named agent was stopped via the control socket.
func (d *Daemon) isAgentStopped(name string) bool {
	d.sup.AgentsMu.RLock()
	_, stopped := d.sup.StoppedAgents[name]
	d.sup.AgentsMu.RUnlock()
	return stopped
}

// isAgentRunning returns true if the named agent has a running superviseAgent goroutine.
func (d *Daemon) isAgentRunning(name string) bool {
	d.sup.AgentsMu.RLock()
	defer d.sup.AgentsMu.RUnlock()
	for _, ap := range d.sup.Agents {
		if ap.Entry.Worktree == name {
			return true
		}
	}
	return false
}

// agentExistsInConfig returns true if an agent with the given name
// exists in the current config. When a fleet-db Store is wired in, also checks
// store.Agents() so reconciles stay correct after store CRUD writes.
func (d *Daemon) agentExistsInConfig(name string) bool {
	cfg := d.configSnapshot()
	for _, agent := range cfg.Agents {
		if agent.Worktree == name {
			return true
		}
	}
	if d.store != nil {
		if d.agentExistsInStore(name) {
			return true
		}
	}
	return false
}

// agentExistsInStore reports whether the named agent is registered in
// the store for the daemon's workspace.
func (d *Daemon) agentExistsInStore(name string) bool {
	if d.store == nil || d.sup == nil || d.sup.WorkspaceID == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := d.store.Agents().Get(ctx, d.sup.WorkspaceID, name); err == nil {
		return true
	}
	return false
}

// findAgentEntry looks up a config.AgentEntry by worktree name in the current config.
func (d *Daemon) findAgentEntry(name string) (cfgpkg.AgentEntry, bool) {
	cfg := d.configSnapshot()
	for _, agent := range cfg.Agents {
		if agent.Worktree == name {
			return agent, true
		}
	}
	return cfgpkg.AgentEntry{}, false
}

// publishMutation publishes a mutation event to the daemon's notification bus.
func (d *Daemon) publishMutation(m backend.MutationData) {
	if d.notifyBus == nil {
		return
	}
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now()
	}
	d.notifyBus.Publish(notify.Event{
		Topic:       "issue." + m.Type,
		WorkspaceID: d.sup.WorkspaceID,
		Payload:     m,
		Timestamp:   m.Timestamp,
	})
}

// wireSupervisorCallbacks sets up the function callbacks on the supervisor.
func wireSupervisorCallbacks(sup *supervisor.Supervisor, issueBackend backend.IssueBackend) {
	sup.EmitEvent = func(evt events.Event) {
		if sup.EventBus == nil {
			return
		}
		if err := sup.EventBus.Emit(evt); err != nil {
			slog.Warn("failed to emit event", "event_type", evt.Type, "err", err)
		}
	}
	sup.FindRepoConfig = func(repoName string) *cfgpkg.RepoConfig {
		if repoName == "" {
			return nil
		}
		for i := range sup.Repos {
			if sup.Repos[i].Name == repoName {
				return &sup.Repos[i]
			}
		}
		return nil
	}
	sup.IssueBackendReady = func(epicID string) (bool, error) {
		issues, err := issueBackend.Ready(cmdstore.RootContext(), backend.ReadyOpts{
			ParentID: epicID,
			Limit:    1,
		})
		if err != nil {
			return false, fmt.Errorf("failed to check ready tasks for epic %s: %w", epicID, err)
		}
		return len(issues) > 0, nil
	}
}

// loadSupervisorWorkspace loads workspace repos and ID for source repo resolution.
func loadSupervisorWorkspace(sup *supervisor.Supervisor) {
	if ws, err := cfgpkg.ResolveActiveWorkspace(); err == nil && ws != nil {
		sup.Repos = ws.Repos
		sup.WorkspaceID = ws.ID
	}
}

// initSupervisorAgents creates agent processes from config entries.
func initSupervisorAgents(sup *supervisor.Supervisor, agents []cfgpkg.AgentEntry, roles map[string]cfgpkg.RoleConfig) error {
	for i, entry := range agents {
		if !entry.ShouldSuperviseWithRoles(roles) {
			slog.Info("skipping agent with non-running desired state", "worktree", entry.Worktree, "desired_state", entry.DesiredState)
			continue
		}
		ap, err := sup.NewAgent(entry, i)
		if err != nil {
			return err
		}
		sup.Agents = append(sup.Agents, ap)
	}
	return nil
}
