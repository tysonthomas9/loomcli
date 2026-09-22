// Package preflight implements the loom preflight diagnostic command.
package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/runtimepreflight"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	schemaVersion = 1
	reportKind    = "local_task_runner"
)

type options struct {
	agentName       string
	backendOverride string
	json            bool
}

type commandDeps struct {
	openStore       func(context.Context) (*bootstrap.StoreHandle, error)
	activeWorkspace func(context.Context, store.Store) (string, error)
}

type report struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Workspace     string `json:"workspace,omitempty"`
	Agent         string `json:"agent,omitempty"`
	runtimepreflight.Result
}

func init() {
	cli.RegisterCommand(newCommand(commandDeps{
		openStore:       cmdstore.OpenStore,
		activeWorkspace: cmdstore.ActiveWorkspace,
	}))
}

func newCommand(deps commandDeps) *cobra.Command {
	opts := options{}
	cmd := &cobra.Command{
		Use:     "preflight",
		Short:   "Check local task runner backend readiness",
		GroupID: "workspace",
		Long: `Check whether the resolved AI backend can run Loom's local task runner.

The check is read-only. It resolves the same workspace and agent configuration
used by automatic run gates, probes backend health, and reports one canonical
readiness verdict without changing the selected backend or workspace. The
global --backend flag does not affect preflight targeting; use --ai-backend.`,
		Example: `  loom preflight
  loom preflight --agent worker-a
  loom preflight --ai-backend codex
  loom preflight --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, opts, deps)
		},
		PersistentPreRunE: cli.PrepareQuietCommand,
	}
	cmd.Flags().StringVar(&opts.agentName, "agent", "", "Agent whose configured AI backend should be checked")
	cmd.Flags().StringVar(&opts.backendOverride, "ai-backend", "", "AI backend to check without changing stored configuration")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output one canonical JSON report")
	return cmd
}

func run(cmd *cobra.Command, opts options, deps commandDeps) error {
	result, workspace, checkErr := checkTarget(cmd.Context(), opts, deps)
	report := report{
		SchemaVersion: schemaVersion,
		Kind:          reportKind,
		Workspace:     workspace,
		Agent:         strings.TrimSpace(opts.agentName),
		Result:        result,
	}
	if err := render(cmd.OutOrStdout(), report, opts.json); err != nil {
		return commandExitError(cmd, 2, fmt.Errorf("preflight: write report: %w", err))
	}
	if checkErr != nil {
		return commandExitError(cmd, 2, fmt.Errorf("preflight: %s", result.ErrorClass))
	}
	if !result.Ready {
		return commandExitError(cmd, 1, fmt.Errorf("preflight: %s", result.ErrorClass))
	}
	return nil
}

func checkTarget(cmdCtx context.Context, opts options, deps commandDeps) (runtimepreflight.Result, string, error) {
	req := runtimepreflight.Request{
		AgentName:       strings.TrimSpace(opts.agentName),
		AgentRequired:   strings.TrimSpace(opts.agentName) != "",
		BackendOverride: strings.TrimSpace(opts.backendOverride),
	}
	workspaceHint := strings.TrimSpace(os.Getenv(bootstrap.EnvWorkspace))
	if req.BackendOverride != "" && !req.AgentRequired {
		result, err := runtimepreflight.CheckLocalTaskRunner(cmdCtx, nil, req)
		return result, workspaceHint, err
	}
	if deps.openStore == nil {
		return targetAcquisitionFailure(cmdCtx, req, workspaceHint, errors.New("preflight store opener is not configured"))
	}
	handle, err := deps.openStore(cmdCtx)
	if err != nil {
		return targetAcquisitionFailure(cmdCtx, req, workspaceHint, err)
	}
	if handle == nil || handle.Store == nil {
		return targetAcquisitionFailure(cmdCtx, req, workspaceHint, errors.New("preflight store opener returned no store"))
	}
	defer func() { _ = handle.Close() }()
	if deps.activeWorkspace == nil {
		return targetAcquisitionFailure(cmdCtx, req, workspaceHint, errors.New("preflight workspace resolver is not configured"))
	}
	workspace, err := deps.activeWorkspace(cmdCtx, handle.Store)
	if err != nil {
		return targetAcquisitionFailure(cmdCtx, req, workspaceHint, err)
	}
	req.WorkspaceKey = strings.TrimSpace(workspace)
	result, checkErr := runtimepreflight.CheckLocalTaskRunner(cmdCtx, handle.Store, req)
	return result, req.WorkspaceKey, checkErr
}

func targetAcquisitionFailure(
	ctx context.Context,
	req runtimepreflight.Request,
	workspace string,
	cause error,
) (runtimepreflight.Result, string, error) {
	req.WorkspaceKey = workspace
	result, _ := runtimepreflight.CheckLocalTaskRunner(ctx, nil, req)
	if summary := safeErrorSummary(cause); summary != "" {
		result.Message += ": " + summary
	}
	return result, workspace, cause
}

func safeErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	const maxRunes = 512
	fields := strings.Fields(err.Error())
	for index, field := range fields {
		if strings.Contains(field, "://") {
			fields[index] = "[redacted]"
		}
	}
	value := strings.Join(fields, " ")
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes-1]) + "…"
}

func render(w io.Writer, value report, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(value); err != nil {
			return fmt.Errorf("encode JSON: %w", err)
		}
		return nil
	}
	return renderHuman(w, value)
}

func renderHuman(w io.Writer, value report) error {
	var output strings.Builder
	verdict := "NOT READY"
	if value.Ready {
		verdict = "READY"
	}
	fmt.Fprintf(&output, "Local task runner: %s\n", verdict)
	if value.Workspace != "" {
		fmt.Fprintf(&output, "Workspace: %s\n", value.Workspace)
	}
	if value.Agent != "" {
		fmt.Fprintf(&output, "Agent: %s\n", value.Agent)
	}
	if value.Backend != "" {
		fmt.Fprintf(&output, "Backend: %s (%s)\n", value.Backend, backendSourceLabel(value.BackendSource))
	}
	if value.Health != nil {
		fmt.Fprintf(&output, "Installed: %s\n", yesNo(value.Health.Installed))
		fmt.Fprintf(&output, "Authenticated: %s\n", yesNo(value.Health.APIKeySet))
		fmt.Fprintf(&output, "Healthy: %s\n", yesNo(value.Health.Healthy))
	}
	if value.ErrorClass != "" {
		fmt.Fprintf(&output, "Class: %s\n", value.ErrorClass)
	}
	fmt.Fprintf(&output, "Reason: %s\n", value.Message)
	if showProbeDetail(value.Result) {
		fmt.Fprintf(&output, "Detail: %s\n", strings.TrimSpace(value.Health.Message))
	}
	if len(value.Remediation) > 0 {
		fmt.Fprintf(&output, "Next: %s\n", value.Remediation[0])
	}
	if _, err := io.WriteString(w, output.String()); err != nil {
		return fmt.Errorf("write human report: %w", err)
	}
	return nil
}

func showProbeDetail(result runtimepreflight.Result) bool {
	if result.Health == nil || strings.TrimSpace(result.Health.Message) == "" {
		return false
	}
	return result.ErrorClass == runtimepreflight.ErrorClassAuthMissing ||
		result.ErrorClass == runtimepreflight.ErrorClassUnhealthy
}

func backendSourceLabel(source runtimepreflight.BackendSource) string {
	switch source {
	case runtimepreflight.BackendSourceOverride:
		return "explicit override"
	case runtimepreflight.BackendSourceAgent:
		return "agent override"
	case runtimepreflight.BackendSourceWorkspace:
		return "workspace default"
	case runtimepreflight.BackendSourceDefault:
		return "built-in default"
	default:
		return string(source)
	}
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func commandExitError(cmd *cobra.Command, code int, err error) error {
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.Root().SilenceErrors = true
	cmd.Root().SilenceUsage = true
	return cli.NewCommandExitError(code, err)
}
