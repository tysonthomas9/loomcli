package workflow

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/infra/platformdb"
	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

var logsFollow bool

var logsCmd = &cobra.Command{
	Use:   "logs <run-id>",
	Short: "Show a run's lifecycle events, summary, and output",
	Long: `Prints the run's fleet-db lifecycle events (create/claim/heartbeat/
finish) and, once terminal, its summary and captured output aggregates.
With --follow, polls until the run reaches a terminal status.`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkflowLogs,
}

func initLogsFlags() {
	logsCmd.Flags().BoolVar(&logsFollow, "follow", false, "Poll until the run is terminal")
}

func runWorkflowLogs(_ *cobra.Command, args []string) error {
	runID := args[0]
	return withPlatform(func(ctx context.Context, pc *platformdb.Client, ws string) error {
		cursor := "0"
		for {
			run, err := pc.DriverRuns().Get(ctx, ws, runID)
			if err != nil {
				return err
			}
			events, next, err := pc.DriverRuns().Events(ctx, ws, runID, cursor, 1000)
			if err != nil {
				return err
			}
			cursor = next
			for _, e := range events {
				fmt.Printf("%s  %-22s  %s\n", e.Timestamp.Local().Format("15:04:05.000"), e.Action, e.Actor)
			}
			if run.Status.Terminal() {
				printRunResult(run)
				return nil
			}
			if !logsFollow {
				fmt.Printf("status: %s (use --follow to wait for completion)\n", run.Status)
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	})
}

func printRunResult(run *platform.DriverRun) {
	fmt.Printf("status: %s\n", run.Status)
	if run.ErrorClass != "" {
		fmt.Printf("error class: %s\n", run.ErrorClass)
	}
	if run.Summary != "" {
		fmt.Printf("summary: %s\n", run.Summary)
	}
	for k, v := range run.Output {
		if v != "" {
			fmt.Printf("output.%s: %s\n", k, v)
		}
	}
}
