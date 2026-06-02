package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
)

// sandboxHostGateway is the address a sandbox container uses to reach a service
// (e.g. `loom serve`) on the host when only a localhost server URL is known.
const sandboxHostGateway = "host.docker.internal"

// sandboxLoomPath is where the loom binary is uploaded inside the sandbox. The
// sandbox root is read-write, so no directory needs pre-creating.
const sandboxLoomPath = "/sandbox/loom"

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

	// Resolved at runtime (not set by the flag): the FleetDB/loom-serve endpoint
	// the in-container agent uses to claim/update tasks (v5 task state is in
	// FleetDB, not the repo), plus the workspace it belongs to.
	ServerURL   string // LOOM_SERVER_URL exported into the sandbox
	WorkspaceID string // LOOM_WORKSPACE exported into the sandbox
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

	// v5 keeps task state in FleetDB, not in the repo, so the agent inside the
	// sandbox must reach the loom-serve/FleetDB HTTP API. Fail fast if we can't.
	if err := applySandboxFleetConfig(&cfg); err != nil {
		return err
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
	exitCode, err := runSandboxAgent(sandboxName, defaultSandboxConfig(), branch, cfg, repoURL)
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

// runSandboxAgent runs the agent inside a fresh sandbox using the OpenShell
// v0.0.53 flow: create (keep-alive) → upload the loom binary → exec the
// bootstrap. (Earlier OpenShell took a single `create --upload … -- cmd` call;
// v0.0.53 split upload and exec into their own subcommands.) Returns the agent's
// exit code.
func runSandboxAgent(name string, cfg SandboxConfig, branch string, oneshot SandboxOneshotConfig, repoURL string) (int, error) {
	if err := runOpenshell(buildSandboxCreateArgs(name, cfg)); err != nil {
		return 0, fmt.Errorf("openshell sandbox create: %w", err)
	}
	if err := uploadLoomBinary(name); err != nil {
		return 0, err
	}
	return runOpenshellExit([]string{"sandbox", "exec", "-n", name, "--", "sh", "-c",
		buildOneshotCommand(branch, oneshot, repoURL, cfg.Backend)})
}

// uploadLoomBinary uploads the running loom binary to sandboxLoomPath in the sandbox.
func uploadLoomBinary(name string) error {
	loomBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate loom binary: %w", err)
	}
	if err := runOpenshell([]string{"sandbox", "upload", name, loomBin, sandboxLoomPath}); err != nil {
		return fmt.Errorf("openshell sandbox upload: %w", err)
	}
	return nil
}

