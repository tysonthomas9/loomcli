package stack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/stackstore"
)

func init() { stackCmd.AddCommand(migrateCmd()) }

func migrateCmd() *cobra.Command {
	var dryRun, jsonOut bool
	var fromDir string
	c := &cobra.Command{
		Use:   "migrate",
		Short: "Import this workspace's machine-local stacks.json lineage into the canonical stack store",
		Long: `Import stack lineage and publish state from the machine-local stacks.json
into the canonical (FleetDB) stack store for the active workspace.

Every stack is planned before anything is written. If any local stack
disagrees with the destination (different header, node lineage or publish
state, a task already in another stack, or an output branch that would be
reassigned) the whole run is refused and nothing is written. Stacks already
imported are skipped, so re-running is safe. The local file is never
modified; a timestamped backup is written beside it before the first write.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrate(cmd, fromDir, dryRun, jsonOut)
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "plan the migration without writing a backup or the destination")
	c.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	c.Flags().StringVar(&fromDir, "from", "", "directory holding the stacks.json to import (default: the loom directory)")
	return c
}

func runMigrate(cmd *cobra.Command, fromDir string, dryRun, jsonOut bool) error {
	ws, err := activeWorkspace()
	if err != nil {
		return err
	}
	src, err := stackstore.Default()
	if strings.TrimSpace(fromDir) != "" {
		src, err = stackstore.New(fromDir), nil
	}
	if err != nil {
		return err
	}
	dst, err := openStore()
	if err != nil {
		return err
	}
	rep, err := stackstore.Migrate(cmd.Context(), src, dst, stackstore.MigrateOptions{Workspace: ws, DryRun: dryRun})
	if errors.Is(err, stackstore.ErrMigrateSameStore) {
		return fmt.Errorf("%w: the canonical stack store is still the local file, so there is nothing to migrate", err)
	}
	if rep != nil {
		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if encErr := enc.Encode(rep); encErr != nil {
				return encErr
			}
		} else {
			fmt.Print(formatMigrateReport(rep))
		}
	}
	return err
}

func formatMigrateReport(rep *stackstore.MigrateReport) string {
	var w strings.Builder
	mode := ""
	if rep.DryRun {
		mode = " (dry run)"
	}
	fmt.Fprintf(&w, "migrate %s → canonical stack store, workspace %s%s\n", rep.Source, rep.Workspace, mode)
	if rep.SourceMissing {
		fmt.Fprintln(&w, "  no local stacks.json; nothing to migrate")
		return w.String()
	}
	if len(rep.Stacks) == 0 {
		fmt.Fprintln(&w, "  no local stacks for this workspace")
	}
	for _, p := range rep.Stacks {
		line := fmt.Sprintf("  %-10s %s  repo=%s", p.Action, p.StackID, p.RepoName)
		if len(p.AddNodes) > 0 {
			line += "  add=" + strings.Join(p.AddNodes, ",")
		}
		fmt.Fprintln(&w, line)
		for _, c := range p.Conflicts {
			fmt.Fprintf(&w, "             ! %s\n", c)
		}
	}
	if len(rep.OtherWorkspaces) > 0 {
		fmt.Fprintf(&w, "  other workspaces in the file (not migrated by this run): %s\n", strings.Join(rep.OtherWorkspaces, ", "))
	}
	if rep.Backup != "" {
		fmt.Fprintf(&w, "  backup: %s\n", rep.Backup)
	}
	switch {
	case rep.Conflicted():
		fmt.Fprintln(&w, "refused: resolve the conflicts above; nothing was written")
	case rep.Applied:
		fmt.Fprintln(&w, "migrated")
	}
	return w.String()
}
