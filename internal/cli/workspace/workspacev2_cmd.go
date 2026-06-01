package workspace

import (
	"context"
	"fmt"
	"regexp"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

var (
	wsAddDescription string
	wsAddBranch      string
	wsAddPath        string

	wsShowJSON   bool
	wsStatusJSON bool
)

var workspaceKeyPattern = regexp.MustCompile(`^[A-Z]([A-Z0-9-]{0,30}[A-Z0-9])?$`)

var workspaceAddCmd = &cobra.Command{
	Use:   "add <KEY>",
	Short: "Create a new local FleetDB-backed workspace",
	Long: `Create a new workspace in FleetDB and bind it to a local workspace
directory on this machine. KEY must match fleet-db's key regex
(uppercase letters, digits, hyphens; 1-32 chars; starts with a letter).

Examples:
  loom workspace add MYPROJ --description "My project"
  loom workspace add ACME --branch main
  loom workspace add ACME --path ~/code/acme`,
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
	workspaceAddCmd.Flags().StringVar(&wsAddPath, "path", "", "Workspace directory path (default: ~/.loom/workspaces/<KEY>)")
	workspaceShowCmd.Flags().BoolVar(&wsShowJSON, "json", false, "JSON output")
	workspaceStatusCmd.Flags().BoolVar(&wsStatusJSON, "json", false, "JSON output")

	workspaceCmd.AddCommand(workspaceAddCmd)
	workspaceCmd.AddCommand(workspaceUseCmd)
	workspaceCmd.AddCommand(workspaceShowCmd)
	workspaceCmd.AddCommand(workspaceStatusCmd)
}

func runWorkspaceAdd(_ *cobra.Command, args []string) error {
	return cmdstore.WithStore(func(ctx context.Context, h *bootstrap.StoreHandle) error {
		key := args[0]
		if !workspaceKeyPattern.MatchString(key) {
			return fmt.Errorf("workspace key %q must match ^[A-Z]([A-Z0-9-]{0,30}[A-Z0-9])?$", key)
		}
		branch := wsAddBranch
		if branch == "" {
			branch = "main"
		}
		create := workspacemgr.BuildStoreBackedCreateWorkspace(h.Store)
		result, err := create(ctx, service.WorkspaceCreateRequest{
			Name:        key,
			Description: wsAddDescription,
			Type:        "empty",
			Path:        wsAddPath,
			Branch:      branch,
		})
		if err != nil {
			return fmt.Errorf("create workspace: %w", err)
		}
		if result.WorkspaceID != key {
			return fmt.Errorf("created workspace key %q, want %q", result.WorkspaceID, key)
		}
		fmt.Printf("Created workspace %s at %s (mode=%s)\n", result.WorkspaceID, result.WorkspacePath, h.Mode())
		return nil
	})
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
