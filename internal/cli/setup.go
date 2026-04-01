package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	setupYes     bool
	setupBackend string
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Comprehensive setup wizard for loom",
	Long: `Run a comprehensive, interactive setup wizard that configures:

  - Backend selection (claude, codex, opencode) with auto-detection
  - Project config (loom.yaml) with backend and agent entries
  - API key secrets (~/.loom/secrets/)
  - Worktree creation for agents

Unlike 'loom init' (lightweight quick start), 'loom setup' provides a
full configuration experience. It is idempotent — run it multiple times
to update or add configuration.

Flags:
  -y, --yes          Non-interactive mode with defaults
  --backend NAME     Pre-select backend (skip detection/prompt)

Examples:
  loom setup                    # Interactive full setup
  loom setup --yes              # Non-interactive with defaults
  loom setup --backend codex    # Pre-select codex backend`,
	Args: cobra.NoArgs,
	Run:  runSetup,
}

func init() {
	setupCmd.Flags().BoolVarP(&setupYes, "yes", "y", false, "Non-interactive mode with defaults")
	setupCmd.Flags().StringVar(&setupBackend, "backend", "", "Pre-select backend (claude, codex, opencode)")
	rootCmd.AddCommand(setupCmd)
}

// knownBackends lists the backend CLIs we look for on PATH.
var knownBackends = []string{"claude", "codex", "opencode"}

// backendAPIKeys maps backend names to the API key env var and secret name.
var backendAPIKeys = map[string]struct {
	envVar     string
	secretName string
}{
	"claude":   {envVar: "ANTHROPIC_API_KEY", secretName: "anthropic-api-key"}, //nolint:gosec // G101 — env var name, not a credential
	"codex":    {envVar: "OPENAI_API_KEY", secretName: "openai-api-key"},       //nolint:gosec // G101 — env var name, not a credential
	"opencode": {envVar: "OPENAI_API_KEY", secretName: "openai-api-key"},       //nolint:gosec // G101 — env var name, not a credential
}

func runSetup(_ *cobra.Command, _ []string) {
	fmt.Println("")
	fmt.Println("Loom Setup Wizard")
	fmt.Println("=================")
	fmt.Println("")

	// Step 1: Prerequisites
	fmt.Println("Step 1: Prerequisites")
	if !checkPrerequisites() {
		os.Exit(1)
	}
	fmt.Println("")

	// Step 2: Backend detection & selection
	fmt.Println("Step 2: Backend selection")
	backend := stepDetectBackends()
	fmt.Println("")

	// Step 3: Worktree setup (delegate to existing helpers)
	fmt.Println("Step 3: Worktree setup")
	worktreesDir := getWorktreesDirForInit()
	if !createWorktreesDir(worktreesDir) {
		os.Exit(1)
	}

	// Set initYes so createWorktrees uses non-interactive mode if --yes
	origInitYes := initYes
	initYes = setupYes
	defer func() { initYes = origInitYes }()
	names := createWorktrees(worktreesDir)
	fmt.Println("")

	// Step 4: Generate loom.yaml
	fmt.Println("Step 4: Project configuration (loom.yaml)")
	stepGenerateLoomYaml(backend, names)
	fmt.Println("")

	// Step 5: Secrets configuration
	fmt.Println("Step 5: Secrets configuration")
	stepConfigureSecrets(backend)
	fmt.Println("")

	// Step 6: Summary
	showSetupSummary(backend, names)
}

// stepDetectBackends scans PATH for known backend CLIs and prompts for selection.
func stepDetectBackends() string {
	// If --backend flag was provided, use it directly
	if setupBackend != "" {
		fmt.Printf("-> Using pre-selected backend: %s\n", setupBackend)
		return setupBackend
	}

	// Detect available backends
	var available []string
	for _, name := range knownBackends {
		if _, err := defaultDeps.LookPath(name); err == nil {
			available = append(available, name)
			fmt.Printf("  [available] %s\n", name)
		} else {
			fmt.Printf("  [not found] %s\n", name)
		}
	}

	if len(available) == 0 {
		fmt.Println("  No backends detected on PATH.")
	}

	// Determine default
	defaultBackend := "claude"
	if len(available) > 0 {
		defaultBackend = available[0]
	}

	if setupYes {
		fmt.Printf("-> Using default backend: %s\n", defaultBackend)
		return defaultBackend
	}

	// Interactive prompt
	selected := promptString("Select backend", defaultBackend)
	selected = strings.TrimSpace(selected)
	if selected == "" {
		selected = defaultBackend
	}

	if !isKnownBackend(selected) {
		fmt.Fprintf(os.Stderr, "  Warning: %q is not a known backend (known: %s)\n",
			selected, strings.Join(knownBackends, ", "))
	}

	fmt.Printf("-> Selected backend: %s\n", selected)
	return selected
}

