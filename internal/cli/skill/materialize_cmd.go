package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/skillmat"
)

// A refresh may need to validate and download hundreds of files from a remote
// FleetDB/object-store pair before it can atomically swap the projection. Keep
// the hook bounded, but budget for real cloud round trips rather than only a
// local FleetDB daemon.
const skillMaterializeTimeout = 2 * time.Minute

var skillMaterializeOpenStore = cmdstore.OpenStore

func newSkillMaterializeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "materialize",
		Short:  "Materialize skills for the current agent turn",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSkillMaterialize(cmd)
		},
	}
	// Hook execution should not initialize a backend or logging before the
	// bounded store operation. Cobra uses the nearest persistent pre-run hook.
	cmd.PersistentPreRunE = func(_ *cobra.Command, _ []string) error { return nil }
	return cmd
}

func runSkillMaterialize(cmd *cobra.Command) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, skillMaterializeTimeout)
	defer cancel()

	err := materializeCurrentWorkdir(ctx)
	if skillmat.IsStoreUnavailable(err) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: skill store unavailable; continuing with existing materialization: %v\n", err)
		return nil
	}
	if err != nil {
		return cli.NewCommandExitError(2, fmt.Errorf("skill materialization was refused: %w", err))
	}
	return nil
}

func materializeCurrentWorkdir(ctx context.Context) error {
	handle, err := skillMaterializeOpenStore(ctx)
	if err != nil {
		return &skillmat.StoreUnavailableError{Err: err}
	}
	defer func() { _ = handle.Close() }()

	workspace, err := bootstrap.ResolveActiveWorkspaceKey(ctx, handle.Store.Workspaces())
	if err != nil {
		if !errors.Is(err, bootstrap.ErrNoActiveWorkspace) && !errors.Is(err, domain.ErrNotFound) {
			return &skillmat.StoreUnavailableError{Err: err}
		}
		return err
	}

	roleName := strings.TrimSpace(os.Getenv("LOOM_AGENT_ROLE"))
	if roleName == "" {
		agentName := strings.TrimSpace(os.Getenv(bootstrap.EnvAgentName))
		if agentName != "" {
			if agent, getErr := handle.Store.Agents().Get(ctx, workspace, agentName); getErr == nil && agent != nil {
				roleName = agent.RoleName
			}
		}
	}
	targetDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve skill materialization directory: %w", err)
	}
	return skillmat.MaterializeLeased(ctx, handle.Store, workspace, roleName, targetDir)
}
