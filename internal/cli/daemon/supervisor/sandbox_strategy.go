package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/cmdstore"
	"github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/sandbox"
)

// sandboxDaemonKeyTTLSeconds bounds a daemon-provisioned sandbox credential.
const sandboxDaemonKeyTTLSeconds = 6 * 60 * 60 // 6 hours

// sandboxFleetEnv is the FleetDB connectivity exported into a sandbox container.
type sandboxFleetEnv struct {
	URL       string
	Key       string
	Actor     string
	Workspace string
}

// buildSandboxCommand provisions an OpenShell sandbox for ap and returns the
// (unstarted) `sandbox exec` command whose lifetime is the agent's. It mirrors
// the proven one-shot flow, supervisor-driven: push the worktree branch → create
// the sandbox (v0.0.53: `-- true`) → upload loom + bootstrap → return the exec
// cmd. spawnAgent owns cmd.Start(); waitForAgent owns cmd.Wait(); postExitCleanup
// → cleanupSandbox fetches the work back and deletes the sandbox.
//
//nolint:funlen // Linear setup: branch → push → provision → create → upload → exec.
func (s *Supervisor) buildSandboxCommand(ap *AgentProcess) (*exec.Cmd, error) {
	branch, err := cli.GetCurrentBranch(ap.WorktreePath)
	if err != nil || branch == "" {
		return nil, fmt.Errorf("sandbox agent %q: resolve worktree branch: %w", ap.Entry.Worktree, err)
	}
	repoURL := s.sandboxRepoURL(ap)
	if repoURL == "" {
		return nil, fmt.Errorf("sandbox agent %q: no container-reachable repo URL", ap.Entry.Worktree)
	}
	if err := sandboxGit(ap.WorktreePath, "push", "origin", branch+":"+branch, "--force-with-lease"); err != nil {
		return nil, fmt.Errorf("sandbox push branch %q: %w", branch, err)
	}

	loomBin, err := sandbox.ResolveLoomBinary()
	if err != nil {
		return nil, err
	}
	fleetEnv, revoke, err := s.provisionSandboxCredential(ap)
	if err != nil {
		return nil, err
	}

	cfg := sandbox.DefaultConfig()
	if cfg.Backend == "" {
		cfg.Backend = s.GetEffectiveBackend(ap)
	}

	name := fmt.Sprintf("loom-%s-%x", ap.Entry.Worktree, time.Now().UnixMilli())
	ap.Mu.Lock()
	ap.SandboxName = name
	ap.sandboxRevoke = revoke
	ap.Mu.Unlock()

	sandbox.DeleteSandbox(name) // best-effort stale cleanup
	if err := sandbox.RunOpenshell(sandbox.BuildCreateArgs(name, cfg)); err != nil {
		return nil, fmt.Errorf("sandbox create: %w", err)
	}
	if err := sandbox.RunOpenshell([]string{"sandbox", "upload", name, loomBin, sandbox.LoomPath}); err != nil {
		return nil, fmt.Errorf("sandbox upload loom: %w", err)
	}
	scriptPath, cleanup, err := sandbox.WriteBootstrapScript(s.buildSandboxBootstrap(ap, branch, repoURL, fleetEnv, cfg.Backend))
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if err := sandbox.RunOpenshell([]string{"sandbox", "upload", name, scriptPath, sandbox.BootstrapPath}); err != nil {
		return nil, fmt.Errorf("sandbox upload bootstrap: %w", err)
	}
	return exec.Command(sandbox.OpenshellBinary(), "sandbox", "exec", "-n", name, "--", "sh", sandbox.BootstrapPath), nil //nolint:gosec // args built internally
}

