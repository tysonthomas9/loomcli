package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/sandbox"
)

// SandboxOneshotConfig holds settings for a single sandboxed agent invocation
// triggered by the --sandbox flag on `loom task` / `loom plan`.
type SandboxOneshotConfig struct {
	AgentType    string // "task" or "plan"
	AgentName    string // worktree/workspace name
	WorktreePath string // resolved local worktree path
	ParentID     string // --parent epic filter (may be empty)

	// Resolved at runtime (not set by the flag): the FleetDB endpoint + a
	// workspace-scoped credential the in-container agent uses for BOTH config
	// and task state (v5 keeps both in FleetDB). With LOOM_SERVER_URL unset the
	// agent's issue backend resolves to `fleetdb`, so it talks ONLY to fleet-db,
	// confined by the scoped key (least privilege).
	FleetDBURL   string // LOOM_FLEET_DB_URL exported into the sandbox
	FleetDBKey   string // LOOM_FLEET_DB_API_KEY (workspace-scoped developer key)
	FleetDBActor string // LOOM_FLEET_DB_ACTOR
	WorkspaceID  string // LOOM_WORKSPACE
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
	// sandbox must reach the FleetDB HTTP API. Fail fast if we can't.
	if err := applySandboxFleetConfig(&cfg); err != nil {
		return err
	}

	// Mint a short-TTL, workspace-scoped developer credential for this run (when
	// the host holds an admin key); revoke it on teardown so a finished sandbox
	// leaves no live credential.
	revokeCred, err := provisionSandboxCredential(context.Background(), &cfg)
	if err != nil {
		return err
	}
	defer revokeCred()

	if err := pushSandboxBranch(cfg.WorktreePath, branch); err != nil {
		return err
	}

	sandboxName := fmt.Sprintf("loom-%s-%x", cfg.AgentName, time.Now().UnixMilli())
	defer func() {
		fmt.Printf("[sandbox] Cleaning up sandbox %s...\n", sandboxName)
		sandbox.DeleteSandbox(sandboxName)
	}()
	sandbox.DeleteSandbox(sandboxName) // best-effort cleanup of a stale sandbox from a prior crash

	fmt.Printf("[sandbox] Creating sandbox %s...\n", sandboxName)
	exitCode, err := runSandboxAgent(sandboxName, sandbox.DefaultConfig(), branch, cfg, sandboxCloneURL(repoURL))
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
// v0.0.53 flow (see package sandbox): create (keep-alive, trivial command) →
// upload loom → upload bootstrap → exec it. Returns the agent's exit code.
func runSandboxAgent(name string, cfg sandbox.Config, branch string, oneshot SandboxOneshotConfig, repoURL string) (int, error) {
	// Auto-generate an OPA policy opening the fleet-db + repo endpoints unless an
	// explicit LOOM_SANDBOX_POLICY was given (the default "open" doesn't open them).
	if cfg.Network == "open" {
		if eps := sandbox.PolicyEndpoints(oneshot.FleetDBURL, repoURL); len(eps) > 0 {
			policyPath, cleanupPolicy, err := sandbox.WritePolicy(eps, []string{cfg.LoomCmd(), "/usr/bin/git", "/usr/bin/curl"})
			if err != nil {
				return 0, err
			}
			defer cleanupPolicy()
			cfg.Network = policyPath
		}
	}
	if err := sandbox.RunOpenshell(sandbox.BuildCreateArgs(name, cfg)); err != nil {
		return 0, fmt.Errorf("openshell sandbox create: %w", err)
	}
	if cfg.UploadsLoom() {
		loomBin, err := sandbox.ResolveLoomBinary()
		if err != nil {
			return 0, err
		}
		if err := sandbox.RunOpenshell([]string{"sandbox", "upload", name, loomBin, sandbox.LoomPath}); err != nil {
			return 0, fmt.Errorf("openshell sandbox upload loom: %w", err)
		}
	}
	scriptPath, cleanup, err := sandbox.WriteBootstrapScript(buildOneshotCommand(branch, oneshot, repoURL, cfg))
	if err != nil {
		return 0, err
	}
	defer cleanup()
	if err := sandbox.RunOpenshell([]string{"sandbox", "upload", name, scriptPath, sandbox.BootstrapPath}); err != nil {
		return 0, fmt.Errorf("openshell sandbox upload bootstrap: %w", err)
	}
	return sandbox.RunOpenshellExit([]string{"sandbox", "exec", "-n", name, "--", "sh", sandbox.BootstrapPath})
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

// resolveSandboxRepoURL returns the origin remote URL for projectDir, or "" on error.
func resolveSandboxRepoURL(projectDir string) string {
	out, err := cli.RunGitCommand(projectDir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// sandboxCloneURL returns the git URL the sandbox uses to clone the repo. The host
// and the sandbox reach the host at different addresses, so a localhost origin is
// rewritten to the container host gateway — or overridden via LOOM_SANDBOX_REPO_URL
// with an address both sides can reach (e.g. the host LAN IP or a git endpoint).
func sandboxCloneURL(origin string) string {
	if v := strings.TrimSpace(os.Getenv("LOOM_SANDBOX_REPO_URL")); v != "" {
		return v
	}
	out, gw := origin, sandbox.HostGateway()
	for _, lh := range []string{"localhost", "127.0.0.1", "0.0.0.0"} {
		out = strings.ReplaceAll(out, lh, gw)
	}
	return out
}

// applySandboxFleetConfig resolves the FleetDB endpoint + workspace the
// in-container agent needs (v5 task state is in FleetDB) and records them on cfg.
// Returns an error if no reachable server or workspace can be determined, so the
// caller fails before booting a sandbox whose agent could never claim work.
func applySandboxFleetConfig(cfg *SandboxOneshotConfig) error {
	cfg.FleetDBURL = resolveSandboxFleetDBURL()
	if cfg.FleetDBURL == "" {
		return fmt.Errorf("--sandbox needs a FleetDB the container can reach: set LOOM_FLEET_DB_URL " +
			"(+ LOOM_FLEET_DB_API_KEY), or LOOM_SANDBOX_FLEETDB_URL to a container-reachable URL")
	}
	cfg.WorkspaceID = resolveSandboxWorkspace()
	if cfg.WorkspaceID == "" {
		return fmt.Errorf("--sandbox needs a workspace: no active workspace and LOOM_WORKSPACE is unset")
	}
	return nil
}

// provisionSandboxCredential mints a short-TTL, workspace-scoped `developer` API
// key for a unique sandbox actor and records it on cfg, returning a revoke func.
// It runs only when the host holds an admin fleet-db credential
// (LOOM_FLEET_DB_API_KEY + LOOM_FLEET_DB_URL); otherwise it is a no-op, leaving
// the sandbox to use the host config's ambient credential (e.g. a dev/auth-off
// fleet-db). Provisioning errors are fatal: a configured admin path that fails
// must not silently fall back to an over-privileged key.
func provisionSandboxCredential(ctx context.Context, cfg *SandboxOneshotConfig) (func(), error) {
	key, actor, revoke, err := sandbox.ProvisionCredential(ctx, cfg.WorkspaceID, cfg.AgentName)
	if err != nil {
		return nil, err
	}
	if actor != "" { // a scoped key was minted (admin path); else ambient credential
		cfg.FleetDBKey = key
		cfg.FleetDBActor = actor
		fmt.Printf("[sandbox] Provisioned scoped developer credential for %s\n", actor)
	}
	return revoke, nil
}

// resolveSandboxFleetDBURL returns a FleetDB URL the sandbox container can
// reach, or "" if none is configured. It prefers an explicit
// LOOM_SANDBOX_FLEETDB_URL (the operator-supplied, container-reachable address);
// otherwise it rewrites a localhost LOOM_FLEET_DB_URL to the container's host
// gateway so the in-container agent can reach the host's fleet-db.
func resolveSandboxFleetDBURL() string {
	return sandbox.FleetDBURL()
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

// buildOneshotCommand builds the shell bootstrap script run inside the sandbox:
// clone the branch, run loom, then commit and push the code changes back.
func buildOneshotCommand(branch string, oneshot SandboxOneshotConfig, repoURL string, cfg sandbox.Config) string {
	var sb strings.Builder
	sb.WriteString("set -e\n")
	if cfg.UploadsLoom() {
		sb.WriteString("chmod +x " + sandbox.LoomPath + "\n")
	}
	// The OpenShell proxy intercepts HTTPS but its CA cert isn't in the container
	// trust store, so disable git SSL verification for sandbox network operations.
	sb.WriteString("export GIT_SSL_NO_VERIFY=1\n")
	sb.WriteString(fmt.Sprintf("git clone --branch %s --single-branch %s /sandbox/repo\n",
		sandbox.ShellQuote(branch), sandbox.ShellQuote(repoURL)))
	sb.WriteString("cd /sandbox/repo\n")
	sb.WriteString("git config user.name \"loom-sandbox\"\n")
	sb.WriteString("git config user.email \"loom-sandbox@local\"\n")

	// v5 keeps BOTH config and task state in FleetDB. Point the in-container
	// agent at fleet-db directly (LOOM_SERVER_URL unset → the `fleetdb` issue
	// backend) with its workspace-scoped credential, so it authenticates AND is
	// authorized in exactly one workspace.
	if oneshot.FleetDBURL != "" {
		sb.WriteString("export LOOM_FLEET_DB_URL=" + sandbox.ShellQuote(oneshot.FleetDBURL) + "\n")
	}
	if oneshot.FleetDBKey != "" {
		sb.WriteString("export LOOM_FLEET_DB_API_KEY=" + sandbox.ShellQuote(oneshot.FleetDBKey) + "\n")
	}
	if oneshot.FleetDBActor != "" {
		sb.WriteString("export LOOM_FLEET_DB_ACTOR=" + sandbox.ShellQuote(oneshot.FleetDBActor) + "\n")
	}
	if oneshot.WorkspaceID != "" {
		sb.WriteString("export LOOM_WORKSPACE=" + sandbox.ShellQuote(oneshot.WorkspaceID) + "\n")
	}

	loomCmd := fmt.Sprintf("%s %s %s", cfg.LoomCmd(),
		sandbox.ShellQuote(oneshot.AgentType), sandbox.ShellQuote("worktrees/"+oneshot.AgentName))
	if cfg.Backend != "" {
		loomCmd += " --backend " + sandbox.ShellQuote(cfg.Backend)
	}
	if oneshot.ParentID != "" {
		loomCmd += " --parent " + sandbox.ShellQuote(oneshot.ParentID)
	}
	sb.WriteString(loomCmd + "\n")

	// Task state lives in FleetDB (v5), not in the repo, so there is no
	// issue-tracker sync step here; only code changes travel back via git.
	sb.WriteString("git add -A\n")
	sb.WriteString(fmt.Sprintf("git diff --cached --quiet || git commit -m %s\n",
		sandbox.ShellQuote(fmt.Sprintf("sandbox agent work [%s]", branch))))
	sb.WriteString(fmt.Sprintf("git push origin %s\n", sandbox.ShellQuote(branch)))
	return sb.String()
}
