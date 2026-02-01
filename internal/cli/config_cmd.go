package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	configInitForce     bool
	configInitWorkspace string

	configAddRepoPath   string
	configAddRepoBranch string
	configAddRepoRemote string
)

var configCmd = &cobra.Command{
	Use:     "config",
	Short:   "Manage loom configuration",
	GroupID: "config",
	Long: `Manage loom's workspace configuration stored in ~/.loom/config.yaml.

Subcommands:
  init         Create a new config file
  show         Display current configuration
  add-repo     Add a repository to a workspace
  remove-repo  Remove a repository from a workspace

Examples:
  loom config init                                    # Create default config
  loom config show                                    # Display config
  loom config add-repo default myrepo --path /tmp/r   # Add a repo
  loom config remove-repo default myrepo              # Remove a repo`,
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a new loom config file",
	Long: `Create a new loom config file at ~/.loom/config.yaml.

Creates a default workspace named after the current directory.
Will not overwrite an existing config without --force.`,
	Args: cobra.NoArgs,
	Run:  runConfigInit,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Display current loom configuration",
	Args:  cobra.NoArgs,
	Run:   runConfigShow,
}

var configAddRepoCmd = &cobra.Command{
	Use:   "add-repo <workspace> <repo-name>",
	Short: "Add a repository to a workspace",
	Args:  cobra.ExactArgs(2),
	Run:   runConfigAddRepo,
}

var configRemoveRepoCmd = &cobra.Command{
	Use:   "remove-repo <workspace> <repo-name>",
	Short: "Remove a repository from a workspace",
	Args:  cobra.ExactArgs(2),
	Run:   runConfigRemoveRepo,
}

func init() {
	configInitCmd.Flags().BoolVar(&configInitForce, "force", false, "Overwrite existing config")
	configInitCmd.Flags().StringVar(&configInitWorkspace, "workspace", "", "Workspace name (default: current directory basename)")

	configAddRepoCmd.Flags().StringVar(&configAddRepoPath, "path", "", "Path to the repository (required)")
	_ = configAddRepoCmd.MarkFlagRequired("path")
	configAddRepoCmd.Flags().StringVar(&configAddRepoBranch, "branch", "", "Default branch")
	configAddRepoCmd.Flags().StringVar(&configAddRepoRemote, "remote", "", "Git remote name")

	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configAddRepoCmd)
	configCmd.AddCommand(configRemoveRepoCmd)

	rootCmd.AddCommand(configCmd)
}

func runConfigInit(cmd *cobra.Command, args []string) {
	configPath := GetConfigPath()

	if !configInitForce {
		if _, err := os.Stat(configPath); err == nil {
			fmt.Fprintf(os.Stderr, "Config already exists at %s. Use --force to overwrite.\n", configPath)
			os.Exit(1)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine current directory: %v\n", err)
		os.Exit(1)
	}

	wsName := configInitWorkspace
	if wsName == "" {
		wsName = filepath.Base(cwd)
	}

	cfg := &LoomConfig{
		DefaultWorkspace: wsName,
		Workspaces: map[string]WorkspaceConfig{
			wsName: {
				Path:  cwd,
				Repos: []RepoConfig{},
			},
		},
	}

	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Config created at %s\n", configPath)
}

func runConfigShow(cmd *cobra.Command, args []string) {
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg == nil {
		fmt.Printf("No config file found at %s. Run 'loom config init' to create one.\n", GetConfigPath())
		return
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(data))
}

func runConfigAddRepo(cmd *cobra.Command, args []string) {
	wsName := args[0]
	repoName := args[1]

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg == nil {
		fmt.Fprintf(os.Stderr, "No config found. Run 'loom config init' first.\n")
		os.Exit(1)
	}

	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		available := make([]string, 0, len(cfg.Workspaces))
		for name := range cfg.Workspaces {
			available = append(available, name)
		}
		sort.Strings(available)
		fmt.Fprintf(os.Stderr, "Workspace %q not found. Available: %s\n", wsName, strings.Join(available, ", "))
		os.Exit(1)
	}

	for _, r := range ws.Repos {
		if r.Name == repoName {
			fmt.Fprintf(os.Stderr, "Repo %q already exists in workspace %q. Remove it first or use a different name.\n", repoName, wsName)
			os.Exit(1)
		}
	}

	ws.Repos = append(ws.Repos, RepoConfig{
		Name:          repoName,
		Path:          configAddRepoPath,
		DefaultBranch: configAddRepoBranch,
		Remote:        configAddRepoRemote,
	})
	cfg.Workspaces[wsName] = ws

	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Added repo %q to workspace %q.\n", repoName, wsName)
}

func runConfigRemoveRepo(cmd *cobra.Command, args []string) {
	wsName := args[0]
	repoName := args[1]

	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if cfg == nil {
		fmt.Fprintf(os.Stderr, "No config found. Run 'loom config init' first.\n")
		os.Exit(1)
	}

	ws, ok := cfg.Workspaces[wsName]
	if !ok {
		available := make([]string, 0, len(cfg.Workspaces))
		for name := range cfg.Workspaces {
			available = append(available, name)
		}
		sort.Strings(available)
		fmt.Fprintf(os.Stderr, "Workspace %q not found. Available: %s\n", wsName, strings.Join(available, ", "))
		os.Exit(1)
	}

	found := false
	for i, r := range ws.Repos {
		if r.Name == repoName {
			ws.Repos = append(ws.Repos[:i], ws.Repos[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		fmt.Fprintf(os.Stderr, "Repo %q not found in workspace %q.\n", repoName, wsName)
		os.Exit(1)
	}

	cfg.Workspaces[wsName] = ws

	if err := SaveConfig(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Removed repo %q from workspace %q.\n", repoName, wsName)
}
