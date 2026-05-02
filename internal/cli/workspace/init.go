package workspace

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

var (
	initYes          bool
	initWorktreesDir string
	initNames        string
	initWorkspace    string
)

// Default agent names (fast things)
var defaultAgentNames = []string{"falcon", "nova"}
var suggestedAgentNames = []string{"falcon", "nova", "spark", "ember", "flux", "pulse", "dash", "swift", "bolt", "comet"}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize loom with guided setup",
	Long: `Initialize loom with a guided, interactive setup experience.

This command helps new users set up loom agents:
  - Checks prerequisites (git, fleet-db local mode)
  - Confirms fleet-db issue storage
  - Creates worktrees for agents (legacy mode)
  - Or validates workspace setup (workspace mode)

Flags:
  -y, --yes              Non-interactive mode with defaults
  --worktrees-dir DIR    Override worktrees directory (legacy mode only)
  --names NAMES          Comma-separated worktree names (legacy mode only)
  --workspace NAME       Initialize within an existing workspace

Examples:
  loom init                           # Interactive legacy setup
  loom init --yes                     # Non-interactive with defaults
  loom init --workspace myws          # Workspace-aware setup
  loom init --workspace myws --yes    # Non-interactive workspace setup`,
	Args: cobra.NoArgs,
	Run:  runInit,
}

func init() {
	initCmd.Flags().BoolVarP(&initYes, "yes", "y", false, "Non-interactive mode with defaults")
	initCmd.Flags().StringVar(&initWorktreesDir, "worktrees-dir", "", "Worktrees directory (default: from LOOM_WORKTREES_DIR or 'worktrees')")
	initCmd.Flags().StringVar(&initNames, "names", "", "Comma-separated worktree names for non-interactive mode")
	initCmd.Flags().StringVar(&initWorkspace, "workspace", "", "Workspace name for workspace-aware setup")
	cli.RegisterCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) {
	// Check if workspace mode requested
	if initWorkspace != "" {
		runInitWorkspace(cmd, args)
		return
	}

	deps := cli.GetDeps(cmd)

	fmt.Println("")
	fmt.Println("🔧 Loom Setup Wizard")
	fmt.Println("====================")
	fmt.Println("")

	// Step 1: Check prerequisites
	fmt.Println("Step 1: Prerequisites")
	if !checkPrerequisites(deps) {
		os.Exit(1)
	}
	fmt.Println("")

	// Step 2: Confirm issue storage
	fmt.Println("Step 2: Issue storage")
	if !initBeads(deps) {
		os.Exit(1)
	}
	fmt.Println("")

	// Step 3: Create worktrees directory
	fmt.Println("Step 3: Create worktrees directory")
	worktreesDir := getWorktreesDirForInit()
	if !createWorktreesDir(worktreesDir) {
		os.Exit(1)
	}
	fmt.Println("")

	// Step 4: Create worktrees
	fmt.Println("Step 4: Create agent worktrees")
	names := createWorktrees(deps, worktreesDir)
	fmt.Println("")

	// Step 5: Show summary
	showSummary(worktreesDir, names)
}

func runInitWorkspace(cmd *cobra.Command, _ []string) {
	deps := cli.GetDeps(cmd)

	fmt.Println("")
	fmt.Println("🔧 Loom Workspace Setup")
	fmt.Println("=========================")
	fmt.Println("")

	fmt.Println("Step 1: Prerequisites")
	if !checkPrerequisites(deps) {
		os.Exit(1)
	}
	fmt.Println("")

	fmt.Println("Step 2: Validate workspace")
	ws := validateWorkspaceExists()
	fmt.Println("")

	fmt.Println("Step 3: Issue backend")
	initWorkspaceBeads(deps, ws)
	fmt.Println("")

	showWorkspaceSummary(ws)
}

// validateWorkspaceExists loads config and validates the workspace exists.
func validateWorkspaceExists() config.WorkspaceConfig {
	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil {
		fmt.Fprintf(os.Stderr, "✗ No loom config found. Create a workspace first with:\n")
		fmt.Fprintf(os.Stderr, "  loom workspace create %s --repos /path/to/repo\n", initWorkspace)
		os.Exit(1)
	}

	ws, exists := cfg.Workspaces[initWorkspace]
	if !exists {
		available := make([]string, 0, len(cfg.Workspaces))
		for name := range cfg.Workspaces {
			available = append(available, name)
		}
		sort.Strings(available)
		fmt.Fprintf(os.Stderr, "✗ Workspace %q not found.\n", initWorkspace)
		if len(available) > 0 {
			fmt.Fprintf(os.Stderr, "  Available workspaces: %s\n", strings.Join(available, ", "))
		}
		os.Exit(1)
	}
	fmt.Printf("✓ Workspace %q found at %s\n", initWorkspace, ws.Path)
	fmt.Printf("  Repos: %d\n", len(ws.Repos))
	for _, repo := range ws.Repos {
		fmt.Printf("    - %s\n", repo.Name)
	}
	return ws
}

// initWorkspaceBeads is retained as a workspace setup hook while fleet-db is canonical.
func initWorkspaceBeads(deps *cli.Deps, ws config.WorkspaceConfig) {
	_ = deps
	_ = ws
	fmt.Println("→ Fleet-db issue storage is used; no local task database init required")
}

func initBeadsInWorkspace(deps *cli.Deps, wsPath string) {
	_ = deps
	_ = wsPath
	fmt.Println("→ Fleet-db issue storage is used; no local task database init required")
}

func showWorkspaceSummary(ws config.WorkspaceConfig) {
	fmt.Println("Workspace ready! 🎉")
	fmt.Println("")
	fmt.Println("Directory structure:")
	fmt.Printf("  %s/\n", ws.Path)
	fmt.Println("    .loom/          Runtime state")
	for _, repo := range ws.Repos {
		fmt.Printf("    %s/         Repo worktree\n", repo.Name)
	}
	fmt.Println("")
	fmt.Println("Next steps:")
	fmt.Println("  1. Create tasks:     loom serve, then use New issue in the UI")
	if len(ws.Repos) > 0 {
		fmt.Printf("  2. Run agent:        loom agent %s\n", ws.Repos[0].Name)
	}
	fmt.Println("  3. Monitor:          loom monitor")
	fmt.Println("")
}
