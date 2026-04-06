package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
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
  migrate      Migrate config files to the current version

Examples:
  loom config init                                    # Create default config
  loom config show                                    # Display config
  loom config add-repo default myrepo --path /tmp/r   # Add a repo
  loom config remove-repo default myrepo              # Remove a repo
  loom config migrate                                 # Migrate global config
  loom config migrate --project                       # Migrate project config
  loom config migrate --dry-run                       # Preview without applying`,
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

var (
	configMigrateProject bool
	configMigrateDryRun  bool
)

var configMigrateCmd = &cobra.Command{
	Use:   "migrate [path]",
	Short: "Migrate config files to the current version",
	Long: `Upgrades config files to the current schema version. Creates a timestamped
backup before modifying. If no path is given, migrates ~/.loom/config.yaml.
Use --project to migrate the project-local loom.yaml instead.
Use --dry-run to preview migrations without applying.`,
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
	configMigrateCmd.Flags().BoolVar(&configMigrateDryRun, "dry-run", false, "Preview migrations without applying changes")

	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configAddRepoCmd)
	configCmd.AddCommand(configRemoveRepoCmd)
	configCmd.AddCommand(configMigrateCmd)

	rootCmd.AddCommand(configCmd)
}

func runConfigInit(cmd *cobra.Command, args []string) {
	configPath := config.GetConfigPath()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot determine current directory: %v\n", err)
		os.Exit(1)
	}

	wsName := configInitWorkspace
	if wsName == "" {
		wsName = filepath.Base(cwd)
	}

	cfg := &config.LoomConfig{
		Version:          config.CurrentConfigVersion,
		DefaultWorkspace: wsName,
		Workspaces: map[string]config.WorkspaceConfig{
			wsName: {
				ID:    config.NewWorkspaceID(),
				Path:  cwd,
				Repos: []config.RepoConfig{},
			},
		},
	}

	// Existence check + save inside the lock to prevent TOCTOU race
	// between concurrent init and create.
	if err := config.WithConfigLock(func() error {
		if !configInitForce {
			if _, err := os.Stat(configPath); err == nil {
				return fmt.Errorf("config already exists at %s; use --force to overwrite", configPath)
			}
		}
		return config.SaveConfigUnlocked(cfg)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Config created at %s\n", configPath)
}

func runConfigShow(cmd *cobra.Command, args []string) {
	configPath := config.GetConfigPath()
	data, err := os.ReadFile(configPath) // #nosec G304 — path from config.GetConfigPath(), not user input
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

	if err := config.ValidateRemoteName(configAddRepoRemote); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := config.WithConfigLock(func() error {
		cfg, err := config.LoadConfigUnlocked()
		if err != nil {
			return err
		}
		if cfg == nil {
			return fmt.Errorf("no config found. Run 'loom config init' first")
		}

		ws, ok := cfg.Workspaces[wsName]
		if !ok {
			available := make([]string, 0, len(cfg.Workspaces))
			for name := range cfg.Workspaces {
				available = append(available, name)
			}
			sort.Strings(available)
			return fmt.Errorf("workspace %q not found. Available: %s", wsName, strings.Join(available, ", "))
		}

		for _, r := range ws.Repos {
			if r.Name == repoName {
				return fmt.Errorf("repo %q already exists in workspace %q. Remove it first or use a different name", repoName, wsName)
			}
		}

		ws.Repos = append(ws.Repos, config.RepoConfig{
			Name:          repoName,
			Path:          configAddRepoPath,
			DefaultBranch: configAddRepoBranch,
			Remote:        configAddRepoRemote,
		})
		cfg.Workspaces[wsName] = ws

		return config.SaveConfigUnlocked(cfg)
	}); err != nil {
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
		path = config.GetConfigPath()
	}

	// Read and parse to determine current version for the listing.
	content, err := os.ReadFile(path) //nolint:gosec // path from CLI arg or config
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var data map[string]interface{}
	if err := yaml.Unmarshal(content, &data); err != nil {
		fmt.Fprintf(os.Stderr, "Error: parsing config: %v\n", err)
		os.Exit(1)
	}
	if data == nil {
		data = make(map[string]interface{})
	}

	currentVersion := config.GetConfigVersion(data)

	if currentVersion == config.CurrentConfigVersion {
		fmt.Printf("Config is already at version %d, no migration needed.\n", config.CurrentConfigVersion)
		return
	}

	if currentVersion > config.CurrentConfigVersion {
		fmt.Fprintf(os.Stderr, "Error: config version %d is newer than supported version %d; please upgrade loom.\n",
			currentVersion, config.CurrentConfigVersion)
		os.Exit(1)
	}

	// List pending migrations.
	pending := config.PendingMigrations(currentVersion)
	hasDestructive := false
	for _, m := range pending {
		if m.Destructive {
			hasDestructive = true
			break
		}
	}

	fmt.Printf("Config at version %d, target version %d.\n\n", currentVersion, config.CurrentConfigVersion)

	if configMigrateDryRun {
		fmt.Println("Migrations that would be applied:")
	} else {
		fmt.Println("Migrations to apply:")
	}
	for _, m := range pending {
		label := ""
		if m.Destructive {
			label = " [DESTRUCTIVE]"
		}
		fmt.Printf("  v%d → v%d:%s %s\n", m.FromVersion, m.ToVersion, label, m.Description)
	}

	if configMigrateDryRun {
		fmt.Println("\nNo changes made (dry run).")
		return
	}

	if hasDestructive {
		fmt.Fprintf(os.Stderr, "\nWarning: destructive migrations may be incompatible with older versions of loom.\n")
	}

	oldVersion, backupPath, err := config.MigrateConfigFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if oldVersion == config.CurrentConfigVersion {
		// File was migrated between our pre-read and MigrateConfigFile (e.g. by auto-migration).
		fmt.Printf("\nConfig was already at version %d, no changes made.\n", config.CurrentConfigVersion)
	} else {
		fmt.Printf("\nMigrated from version %d to %d. Backup saved to %s.\n",
			oldVersion, config.CurrentConfigVersion, backupPath)
	}
}

func runConfigRemoveRepo(cmd *cobra.Command, args []string) {
	wsName := args[0]
	repoName := args[1]

	if err := config.WithConfigLock(func() error {
		cfg, err := config.LoadConfigUnlocked()
		if err != nil {
			return err
		}
		if cfg == nil {
			return fmt.Errorf("no config found. Run 'loom config init' first")
		}

		ws, ok := cfg.Workspaces[wsName]
		if !ok {
			available := make([]string, 0, len(cfg.Workspaces))
			for name := range cfg.Workspaces {
				available = append(available, name)
			}
			sort.Strings(available)
			return fmt.Errorf("workspace %q not found. Available: %s", wsName, strings.Join(available, ", "))
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
			return fmt.Errorf("repo %q not found in workspace %q", repoName, wsName)
		}

		cfg.Workspaces[wsName] = ws

		return config.SaveConfigUnlocked(cfg)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Removed repo %q from workspace %q.\n", repoName, wsName)
}
