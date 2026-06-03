package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/sandbox"
)

// SandboxExecSpec describes a daemon-supervised sandbox agent run. The supervisor
// builds it from its AgentProcess; keeping it free of supervisor types lets this
// live in the agent package (which already owns the one-shot sandbox flow) without
// an import cycle (supervisor → agent is the existing direction).
type SandboxExecSpec struct {
	Worktree      string // agent/worktree label (errors + actor id)
	WorktreePath  string // resolved local worktree path
	WorkspaceID   string
	ProjectDir    string // host project root (fetch target on cleanup)
	Role          string
	IsBuiltinRole bool
	PromptFile    string // custom-role prompt template
	TaskFilter    string // custom-role task filter
	EpicID        string // optional --parent scope
	RepoRemoteURL string // RepoConfig.RemoteURL fallback when origin is unresolved
	Backend       string // resolved agent backend (claude/codex/…)
}

// sandboxExecCred is the FleetDB connectivity exported into the container.
type sandboxExecCred struct{ URL, Key, Actor, Workspace string }

// resolveSandboxRun resolves the sandbox Config (backend default applied), the
// loom binary to upload (when not baked into the image), and the
// container-reachable fleet-db URL.
func resolveSandboxRun(spec SandboxExecSpec) (cfg sandbox.Config, loomBin, fleetURL string, err error) {
	cfg = sandbox.DefaultConfig()
	if cfg.Backend == "" {
		cfg.Backend = spec.Backend
	}
	if cfg.UploadsLoom() {
		if loomBin, err = sandbox.ResolveLoomBinary(); err != nil {
			return cfg, "", "", err
		}
	}
	fleetURL = sandbox.FleetDBURL()
	if fleetURL == "" {
		return cfg, "", "", fmt.Errorf("sandbox agent %q: no container-reachable fleet-db URL (set LOOM_FLEET_DB_URL or LOOM_SANDBOX_FLEETDB_URL)", spec.Worktree)
	}
	return cfg, loomBin, fleetURL, nil
}

// BuildSandboxExecCommand provisions an OpenShell sandbox for a supervised agent
// and returns the (unstarted) `sandbox exec` command, the sandbox name, and a
// credential revoke func. It mirrors the proven one-shot flow: push the worktree
// branch → provision a scoped credential → create (v0.0.53: `-- true`) → upload
// loom + bootstrap → return the exec cmd. On any setup failure it revokes the
// credential and deletes the partial sandbox so nothing leaks.
func BuildSandboxExecCommand(spec SandboxExecSpec) (cmd *exec.Cmd, name string, revoke func(), err error) {
	branch, berr := cli.GetCurrentBranch(spec.WorktreePath)
	if berr != nil || branch == "" {
		return nil, "", nil, fmt.Errorf("sandbox agent %q: resolve worktree branch: %w", spec.Worktree, berr)
	}
	repoURL := sandboxSupervisedRepoURL(spec)
	if repoURL == "" {
		return nil, "", nil, fmt.Errorf("sandbox agent %q: no container-reachable repo URL", spec.Worktree)
	}
	if perr := sandboxSupervisedGit(spec.WorktreePath, "push", "origin", branch+":"+branch, "--force-with-lease"); perr != nil {
		return nil, "", nil, fmt.Errorf("sandbox push branch %q: %w", branch, perr)
	}

	cfg, loomBin, fleetURL, err := resolveSandboxRun(spec)
	if err != nil {
		return nil, "", nil, err
	}
	key, actor, rev, err := sandbox.ProvisionCredential(context.Background(), spec.WorkspaceID, spec.Worktree)
	if err != nil {
		return nil, "", nil, err
	}
	revoke = rev
	// From here on, any failure must release the credential and the partial sandbox.
	success := false
	defer func() {
		if !success {
			if revoke != nil {
				revoke()
			}
			if name != "" {
				sandbox.DeleteSandbox(name)
			}
		}
	}()
	cred := sandboxExecCred{URL: fleetURL, Key: key, Actor: actor, Workspace: spec.WorkspaceID}

	name = fmt.Sprintf("loom-%s-%x", spec.Worktree, time.Now().UnixMilli())
	bootstrapScript := sandboxSupervisedBootstrap(spec, branch, repoURL, cred, cfg)
	cmd, err = launchSandbox(name, cfg, loomBin, cred.URL, repoURL, bootstrapScript)
	if err != nil {
		return nil, "", nil, err
	}
	success = true
	return cmd, name, revoke, nil
}

