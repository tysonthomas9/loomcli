package workspace

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

var (
	wsAddDescription  string
	wsAddBranch       string
	wsAddDesignFormat string

	wsSetDesignFormat string

	wsShowJSON   bool
	wsStatusJSON bool
)

// validDesignFormat reports whether v is an accepted design_format value.
// Empty clears the setting (markdown behavior).
func validDesignFormat(v string) bool {
	return v == "" || v == "markdown" || v == "html"
}

var workspaceAddCmd = &cobra.Command{
	Use:   "add <KEY>",
	Short: "Create a new workspace in fleet-db",
	Long: `Create a new workspace. KEY must match fleet-db's key regex
(uppercase letters, digits, hyphens; 1-32 chars; starts with a letter).

Examples:
  loom workspace add MYPROJ --description "My project"
  loom workspace add ACME --branch main`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspaceAdd,
}

var workspaceUseCmd = &cobra.Command{
	Use:   "use <KEY>",
	Short: "Remember the last selected workspace for UI convenience",
	Long: `Persist KEY in ~/.loom/state.json as a UI selection hint.
Runtime commands no longer use this as an implicit default; set
LOOM_WORKSPACE or pass --workspace for command execution.`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspaceUse,
}

var workspaceSetCmd = &cobra.Command{
	Use:   "set <KEY>",
	Short: "Update workspace settings in fleet-db",
	Long: `Update settings on an existing workspace.

Examples:
  loom workspace set MYPROJ --design-format html
  loom workspace set MYPROJ --design-format markdown`,
	Args: cobra.ExactArgs(1),
	RunE: runWorkspaceSet,
}

var workspaceShowCmd = &cobra.Command{
	Use:   "show [KEY]",
	Short: "Show workspace details (defaults to active)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkspaceShow,
}

var workspaceStatusCmd = &cobra.Command{
	Use:   "status [KEY]",
	Short: "Show workspace lifecycle state",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runWorkspaceStatus,
}

func init() {
	workspaceAddCmd.Flags().StringVar(&wsAddDescription, "description", "", "Optional description")
	workspaceAddCmd.Flags().StringVar(&wsAddBranch, "branch", "", "Default branch (default: main)")
	workspaceAddCmd.Flags().StringVar(&wsAddDesignFormat, "design-format", "", "Planner design output format: markdown or html (default: markdown)")
	workspaceSetCmd.Flags().StringVar(&wsSetDesignFormat, "design-format", "", "Planner design output format: markdown or html (empty clears)")
	workspaceShowCmd.Flags().BoolVar(&wsShowJSON, "json", false, "JSON output")
	workspaceStatusCmd.Flags().BoolVar(&wsStatusJSON, "json", false, "JSON output")

	workspaceCmd.AddCommand(workspaceAddCmd)
	workspaceCmd.AddCommand(workspaceSetCmd)
	workspaceCmd.AddCommand(workspaceUseCmd)
	workspaceCmd.AddCommand(workspaceShowCmd)
	workspaceCmd.AddCommand(workspaceStatusCmd)
}

func runWorkspaceAdd(_ *cobra.Command, args []string) error {
	if !validDesignFormat(wsAddDesignFormat) {
		return fmt.Errorf("invalid --design-format %q: must be \"markdown\" or \"html\"", wsAddDesignFormat)
	}
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		key := args[0]
		ws, err := h.Store.Workspaces().Create(ctx, store.WorkspaceCreate{
			Key:           key,
			Name:          key,
			Description:   wsAddDescription,
			DefaultBranch: wsAddBranch,
			DesignFormat:  wsAddDesignFormat,
		})
		if err != nil {
			return fmt.Errorf("create workspace: %w", err)
		}
		if err := bootstrap.SetActiveWorkspaceKey(key); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: workspace created but selected-workspace hint update failed (run 'loom workspace use %s' to retry): %v\n", key, err)
		}
		fmt.Printf("Created workspace %s (mode=%s)\n", ws.Key, h.Mode())
		return nil
	})
}

func runWorkspaceSet(cmd *cobra.Command, args []string) error {
	if !cmd.Flags().Changed("design-format") {
		return fmt.Errorf("nothing to set: pass --design-format <markdown|html>")
	}
	if !validDesignFormat(wsSetDesignFormat) {
		return fmt.Errorf("invalid --design-format %q: must be \"markdown\" or \"html\"", wsSetDesignFormat)
	}
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		key := args[0]
		v := wsSetDesignFormat
		ws, err := h.Store.Workspaces().Update(ctx, key, store.WorkspaceUpdate{DesignFormat: &v})
		if err != nil {
			if cmdstore.IsNotFound(err) {
				return fmt.Errorf("workspace %q not found", key)
			}
			return fmt.Errorf("update workspace: %w", err)
		}
		if ws.DesignFormat != v {
			fmt.Fprintf(os.Stderr, "Warning: requested design_format %q but store returned %q; the fleet-db server may not support this field\n", v, ws.DesignFormat)
		}
		fmt.Printf("Updated workspace %s: design_format=%s\n", ws.Key, displayDesignFormat(ws.DesignFormat))
		return nil
	})
}

