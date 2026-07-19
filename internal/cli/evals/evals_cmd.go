// Package evals implements `loom evals`, the explicit administration surface
// for session evaluation provisioning and manual rejudge requests.
package evals

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	evalspkg "github.com/tysonthomas9/loomcli/internal/evals"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var evalsCmd = &cobra.Command{
	Use:     "evals",
	Short:   "Manage session evaluation jobs",
	GroupID: "workspace",
}

var evalsEnableSchedule string

var evalsEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Provision the session-eval-agent cron binding",
	Long: "Provision the built-in session-eval-agent workflow and its cron binding for the active workspace.\n\n" +
		"Run `loom doctor --fix` first if you want to backfill missing transcript refs before enabling evaluation.",
	Args: cobra.NoArgs,
	RunE: runEvalsEnable,
}

var evalsDisableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Pause the session-eval-agent cron binding",
	Args:  cobra.NoArgs,
	RunE:  runEvalsDisable,
}

var evalsRejudgeCmd = &cobra.Command{
	Use:   "rejudge <session-id> [<session-id>...]",
	Short: "Queue one or more sessions for re-evaluation",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runEvalsRejudge,
}

func init() {
	evalsEnableCmd.Flags().StringVar(&evalsEnableSchedule, "schedule", "", "cron expression for eval runs (default hourly: 0 * * * *)")
	evalsCmd.AddCommand(evalsEnableCmd, evalsDisableCmd, evalsRejudgeCmd)
	cli.RegisterCommand(evalsCmd)
}

func runEvalsEnable(cmd *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		result, err := evalspkg.EnsureEvalCron(ctx, h.Store, ws, evalsEnableSchedule)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Session evals %s: binding=%s route=%s schedule=%q enabled=%t driver=%s version=%s\n",
			result.Action, result.BindingID, result.RouteKey, result.Schedule, result.Enabled, result.DriverID, result.DriverVersionID)
		if !result.Enabled {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Session evals are still paused because the existing binding is disabled; re-enable it with `loom trigger bindings update "+result.BindingID+" --enabled true`.")
		}
		return nil
	})
}

func runEvalsDisable(cmd *cobra.Command, _ []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		binding, err := h.Store.TriggerBindings().GetByRouteKey(ctx, ws, evalspkg.EvalCronRouteKey)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				return fmt.Errorf("session eval cron binding not found; run loom evals enable")
			}
			return fmt.Errorf("load session eval cron binding: %w", err)
		}
		enabled := false
		updated, err := h.Store.TriggerBindings().Update(ctx, ws, binding.BindingID, store.TriggerBindingUpdate{Enabled: &enabled})
		if err != nil {
			return fmt.Errorf("disable session eval cron binding: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Session evals disabled: binding=%s enabled=%t\n", updated.BindingID, updated.Enabled)
		return nil
	})
}

func runEvalsRejudge(cmd *cobra.Command, args []string) error {
	return cmdstore.WithActiveWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		var errs []error
		queued := 0
		for _, raw := range args {
			sessionID := strings.TrimSpace(raw)
			if sessionID == "" {
				continue
			}
			if err := evalspkg.Rejudge(ctx, h.Store, ws, sessionID); err != nil {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "rejudge %s: failed: %v\n", sessionID, err)
				errs = append(errs, fmt.Errorf("%s: %w", sessionID, err))
				continue
			}
			queued++
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "rejudge %s: requested\n", sessionID)
		}
		warnIfEvalCronDisabled(ctx, cmd, h.Store, ws)
		if len(errs) > 0 {
			return fmt.Errorf("queued %d rejudge request(s), %d failed: %w", queued, len(errs), errors.Join(errs...))
		}
		return nil
	})
}

func warnIfEvalCronDisabled(ctx context.Context, cmd *cobra.Command, st store.Store, ws string) {
	binding, err := st.TriggerBindings().GetByRouteKey(ctx, ws, evalspkg.EvalCronRouteKey)
	if err != nil || binding.Enabled {
		return
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "warning: session eval cron binding is disabled; rejudge requests queue until re-enable.")
}