// launchSandbox auto-generates the OPA policy (when the default "open" network is
// in effect, opening the fleet-db + repo endpoints), creates the sandbox, uploads
// the loom binary (unless baked into the image) and the bootstrap script, and
// returns the unstarted `sandbox exec` command.
func launchSandbox(name string, cfg sandbox.Config, loomBin, fleetURL, repoURL, bootstrapScript string) (*exec.Cmd, error) {
	if cfg.Network == "open" {
		if eps := sandbox.PolicyEndpoints(fleetURL, repoURL); len(eps) > 0 {
			policyPath, cleanupPolicy, perr := sandbox.WritePolicy(eps, []string{cfg.LoomCmd(), "/usr/bin/git", "/usr/bin/curl"})
			if perr != nil {
				return nil, perr
			}
			defer cleanupPolicy()
			cfg.Network = policyPath
		}
	}
	sandbox.DeleteSandbox(name) // best-effort stale cleanup
	if err := sandbox.RunOpenshell(sandbox.BuildCreateArgs(name, cfg)); err != nil {
		return nil, fmt.Errorf("sandbox create: %w", err)
	}
	if cfg.UploadsLoom() {
		if err := sandbox.RunOpenshell([]string{"sandbox", "upload", name, loomBin, sandbox.LoomPath}); err != nil {
			return nil, fmt.Errorf("sandbox upload loom: %w", err)
		}
	}
	scriptPath, cleanup, err := sandbox.WriteBootstrapScript(bootstrapScript)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if err := sandbox.RunOpenshell([]string{"sandbox", "upload", name, scriptPath, sandbox.BootstrapPath}); err != nil {
		return nil, fmt.Errorf("sandbox upload bootstrap: %w", err)
	}
	return exec.Command(sandbox.OpenshellBinary(), "sandbox", "exec", "-n", name, "--", "sh", sandbox.BootstrapPath), nil //nolint:gosec // args built internally
}

// CleanupSandboxExec fetches the branch the sandbox pushed, fast-forwards the host
// worktree, revokes the scoped credential, and deletes the sandbox. branch is the
// agent's worktree branch (resolved by the caller); a non-fast-forward is logged,
// not deleted, so un-merged work is never silently lost.
func CleanupSandboxExec(projectDir, worktreePath, branch, name string, revoke func()) {
	if branch != "" {
		if err := sandboxSupervisedGit(projectDir, "fetch", "origin", branch); err != nil {
			slog.Warn("sandbox cleanup: fetch failed", "worktree", worktreePath, "err", err)
		}
		if err := sandboxSupervisedGit(worktreePath, "merge", "--ff-only", "origin/"+branch); err != nil {
			slog.Warn("sandbox cleanup: ff-merge failed (may need manual resolution)", "worktree", worktreePath, "err", err)
		}
	}
	if revoke != nil {
		revoke()
	}
	sandbox.DeleteSandbox(name)
}

