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
  agent_backend   string (workspace default AI backend, e.g. claude|codex|flue)
  max_agents      integer
  startup_timeout integer (seconds)`,
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
		if p.AgentBackend != "" {
			fmt.Printf("Agent backend:  %s\n", p.AgentBackend)
		}
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
	case "agent_backend":
		p.AgentBackend = value
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
		return fmt.Errorf("unknown key %q (run 'loom daemon profile set --help' for supported keys)", key)
	}
	return nil
}
