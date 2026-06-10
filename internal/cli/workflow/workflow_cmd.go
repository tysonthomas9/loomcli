// Package workflow registers the `loom workflow` noun-verb commands
// for the dynamic workflow runner: local dev supervision of the Flue
// execution plane, run admission, and run inspection. All state shown
// here is read from fleet-db's platform API.
package workflow

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/infra/platformdb"
	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

var workflowCmd = &cobra.Command{
	Use:     "workflow",
	Short:   "Run and inspect dynamic workflows (epic runners)",
	GroupID: "agents",
	Long: `Dynamic workflows are TypeScript programs executed by a local Flue
server and recorded in fleet-db. 'loom workflow dev' supervises the
Flue child for a workflow project; the loom daemon watches fleet-db
and drives epic-runner wakes against it.`,
}

func init() {
	initDevFlags()
	initRunFlags()
	initRunsFlags()
	initLogsFlags()
	workflowCmd.AddCommand(devCmd, runCmd, runsCmd, logsCmd)
	cli.RegisterCommand(workflowCmd)
}

// withPlatform opens the store handle (which resolves or boots the
// embedded fleet-db), then hands a platform client + workspace to fn.
func withPlatform(fn func(ctx context.Context, pc *platformdb.Client, ws string) error) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		pc, err := platformdb.New(platformdb.Config{BaseURL: h.URL(), Actor: "loom-cli"})
		if err != nil {
			return err
		}
		return fn(ctx, pc, ws)
	})
}

// activeDriverVersion resolves the driver's active version, with a
// actionable error when the driver has never been stamped.
func activeDriverVersion(ctx context.Context, pc *platformdb.Client, ws, driverName string) (*platform.Driver, error) {
	d, err := pc.Drivers().Get(ctx, ws, driverName)
	if err != nil {
		return nil, fmt.Errorf("driver %q is not registered in workspace %s — start `loom workflow dev` (with the daemon running) so a dev version is stamped, or register one: %w", driverName, ws, err)
	}
	if d.ActiveVersionID == "" {
		return nil, fmt.Errorf("driver %q has no active version — start `loom workflow dev` so a dev version is stamped", driverName)
	}
	return d, nil
}
