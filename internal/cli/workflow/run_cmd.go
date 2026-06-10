package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/infra/platformdb"
	"github.com/tysonthomas9/loomcli/internal/workflows"
	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

var (
	runEpicID      string
	runPayloadJSON string
)

var runCmd = &cobra.Command{
	Use:   "run <driver>",
	Short: "Request a workflow run (admission-checked)",
	Long: `Creates a DriverRun in fleet-db. The loom daemon's reconciler claims
and executes it against the Flue execution plane. For epic runs,
fleet-db enforces one active run per epic — a second request returns
the already-active run.`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowRun,
}

func initRunFlags() {
	runCmd.Flags().StringVar(&runEpicID, "epic", "", "Epic ID to run the driver against")
	runCmd.Flags().StringVar(&runPayloadJSON, "payload-json", "", "JSON payload recorded on the run")
}

func runWorkflowRun(_ *cobra.Command, args []string) error {
	driverName := args[0]
	return withPlatform(func(ctx context.Context, pc *platformdb.Client, ws string) error {
		driver, err := activeDriverVersion(ctx, pc, ws, driverName)
		if err != nil {
			return err
		}
		var payload json.RawMessage
		if runPayloadJSON != "" {
			if !json.Valid([]byte(runPayloadJSON)) {
				return fmt.Errorf("--payload-json is not valid JSON")
			}
			payload = json.RawMessage(runPayloadJSON)
		}
		requestedID := fmt.Sprintf("run-%s-%d", workflows.SanitizeID(runEpicID), time.Now().UnixNano())
		run, err := pc.DriverRuns().Create(ctx, ws, platform.DriverRunCreate{
			RunID:           requestedID,
			DriverID:        driver.DriverID,
			DriverVersionID: driver.ActiveVersionID,
			EpicID:          runEpicID,
			SourceKind:      "cli",
			Payload:         payload,
		})
		if err != nil {
			return fmt.Errorf("admission failed: %w", err)
		}
		if run.RunID != requestedID {
			fmt.Printf("Run not created: epic %s already has an active run.\n", runEpicID)
			fmt.Printf("Active run: %s (status: %s)\n", run.RunID, run.Status)
			return nil
		}
		fmt.Printf("Run %s queued (driver %s@%s", run.RunID, driver.DriverID, driver.ActiveVersionID)
		if runEpicID != "" {
			fmt.Printf(", epic %s", runEpicID)
		}
		fmt.Println(")")
		fmt.Printf("Follow it with: loom workflow logs %s --follow\n", run.RunID)
		return nil
	})
}
