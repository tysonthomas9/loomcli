// Package preflight provides a direct CLI surface for the local task runner's
// backend readiness gate.
package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var preflightJSON bool

type preflightDaemonGetter interface {
	Daemon() store.DaemonProfileStore
}

type preflightOutput struct {
	Workspace  string                        `json:"workspace"`
	Backend    string                        `json:"backend"`
	Ready      bool                          `json:"ready"`
	Health     runtimepreflight.HealthStatus `json:"health"`
	ErrorClass string                        `json:"error_class,omitempty"`
	Error      string                        `json:"error,omitempty"`
}

var preflightCmd = &cobra.Command{
	Use:     "preflight",
	Short:   "Check local task runner backend readiness",
	GroupID: "workspace",
	Long: `Check whether the local task runner can start with the workspace backend.

By default the command uses the workspace Project Default Backend. Pass the
global --backend flag to check a specific backend without changing workspace
settings.`,
	Args: cobra.NoArgs,
	RunE: runPreflight,
}

func init() {
	preflightCmd.Flags().BoolVar(&preflightJSON, "json", false, "Output JSON")
	cli.RegisterCommand(preflightCmd)
}

func runPreflight(cmd *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		return runPreflightCheck(ctx, h.Store, ws, backendOverride(cmd), preflightJSON, cmd.OutOrStdout(), cmd)
	})
}

func runPreflightCheck(ctx context.Context, st preflightDaemonGetter, ws, backendOverride string, jsonOut bool, w io.Writer, cmd *cobra.Command) error {
	result := runtimepreflight.CheckLocalTaskRunner(ctx, st, ws, backendOverride)
	out := preflightOutput{
		Workspace:  ws,
		Backend:    result.Backend,
		Ready:      result.Ready,
		Health:     result.Health,
		ErrorClass: result.ErrorClass,
		Error:      result.Message,
	}

	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return fmt.Errorf("encode JSON: %w", err)
		}
	} else {
		if err := printPreflightHuman(w, out); err != nil {
			return fmt.Errorf("write preflight output: %w", err)
		}
	}

	if result.Ready {
		return nil
	}
	if cmd != nil {
		cmd.SilenceErrors = true
	}
	if result.Message != "" {
		return errors.New(result.Message)
	}
	return fmt.Errorf("local task runner backend %q is not ready", result.Backend)
}

func backendOverride(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	flag := cmd.Flag("backend")
	if flag == nil || !flag.Changed {
		return ""
	}
	return strings.TrimSpace(flag.Value.String())
}

func printPreflightHuman(w io.Writer, out preflightOutput) error {
	var err error
	write := func(format string, args ...any) {
		if err != nil {
			return
		}
		_, err = fmt.Fprintf(w, format, args...)
	}

	write("Workspace: %s\n", out.Workspace)
	write("Backend: %s\n", out.Backend)
	write("Ready: %t\n", out.Ready)
	write("Health:\n")
	write("  Healthy: %t\n", out.Health.Healthy)
	write("  Installed: %t\n", out.Health.Installed)
	if out.Health.Version != "" {
		write("  Version: %s\n", out.Health.Version)
	}
	write("  API Key Set: %t\n", out.Health.APIKeySet)
	if out.Health.Message != "" {
		write("  Message: %s\n", out.Health.Message)
	}
	if out.ErrorClass != "" {
		write("Error Class: %s\n", out.ErrorClass)
	}
	if out.Error != "" {
		write("Error: %s\n", out.Error)
	}
	return err
}
