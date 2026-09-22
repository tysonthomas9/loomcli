package daemon

import (
	"context"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
)

var (
	daemonProfileWorkspace string
	daemonProfileShowJSON  bool
)

// daemonProfileCmd is the parent for fleet-db-backed daemon profile
// CRUD. Per-workspace settings (RestartPolicy, MaxAgents, OTel, etc.)
// live in fleet-db; truly per-host bootstrap config lives in env vars.
var daemonProfileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage per-workspace daemon profile in fleet-db",
}

var daemonProfileShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the active workspace's daemon profile",
	Args:  cobra.NoArgs,
	RunE:  runDaemonProfileShow,
}

var daemonProfileSetCmd = &cobra.Command{
	Use:   "set <KEY> <VALUE>",
	Short: "Set a single daemon-profile field",
	Long: `Set a daemon profile field. Supported keys:
  pid_file        string
  log_dir         string
  events_dir      string
  issue_backend   string (default: fleetdb)
  max_agents      integer
  startup_timeout integer (seconds)
  restart_policy.output_timeout     integer (seconds)
  restart_policy.no_work_backoff    integer (seconds)
  restart_policy.idle_poll_interval integer (seconds)`,
	Args: cobra.ExactArgs(2),
	RunE: runDaemonProfileSet,
}

var daemonProfileUnsetCmd = &cobra.Command{
	Use:   "unset <KEY>",
	Short: "Clear a daemon-profile field back to its default",
	Long: `Revert a daemon profile field to its fleet-db default.
Same key set as 'set'; integer/pointer fields become nil, string
fields become empty.`,
	Args: cobra.ExactArgs(1),
	RunE: runDaemonProfileUnset,
}

func init() {
	// No -w shorthand; the root command already uses it for --worktrees.
	daemonProfileCmd.PersistentFlags().StringVar(&daemonProfileWorkspace, "workspace", "", "Workspace key (default: active)")
	daemonProfileShowCmd.Flags().BoolVar(&daemonProfileShowJSON, "json", false, "JSON output")

	daemonProfileCmd.AddCommand(daemonProfileShowCmd, daemonProfileSetCmd, daemonProfileUnsetCmd)
	daemonCmd.AddCommand(daemonProfileCmd)
}

// withDaemonWorkspace runs fn with the resolved workspace key —
// honoring --workspace if set, falling back to the active key.
// Daemon profile commands accept an explicit override (rare for CRUD
// commands), so they can't simply use cmdstore.WithActiveWorkspace.
func withDaemonWorkspace(fn func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		ws := daemonProfileWorkspace
		if ws == "" {
			active, err := cmdstore.ActiveWorkspace(ctx, h.Store)
			if err != nil {
				return err
			}
			ws = active
		}
		return fn(ctx, h, ws)
	})
}

func runDaemonProfileShow(_ *cobra.Command, _ []string) error {
	return withDaemonWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		p, err := h.Store.Daemon().Get(ctx, ws)
		if err != nil {
			return fmt.Errorf("get daemon profile: %w", err)
		}
		if daemonProfileShowJSON {
			return cmdstore.WriteJSON(p)
		}
		issueBackend := p.IssueBackend
		if issueBackend == "" {
			issueBackend = "(default)"
		}
		fmt.Printf("Workspace:      %s\n", p.WorkspaceKey)
		fmt.Printf("Issue backend:  %s\n", issueBackend)
		if p.PIDFile != "" {
			fmt.Printf("PID file:       %s\n", p.PIDFile)
		}
		if p.LogDir != "" {
			fmt.Printf("Log dir:        %s\n", p.LogDir)
		}
		if p.EventsDir != "" {
			fmt.Printf("Events dir:     %s\n", p.EventsDir)
		}
		if p.MaxAgents != nil {
			fmt.Printf("Max agents:     %d\n", *p.MaxAgents)
		}
		if p.StartupTimeout != nil {
			fmt.Printf("Startup timeout: %ds\n", *p.StartupTimeout)
		}
		if p.RestartPolicy.OutputTimeout != nil {
			fmt.Printf("Output timeout:  %ds\n", *p.RestartPolicy.OutputTimeout)
		}
		if p.RestartPolicy.NoWorkBackoff != nil {
			fmt.Printf("No-work backoff: %ds\n", *p.RestartPolicy.NoWorkBackoff)
		}
		if p.RestartPolicy.IdlePollInterval != nil {
			fmt.Printf("Idle poll:       %ds\n", *p.RestartPolicy.IdlePollInterval)
		}
		return nil
	})
}

