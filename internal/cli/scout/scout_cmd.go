// Package scout implements the human review controls for scout-generated
// workspace agents.md content.
package scout

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/agentprovision"
	"github.com/tysonthomas9/loomcli/internal/agentstate"
	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/scriptedroles"
)

var (
	scoutWorkspaceDir           = cli.GetWorkspaceRuntimeDir
	scoutOutput       io.Writer = os.Stdout

	scoutDiffAgentID    string
	scoutApproveAgentID string
)

var scoutCmd = &cobra.Command{
	Use:     "scout",
	Short:   "Review Scout workspace guidance",
	GroupID: "workspace",
}

var scoutDiffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Show an agent instance's staged agents.md change",
	Args:  cobra.NoArgs,
	RunE:  runScoutDiff,
}

var scoutApproveCmd = &cobra.Command{
	Use:   "approve",
	Short: "Merge an agent instance's staged fence into agents.md",
	Args:  cobra.NoArgs,
	RunE:  runScoutApprove,
}

func init() {
	defaultID := defaultScoutAgentID()
	scoutDiffCmd.Flags().StringVar(&scoutDiffAgentID, "agent", defaultID, "Scout agent instance ID")
	scoutApproveCmd.Flags().StringVar(&scoutApproveAgentID, "agent", defaultID, "Scout agent instance ID")
	scoutCmd.AddCommand(scoutDiffCmd, scoutApproveCmd)
	cli.RegisterCommand(scoutCmd)
}

func runScoutDiff(cmd *cobra.Command, _ []string) error {
	serviceID, root, err := resolveScoutPaths(scoutDiffAgentID)
	if err != nil {
		return err
	}
	currentPath := filepath.Join(root, "agents.md")
	pendingPath := agentstate.PendingAgentsPath(root, serviceID)
	if _, err := os.Stat(pendingPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("agent %q has no pending agents.md", serviceID)
		}
		return fmt.Errorf("stat pending agents.md: %w", err)
	}
	if _, err := os.Stat(currentPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("workspace has no approved agents.md")
		}
		return fmt.Errorf("stat approved agents.md: %w", err)
	}
	ctx := context.Background()
	if cmd != nil && cmd.Context() != nil {
		ctx = cmd.Context()
	}
	diff := exec.CommandContext(ctx, "git", "diff", "--no-index", "--no-ext-diff", "--no-color", "--", currentPath, pendingPath) //nolint:gosec,norawexec // fixed git argv; both paths are rooted under the resolved workspace
	out, diffErr := diff.CombinedOutput()
	if diffErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(diffErr, &exitErr) || exitErr.ExitCode() != 1 {
			return fmt.Errorf("diff pending agents.md: %w: %s", diffErr, strings.TrimSpace(string(out)))
		}
	}
	if len(out) == 0 {
		_, err = fmt.Fprintf(scoutOutput, "No pending changes for agent %s.\n", serviceID)
		return err
	}
	_, err = scoutOutput.Write(out)
	return err
}

func runScoutApprove(_ *cobra.Command, _ []string) error {
	serviceID, root, err := resolveScoutPaths(scoutApproveAgentID)
	if err != nil {
		return err
	}
	pendingPath := agentstate.PendingAgentsPath(root, serviceID)
	pending, err := os.ReadFile(pendingPath) //nolint:gosec // grammar-validated service ID under the resolved workspace
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("agent %q has no pending agents.md", serviceID)
		}
		return fmt.Errorf("read pending agents.md: %w", err)
	}
	currentPath := filepath.Join(root, "agents.md")
	current, err := os.ReadFile(currentPath) //nolint:gosec // fixed filename under the resolved workspace
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read approved agents.md: %w", err)
	}
	merged, err := agentstate.MergePendingFence(string(current), string(pending), serviceID)
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(currentPath, []byte(merged), 0o644); err != nil {
		return fmt.Errorf("write approved agents.md: %w", err)
	}
	if err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear pending agents.md: %w", err)
	}
	_, err = fmt.Fprintf(scoutOutput, "Approved pending agents.md for agent %s.\n", serviceID)
	return err
}

func resolveScoutPaths(serviceID string) (string, string, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		serviceID = defaultScoutAgentID()
	}
	if err := agentprovision.ValidateServiceID(serviceID); err != nil {
		return "", "", err
	}
	root := strings.TrimSpace(scoutWorkspaceDir())
	if root == "" || root == "." {
		return "", "", fmt.Errorf("scout workspace runtime directory is unavailable; refusing the repository fallback")
	}
	return serviceID, filepath.Clean(root), nil
}

func defaultScoutAgentID() string {
	role, ok := scriptedroles.ForRole(scriptedroles.ScoutRoleName)
	if !ok || role.DefaultInstance == nil {
		return ""
	}
	return role.DefaultInstance.ServiceID
}