// sandboxSupervisedRepoURL returns a container-reachable git URL: the worktree's
// origin remote rewritten to the container host gateway (or an explicit
// LOOM_SANDBOX_REPO_URL). Falls back to spec.RepoRemoteURL.
func sandboxSupervisedRepoURL(spec SandboxExecSpec) string {
	if v := strings.TrimSpace(os.Getenv("LOOM_SANDBOX_REPO_URL")); v != "" {
		return v
	}
	origin := ""
	if out, err := exec.Command("git", "-C", spec.WorktreePath, "remote", "get-url", "origin").Output(); err == nil { //nolint:gosec // dir from resolved worktree
		origin = strings.TrimSpace(string(out))
	}
	if origin == "" {
		origin = spec.RepoRemoteURL
	}
	if origin == "" {
		return ""
	}
	gw := sandbox.HostGateway()
	for _, lh := range []string{"localhost", "127.0.0.1", "0.0.0.0"} {
		origin = strings.ReplaceAll(origin, lh, gw)
	}
	return origin
}

// sandboxSupervisedBootstrap builds the in-container script: clone the branch,
// point at fleet-db with the scoped credential, run the agent, push work back.
func sandboxSupervisedBootstrap(spec SandboxExecSpec, branch, repoURL string, cred sandboxExecCred, cfg sandbox.Config) string {
	var sb strings.Builder
	sb.WriteString("set -e\n")
	if cfg.UploadsLoom() {
		sb.WriteString("chmod +x " + sandbox.LoomPath + "\n")
	}
	sb.WriteString("export GIT_SSL_NO_VERIFY=1\n")
	sb.WriteString(fmt.Sprintf("git clone --branch %s --single-branch %s /sandbox/repo\n",
		sandbox.ShellQuote(branch), sandbox.ShellQuote(repoURL)))
	sb.WriteString("cd /sandbox/repo\n")
	sb.WriteString("git config user.name \"loom-sandbox\"\n")
	sb.WriteString("git config user.email \"loom-sandbox@local\"\n")
	for _, kv := range []struct{ k, v string }{
		{"LOOM_FLEET_DB_URL", cred.URL},
		{"LOOM_FLEET_DB_API_KEY", cred.Key},
		{"LOOM_FLEET_DB_ACTOR", cred.Actor},
		{"LOOM_WORKSPACE", cred.Workspace},
	} {
		if kv.v != "" {
			sb.WriteString("export " + kv.k + "=" + sandbox.ShellQuote(kv.v) + "\n")
		}
	}
	sb.WriteString(sandboxSupervisedLoomInvocation(spec, cfg) + "\n")
	sb.WriteString("git add -A\n")
	sb.WriteString(fmt.Sprintf("git diff --cached --quiet || git commit -m %s\n",
		sandbox.ShellQuote(fmt.Sprintf("sandbox agent work [%s]", branch))))
	sb.WriteString(fmt.Sprintf("git push origin %s\n", sandbox.ShellQuote(branch)))
	return sb.String()
}

// sandboxSupervisedLoomInvocation builds the in-container loom command. It omits
// --daemon-mode: the container has no daemon, IPC socket, or host transcript — the
// host supervisor manages lifecycle via the openshell exec process.
func sandboxSupervisedLoomInvocation(spec SandboxExecSpec, cfg sandbox.Config) string {
	q := sandbox.ShellQuote
	const repo = "/sandbox/repo"
	parts := []string{cfg.LoomCmd()}
	if spec.IsBuiltinRole {
		parts = append(parts, q(spec.Role), q(repo), "--auto")
	} else {
		parts = append(parts, "agent", q(repo), "--prompt", q(spec.PromptFile), "--auto")
		if spec.TaskFilter != "" {
			parts = append(parts, "--task-filter", q(spec.TaskFilter))
		}
	}
	if cfg.Backend != "" {
		parts = append(parts, "--backend", q(cfg.Backend))
	}
	if spec.EpicID != "" {
		parts = append(parts, "--parent", q(spec.EpicID))
	}
	return strings.Join(parts, " ")
}

// sandboxSupervisedGit runs a git command in dir, surfacing combined output on error.
func sandboxSupervisedGit(dir string, args ...string) error {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...) //nolint:gosec // dir + args from resolved worktree/config
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return nil
}
