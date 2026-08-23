package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/workflows"
	"github.com/tysonthomas9/loomcli/internal/workflows/packaged"
)

var (
	workflowActivateBuiltin bool
	workflowActivateTrack   string
	workflowSyncJSON        bool
	workflowRollbackVersion string
	workflowRollbackJSON    bool
)

var workflowSyncCmd = &cobra.Command{
	Use:   "sync <workflow>",
	Short: "Sync a built-in workflow to this build's packaged version (register + track policy)",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowSync,
}

var workflowRollbackCmd = &cobra.Command{
	Use:   "rollback <workflow>",
	Short: "Roll back a workflow to a previous version",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkflowRollback,
}

func init() {
	workflowActivateCmd.Flags().BoolVar(&workflowActivateBuiltin, "builtin", false, "Activate this build's packaged built-in version and follow updates (auto track)")
	workflowActivateCmd.Flags().StringVar(&workflowActivateTrack, "track", "pinned", "Built-in track for --version activations: pinned|auto")

	workflowSyncCmd.Flags().BoolVar(&workflowSyncJSON, "json", false, "JSON output")
	workflowRollbackCmd.Flags().StringVar(&workflowRollbackVersion, "version", "", "Target DriverVersion id (default: recorded previous active version)")
	workflowRollbackCmd.Flags().BoolVar(&workflowRollbackJSON, "json", false, "JSON output")

	workflowCmd.AddCommand(workflowSyncCmd, workflowRollbackCmd)
}

// parseBuiltinTrack validates the --track value.
func parseBuiltinTrack(value string) (driverpkg.BuiltinTrack, error) {
	switch driverpkg.BuiltinTrack(value) {
	case driverpkg.BuiltinTrackAuto:
		return driverpkg.BuiltinTrackAuto, nil
	case driverpkg.BuiltinTrackPinned:
		return driverpkg.BuiltinTrackPinned, nil
	default:
		return "", fmt.Errorf("builtin_track_invalid: --track must be auto or pinned, got %q: %w", value, domain.ErrInvalid)
	}
}

func runWorkflowActivate(_ *cobra.Command, args []string) error {
	name := args[0]
	if workflowActivateBuiltin && workflowVersionID != "" {
		return fmt.Errorf("--builtin and --version are mutually exclusive: %w", domain.ErrInvalid)
	}
	if !workflowActivateBuiltin && workflowVersionID == "" {
		return fmt.Errorf("one of --version or --builtin is required: %w", domain.ErrInvalid)
	}
	if workflowActivateBuiltin {
		return runWorkflowActivateBuiltin(name)
	}
	track, err := parseBuiltinTrack(workflowActivateTrack)
	if err != nil {
		return err
	}
	versionID := workflowVersionID
	return workflowVersionAction(name, workflowActivateJSON, "activated", func(ctx context.Context, s store.Store, ws, driverID string) (*domain.Driver, *domain.DriverVersion, error) {
		if track == driverpkg.BuiltinTrackAuto {
			// --track auto is accepted on a plain --version activation only when
			// that version IS this build's packaged built-in version.
			info, derr := workflows.DescribeBuiltinVersions(ctx, s, ws, name)
			if derr != nil || info == nil || info.PackagedVersionID == "" || info.PackagedVersionID != versionID {
				return nil, nil, fmt.Errorf("builtin_track_invalid: --track auto requires %q to be this build's packaged built-in version: %w", versionID, domain.ErrInvalid)
			}
		}
		return driverpkg.ActivateDriverVersionWithOptions(ctx, s, ws, driverID, versionID, driverpkg.ActivationOptions{
			Actor:  driverpkg.ActivationActorUser,
			Reason: driverpkg.ActivationReasonOperator,
			Track:  track,
		})
	})
}

// runWorkflowActivateBuiltin implements `activate --builtin`: sync the built-in
// with ForceTrack=auto, activating this build's packaged version on the auto
// track ("use built-in and follow updates").
func runWorkflowActivateBuiltin(name string) error {
	if _, ok := workflows.BuiltinWorkflow(name); !ok {
		return fmt.Errorf("not_builtin_workflow: %q is not a built-in workflow: %w", name, domain.ErrInvalid)
	}
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		res, err := workflows.SyncBuiltinWorkflow(ctx, h.Store, ws, name, workflows.BuiltinSyncOptions{ForceTrack: driverpkg.BuiltinTrackAuto})
		if err != nil {
			return mapWorkflowSyncErr(name, err)
		}
		if workflowActivateJSON {
			return cmdstore.WriteJSON(res)
		}
		fmt.Printf("Activated built-in workflow %s: active=%s track=%s\n", res.Workflow, res.ActiveVersionID, res.Track)
		return nil
	})
}

func runWorkflowSync(_ *cobra.Command, args []string) error {
	name := args[0]
	if _, ok := workflows.BuiltinWorkflow(name); !ok {
		return fmt.Errorf("not_builtin_workflow: %q is not a built-in workflow: %w", name, domain.ErrInvalid)
	}
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		res, err := workflows.SyncBuiltinWorkflow(ctx, h.Store, ws, name, workflows.BuiltinSyncOptions{})
		if err != nil {
			return mapWorkflowSyncErr(name, err)
		}
		if workflowSyncJSON {
			return cmdstore.WriteJSON(res)
		}
		fmt.Printf("Synced workflow %s: packaged=%s active=%s track=%s activated=%t update_available=%t\n",
			res.Workflow, res.Packaged.VersionID, res.ActiveVersionID, res.Track, res.Activated, res.UpdateAvailable)
		return nil
	})
}

// mapWorkflowSyncErr applies DEV-V5-31's fail-closed wording to ErrNotPackaged
// on a packaged/desktop build; on a dev binary without artifacts there is
// nothing to sync, which is a plain (exit-1) error carrying the reason.
func mapWorkflowSyncErr(name string, err error) error {
	if errors.Is(err, packaged.ErrNotPackaged) {
		if packaged.FailClosed() {
			return fmt.Errorf("sync built-in workflow %q: %w (desktop packaging error: this Loom build ships no built-in workflow artifact for %s; reinstall Loom)", name, err, name)
		}
		return fmt.Errorf("sync built-in workflow %q: %w (this dev binary ships no packaged artifact; nothing to sync)", name, err)
	}
	return err
}

func runWorkflowRollback(_ *cobra.Command, args []string) error {
	name := args[0]
	return workflowWithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		driverID, err := workflows.ResolveDriverID(ctx, h.Store, ws, name)
		if err != nil {
			return fmt.Errorf("resolve workflow driver: %w", err)
		}
		driver, version, err := driverpkg.RollbackDriverVersion(ctx, h.Store, ws, driverID, workflowRollbackVersion)
		if err != nil {
			return err
		}
		previous := driver.Metadata[driverpkg.MetadataKeyActivationPreviousVersion]
		if workflowRollbackJSON {
			return cmdstore.WriteJSON(workflowVersionOutput{
				Version:        version,
				Active:         driver.ActiveVersionID == version.VersionID,
				Approved:       driverpkg.DriverVersionApproved(driver, version),
				EffectiveTrust: driverpkg.DriverVersionEffectiveTrust(driver, version),
			})
		}
		fmt.Printf("Rolled back workflow %s to version %s (previous %s)\n", driver.DriverID, version.VersionID, previous)
		return nil
	})
}