// displayDesignFormat renders the effective value for user output:
// empty means the markdown default.
func displayDesignFormat(v string) string {
	if v == "" {
		return "markdown (default)"
	}
	return v
}

func runWorkspaceUse(_ *cobra.Command, args []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		key := args[0]
		if _, err := h.Store.Workspaces().Get(ctx, key); err != nil {
			if cmdstore.IsNotFound(err) {
				return fmt.Errorf("workspace %q not found", key)
			}
			return err
		}
		if err := bootstrap.SetActiveWorkspaceKey(key); err != nil {
			return fmt.Errorf("save selected workspace: %w", err)
		}
		fmt.Printf("Selected workspace: %s\n", key)
		fmt.Printf("For runtime commands: export %s=%s\n", bootstrap.EnvWorkspace, key)
		return nil
	})
}

func runWorkspaceShow(_ *cobra.Command, args []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		key, err := pickWorkspaceKey(ctx, h.Store, args)
		if err != nil {
			return err
		}
		ws, repos, agents, roles, err := gatherWorkspaceDetails(ctx, h.Store, key)
		if err != nil {
			return fmt.Errorf("load workspace details: %w", err)
		}
		if wsShowJSON {
			return cmdstore.WriteJSON(struct {
				Workspace *domain.Workspace `json:"workspace"`
				Repos     []*domain.Repo    `json:"repos"`
				Agents    []*domain.Agent   `json:"agents"`
				Roles     []*domain.Role    `json:"roles"`
			}{ws, repos, agents, roles})
		}
		fmt.Printf("Workspace:    %s\n", ws.Key)
		fmt.Printf("Name:         %s\n", ws.Name)
		if ws.Description != "" {
			fmt.Printf("Description:  %s\n", ws.Description)
		}
		if ws.DefaultBranch != "" {
			fmt.Printf("Default branch: %s\n", ws.DefaultBranch)
		}
		if ws.DesignFormat != "" {
			fmt.Printf("Design format: %s\n", ws.DesignFormat)
		}
		fmt.Printf("Repos:        %d\n", len(repos))
		fmt.Printf("Agents:       %d\n", len(agents))
		fmt.Printf("Roles:        %d\n", len(roles))
		return nil
	})
}

// gatherWorkspaceDetails fetches the workspace plus its repos/agents/roles
// in parallel. Each List is independent and goes over HTTP, so serial
// fan-out adds 2-3× round-trip latency for no benefit. Returns the first
// error any of the four sub-fetches produces.
func gatherWorkspaceDetails(ctx context.Context, s store.Store, key string) (*domain.Workspace, []*domain.Repo, []*domain.Agent, []*domain.Role, error) {
	var (
		ws     *domain.Workspace
		repos  []*domain.Repo
		agents []*domain.Agent
		roles  []*domain.Role
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) { ws, err = s.Workspaces().Get(gctx, key); return })
	g.Go(func() (err error) { repos, err = s.Repos().List(gctx, key); return })
	g.Go(func() (err error) { agents, err = s.Agents().List(gctx, key); return })
	g.Go(func() (err error) { roles, err = s.Roles().List(gctx, key); return })
	if err := g.Wait(); err != nil {
		return nil, nil, nil, nil, err
	}
	return ws, repos, agents, roles, nil
}

func runWorkspaceStatus(_ *cobra.Command, args []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		key, err := pickWorkspaceKey(ctx, h.Store, args)
		if err != nil {
			return err
		}
		ws, err := h.Store.Workspaces().Get(ctx, key)
		if err != nil {
			return fmt.Errorf("get workspace: %w", err)
		}
		if wsStatusJSON {
			return cmdstore.WriteJSON(map[string]any{"key": ws.Key, "state": ws.State, "error": ws.ErrorMessage})
		}
		state := string(ws.State)
		if state == "" {
			state = "ready"
		}
		fmt.Printf("%s\t%s", ws.Key, state)
		if ws.ErrorMessage != "" {
			fmt.Printf("\t%s", ws.ErrorMessage)
		}
		fmt.Println()
		return nil
	})
}

// pickWorkspaceKey returns args[0] if provided, else the active workspace.
func pickWorkspaceKey(ctx context.Context, s store.Store, args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	return cmdstore.ActiveWorkspace(ctx, s)
}
