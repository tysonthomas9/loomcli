package driver

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

var (
	driverEpicGetWorkspaceKey string
	driverEpicGetDriverRunID  string
	driverEpicGetEpicID       string
	driverEpicGetJSON         bool

	driverEpicSnapshotWorkspaceKey string
	driverEpicSnapshotDriverRunID  string
	driverEpicSnapshotEpicID       string
	driverEpicSnapshotJSON         bool
)

var driverEpicGetCmd = &cobra.Command{
	Use:    "epic-get",
	Short:  "Read epic issue detail for a driver runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverEpicGet,
}

var driverEpicSnapshotCmd = &cobra.Command{
	Use:    "epic-snapshot",
	Short:  "Snapshot epic child task state for a driver runtime",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE:   runDriverEpicSnapshot,
}

func bindDriverEpicGetFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverEpicGetWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverEpicGetDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverEpicGetEpicID, "epic-id", "", "Epic ID to read (default: parent DriverRun epic)")
	cmd.Flags().BoolVar(&driverEpicGetJSON, "json", false, "JSON output")
}

func bindDriverEpicSnapshotFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverEpicSnapshotWorkspaceKey, "workspace-key", "", "Workspace key (default: LOOM_DRIVER_WORKSPACE or active workspace)")
	cmd.Flags().StringVar(&driverEpicSnapshotDriverRunID, "driver-run-id", "", "Parent DriverRun ID (default: LOOM_DRIVER_RUN_ID)")
	cmd.Flags().StringVar(&driverEpicSnapshotEpicID, "epic-id", "", "Epic ID to snapshot (default: parent DriverRun epic)")
	cmd.Flags().BoolVar(&driverEpicSnapshotJSON, "json", false, "JSON output")
}

func runDriverEpicGet(cmd *cobra.Command, _ []string) error {
	return cmdstore.WithStore(cmd.Context(), func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, parent, err := resolveRunningDriverRun(ctx, h, driverEpicGetWorkspaceKey, driverEpicGetDriverRunID)
		if err != nil {
			return err
		}
		epicID := firstNonEmpty(driverEpicGetEpicID, parent.EpicID, driverRunPayloadEpicID(parent.Payload))
		if epicID == "" {
			return fmt.Errorf("epic id required: %w", domain.ErrInvalid)
		}
		items, err := newDriverWorkItems(h, ws, driverRunActor(parent.RunID))
		if err != nil {
			return err
		}
		epic, err := items.Get(ctx, workitems.GetQuery{IssueID: epicID})
		if err != nil {
			return fmt.Errorf("get epic: %w", err)
		}
		if driverEpicGetJSON {
			return cmdstore.WriteJSON(epic)
		}
		if epic == nil {
			fmt.Printf("Epic %s not found\n", epicID)
			return nil
		}
		fmt.Printf("Epic %s: %s\n", epic.ID, epic.Title)
		return nil
	})
}

func runDriverEpicSnapshot(cmd *cobra.Command, _ []string) error {
	return cmdstore.WithStore(cmd.Context(), func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws, parent, err := resolveRunningDriverRun(ctx, h, driverEpicSnapshotWorkspaceKey, driverEpicSnapshotDriverRunID)
		if err != nil {
			return err
		}
		epicID := firstNonEmpty(driverEpicSnapshotEpicID, parent.EpicID, driverRunPayloadEpicID(parent.Payload))
		items, err := newDriverWorkItems(h, ws, driverRunActor(parent.RunID))
		if err != nil {
			return err
		}
		snapshot, err := driverpkg.LoadEpicSnapshot(ctx, items, driverpkg.EpicSnapshotOptions{EpicID: epicID})
		if err != nil {
			return fmt.Errorf("snapshot epic: %w", err)
		}
		if driverEpicSnapshotJSON {
			return cmdstore.WriteJSON(snapshot)
		}
		fmt.Printf("Epic %s: %d ready, %d blocked, %d open child task(s)\n", snapshot.EpicID, snapshot.ReadyCount, snapshot.BlockedCount, snapshot.OpenChildrenCount)
		return nil
	})
}
