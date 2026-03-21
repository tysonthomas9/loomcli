package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SandboxOneshotConfig holds settings for a one-shot sandbox execution
// triggered by the --sandbox flag on loom task / loom plan commands.
type SandboxOneshotConfig struct {
	AgentType    string // "task" or "plan"
	AgentName    string // worktree/workspace name
	WorktreePath string // resolved local worktree path
	ParentID     string // --parent filter (may be empty)
}

// runSandboxOneshot runs a single agent invocation inside an OpenShell sandbox.
// It pushes the current branch, creates a sandbox, waits for completion,
// fetches changes back, and cleans up the sandbox.
func runSandboxOneshot(cfg SandboxOneshotConfig) error {
	// 1. Determine the current branch
	branch, err := GetCurrentBranch(cfg.WorktreePath)
	if err != nil {
		return fmt.Errorf("getting current branch: %w", err)
	}
	if branch == "" {
		return fmt.Errorf("detached HEAD in %s; --sandbox requires a named branch", cfg.WorktreePath)
	}

	// 2. Resolve project directory (the main repo root, not the worktree)
	projectDir, err := resolveProjectDir(cfg.WorktreePath)
	if err != nil {
		return fmt.Errorf("resolving project directory: %w", err)
	}

	// 3. Load sandbox config from project loom.yaml (if it exists)
	sandboxCfg := loadSandboxDefaults(projectDir)

	// 4. Resolve repo URL for cloning inside the sandbox
	repoURL := resolveRepoURL(projectDir)
	if repoURL == "" {
		return fmt.Errorf("could not determine git remote URL for %s", projectDir)
	}

	// 5. Push the branch so the sandbox can clone it
	fmt.Printf("[sandbox] Pushing branch %s to origin...\n", branch)
	pushCmd := exec.Command("git", "push", "origin",
		fmt.Sprintf("%s:%s", branch, branch), "--force-with-lease")
	pushCmd.Dir = cfg.WorktreePath
	pushCmd.Stdout = os.Stdout
	pushCmd.Stderr = os.Stderr
	if err := pushCmd.Run(); err != nil {
		return fmt.Errorf("git push failed: %w", err)
	}

	// 6. Build sandbox strategy
	strategy := &SandboxStrategy{
		cfg:        sandboxCfg,
		projectDir: projectDir,
		repoURL:    repoURL,
	}

	// 7. Generate a unique sandbox name
	sandboxName := fmt.Sprintf("loom-%s-%x", cfg.AgentName, time.Now().UnixMilli())
	defer func() {
		fmt.Printf("[sandbox] Cleaning up sandbox %s...\n", sandboxName)
		strategy.deleteSandbox(sandboxName)
	}()
	fmt.Printf("[sandbox] Creating sandbox %s...\n", sandboxName)

	// Best-effort cleanup of stale sandbox with same name
	strategy.deleteSandbox(sandboxName)

	// 8. Build the openshell create arguments
	args := buildOneshotCreateArgs(strategy, sandboxName, branch, cfg, repoURL)

	// 9. Run the sandbox (interactive, with inherited stdio)
	cmd := exec.Command("openshell", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("openshell sandbox create: %w", err)
	}

	// 10. Wait for the sandbox to finish
	exitCode := 0
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return fmt.Errorf("waiting for sandbox: %w", err)
		}
	}

	// 11. Fetch changes back
	fmt.Printf("[sandbox] Fetching changes from origin/%s...\n", branch)
	fetchCmd := exec.Command("git", "fetch", "origin", branch)
	fetchCmd.Dir = projectDir
	fetchCmd.Stdout = os.Stdout
	fetchCmd.Stderr = os.Stderr
	if err := fetchCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[sandbox] Warning: git fetch failed: %v\n", err)
	}

	// Fast-forward merge the worktree branch
	mergeCmd := exec.Command("git", "-C", cfg.WorktreePath, "merge",
		"--ff-only", fmt.Sprintf("origin/%s", branch))
	mergeCmd.Stdout = os.Stdout
	mergeCmd.Stderr = os.Stderr
	if err := mergeCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[sandbox] Warning: fast-forward merge failed (may need manual resolution): %v\n", err)
	}

	// 12. Exit with the agent's exit code (sandbox cleanup handled by defer)
	if exitCode != 0 {
		os.Exit(exitCode)
	}
	return nil
}

