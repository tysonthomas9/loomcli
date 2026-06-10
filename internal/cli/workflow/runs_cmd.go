package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/infra/platformdb"
	"github.com/tysonthomas9/loomcli/internal/workflows/platform"
)

var (
	runsEpicID string
	runsStatus string
	runsLimit  int
	runsJSON   bool
)

var runsCmd = &cobra.Command{
	Use:   "runs [<driver>]",
	Short: "List workflow runs",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkflowRuns,
}

func initRunsFlags() {
	runsCmd.Flags().StringVar(&runsEpicID, "epic", "", "Filter by epic ID")
	runsCmd.Flags().StringVar(&runsStatus, "status", "", "Filter by status (queued|running|completed|failed|needs_review|cancelled)")
	runsCmd.Flags().IntVar(&runsLimit, "limit", 50, "Maximum runs to list")
	runsCmd.Flags().BoolVar(&runsJSON, "json", false, "JSON output")
}

func runWorkflowRuns(_ *cobra.Command, args []string) error {
	var driverName string
	if len(args) == 1 {
		driverName = args[0]
	}
	return withPlatform(func(ctx context.Context, pc *platformdb.Client, ws string) error {
		runs, err := pc.DriverRuns().List(ctx, ws, platform.DriverRunFilter{
			DriverID: driverName,
			EpicID:   runsEpicID,
			Status:   platform.DriverRunStatus(runsStatus),
			Limit:    runsLimit,
		})
		if err != nil {
			return err
		}
		if runsJSON {
			return json.NewEncoder(os.Stdout).Encode(runs)
		}
		if len(runs) == 0 {
			fmt.Println("No workflow runs found.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "RUN ID\tDRIVER\tEPIC\tSTATUS\tCREATED\tSUMMARY")
		for _, r := range runs {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
				r.RunID, r.DriverID, orDash(r.EpicID), r.Status,
				r.CreatedAt.Local().Format(time.RFC3339), truncate(r.Summary, 60))
		}
		return w.Flush()
	})
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