// stepGenerateLoomYaml creates or updates loom.yaml with backend and agent entries.
func stepGenerateLoomYaml(backend string, worktreeNames []string) {
	loomYamlPath := filepath.Join(".", "loom.yaml")

	// Check for existing loom.yaml
	existing, err := LoadProjectFile(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Warning: error reading existing loom.yaml: %v\n", err)
		existing = nil
	}

	if existing != nil {
		fmt.Println("  Existing loom.yaml found.")
		if !setupYes {
			if !promptYesNo("Update loom.yaml with new settings?", true) {
				fmt.Println("  -> Skipping loom.yaml update")
				return
			}
		}
	}

	// Build project file: start from existing to preserve customizations, or create fresh
	var pf *ProjectFile
	if existing != nil {
		pf = existing
	} else {
		pf = &ProjectFile{
			Daemon: &DaemonSettings{
				PIDFile:   ".loom/daemon.pid",
				LogDir:    ".loom/logs",
				EventsDir: ".loom/events",
				RestartPolicy: RestartPolicy{
					MaxRetries:     intPtr(3),
					BackoffInitial: intPtr(2),
					BackoffMax:     intPtr(300),
				},
			},
		}
	}

	// Update fields managed by setup
	pf.Version = CurrentConfigVersion
	pf.Backend = backend

	// Merge agent entries: add new worktrees without duplicating existing ones
	for _, name := range worktreeNames {
		if !agentEntryExists(pf.Agents, name) {
			pf.Agents = append(pf.Agents, AgentEntry{
				Worktree: name,
				Role:     "task",
				Auto:     true,
			})
		}
	}

	// Marshal to YAML
	data, err := yaml.Marshal(pf)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Error: failed to marshal loom.yaml: %v\n", err)
		return
	}

	header := "# loom.yaml - Project configuration for loom daemon\n# See: loom help setup\n"
	content := header + string(data)

	if err := os.WriteFile(loomYamlPath, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "  Error: failed to write loom.yaml: %v\n", err)
		return
	}
	fmt.Println("  -> loom.yaml written")
}

// stepConfigureSecrets prompts for API keys and writes them to ~/.loom/secrets/.
func stepConfigureSecrets(backend string) {
	keyInfo, ok := backendAPIKeys[backend]
	if !ok {
		fmt.Printf("  No known API key for backend %q, skipping.\n", backend)
		return
	}

	// Check if already configured via environment
	if val := os.Getenv(keyInfo.envVar); val != "" {
		fmt.Printf("  [ok] %s already set via environment variable\n", keyInfo.envVar)
		return
	}

	// Check if already configured via LOOM_SECRET_ env var
	loomEnvName := "LOOM_SECRET_" + toEnvName(keyInfo.secretName)
	if val := os.Getenv(loomEnvName); val != "" {
		fmt.Printf("  [ok] %s already set via %s\n", keyInfo.secretName, loomEnvName)
		return
	}

	// Check if secret file already exists
	secretsDir := filepath.Join(GetConfigDir(), "secrets")
	secretPath := filepath.Join(secretsDir, keyInfo.secretName)
	if _, err := os.Stat(secretPath); err == nil {
		fmt.Printf("  [ok] Secret file already exists: %s\n", secretPath)
		return
	}

	// In non-interactive mode, skip secret prompts
	if setupYes {
		fmt.Printf("  -> Skipping %s (non-interactive mode)\n", keyInfo.envVar)
		fmt.Printf("     Set it later: echo 'your-key' > %s && chmod 600 %s\n", secretPath, secretPath)
		return
	}

	// Prompt for the key
	apiKey := promptString(fmt.Sprintf("  Enter %s (empty to skip)", keyInfo.envVar), "")
	if apiKey == "" {
		fmt.Printf("  -> Skipped. Set it later: echo 'your-key' > %s && chmod 600 %s\n", secretPath, secretPath)
		return
	}

	// Create secrets directory
	if err := os.MkdirAll(secretsDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "  Error: failed to create secrets directory: %v\n", err)
		return
	}

	// Write secret file
	if err := os.WriteFile(secretPath, []byte(apiKey+"\n"), 0600); err != nil {
		fmt.Fprintf(os.Stderr, "  Error: failed to write secret file: %v\n", err)
		return
	}
	fmt.Printf("  -> Saved to %s (mode 0600)\n", secretPath)
}

// isKnownBackend checks if a backend name is in the known list.
func isKnownBackend(name string) bool {
	for _, b := range knownBackends {
		if b == name {
			return true
		}
	}
	return false
}

// agentEntryExists checks if an agent entry for the given worktree already exists.
func agentEntryExists(agents []AgentEntry, worktree string) bool {
	for _, a := range agents {
		if a.Worktree == worktree {
			return true
		}
	}
	return false
}

// showSetupSummary displays the final setup summary.
func showSetupSummary(backend string, worktreeNames []string) {
	fmt.Println("Setup complete!")
	fmt.Println("")
	fmt.Println("Configuration:")
	fmt.Printf("  Backend:    %s\n", backend)
	fmt.Printf("  Config:     loom.yaml\n")
	fmt.Printf("  Worktrees:  %d (%s)\n", len(worktreeNames), strings.Join(worktreeNames, ", "))
	fmt.Println("")
	fmt.Println("Next steps:")
	fmt.Println("  1. Create tasks:     bd create --title=\"My task\" --type=task")
	fmt.Println("  2. Run planner:      loom plan " + getFirstName(worktreeNames))
	fmt.Println("  3. Review plans:     loom lead")
	fmt.Println("  4. Implement:        loom task " + getFirstName(worktreeNames))
	fmt.Println("  5. Start daemon:     loom daemon")
	fmt.Println("  6. Monitor:          loom monitor")
	fmt.Println("")
}
