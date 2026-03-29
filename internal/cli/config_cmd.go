package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
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
  migrate      Migrate config files to the current version

Examples:
  loom config init                                    # Create default config
  loom config show                                    # Display config
  loom config add-repo default myrepo --path /tmp/r   # Add a repo
  loom config remove-repo default myrepo              # Remove a repo
  loom config migrate                                 # Migrate global config
  loom config migrate --project                       # Migrate project config`,
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

var configMigrateProject bool

var configMigrateCmd = &cobra.Command{
	Use:   "migrate [path]",
	Short: "Migrate config files to the current version",
	Long: `Upgrades config files to the current schema version. Creates a timestamped
backup before modifying. If no path is given, migrates ~/.loom/config.yaml.
Use --project to migrate the project-local loom.yaml instead.`,
	Args: cobra.MaximumNArgs(1),
	Run:  runConfigMigrate,
}

func init() {
	configInitCmd.Flags().BoolVar(&configInitForce, "force", false, "Overwrite existing config")
	configInitCmd.Flags().StringVar(&configInitWorkspace, "workspace", "", "Workspace name (default: current directory basename)")

	configAddRepoCmd.Flags().StringVar(&configAddRepoPath, "path", "", "Path to the repository (required)")
	_ = configAddRepoCmd.MarkFlagRequired("path")
	configAddRepoCmd.Flags().StringVar(&configAddRepoBranch, "branch", "", "Default branch")
	configAddRepoCmd.Flags().StringVar(&configAddRepoRemote, "remote", "", "Git remote name")

	configMigrateCmd.Flags().BoolVar(&configMigrateProject, "project", false, "Migrate project-local loom.yaml instead of global config")

	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configAddRepoCmd)
	configCmd.AddCommand(configRemoveRepoCmd)
	configCmd.AddCommand(configMigrateCmd)

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
		Version:          CurrentConfigVersion,
		DefaultWorkspace: wsName,
		Workspaces: map[string]WorkspaceConfig{
			wsName: {
				ID:    NewWorkspaceID(),
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
	configPath := GetConfigPath()
	data, err := os.ReadFile(configPath) // #nosec G304 — path from GetConfigPath(), not user input
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("No config file found at %s. Run 'loom config init' to create one.\n", configPath)
			return
		}
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

	if err := ValidateRemoteName(configAddRepoRemote); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
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

func runConfigMigrate(cmd *cobra.Command, args []string) {
	var path string
	if len(args) > 0 {
		path = args[0]
	} else if configMigrateProject {
		path = "loom.yaml"
	} else {
		path = GetConfigPath()
	}

	oldVersion, backupPath, err := MigrateConfigFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if oldVersion == CurrentConfigVersion {
		fmt.Printf("Config is already at version %d, no migration needed.\n", CurrentConfigVersion)
		return
	}

	fmt.Printf("Migrated from version %d to %d. Backup saved to %s.\n", oldVersion, CurrentConfigVersion, backupPath)
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
