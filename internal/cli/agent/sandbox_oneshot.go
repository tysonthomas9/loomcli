package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
)

// SandboxConfig configures OpenShell sandbox execution for a one-shot agent run.
//
// NOTE: daemon-mode sandbox config (per-agent `execution: sandbox`) is a separate,
// deferred change — v5 moved daemon config into the FleetDB/domain store, so it
// needs domain + store plumbing. See SANDBOX-PORT-TODO.md and the rescue tag
// rescue-sandbox-openshell-pr20 for the original daemon SandboxStrategy.
type SandboxConfig struct {
	Providers []string // credential providers injected into the sandbox (e.g. "claude", "github")
	Network   string   // "open" (default) or a path to a custom OPA/Rego policy YAML
	From      string   // container base image (--from); empty uses the openshell default
	Backend   string   // backend override inside the sandbox; empty inherits the host default
}

// SandboxOneshotConfig holds settings for a single sandboxed agent invocation
// triggered by the --sandbox flag on `loom task` / `loom plan`.
type SandboxOneshotConfig struct {
	AgentType    string // "task" or "plan"
	AgentName    string // worktree/workspace name
	WorktreePath string // resolved local worktree path
	ParentID     string // --parent epic filter (may be empty)
}

// handleSandboxMode validates flags and runs a one-shot sandbox agent, exiting the
// process on error or with the agent's non-zero exit code. Called from runTask /
// runPlan when --sandbox is set.
func handleSandboxMode(agentType, agentName, worktreePath, parentID string, autoMode bool) {
	if autoMode {
		fmt.Fprintf(os.Stderr, "Error: --sandbox and --auto are mutually exclusive\n")
		cli.ExitWithFlush(1)
	}
	if err := runSandboxOneshot(SandboxOneshotConfig{
		AgentType:    agentType,
		AgentName:    agentName,
		WorktreePath: worktreePath,
		ParentID:     parentID,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		cli.ExitWithFlush(1)
	}
}

// runSandboxOneshot runs a single agent invocation inside an OpenShell sandbox:
// push the branch, create the sandbox (cloning the branch and running loom
// inside), wait for completion, fast-forward the results back, and clean up.
func runSandboxOneshot(cfg SandboxOneshotConfig) error {
	branch, err := cli.GetCurrentBranch(cfg.WorktreePath)
	if err != nil {
		return fmt.Errorf("getting current branch: %w", err)
	}
	if branch == "" {
		return fmt.Errorf("detached HEAD in %s; --sandbox requires a named branch", cfg.WorktreePath)
	}

	projectDir, err := resolveSandboxProjectDir(cfg.WorktreePath)
	if err != nil {
		return fmt.Errorf("resolving project directory: %w", err)
	}

	repoURL := resolveSandboxRepoURL(projectDir)
	if repoURL == "" {
		return fmt.Errorf("could not determine git remote URL for %s", projectDir)
	}

	if err := pushSandboxBranch(cfg.WorktreePath, branch); err != nil {
		return err
	}

	sandboxName := fmt.Sprintf("loom-%s-%x", cfg.AgentName, time.Now().UnixMilli())
	defer func() {
		fmt.Printf("[sandbox] Cleaning up sandbox %s...\n", sandboxName)
		deleteSandbox(sandboxName)
	}()
	deleteSandbox(sandboxName) // best-effort cleanup of a stale sandbox from a prior crash

	fmt.Printf("[sandbox] Creating sandbox %s...\n", sandboxName)
	args := buildOneshotCreateArgs(defaultSandboxConfig(), sandboxName, branch, cfg, repoURL)
	exitCode, err := runOpenshellSandbox(args)
	if err != nil {
		return err
	}

	mergeSandboxResults(projectDir, cfg.WorktreePath, branch)

	if exitCode != 0 {
		cli.ExitWithFlush(exitCode)
	}
	return nil
}

// pushSandboxBranch pushes the worktree's branch to origin so the sandbox can
// clone it. Uses --force-with-lease so a re-run updates the remote branch safely.
func pushSandboxBranch(worktreePath, branch string) error {
	fmt.Printf("[sandbox] Pushing branch %s to origin...\n", branch)
	cmd := exec.Command("git", "push", "origin", //nolint:gosec // branch derived from the local worktree
		fmt.Sprintf("%s:%s", branch, branch), "--force-with-lease")
	cmd.Dir = worktreePath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git push failed: %w", err)
	}
	return nil
}

// runOpenshellSandbox starts the openshell process with inherited stdio (so the
// run is interactive) and waits for it, returning the sandbox exit code.
func runOpenshellSandbox(args []string) (int, error) {
	cmd := exec.Command("openshell", args...) //nolint:gosec // args built by buildOneshotCreateArgs
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("openshell sandbox create: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 0, fmt.Errorf("waiting for sandbox: %w", err)
	}
	return 0, nil
}

// mergeSandboxResults fetches the branch the sandbox pushed and fast-forwards the
// local worktree to it. Failures are warnings — the agent's work is already on
// the remote branch and can be merged manually.
func mergeSandboxResults(projectDir, worktreePath, branch string) {
	fmt.Printf("[sandbox] Fetching changes from origin/%s...\n", branch)
	fetchCmd := exec.Command("git", "fetch", "origin", branch) //nolint:gosec // branch derived from the local worktree
	fetchCmd.Dir = projectDir
	fetchCmd.Stdout = os.Stdout
	fetchCmd.Stderr = os.Stderr
	if err := fetchCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[sandbox] Warning: git fetch failed: %v\n", err)
	}

	mergeCmd := exec.Command("git", "-C", worktreePath, "merge", //nolint:gosec // branch derived from the local worktree
		"--ff-only", "origin/"+branch)
	mergeCmd.Stdout = os.Stdout
	mergeCmd.Stderr = os.Stderr
	if err := mergeCmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "[sandbox] Warning: fast-forward merge failed (may need manual resolution): %v\n", err)
	}
}