func runDaemonProfileSet(_ *cobra.Command, args []string) error {
	return withDaemonWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		p, err := h.Store.Daemon().Get(ctx, ws)
		if err != nil {
			return fmt.Errorf("read profile: %w", err)
		}
		key, value := args[0], args[1]
		if err := applyProfileField(p, key, value, false /* unset */); err != nil {
			return err
		}
		if _, err := h.Store.Daemon().Upsert(ctx, p); err != nil {
			return fmt.Errorf("write profile: %w", err)
		}
		fmt.Printf("Set %s.%s = %s\n", ws, key, value)
		return nil
	})
}

func runDaemonProfileUnset(_ *cobra.Command, args []string) error {
	return withDaemonWorkspace(func(ctx context.Context, h *bootstrap.StoreHandle, ws string) error {
		p, err := h.Store.Daemon().Get(ctx, ws)
		if err != nil {
			return fmt.Errorf("read profile: %w", err)
		}
		key := args[0]
		if err := applyProfileField(p, key, "" /* value */, true /* unset */); err != nil {
			return err
		}
		if _, err := h.Store.Daemon().Upsert(ctx, p); err != nil {
			return fmt.Errorf("write profile: %w", err)
		}
		fmt.Printf("Cleared %s.%s\n", ws, key)
		return nil
	})
}

// applyProfileField mutates p for the given key. When unset is true the
// field is reverted to its zero (nil for *int, "" for strings); otherwise
// the typed value is parsed from the value string.
func applyProfileField(p *domain.DaemonProfile, key, value string, unset bool) error {
	switch key {
	case "pid_file":
		p.PIDFile = value
	case "log_dir":
		p.LogDir = value
	case "events_dir":
		p.EventsDir = value
	case "issue_backend":
		p.IssueBackend = value
	case "max_agents":
		if unset {
			p.MaxAgents = nil
			return nil
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("max_agents must be an integer: %w", err)
		}
		p.MaxAgents = &n
	case "startup_timeout":
		if unset {
			p.StartupTimeout = nil
			return nil
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("startup_timeout must be an integer (seconds): %w", err)
		}
		p.StartupTimeout = &n
	default:
		return applyRestartPolicyField(p, key, value, unset)
	}
	return nil
}

// applyRestartPolicyField handles the restart_policy.* keys. It is split
// out of applyProfileField only so neither grows past the funlen limit;
// an unknown key still reports the same error to the user.
func applyRestartPolicyField(p *domain.DaemonProfile, key, value string, unset bool) error {
	target := map[string]**int{
		"restart_policy.output_timeout":     &p.RestartPolicy.OutputTimeout,
		"restart_policy.no_work_backoff":    &p.RestartPolicy.NoWorkBackoff,
		"restart_policy.idle_poll_interval": &p.RestartPolicy.IdlePollInterval,
	}[key]
	if target == nil {
		return fmt.Errorf("unknown key %q (run 'loom daemon profile set --help' for supported keys)", key)
	}
	n, err := parseProfileInt(key, value, unset)
	if err != nil {
		return err
	}
	*target = n
	return nil
}

// parseProfileInt returns nil for an unset, otherwise the parsed value.
// Restart-policy fields are all *int, so the caller assigns the result
// directly rather than branching on unset itself.
func parseProfileInt(key, value string, unset bool) (*int, error) {
	if unset {
		return nil, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil, fmt.Errorf("%s must be an integer (seconds): %w", key, err)
	}
	return &n, nil
}
