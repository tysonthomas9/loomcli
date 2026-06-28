package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
)

var (
	driverRegisterFlueDist     string
	driverRegisterManifest     string
	driverRegisterName         string
	driverRegisterID           string
	driverRegisterWorkflow     string
	driverRegisterSourceRef    string
	driverRegisterSourceDigest string
	driverRegisterActivate     bool
	driverRegisterTrusted      bool
	driverRegisterUntrusted    bool
	driverRegisterJSON         bool

	driverRunEpic           string
	driverRunID             string
	driverRunIdempotencyKey string
	driverRunEntrypoint     string
	driverRunInput          []string
	driverRunJSON           bool
)

var driverCmd = &cobra.Command{
	Use:     "driver",
	Short:   "Register and run dynamic Loom drivers",
	GroupID: "workspace",
}

var driverRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a built native Flue driver artifact as an immutable DriverVersion",
	Long: `Register a built native Flue driver artifact as an immutable DriverVersion.

The artifact must already be built by Flue, for example:

  flue build --target node
  loom driver register --flue-dist ./dist --name epic-runner --activate

Loom stages the built dist directory and records the DriverVersion. It does not
generate a Flue project or adapter source.`,
	Args: cobra.NoArgs,
	RunE: runDriverRegister,
}

var driverRunCmd = &cobra.Command{
	Use:   "run <driver_id>",
	Short: "Record a queued DriverRun for a published driver",
	Long: `Record a queued DriverRun for a published driver.

The run is pinned to the driver's active DriverVersion. Driver execution is
handled by a later runtime/executor slice; this command records durable work
without claiming or running it synchronously.`,
	Args: cobra.ExactArgs(1),
	RunE: runDriverRun,
}

func init() {
	bindDriverRegisterFlags(driverRegisterCmd)
	bindDriverRunFlags(driverRunCmd)
	bindDriverExecTaskFlags(driverExecTaskCmd)
	bindDriverWorkTaskRunFlags(driverWorkTaskRunCmd)
	bindDriverClaimReadyFlags(driverClaimReadyCmd)
	bindDriverEpicGetFlags(driverEpicGetCmd)
	bindDriverEpicSnapshotFlags(driverEpicSnapshotCmd)
	bindDriverListAgentsFlags(driverListAgentsCmd)
	bindDriverAgentOrchestrationSessionFlags(driverAgentOrchestrationSessionCmd)
	bindDriverUpdateAgentParentFlags(driverUpdateAgentParentCmd)
	bindDriverDeliverLeadAssignmentFlags(driverDeliverLeadAssignmentCmd)
	bindDriverDeliverAgentMessageFlags(driverDeliverAgentMessageCmd)
	bindDriverActiveTaskRunsFlags(driverActiveTaskRunsCmd)
	bindDriverCompleteTaskFlags(driverCompleteTaskCmd)
	bindDriverReleaseTaskFlags(driverReleaseTaskCmd)
	bindDriverRecoverStaleTasksFlags(driverRecoverStaleTasksCmd)

	driverCmd.AddCommand(driverRegisterCmd, driverRunCmd, driverExecTaskCmd, driverWorkTaskRunCmd, driverClaimReadyCmd, driverEpicGetCmd, driverEpicSnapshotCmd, driverListAgentsCmd, driverAgentOrchestrationSessionCmd, driverUpdateAgentParentCmd, driverDeliverLeadAssignmentCmd, driverDeliverAgentMessageCmd, driverActiveTaskRunsCmd, driverCompleteTaskCmd, driverReleaseTaskCmd, driverRecoverStaleTasksCmd)
	cli.RegisterCommand(driverCmd)
}

func bindDriverRegisterFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverRegisterFlueDist, "flue-dist", "", "Built Flue dist directory containing server.mjs")
	cmd.Flags().StringVar(&driverRegisterManifest, "manifest", "", "Optional native Flue driver manifest JSON (default: <flue-dist>/loom-driver.json if present)")
	cmd.Flags().StringVar(&driverRegisterName, "name", "", "Driver name")
	cmd.Flags().StringVar(&driverRegisterID, "id", "", "Driver ID (default: slug of --name or manifest driver_name)")
	cmd.Flags().StringVar(&driverRegisterWorkflow, "workflow", "", "Flue workflow name (default: driver ID or manifest workflow_name)")
	cmd.Flags().StringVar(&driverRegisterSourceRef, "source-ref", "", "Optional source/provenance ref recorded on the DriverVersion")
	cmd.Flags().StringVar(&driverRegisterSourceDigest, "source-digest", "", "Optional source digest recorded on the DriverVersion")
	cmd.Flags().BoolVar(&driverRegisterActivate, "activate", false, "Activate the registered version after validation")
	cmd.Flags().BoolVar(&driverRegisterTrusted, "trusted", false, "Register as operator-trusted for local process execution")
	cmd.Flags().BoolVar(&driverRegisterUntrusted, "untrusted", false, "Register as untrusted (default); requires an isolating launcher unless later approved")
	cmd.Flags().BoolVar(&driverRegisterJSON, "json", false, "JSON output")
	_ = cmd.MarkFlagRequired("flue-dist")
}

func bindDriverRunFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&driverRunEpic, "epic", "", "Epic ID to pass as input.epicId")
	cmd.Flags().StringVar(&driverRunID, "run-id", "", "Run ID (default: generated)")
	cmd.Flags().StringVar(&driverRunIdempotencyKey, "idempotency-key", "", "DriverRun admission idempotency key")
	cmd.Flags().StringVar(&driverRunEntrypoint, "entrypoint", driverpkg.EntrypointRun, "Driver entrypoint")
	cmd.Flags().StringArrayVar(&driverRunInput, "input", nil, "Input key=value (repeatable)")
	cmd.Flags().BoolVar(&driverRunJSON, "json", false, "JSON output")
	_ = cmd.MarkFlagRequired("epic")
}

func runDriverRegister(_ *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		workDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve work dir: %w", err)
		}
		trust, err := driverRegisterTrust()
		if err != nil {
			return err
		}
		result, err := driverpkg.RegisterFlueDriver(ctx, h.Store, driverpkg.RegisterFlueOptions{
			WorkspaceKey: ws,
			WorkDir:      workDir,
			DistPath:     driverRegisterFlueDist,
			ManifestPath: driverRegisterManifest,
			DriverName:   driverRegisterName,
			DriverID:     driverRegisterID,
			WorkflowName: driverRegisterWorkflow,
			SourceRef:    driverRegisterSourceRef,
			SourceDigest: driverRegisterSourceDigest,
			CreatedBy:    publishActor(),
			Activate:     driverRegisterActivate,
			Trust:        trust,
		})
		if driverRegisterJSON && result != nil {
			if writeErr := cmdstore.WriteJSON(result); writeErr != nil && err == nil {
				err = writeErr
			}
		}
		if err != nil {
			return fmt.Errorf("register driver: %w", err)
		}
		if !driverRegisterJSON {
			fmt.Printf("Registered native Flue driver %s version %s\n", result.Driver.DriverID, result.Version.VersionID)
			fmt.Printf("Bundle: %s %s\n", result.Version.BundleRef, result.Version.BundleDigest)
			if result.Activated {
				fmt.Printf("Activated: %s\n", result.Version.VersionID)
			}
		}
		return nil
	})
}

func driverRegisterTrust() (domain.DriverTrustLevel, error) {
	if driverRegisterTrusted && driverRegisterUntrusted {
		return "", fmt.Errorf("only one of --trusted or --untrusted may be set: %w", domain.ErrInvalid)
	}
	if driverRegisterTrusted {
		return domain.DriverTrustTrusted, nil
	}
	return domain.DriverTrustUntrusted, nil
}

func runDriverRun(_ *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		payload, err := parseDriverRunPayload(driverRunInput, driverRunEpic)
		if err != nil {
			return err
		}
		run, err := driverpkg.CreateDriverRun(ctx, h.Store, driverpkg.RunOptions{
			WorkspaceKey:   ws,
			DriverID:       args[0],
			EpicID:         driverRunEpic,
			RunID:          driverRunID,
			IdempotencyKey: driverRunIdempotencyKey,
			Entrypoint:     driverRunEntrypoint,
			Payload:        payload,
		})
		if err != nil {
			return fmt.Errorf("create driver run: %w", err)
		}
		if driverRunJSON {
			return cmdstore.WriteJSON(run)
		}
		fmt.Printf("Recorded driver run %s (%s)\n", run.RunID, run.Status)
		fmt.Printf("Driver: %s version %s\n", run.DriverID, run.DriverVersionID)
		fmt.Printf("Epic: %s\n", run.EpicID)
		fmt.Println("Execution pending: start a driver executor/runtime to claim queued runs.")
		return nil
	})
}

func parseDriverRunPayload(values []string, epicID string) (json.RawMessage, error) {
	out := map[string]string{}
	if strings.TrimSpace(epicID) != "" {
		out["epicId"] = strings.TrimSpace(epicID)
	}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("input must be key=value: %q", value)
		}
		out[key] = val
	}
	if len(out) == 0 {
		return json.RawMessage(`{}`), nil
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	return payload, nil
}

func publishActor() string {
	if actor := os.Getenv("LOOM_FLEET_ACTOR"); actor != "" {
		return actor
	}
	if actor := os.Getenv("USER"); actor != "" {
		return actor
	}
	return "loom-cli"
}
