package daemon

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/daemon/supervisor"
	"github.com/tysonthomas9/loomcli/internal/infra/platformdb"
	"github.com/tysonthomas9/loomcli/internal/workflows"
	flueplane "github.com/tysonthomas9/loomcli/internal/workflows/execplane/flue"
)

// flueDiscoveryInterval is how often the workflow runner re-checks for
// a Flue runtime while none is available.
const flueDiscoveryInterval = 5 * time.Second

// workflowNodeID is this control plane's stable identity for DriverRun
// claims. It must survive restarts so the reconciler can recognize (and
// fail over) runs orphaned by its previous incarnation.
func workflowNodeID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return "loom-daemon-" + host
}

// startWorkflowRunner launches the dynamic-workflow service: it waits
// for an execution plane (the Flue child recorded in flue/runtime.json,
// started by `loom workflow dev`), then runs the epic reconciler
// against it. Fleet-db being the data plane is a precondition
// (fleetURL non-empty); without a workspace or store the runner stays
// off and the daemon behaves exactly as before.
func (d *Daemon) startWorkflowRunner(fleetURL string) {
	if fleetURL == "" || d.sup.WorkspaceID == "" {
		slog.Info("workflow runner disabled", "fleet_url_set", fleetURL != "", "workspace", d.sup.WorkspaceID)
		return
	}
	sup := d.sup
	sup.RegisterTick(supervisor.GoroutineWorkflowRunner)
	sup.RunCritical(supervisor.GoroutineWorkflowRunner, func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			<-sup.Shutdown
			cancel()
		}()
		d.workflowRunnerLoop(ctx, fleetURL)
	})
}

// workflowRunnerLoop keeps a reconciler running for as long as the
// daemon lives. Errors (fleet-db hiccups, Flue going away) degrade to
// retry — the workflow runner must never take the agent daemon down.
func (d *Daemon) workflowRunnerLoop(ctx context.Context, fleetURL string) {
	logger := slog.Default().With("component", "workflow-runner")
	for {
		if ctx.Err() != nil {
			return
		}
		d.sup.RecordTick(supervisor.GoroutineWorkflowRunner)

		flueURL, ok, err := bootstrap.ReuseFlueRuntime(ctx, bootstrap.LoomDir())
		if !ok {
			if err != nil {
				logger.Debug("flue runtime not usable yet", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(flueDiscoveryInterval):
			}
			continue
		}

		if err := d.runWorkflowReconciler(ctx, fleetURL, flueURL, logger); err != nil && ctx.Err() == nil {
			logger.Warn("workflow reconciler stopped; retrying", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(flueDiscoveryInterval):
			}
		}
	}
}

// runWorkflowReconciler builds and runs one reconciler session against
// the discovered Flue endpoint. Returns when ctx is done (nil) or on a
// startup error (retried by the loop).
func (d *Daemon) runWorkflowReconciler(ctx context.Context, fleetURL, flueURL string, logger *slog.Logger) error {
	store, err := platformdb.New(platformdb.Config{BaseURL: fleetURL, Actor: "loom-daemon"})
	if err != nil {
		return err
	}
	plane, err := flueplane.New(flueplane.Config{BaseURL: flueURL})
	if err != nil {
		return err
	}
	workspace := d.sup.WorkspaceID
	rec, err := workflows.NewEpicReconciler(workflows.EpicReconcilerConfig{
		Workspace:    workspace,
		NodeID:       workflowNodeID(),
		Store:        store,
		Plane:        plane,
		FleetBaseURL: fleetURL,
		Logger:       logger,
		Tick:         func() { d.sup.RecordTick(supervisor.GoroutineWorkflowRunner) },
		// Resolve the parent epic straight from fleet-db: the
		// IssueBackend detail conversion drops parent_id today, and the
		// reconciler must not silently lose wake signals to that.
		ResolveEpic: func(ctx context.Context, issueID string) (string, bool) {
			parent, err := store.IssueParent(ctx, workspace, issueID)
			if err != nil || parent == "" {
				return "", false
			}
			return parent, true
		},
	})
	if err != nil {
		return err
	}
	logger.Info("workflow reconciler starting", "flue_url", flueURL, "workspace", d.sup.WorkspaceID)
	return rec.Run(ctx)
}