// cleanupSandbox fetches the branch the sandbox pushed, fast-forwards the host
// worktree, revokes the scoped credential, and deletes the sandbox. Called from
// postExitCleanup for execution:sandbox agents after the exec process exits.
func (s *Supervisor) cleanupSandbox(ap *AgentProcess) {
	ap.Mu.Lock()
	name := ap.SandboxName
	revoke := ap.sandboxRevoke
	ap.SandboxName = ""
	ap.sandboxRevoke = nil
	ap.Mu.Unlock()

	if branch, _ := cli.GetCurrentBranch(ap.WorktreePath); branch != "" {
		if err := sandboxGit(s.ProjectDir, "fetch", "origin", branch); err != nil {
			slog.Warn("sandbox cleanup: fetch failed", "worktree", ap.Entry.Worktree, "err", err)
		}
		if err := sandboxGit(ap.WorktreePath, "merge", "--ff-only", "origin/"+branch); err != nil {
			slog.Warn("sandbox cleanup: ff-merge failed (may need manual resolution)", "worktree", ap.Entry.Worktree, "err", err)
		}
	}
	if revoke != nil {
		revoke()
	}
	sandbox.DeleteSandbox(name)
}

// sandboxRepoURL returns a container-reachable git URL for the agent's repo: the
// worktree's origin remote rewritten to the container host gateway (or an
// explicit LOOM_SANDBOX_REPO_URL). Falls back to RepoConfig.RemoteURL.
func (s *Supervisor) sandboxRepoURL(ap *AgentProcess) string {
	if v := strings.TrimSpace(os.Getenv("LOOM_SANDBOX_REPO_URL")); v != "" {
		return v
	}
	origin := ""
	if out, err := exec.Command("git", "-C", ap.WorktreePath, "remote", "get-url", "origin").Output(); err == nil { //nolint:gosec // dir from resolved worktree
		origin = strings.TrimSpace(string(out))
	}
	if origin == "" && ap.RepoConfig != nil {
		origin = ap.RepoConfig.RemoteURL
	}
	if origin == "" {
		return ""
	}
	for _, lh := range []string{"localhost", "127.0.0.1", "0.0.0.0"} {
		origin = strings.ReplaceAll(origin, lh, sandbox.HostGateway)
	}
	return origin
}

// provisionSandboxCredential mints a short-TTL, workspace-scoped developer key
// for this sandbox run (when the daemon host holds an admin fleet-db key) and
// returns the FleetDB env + a revoke func. Without an admin key it passes the
// ambient fleet-db URL through (dev / auth-off). Provisioning errors are fatal.
func (s *Supervisor) provisionSandboxCredential(ap *AgentProcess) (sandboxFleetEnv, func(), error) {
	env := sandboxFleetEnv{URL: sandboxFleetDBURL(), Workspace: s.WorkspaceID}
	if env.URL == "" {
		return sandboxFleetEnv{}, nil, fmt.Errorf("sandbox agent %q: no container-reachable fleet-db URL (set LOOM_FLEET_DB_URL or LOOM_SANDBOX_FLEETDB_URL)", ap.Entry.Worktree)
	}
	adminKey := strings.TrimSpace(os.Getenv(bootstrap.EnvFleetDBAPIKey))
	hostURL := strings.TrimSpace(os.Getenv(bootstrap.EnvFleetDBURL))
	if adminKey == "" || hostURL == "" {
		return env, func() {}, nil // dev / auth-off: ambient credential
	}
	client, err := fleetdb.New(fleetdb.Config{BaseURL: hostURL, APIKey: adminKey, Actor: strings.TrimSpace(os.Getenv(bootstrap.EnvFleetDBActor))})
	if err != nil {
		return sandboxFleetEnv{}, nil, fmt.Errorf("sandbox credential client: %w", err)
	}
	actor := fmt.Sprintf("sandbox:%s:%s:%x", s.WorkspaceID, ap.Entry.Worktree, time.Now().UnixMilli())
	key, err := client.ProvisionScopedKey(cmdstore.RootContext(), actor, s.WorkspaceID, "developer", sandboxDaemonKeyTTLSeconds)
	if err != nil {
		return sandboxFleetEnv{}, nil, fmt.Errorf("provision sandbox key for %q: %w", actor, err)
	}
	env.Key, env.Actor = key, actor
	slog.Info("provisioned scoped sandbox credential", "actor", actor, "workspace", s.WorkspaceID)
	revoke := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := client.RevokeKey(ctx, actor); err != nil {
			slog.Warn("sandbox credential revoke failed", "actor", actor, "err", err)
		}
	}
	return env, revoke, nil
}