// resolveProjectDir finds the main repository root from a worktree path.
// For git worktrees, the toplevel is the worktree itself, so we walk up
// to find the directory containing loom.yaml or .beads/.
func resolveProjectDir(worktreePath string) (string, error) {
	// First try: git rev-parse --show-toplevel from the worktree
	output, err := RunGitCommand(worktreePath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	toplevel := strings.TrimSpace(output)

	// Check if this toplevel has loom.yaml (it's the project root)
	if _, err := os.Stat(filepath.Join(toplevel, "loom.yaml")); err == nil {
		return toplevel, nil
	}

	// For worktrees under <project>/worktrees/<name>, walk up to find project root
	dir := toplevel
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		// Check for loom.yaml in parent
		if _, err := os.Stat(filepath.Join(parent, "loom.yaml")); err == nil {
			return parent, nil
		}
		// Check for .beads/ directory as alternate project root marker
		if _, err := os.Stat(filepath.Join(parent, ".beads")); err == nil {
			return parent, nil
		}
		dir = parent
	}

	// Fall back to the git toplevel
	return toplevel, nil
}

// loadSandboxDefaults loads sandbox configuration from loom.yaml in the project
// directory. If no config exists, returns sensible defaults.
func loadSandboxDefaults(projectDir string) SandboxConfig {
	pf, err := LoadProjectFile(projectDir)
	if err == nil && pf != nil && pf.Daemon != nil && pf.Daemon.Sandbox != nil {
		return *pf.Daemon.Sandbox
	}

	// Default config: open network, common providers
	return SandboxConfig{
		Network:   "open",
		Providers: []string{"claude", "github"},
	}
}

// buildOneshotCreateArgs constructs the openshell CLI arguments for a one-shot
// sandbox execution. Unlike the daemon's buildCreateArgs, this version enables
// TTY (interactive mode) and builds a bootstrap script tailored for one-shot use.
func buildOneshotCreateArgs(strategy *SandboxStrategy, sandboxName, branch string, cfg SandboxOneshotConfig, repoURL string) []string {
	args := []string{"sandbox", "create", "--name", sandboxName}

	// Upload the loom binary. --upload destination is a directory;
	// the file keeps its original name inside.
	if loomBin, err := os.Executable(); err == nil {
		uploadPath := loomBin
		if filepath.Base(loomBin) != "loom" {
			tmpLoom := filepath.Join(os.TempDir(), "loom")
			if data, err := os.ReadFile(loomBin); err == nil {
				if err := os.WriteFile(tmpLoom, data, 0755); err == nil {
					uploadPath = tmpLoom
				}
			}
		}
		args = append(args, "--upload", uploadPath+":/sandbox/bin")
	}

	// Container image (--from)
	if strategy.cfg.From != "" {
		args = append(args, "--from", strategy.cfg.From)
	}

	// Providers for credential injection
	for _, p := range strategy.cfg.Providers {
		args = append(args, "--provider", p)
	}

	// Policy YAML file — only pass custom policies.
	// Skip "open" policy to use default sandbox network access.
	if strategy.cfg.Network != "" && strategy.cfg.Network != "open" {
		args = append(args, "--policy", strategy.cfg.Network)
	}

	// NOTE: no --no-tty for interactive one-shot mode

	// Trailing command: bootstrap script
	args = append(args, "--")
	args = append(args, "sh", "-c", buildOneshotCommand(branch, cfg, repoURL, strategy.cfg.Backend))

	return args
}

// buildOneshotCommand constructs the shell bootstrap script for one-shot sandbox execution.
func buildOneshotCommand(branch string, cfg SandboxOneshotConfig, repoURL, backendOverride string) string {
	var sb strings.Builder
	sb.WriteString("set -e\n")
	sb.WriteString("chmod +x /sandbox/bin/loom\n")
	// The sandbox proxy intercepts HTTPS but its CA cert is not in the container's
	// trust store. Disable git SSL verification for sandbox network operations.
	sb.WriteString("export GIT_SSL_NO_VERIFY=1\n")
	sb.WriteString(fmt.Sprintf("git clone --branch %s --single-branch %s /sandbox/repo\n",
		shellQuote(branch), shellQuote(repoURL)))
	sb.WriteString("cd /sandbox/repo\n")
	sb.WriteString("git config user.name \"loom-sandbox\"\n")
	sb.WriteString("git config user.email \"loom-sandbox@local\"\n")

	// Build the loom command
	loomCmd := fmt.Sprintf("/sandbox/bin/loom %s %s",
		shellQuote(cfg.AgentType),
		shellQuote("worktrees/"+cfg.AgentName))
	if backendOverride != "" {
		loomCmd += fmt.Sprintf(" --backend %s", shellQuote(backendOverride))
	}
	if cfg.ParentID != "" {
		loomCmd += fmt.Sprintf(" --parent %s", shellQuote(cfg.ParentID))
	}
	sb.WriteString(loomCmd + "\n")

	// Sync beads state and push results back
	sb.WriteString("bd sync\n")
	sb.WriteString("git add -A\n")
	sb.WriteString(fmt.Sprintf("git diff --cached --quiet || git commit -m %s\n",
		shellQuote(fmt.Sprintf("sandbox agent work [%s]", branch))))
	sb.WriteString(fmt.Sprintf("git push origin %s\n", shellQuote(branch)))

	return sb.String()
}

