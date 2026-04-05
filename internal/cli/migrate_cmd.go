package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:     "migrate",
	Short:   "Migrate issue data between backends",
	Long:    "Migrate issue data from one backend to another. Currently supports migrating from beads to fleet.",
	GroupID: "config",
	RunE:    runMigrate,
}

func init() {
	migrateCmd.Flags().Bool("to-fleet", false, "Migrate beads data to a fleet server")
	migrateCmd.Flags().String("fleet-url", "", "Fleet server URL (env: LOOM_FLEET_URL)")
	migrateCmd.Flags().String("fleet-workspace", "", "Target workspace ID (env: LOOM_FLEET_WORKSPACE)")
	migrateCmd.Flags().String("fleet-api-key", "", "Fleet API key (env: LOOM_FLEET_API_KEY)")
	migrateCmd.Flags().Bool("dry-run", false, "Preview migration without making changes")
	migrateCmd.Flags().Bool("update-config", false, "Update loom.yaml to use fleet backend after migration")
	migrateCmd.Flags().Bool("include-closed", false, "Include closed issues in migration")
	migrateCmd.Flags().Int("batch-size", 50, "Number of issues to process before printing progress")

	rootCmd.AddCommand(migrateCmd)
}

func runMigrate(cmd *cobra.Command, _ []string) error {
	toFleet, _ := cmd.Flags().GetBool("to-fleet")
	if !toFleet {
		return fmt.Errorf("migration direction required; use --to-fleet")
	}

	cfg, err := parseMigrateFlags(cmd)
	if err != nil {
		return err
	}

	return runMigrateToFleet(cfg)
}

func parseMigrateFlags(cmd *cobra.Command) (*migrateConfig, error) {
	fleetURL, _ := cmd.Flags().GetString("fleet-url")
	if fleetURL == "" {
		fleetURL = os.Getenv("LOOM_FLEET_URL")
	}
	if fleetURL == "" {
		return nil, fmt.Errorf("--fleet-url is required (or set LOOM_FLEET_URL)")
	}

	workspace, _ := cmd.Flags().GetString("fleet-workspace")
	if workspace == "" {
		workspace = os.Getenv("LOOM_FLEET_WORKSPACE")
	}
	if workspace == "" {
		return nil, fmt.Errorf("--fleet-workspace is required (or set LOOM_FLEET_WORKSPACE)")
	}

	apiKey, _ := cmd.Flags().GetString("fleet-api-key")
	if apiKey == "" {
		apiKey = os.Getenv("LOOM_FLEET_API_KEY")
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	updateConfig, _ := cmd.Flags().GetBool("update-config")
	includeClosed, _ := cmd.Flags().GetBool("include-closed")
	batchSize, _ := cmd.Flags().GetInt("batch-size")
	if batchSize <= 0 {
		batchSize = 50
	}

	projectDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}

	return &migrateConfig{
		fleetURL:      fleetURL,
		workspace:     workspace,
		apiKey:        apiKey,
		dryRun:        dryRun,
		includeClosed: includeClosed,
		batchSize:     batchSize,
		updateConfig:  updateConfig,
		projectDir:    projectDir,
	}, nil
}