// runOpenshell runs an openshell subcommand with inherited stdio and waits.
func runOpenshell(args []string) error {
	cmd := exec.Command("openshell", args...) //nolint:gosec // args built internally
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// runOpenshellExit runs an openshell subcommand and returns its exit code (0/nil
// on success; the remote exit code with a nil error on a clean non-zero exit).
func runOpenshellExit(args []string) (int, error) {
	cmd := exec.Command("openshell", args...) //nolint:gosec // args built internally
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 0, fmt.Errorf("openshell exec: %w", err)
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

// applySandboxFleetConfig resolves the FleetDB/loom-serve endpoint + workspace the
// in-container agent needs (v5 task state is in FleetDB) and records them on cfg.
// Returns an error if no reachable server or workspace can be determined, so the
// caller fails before booting a sandbox whose agent could never claim work.
func applySandboxFleetConfig(cfg *SandboxOneshotConfig) error {
	cfg.ServerURL = resolveSandboxServerURL()
	if cfg.ServerURL == "" {
		return fmt.Errorf("--sandbox needs a FleetDB server the container can reach: " +
			"run `loom serve` and set LOOM_SERVER_URL, or set LOOM_SANDBOX_SERVER_URL to a container-reachable URL")
	}
	cfg.WorkspaceID = resolveSandboxWorkspace()
	if cfg.WorkspaceID == "" {
		return fmt.Errorf("--sandbox needs a workspace: no active workspace and LOOM_WORKSPACE is unset")
	}
	return nil
}

// resolveSandboxServerURL returns a FleetDB / loom-serve URL the sandbox
// container can reach, or "" if none is configured. It prefers an explicit
// LOOM_SANDBOX_SERVER_URL (the operator-supplied, container-reachable address);
// otherwise it rewrites a localhost LOOM_SERVER_URL to the container's host
// gateway so the in-container agent can reach the host's serve.
func resolveSandboxServerURL() string {
	if v := strings.TrimSpace(os.Getenv("LOOM_SANDBOX_SERVER_URL")); v != "" {
		return v
	}
	host := strings.TrimSpace(os.Getenv("LOOM_SERVER_URL"))
	if host == "" {
		return ""
	}
	for _, lh := range []string{"localhost", "127.0.0.1", "0.0.0.0"} {
		host = strings.ReplaceAll(host, lh, sandboxHostGateway)
	}
	return host
}

// resolveSandboxWorkspace returns the workspace ID to pass to the in-sandbox
// agent's API backend (LOOM_WORKSPACE), preferring the active workspace, then env.
func resolveSandboxWorkspace() string {
	if ws, err := config.ResolveActiveWorkspace(); err == nil && ws != nil && ws.ID != "" {
		return ws.ID
	}
	if v := strings.TrimSpace(os.Getenv("LOOM_WORKSPACE")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv("LOOM_WORKSPACE_ID"))
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
	for _, marker := range []string{".loom", "loom.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}

// buildSandboxCreateArgs builds the `openshell sandbox create` arguments. The
// sandbox is created keep-alive (no trailing command and no --upload — those are
// separate subcommands in OpenShell v0.0.53) so the loom binary can be uploaded
// and the bootstrap exec'd afterwards.
func buildSandboxCreateArgs(name string, cfg SandboxConfig) []string {
	args := []string{"sandbox", "create", "--name", name}
	if cfg.From != "" {
		args = append(args, "--from", cfg.From)
	}
	for _, p := range cfg.Providers {
		args = append(args, "--provider", p)
	}
	if len(cfg.Providers) > 0 {
		// Non-interactive: auto-create missing providers from local credentials
		// rather than prompting (which errors without a TTY).
		args = append(args, "--auto-providers")
	}
	// Only pass --policy for custom networks; the default "open" relies on the
	// sandbox's built-in policy.
	if cfg.Network != "" && cfg.Network != "open" {
		args = append(args, "--policy", cfg.Network)
	}
	return args
}

// buildOneshotCommand builds the shell bootstrap script run inside the sandbox:
// clone the branch, run loom, then commit and push the code changes back.
func buildOneshotCommand(branch string, oneshot SandboxOneshotConfig, repoURL, backendOverride string) string {
	var sb strings.Builder
	sb.WriteString("set -e\n")
	sb.WriteString("chmod +x " + sandboxLoomPath + "\n")
	// The OpenShell proxy intercepts HTTPS but its CA cert isn't in the container
	// trust store, so disable git SSL verification for sandbox network operations.
	sb.WriteString("export GIT_SSL_NO_VERIFY=1\n")
	sb.WriteString(fmt.Sprintf("git clone --branch %s --single-branch %s /sandbox/repo\n",
		shellQuote(branch), shellQuote(repoURL)))
	sb.WriteString("cd /sandbox/repo\n")
	sb.WriteString("git config user.name \"loom-sandbox\"\n")
	sb.WriteString("git config user.email \"loom-sandbox@local\"\n")

	// v5 keeps task state in FleetDB, not the repo. Point the in-container agent
	// at the loom-serve HTTP API (LOOM_SERVER_URL auto-selects the api backend)
	// so it can claim/update/close work; LOOM_WORKSPACE scopes it.
	if oneshot.ServerURL != "" {
		sb.WriteString("export LOOM_SERVER_URL=" + shellQuote(oneshot.ServerURL) + "\n")
	}
	if oneshot.WorkspaceID != "" {
		sb.WriteString("export LOOM_WORKSPACE=" + shellQuote(oneshot.WorkspaceID) + "\n")
	}

	loomCmd := fmt.Sprintf("%s %s %s", sandboxLoomPath,
		shellQuote(oneshot.AgentType), shellQuote("worktrees/"+oneshot.AgentName))
	if backendOverride != "" {
		loomCmd += " --backend " + shellQuote(backendOverride)
	}
	if oneshot.ParentID != "" {
		loomCmd += " --parent " + shellQuote(oneshot.ParentID)
	}
	sb.WriteString(loomCmd + "\n")

	// Task state lives in FleetDB (v5), not in the repo, so there is no
	// issue-tracker sync step here; only code changes travel back via git.
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
		slog.Warn("sandbox delete failed", "name", name, "output", string(out), "err", err)
	}
}

// shellQuote wraps s in single quotes for safe use inside a /bin/sh -c string,
// escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