// buildSandboxBootstrap builds the in-container script: clone the branch, point
// at fleet-db with the scoped credential, run the agent, push the work back.
func (s *Supervisor) buildSandboxBootstrap(ap *AgentProcess, branch, repoURL string, env sandboxFleetEnv, backend string) string {
	var sb strings.Builder
	sb.WriteString("set -e\n")
	sb.WriteString("chmod +x " + sandbox.LoomPath + "\n")
	sb.WriteString("export GIT_SSL_NO_VERIFY=1\n")
	sb.WriteString(fmt.Sprintf("git clone --branch %s --single-branch %s /sandbox/repo\n",
		sandbox.ShellQuote(branch), sandbox.ShellQuote(repoURL)))
	sb.WriteString("cd /sandbox/repo\n")
	sb.WriteString("git config user.name \"loom-sandbox\"\n")
	sb.WriteString("git config user.email \"loom-sandbox@local\"\n")
	for _, kv := range []struct{ k, v string }{
		{"LOOM_FLEET_DB_URL", env.URL},
		{"LOOM_FLEET_DB_API_KEY", env.Key},
		{"LOOM_FLEET_DB_ACTOR", env.Actor},
		{"LOOM_WORKSPACE", env.Workspace},
	} {
		if kv.v != "" {
			sb.WriteString("export " + kv.k + "=" + sandbox.ShellQuote(kv.v) + "\n")
		}
	}
	sb.WriteString(sandboxLoomInvocation(ap, backend) + "\n")
	sb.WriteString("git add -A\n")
	sb.WriteString(fmt.Sprintf("git diff --cached --quiet || git commit -m %s\n",
		sandbox.ShellQuote(fmt.Sprintf("sandbox agent work [%s]", branch))))
	sb.WriteString(fmt.Sprintf("git push origin %s\n", sandbox.ShellQuote(branch)))
	return sb.String()
}

// sandboxLoomInvocation builds the in-container loom command. It deliberately
// omits --daemon-mode: the container has no daemon, IPC socket, or host
// transcript — the host supervisor manages lifecycle via the openshell exec
// process. Mirrors buildAgentExecCmd's role logic otherwise.
func sandboxLoomInvocation(ap *AgentProcess, backend string) string {
	q := sandbox.ShellQuote
	const repo = "/sandbox/repo"
	parts := []string{sandbox.LoomPath}
	if BuiltInRoles[ap.Entry.Role] {
		parts = append(parts, q(ap.Entry.Role), q(repo), "--auto")
	} else {
		parts = append(parts, "agent", q(repo), "--prompt", q(ap.RoleConfig.PromptFile), "--auto")
		if ap.RoleConfig.TaskFilter != "" {
			parts = append(parts, "--task-filter", q(ap.RoleConfig.TaskFilter))
		}
	}
	if backend != "" {
		parts = append(parts, "--backend", q(backend))
	}
	ap.Mu.Lock()
	epicID := ap.AssignedEpicID
	ap.Mu.Unlock()
	if epicID != "" {
		parts = append(parts, "--parent", q(epicID))
	}
	return strings.Join(parts, " ")
}

// sandboxFleetDBURL returns a container-reachable fleet-db URL: an explicit
// LOOM_SANDBOX_FLEETDB_URL, or a localhost→host-gateway rewrite of LOOM_FLEET_DB_URL.
func sandboxFleetDBURL() string {
	if v := strings.TrimSpace(os.Getenv("LOOM_SANDBOX_FLEETDB_URL")); v != "" {
		return v
	}
	host := strings.TrimSpace(os.Getenv(bootstrap.EnvFleetDBURL))
	if host == "" {
		return ""
	}
	for _, lh := range []string{"localhost", "127.0.0.1", "0.0.0.0"} {
		host = strings.ReplaceAll(host, lh, sandbox.HostGateway)
	}
	return host
}

// sandboxGit runs a git command in dir, surfacing combined output on error.
func sandboxGit(dir string, args ...string) error {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...) //nolint:gosec // dir + args from resolved worktree/config
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return nil
}