// defaultSandboxConfig returns the built-in sandbox defaults for one-shot runs.
// v5 dropped loom.yaml; daemon-level FleetDB-backed config is a deferred change.
func defaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Network:   "open",
		Providers: []string{"claude", "github"},
	}
}

// resolveSandboxRepoURL returns the origin remote URL for projectDir, or "" on error.
func resolveSandboxRepoURL(projectDir string) string {
	out, err := cli.RunGitCommand(projectDir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// resolveSandboxProjectDir finds the main repository root from a worktree path by
// walking up from the git toplevel to the nearest project-root marker.
func resolveSandboxProjectDir(worktreePath string) (string, error) {
	out, err := cli.RunGitCommand(worktreePath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	toplevel := strings.TrimSpace(out)
	if isSandboxProjectRoot(toplevel) {
		return toplevel, nil
	}
	for dir := toplevel; ; {
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		if isSandboxProjectRoot(parent) {
			return parent, nil
		}
		dir = parent
	}
	return toplevel, nil
}

// isSandboxProjectRoot reports whether dir looks like a loom project root.
func isSandboxProjectRoot(dir string) bool {
	for _, marker := range []string{".loom", ".beads", "loom.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// buildOneshotCreateArgs constructs the `openshell sandbox create ...` arguments
// for an interactive one-shot run (PTY enabled — no --no-tty).
func buildOneshotCreateArgs(cfg SandboxConfig, sandboxName, branch string, oneshot SandboxOneshotConfig, repoURL string) []string {
	args := []string{"sandbox", "create", "--name", sandboxName}

	// Upload the loom binary. --upload accepts a single value; the destination is
	// a directory and the file keeps its name, so it must be named "loom".
	if loomBin, err := os.Executable(); err == nil {
		uploadPath := loomBin
		if filepath.Base(loomBin) != "loom" {
			tmpLoom := filepath.Join(os.TempDir(), "loom")
			if data, err := os.ReadFile(loomBin); err == nil { //nolint:gosec // loomBin from os.Executable()
				if err := os.WriteFile(tmpLoom, data, 0o755); err == nil { //nolint:gosec // uploaded binary must be executable
					uploadPath = tmpLoom
				}
			}
		}
		args = append(args, "--upload", uploadPath+":/sandbox/bin")
	}

	if cfg.From != "" {
		args = append(args, "--from", cfg.From)
	}
	for _, p := range cfg.Providers {
		args = append(args, "--provider", p)
	}
	// Only pass --policy for custom networks; the default "open" relies on the
	// sandbox's built-in policy (passing --policy can hang provisioning on some
	// OpenShell versions).
	if cfg.Network != "" && cfg.Network != "open" {
		args = append(args, "--policy", cfg.Network)
	}

	args = append(args, "--", "sh", "-c", buildOneshotCommand(branch, oneshot, repoURL, cfg.Backend))
	return args
}

// buildOneshotCommand builds the shell bootstrap script run inside the sandbox:
// clone the branch, run loom, sync beads state, and push the results back.
func buildOneshotCommand(branch string, oneshot SandboxOneshotConfig, repoURL, backendOverride string) string {
	var sb strings.Builder
	sb.WriteString("set -e\n")
	sb.WriteString("chmod +x /sandbox/bin/loom\n")
	// The OpenShell proxy intercepts HTTPS but its CA cert isn't in the container
	// trust store, so disable git SSL verification for sandbox network operations.
	sb.WriteString("export GIT_SSL_NO_VERIFY=1\n")
	sb.WriteString(fmt.Sprintf("git clone --branch %s --single-branch %s /sandbox/repo\n",
		shellQuote(branch), shellQuote(repoURL)))
	sb.WriteString("cd /sandbox/repo\n")
	sb.WriteString("git config user.name \"loom-sandbox\"\n")
	sb.WriteString("git config user.email \"loom-sandbox@local\"\n")

	loomCmd := fmt.Sprintf("/sandbox/bin/loom %s %s",
		shellQuote(oneshot.AgentType), shellQuote("worktrees/"+oneshot.AgentName))
	if backendOverride != "" {
		loomCmd += " --backend " + shellQuote(backendOverride)
	}
	if oneshot.ParentID != "" {
		loomCmd += " --parent " + shellQuote(oneshot.ParentID)
	}
	sb.WriteString(loomCmd + "\n")

	sb.WriteString("bd sync\n")
	sb.WriteString("git add -A\n")
	sb.WriteString(fmt.Sprintf("git diff --cached --quiet || git commit -m %s\n",
		shellQuote(fmt.Sprintf("sandbox agent work [%s]", branch))))
	sb.WriteString(fmt.Sprintf("git push origin %s\n", shellQuote(branch)))
	return sb.String()
}

// openshellBinary returns the openshell CLI binary name.
func openshellBinary() string { return "openshell" }

// deleteSandbox runs `openshell sandbox delete <name>` with a 30s timeout.
// Best-effort: errors are logged, not returned.
func deleteSandbox(name string) {
	if name == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, openshellBinary(), "sandbox", "delete", name) //nolint:gosec // name is internally generated
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[sandbox] delete %s: %s: %v", name, string(out), err)
	}
}

// shellQuote wraps s in single quotes for safe use inside a /bin/sh -c string,
// escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
