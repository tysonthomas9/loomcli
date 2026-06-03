package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/events"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// AgentRuntimeRunner executes an agent command in a non-local provider runtime.
// Tests inject a fake runner so no real Daytona API call is made.
type AgentRuntimeRunner interface {
	RunAgent(ctx context.Context, req AgentRuntimeRequest) (AgentRuntimeResult, error)
}

type AgentRuntimeRequest struct {
	Provider           domain.RuntimeProvider     `json:"provider"`
	AgentName          string                     `json:"agent_name"`
	Role               string                     `json:"role"`
	RuntimeProfileName string                     `json:"runtime_profile_name,omitempty"`
	Daytona            map[string]any             `json:"daytona,omitempty"`
	Command            string                     `json:"command"`
	Args               []string                   `json:"args"`
	Env                map[string]string          `json:"env,omitempty"`
	CWD                string                     `json:"cwd"`
	HealthCheck        *AgentRuntimeCommand       `json:"health_check,omitempty"`
	Setup              []AgentRuntimeCommand      `json:"setup,omitempty"`
	TimeoutSeconds     int                        `json:"timeout_seconds,omitempty"`
	Progress           func(AgentRuntimeProgress) `json:"-"`
}

type AgentRuntimeCommand struct {
	Name           string            `json:"name,omitempty"`
	Command        string            `json:"command"`
	CWD            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

type AgentRuntimeResult struct {
	SandboxID    string `json:"sandbox_id,omitempty"`
	ExitCode     int    `json:"exit_code"`
	Stdout       string `json:"stdout,omitempty"`
	Stderr       string `json:"stderr,omitempty"`
	Phase        string `json:"phase,omitempty"`
	CleanupState string `json:"cleanup_state,omitempty"`
}

type AgentRuntimeProgress struct {
	SandboxID    string `json:"sandbox_id,omitempty"`
	Phase        string `json:"phase,omitempty"`
	CleanupState string `json:"cleanup_state,omitempty"`
}

type DaytonaAgentRunner struct {
	NodeBin string
	SDKRoot string
}

const (
	daytonaRuntimePhaseProvisioning = "provisioning"
	daytonaRuntimePhaseSetup        = "setup"
	daytonaRuntimePhaseRunning      = "running"
	daytonaRuntimePhaseStopping     = "stopping"
	daytonaRuntimePhaseStopped      = "stopped"
	daytonaRuntimePhaseFailed       = "failed"

	daytonaCleanupPending  = "pending"
	daytonaCleanupRetained = "retained"
	daytonaCleanupDeleted  = "deleted"
	daytonaCleanupFailed   = "failed"
)

const daytonaRunnerCancelGrace = 10 * time.Second

func usesRemoteAgentRuntime(ap *AgentProcess) bool {
	return ap != nil && ap.RoleConfig.RuntimeProvider != "" && ap.RoleConfig.RuntimeProvider != domain.RuntimeProviderLocal
}

func usesDaytonaAgentRuntime(ap *AgentProcess) bool {
	return ap != nil && ap.RoleConfig.RuntimeProvider == domain.RuntimeProviderDaytona
}

func (s *Supervisor) runDaytonaAgent(ap *AgentProcess) (int, error) {
	req, redactions, err := s.buildDaytonaRuntimeRequest(ap)
	if err != nil {
		s.setDaytonaRuntimeState(ap, "", daytonaRuntimePhaseFailed, "not_started")
		return 0, err
	}

	runner := s.RuntimeRunner
	if runner == nil {
		runner = DaytonaAgentRunner{}
	}

	ap.Mu.Lock()
	ap.LastStart = time.Now()
	ap.StopReason = ""
	ap.BackoffUntil = time.Time{}
	ap.DaytonaRuntimePhase = daytonaRuntimePhaseProvisioning
	ap.DaytonaCleanupState = daytonaCleanupPending
	ap.Mu.Unlock()
	s.markControlPlaneAgentSessionRuntimeState(ap)

	log.Printf("[daemon] Agent %s: starting Daytona runtime sandbox", ap.Entry.Worktree)
	if evt, err := events.NewEvent(events.AgentStarted, ap.Entry.Worktree, ap.Entry.Role, ap.AssignedEpicID, events.AgentStartedData{PID: 0}); err == nil {
		s.EmitEvent(evt)
	}
	s.markControlPlaneAgentSessionRunning(ap)
	req.Progress = func(progress AgentRuntimeProgress) {
		s.setDaytonaRuntimeState(ap, progress.SandboxID, progress.Phase, progress.CleanupState)
	}

	ctx, cancel, waitCancel := s.remoteAgentRunContext(ap)
	defer waitCancel()
	stopHeartbeat := s.startAgentWaitHeartbeat(ap)
	result, err := runner.RunAgent(ctx, req)
	stopHeartbeat()
	cancel()
	if err != nil {
		phase := daytonaRuntimePhaseFailed
		select {
		case <-ap.StopCh:
			phase = daytonaRuntimePhaseStopping
		default:
		}
		ap.Mu.Lock()
		ap.LastExit = time.Now()
		ap.LastExitCode = -1
		ap.DaytonaRuntimePhase = phase
		if ap.DaytonaCleanupState == "" || ap.DaytonaCleanupState == daytonaCleanupPending {
			ap.DaytonaCleanupState = daytonaCleanupFailed
		}
		ap.Mu.Unlock()
		s.markControlPlaneAgentSessionRuntimeState(ap)
		return 0, fmt.Errorf("run Daytona agent: %s", redactText(err.Error(), redactions))
	}
	s.appendRemoteRunOutput(ap, result, redactions)

	ap.Mu.Lock()
	ap.DaytonaSandboxID = result.SandboxID
	ap.LastExit = time.Now()
	ap.LastExitCode = result.ExitCode
	if result.CleanupState != "" {
		ap.DaytonaCleanupState = result.CleanupState
	}
	if result.ExitCode == 0 {
		ap.DaytonaRuntimePhase = daytonaRuntimePhaseStopped
	} else if result.Phase == daytonaRuntimePhaseSetup || result.Phase == daytonaRuntimePhaseStopping {
		ap.DaytonaRuntimePhase = result.Phase
	} else {
		ap.DaytonaRuntimePhase = daytonaRuntimePhaseFailed
	}
	exitCode := ap.LastExitCode
	sandboxID := ap.DaytonaSandboxID
	ap.Mu.Unlock()

	s.markControlPlaneAgentSessionRunning(ap)
	log.Printf("[daemon] Agent %s: Daytona sandbox %s exited with code %d", ap.Entry.Worktree, sandboxID, exitCode)
	if evt, err := events.NewEvent(events.AgentStopped, ap.Entry.Worktree, ap.Entry.Role, ap.AssignedEpicID, events.AgentStoppedData{PID: 0, ExitCode: exitCode}); err == nil {
		s.EmitEvent(evt)
	}
	return exitCode, nil
}

func (s *Supervisor) buildDaytonaRuntimeRequest(ap *AgentProcess) (AgentRuntimeRequest, []string, error) {
	runtimeMap, profile, err := s.resolveDaytonaRuntimeConfig(ap)
	if err != nil {
		return AgentRuntimeRequest{}, nil, err
	}
	if err := validateDaytonaRuntimeSecretConfig(runtimeMap); err != nil {
		return AgentRuntimeRequest{}, nil, err
	}
	remoteCWD := normalizeRemoteCWD(remoteAgentCWDFromMap(ap, runtimeMap))
	if err := validateRemoteCWD(remoteCWD); err != nil {
		return AgentRuntimeRequest{}, nil, err
	}
	cmd, err := s.buildCommandForWorktree(ap, remoteCWD, remoteCWD, "loom")
	if err != nil {
		return AgentRuntimeRequest{}, nil, fmt.Errorf("build Daytona command: %w", err)
	}

	envGrants := daytonaEnvGrants(profile, runtimeMap)
	env := remoteCommandEnv(cmd.Env, envGrants)
	mergeRuntimeEnvVars(env, runtimeMap)
	env["LOOM_ISSUE_BACKEND"] = "fleetdb"
	backend := s.GetEffectiveBackend(ap)
	if err := validateDaytonaControlPlaneEnv(env); err != nil {
		return AgentRuntimeRequest{}, nil, err
	}
	if err := validateDaytonaProviderEnv(runtimeMap); err != nil {
		return AgentRuntimeRequest{}, nil, err
	}
	if err := applyDaytonaAIAuthEnv(env, runtimeMap, backend); err != nil {
		return AgentRuntimeRequest{}, nil, err
	}
	gitAuth, err := daytonaGitAuthFromRuntimeMap(runtimeMap, env)
	if err != nil {
		return AgentRuntimeRequest{}, nil, err
	}
	applyDaytonaGitAuthEnv(env, gitAuth)

	repo, err := s.resolveDaytonaRepoMaterialization(ap, profile, runtimeMap, remoteCWD)
	if err != nil {
		return AgentRuntimeRequest{}, nil, err
	}
	setup := []AgentRuntimeCommand{
		{
			Name:           "repo.materialize",
			Command:        buildRepoMaterializeCommand(repo.RepoURL, repo.Branch, repo.Ref, remoteCWD, gitAuth),
			CWD:            "/",
			Env:            env,
			TimeoutSeconds: intFromRuntimeMap(runtimeMap, "setup_timeout", "setup_timeout_seconds", "clone_timeout", "clone_timeout_seconds"),
		},
	}
	for i, command := range stringSliceFromRuntimeMap(runtimeMap, "setup_commands", "setupCommands", "install_commands", "installCommands") {
		setup = append(setup, AgentRuntimeCommand{
			Name:           fmt.Sprintf("setup.%d", i+1),
			Command:        command,
			CWD:            remoteCWD,
			Env:            env,
			TimeoutSeconds: intFromRuntimeMap(runtimeMap, "setup_timeout", "setup_timeout_seconds"),
		})
	}
	setup = append(setup, daytonaRuntimePrerequisiteCommands(backend, remoteCWD, env)...)

	req := AgentRuntimeRequest{
		Provider:           domain.RuntimeProviderDaytona,
		AgentName:          ap.Entry.Worktree,
		Role:               ap.Entry.Role,
		RuntimeProfileName: ap.RoleConfig.RuntimeProfileName,
		Daytona:            runtimeMap,
		Command:            shellQuoteArgs(cmd.Args),
		Args:               append([]string(nil), cmd.Args...),
		Env:                env,
		CWD:                remoteCWD,
		HealthCheck: &AgentRuntimeCommand{
			Name:           "fleetdb.health",
			Command:        buildFleetDBHealthCheckCommand(env[bootstrap.EnvFleetDBURL]),
			CWD:            "/",
			Env:            env,
			TimeoutSeconds: intFromRuntimeMap(runtimeMap, "health_timeout", "health_timeout_seconds"),
		},
		Setup:          setup,
		TimeoutSeconds: intFromRuntimeMap(runtimeMap, "run_timeout", "run_timeout_seconds", "command_timeout"),
	}
	return req, daytonaRedactionValues(req), nil
}

type daytonaRepoMaterialization struct {
	RepoURL string
	Branch  string
	Ref     string
}

type daytonaGitAuthConfig struct {
	TokenEnv     string
	Username     string
	DeployKeyEnv string
}

const (
	daytonaGitAskpassPath = "/tmp/loom-git-askpass"
	daytonaGitSSHKeyPath  = "/tmp/loom-git-deploy-key"
)

func (s *Supervisor) resolveDaytonaRuntimeConfig(ap *AgentProcess) (map[string]any, *domain.RuntimeProfile, error) {
	out := map[string]any{}
	var profile *domain.RuntimeProfile
	if ap != nil && ap.RoleConfig.RuntimeProfileName != "" && s.ControlStore != nil && s.WorkspaceID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
		defer cancel()
		resolved, err := s.ControlStore.RuntimeProfiles().Get(ctx, s.WorkspaceID, ap.RoleConfig.RuntimeProfileName)
		if err != nil {
			return nil, nil, fmt.Errorf("resolve Daytona runtime profile %q: %w", ap.RoleConfig.RuntimeProfileName, err)
		}
		profile = resolved
		mergeDaytonaProfile(out, profile)
	}
	if ap != nil {
		mergeRuntimeMap(out, ap.RoleConfig.RuntimeDaytona)
		if ap.RoleConfig.RuntimeCWD != "" {
			out["cwd"] = ap.RoleConfig.RuntimeCWD
		}
	}
	return out, profile, nil
}

func validateDaytonaRuntimeSecretConfig(runtimeMap map[string]any) error {
	for _, key := range []string{
		"api_key", "apiKey", "daytona_api_key", "daytonaApiKey",
		"openai_api_key", "openaiApiKey",
		"github_token", "githubToken",
		"git_token", "gitToken", "git_auth_token", "gitAuthToken",
		"git_deploy_key", "gitDeployKey", "deploy_key", "deployKey", "ssh_key", "sshKey",
		"token", "secret", "password",
	} {
		if value, ok := runtimeMap[key]; ok && strings.TrimSpace(fmt.Sprint(value)) != "" {
			return fmt.Errorf("Daytona runtime config %q must not contain a direct credential value; use an *_env selector and grant that environment variable instead", key)
		}
	}
	return nil
}

func mergeDaytonaProfile(out map[string]any, profile *domain.RuntimeProfile) {
	if out == nil || profile == nil {
		return
	}
	if profile.Image != "" {
		out["image"] = profile.Image
	}
	var manifest struct {
		CWD     string         `json:"cwd,omitempty"`
		Daytona map[string]any `json:"daytona,omitempty"`
	}
	if len(profile.Manifest) > 0 && json.Unmarshal(profile.Manifest, &manifest) == nil {
		mergeRuntimeMap(out, manifest.Daytona)
		if manifest.CWD != "" {
			out["cwd"] = manifest.CWD
		}
	}
}

func (s *Supervisor) resolveDaytonaRepoMaterialization(ap *AgentProcess, profile *domain.RuntimeProfile, runtimeMap map[string]any, remoteCWD string) (daytonaRepoMaterialization, error) {
	repo := daytonaRepoMaterialization{
		RepoURL: stringFromRuntimeMap(runtimeMap, "repo_url", "repoURL", "remote_url", "remoteURL", "git_url", "gitURL", "clone_url", "cloneURL"),
		Branch:  stringFromRuntimeMap(runtimeMap, "branch", "checkout_branch", "checkoutBranch", "git_branch", "gitBranch"),
		Ref:     stringFromRuntimeMap(runtimeMap, "ref", "checkout_ref", "checkoutRef", "git_ref", "gitRef"),
	}
	if repo.RepoURL != "" {
		if err := validateDaytonaRepoURL(repo.RepoURL); err != nil {
			return repo, err
		}
		return repo, nil
	}
	candidates := repoNameCandidates(ap, profile, runtimeMap)
	for _, name := range candidates {
		domainRepo, err := s.lookupControlPlaneRepo(name)
		if err != nil {
			return repo, err
		}
		if domainRepo == nil || strings.TrimSpace(domainRepo.RemoteURL) == "" {
			continue
		}
		repo.RepoURL = strings.TrimSpace(domainRepo.RemoteURL)
		if err := validateDaytonaRepoURL(repo.RepoURL); err != nil {
			return repo, err
		}
		if repo.Branch == "" {
			repo.Branch = strings.TrimSpace(domainRepo.DefaultBranch)
		}
		return repo, nil
	}
	if ap != nil && ap.RepoConfig != nil && repo.Branch == "" {
		repo.Branch = strings.TrimSpace(ap.RepoConfig.DefaultBranch)
	}
	return repo, fmt.Errorf("Daytona runtime requires a repo URL for %s; set runtime_daytona.repo_url or bind the agent to a FleetDB repo with remote_url", remoteCWD)
}

func (s *Supervisor) lookupControlPlaneRepo(name string) (*domain.Repo, error) {
	name = strings.TrimSpace(name)
	if name == "" || s.ControlStore == nil || s.WorkspaceID == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	repo, err := s.ControlStore.Repos().Get(ctx, s.WorkspaceID, name)
	if err != nil {
		return nil, fmt.Errorf("resolve Daytona repo %q: %w", name, err)
	}
	return repo, nil
}

func repoNameCandidates(ap *AgentProcess, profile *domain.RuntimeProfile, runtimeMap map[string]any) []string {
	var out []string
	for _, key := range []string{"repo", "repository", "repo_name", "repoName"} {
		if name := stringFromRuntimeMap(runtimeMap, key); name != "" && !looksLikeRepoURL(name) {
			out = append(out, name)
		}
	}
	for _, key := range []string{"repos", "repositories"} {
		if repos := stringSliceFromRuntimeMap(runtimeMap, key); len(repos) == 1 && !looksLikeRepoURL(repos[0]) {
			out = append(out, repos[0])
		}
	}
	if ap != nil {
		if ap.Entry.Repo != "" {
			out = append(out, ap.Entry.Repo)
		}
		if len(ap.Entry.Repos) == 1 && !ap.Entry.CrossRepo {
			out = append(out, ap.Entry.Repos[0])
		}
	}
	if profile != nil && len(profile.Repos) == 1 {
		out = append(out, profile.Repos[0])
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(out))
	for _, name := range out {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		unique = append(unique, name)
	}
	return unique
}

func looksLikeRepoURL(value string) bool {
	value = strings.TrimSpace(value)
	return strings.Contains(value, "://") || strings.HasPrefix(value, "git@") || strings.HasPrefix(value, "ssh://")
}

func validateDaytonaRepoURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("Daytona runtime repo URL is required")
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "../") || strings.HasPrefix(raw, "~/") {
		return fmt.Errorf("Daytona runtime repo URL %q is host-local; provide a Git remote reachable from Daytona", raw)
	}
	if strings.HasPrefix(raw, "file://") {
		return fmt.Errorf("Daytona runtime repo URL %q uses file:// and is host-local; provide a Git remote reachable from Daytona", raw)
	}
	if scheme := malformedDaytonaRepoScheme(raw); scheme != "" {
		return fmt.Errorf("Daytona runtime repo URL %q uses malformed scheme %q; use %s://... or scp-like Git syntax", raw, scheme, scheme)
	}
	if host := scpLikeGitHost(raw); host != "" {
		if isLocalOnlyHost(host) {
			return fmt.Errorf("Daytona runtime repo URL host %q is local-only; provide a Git remote reachable from Daytona", host)
		}
		return nil
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("Daytona runtime repo URL %q is invalid: %w", raw, err)
		}
		if u.Scheme == "file" {
			return fmt.Errorf("Daytona runtime repo URL %q uses file:// and is host-local; provide a Git remote reachable from Daytona", raw)
		}
		if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ssh" && u.Scheme != "git" {
			return fmt.Errorf("Daytona runtime repo URL %q uses unsupported scheme %q; use https, ssh, git, or scp-like Git syntax", raw, u.Scheme)
		}
		if u.Hostname() == "" {
			return fmt.Errorf("Daytona runtime repo URL %q is missing a host", raw)
		}
		if isLocalOnlyHost(u.Hostname()) {
			return fmt.Errorf("Daytona runtime repo URL host %q is local-only; provide a Git remote reachable from Daytona", u.Hostname())
		}
		return nil
	}
	return fmt.Errorf("Daytona runtime repo URL %q must be a Git remote URL reachable from Daytona", raw)
}

func malformedDaytonaRepoScheme(raw string) string {
	if strings.Contains(raw, "://") {
		return ""
	}
	beforeColon, _, ok := strings.Cut(raw, ":")
	if !ok || beforeColon == "" || strings.Contains(beforeColon, "/") || strings.Contains(beforeColon, "@") {
		return ""
	}
	switch lower := strings.ToLower(beforeColon); lower {
	case "http", "https", "ssh", "git", "file", "ftp":
		return lower
	default:
		return ""
	}
}

func scpLikeGitHost(raw string) string {
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "/") {
		return ""
	}
	beforeColon, repoPath, ok := strings.Cut(raw, ":")
	if !ok || beforeColon == "" || strings.TrimSpace(repoPath) == "" || strings.Contains(beforeColon, "/") {
		return ""
	}
	if user, host, ok := strings.Cut(beforeColon, "@"); ok {
		if user == "" {
			return ""
		}
		return host
	}
	return beforeColon
}

func isLocalOnlyHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	lowerHost := strings.ToLower(strings.TrimSuffix(host, "."))
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") || lowerHost == "host.docker.internal" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return true
	}
	return false
}

func buildRepoMaterializeCommand(repoURL, branch, ref, remoteCWD string, gitAuth daytonaGitAuthConfig) string {
	parent := path.Dir(remoteCWD)
	commands := []string{
		shellQuoteArgs([]string{"mkdir", "-p", parent}),
		shellQuoteArgs([]string{"rm", "-rf", remoteCWD}),
	}
	cloneArgs := []string{"git", "clone"}
	if branch != "" {
		cloneArgs = append(cloneArgs, "--branch", branch, "--single-branch")
	}
	cloneArgs = append(cloneArgs, repoURL, remoteCWD)
	commands = append(commands, shellQuoteArgs(cloneArgs))
	if ref != "" {
		commands = append(commands,
			shellQuoteArgs([]string{"git", "-C", remoteCWD, "fetch", "--depth=1", "origin", ref}),
			shellQuoteArgs([]string{"git", "-C", remoteCWD, "checkout", "--detach", "FETCH_HEAD"}),
		)
	}
	return buildGitAuthenticatedScript(commands, gitAuth)
}

func buildGitAuthenticatedScript(commands []string, auth daytonaGitAuthConfig) string {
	parts := []string{"set -e"}
	if auth.TokenEnv != "" || auth.DeployKeyEnv != "" {
		parts = append(parts, "export GIT_TERMINAL_PROMPT=0")
	}
	if auth.TokenEnv != "" {
		username := strings.TrimSpace(auth.Username)
		if username == "" {
			username = "x-access-token"
		}
		askpassLines := []string{
			"printf", "%s\\n",
			"#!/bin/sh",
			"case \"$1\" in",
			"*Username*) printf \"%s\" \"${LOOM_GIT_USERNAME:-x-access-token}\" ;;",
			fmt.Sprintf("*Password*) printf \"%%s\" \"${%s:-}\" ;;", auth.TokenEnv),
			"*) printf \"%s\" \"\" ;;",
			"esac",
		}
		parts = append(parts,
			shellQuoteArgs(askpassLines)+" > "+shellQuoteValue(daytonaGitAskpassPath),
			"chmod 700 "+shellQuoteValue(daytonaGitAskpassPath),
			"export GIT_ASKPASS="+shellQuoteValue(daytonaGitAskpassPath),
			"export LOOM_GIT_ASKPASS="+shellQuoteValue(daytonaGitAskpassPath),
			"export LOOM_GIT_USERNAME="+shellQuoteValue(username),
		)
	}
	if auth.DeployKeyEnv != "" {
		parts = append(parts,
			fmt.Sprintf(`printf '%%s\n' "${%s:-}" > %s`, auth.DeployKeyEnv, shellQuoteValue(daytonaGitSSHKeyPath)),
			"chmod 600 "+shellQuoteValue(daytonaGitSSHKeyPath),
			"export LOOM_GIT_SSH_KEY="+shellQuoteValue(daytonaGitSSHKeyPath),
			"export GIT_SSH_COMMAND="+shellQuoteValue(daytonaGitSSHCommand(daytonaGitSSHKeyPath)),
		)
	}
	parts = append(parts, commands...)
	return strings.Join(parts, "; ")
}

func daytonaRuntimePrerequisiteCommands(backend, remoteCWD string, env map[string]string) []AgentRuntimeCommand {
	checks := []string{daytonaCommandExistsSnippet("loom", "Daytona runtime image/snapshot must include loom on PATH before agent start")}
	backend = strings.TrimSpace(backend)
	if backend != "" && backend != "noop" && backend != "none" {
		checks = append(checks, daytonaCommandExistsSnippet(backend, "Daytona runtime image/snapshot must include "+backend+" on PATH for this agent backend"))
	}
	return []AgentRuntimeCommand{{
		Name:    "runtime.prerequisites",
		Command: strings.Join(checks, " && "),
		CWD:     remoteCWD,
		Env:     env,
	}}
}

func daytonaCommandExistsSnippet(command, message string) string {
	return "command -v " + shellQuoteValue(command) + " >/dev/null 2>&1 || { echo " + shellQuoteValue(message) + " >&2; exit 127; }"
}

func buildFleetDBHealthCheckCommand(baseURL string) string {
	healthURL := strings.TrimRight(baseURL, "/") + "/health"
	quotedURL := shellQuoteValue(healthURL)
	curlScript := "url=" + quotedURL + "; set --; " +
		"if [ -n \"${LOOM_FLEET_DB_API_KEY:-}\" ]; then set -- \"$@\" -H \"X-Fleet-API-Key: ${LOOM_FLEET_DB_API_KEY}\"; fi; " +
		"if [ -n \"${LOOM_FLEET_DB_ACTOR:-}\" ]; then set -- \"$@\" -H \"X-Actor: ${LOOM_FLEET_DB_ACTOR}\"; fi; " +
		"curl -fsS --max-time 10 \"$@\" \"$url\" >/dev/null"
	wgetScript := "url=" + quotedURL + "; set --; " +
		"if [ -n \"${LOOM_FLEET_DB_API_KEY:-}\" ]; then set -- \"$@\" --header \"X-Fleet-API-Key: ${LOOM_FLEET_DB_API_KEY}\"; fi; " +
		"if [ -n \"${LOOM_FLEET_DB_ACTOR:-}\" ]; then set -- \"$@\" --header \"X-Actor: ${LOOM_FLEET_DB_ACTOR}\"; fi; " +
		"wget -q -T 10 -O /dev/null \"$@\" \"$url\""
	return strings.Join([]string{
		"(command -v curl >/dev/null 2>&1 && " + curlScript + ")",
		"(command -v wget >/dev/null 2>&1 && " + wgetScript + ")",
	}, " || ")
}

func validateRemoteCWD(cwd string) error {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || !strings.HasPrefix(cwd, "/") || cwd == "/" || cwd == "/workspace" || cwd == "/home" {
		return fmt.Errorf("Daytona runtime cwd must be an absolute project directory, got %q", cwd)
	}
	return nil
}

func validateDaytonaControlPlaneEnv(env map[string]string) error {
	baseURL := strings.TrimSpace(env[bootstrap.EnvFleetDBURL])
	if baseURL == "" {
		return fmt.Errorf("Daytona runtime requires %s; embedded FleetDB and local daemon IPC are not reachable from Daytona", bootstrap.EnvFleetDBURL)
	}
	if err := validateRemoteControlPlaneURL(baseURL); err != nil {
		return fmt.Errorf("Daytona runtime invalid %s: %w", bootstrap.EnvFleetDBURL, err)
	}
	if strings.TrimSpace(env[bootstrap.EnvWorkspace]) == "" {
		return fmt.Errorf("Daytona runtime requires %s", bootstrap.EnvWorkspace)
	}
	if strings.TrimSpace(env[bootstrap.EnvFleetDBAPIKey]) == "" && strings.TrimSpace(env[bootstrap.EnvFleetDBActor]) == "" {
		return fmt.Errorf("Daytona runtime requires %s or %s", bootstrap.EnvFleetDBAPIKey, bootstrap.EnvFleetDBActor)
	}
	return nil
}

func validateDaytonaProviderEnv(runtimeMap map[string]any) error {
	apiKeyEnv := daytonaAPIKeyEnvName(runtimeMap)
	if !isShellIdentifier(apiKeyEnv) {
		return fmt.Errorf("Daytona runtime api key env %q is not a valid environment variable name", apiKeyEnv)
	}
	if strings.TrimSpace(os.Getenv(apiKeyEnv)) == "" {
		return fmt.Errorf("Daytona runtime requires %s for Daytona provider authentication", apiKeyEnv)
	}
	return nil
}

func daytonaAPIKeyEnvName(runtimeMap map[string]any) string {
	if name := stringFromRuntimeMap(runtimeMap, "api_key_env", "apiKeyEnv"); name != "" {
		return name
	}
	return "DAYTONA_API_KEY"
}

func validateRemoteControlPlaneURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL host is required")
	}
	lowerHost := strings.ToLower(strings.TrimSuffix(host, "."))
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") || lowerHost == "host.docker.internal" {
		return fmt.Errorf("host %q is local-only", host)
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return fmt.Errorf("host %q is local-only", host)
	}
	return nil
}

func daytonaEnvGrants(profile *domain.RuntimeProfile, runtimeMap map[string]any) map[string]bool {
	grants := map[string]bool{}
	if profile != nil {
		for _, key := range profile.Env {
			if key = strings.TrimSpace(key); key != "" {
				grants[key] = true
			}
		}
	}
	for _, key := range stringSliceFromRuntimeMap(runtimeMap, "env", "env_grants", "envGrants", "secrets", "secret_env", "secretEnv") {
		grants[key] = true
	}
	for _, key := range daytonaRuntimeEnvGrantNames(runtimeMap) {
		grants[key] = true
	}
	return grants
}

func daytonaRuntimeEnvGrantNames(runtimeMap map[string]any) []string {
	var out []string
	for _, key := range []string{
		"git_token_env", "gitTokenEnv", "github_token_env", "githubTokenEnv", "git_auth_token_env", "gitAuthTokenEnv",
		"git_deploy_key_env", "gitDeployKeyEnv", "deploy_key_env", "deployKeyEnv", "ssh_key_env", "sshKeyEnv",
		"openai_api_key_env", "openaiApiKeyEnv", "codex_auth_file_env", "codexAuthFileEnv",
	} {
		if name := stringFromRuntimeMap(runtimeMap, key); name != "" && isShellIdentifier(name) {
			out = append(out, name)
		}
	}
	return out
}

func daytonaGitAuthFromRuntimeMap(runtimeMap map[string]any, env map[string]string) (daytonaGitAuthConfig, error) {
	auth := daytonaGitAuthConfig{
		TokenEnv: stringFromRuntimeMap(runtimeMap,
			"git_token_env", "gitTokenEnv", "github_token_env", "githubTokenEnv", "git_auth_token_env", "gitAuthTokenEnv",
		),
		Username: strings.TrimSpace(stringFromRuntimeMap(runtimeMap,
			"git_username", "gitUsername", "github_username", "githubUsername",
		)),
		DeployKeyEnv: stringFromRuntimeMap(runtimeMap,
			"git_deploy_key_env", "gitDeployKeyEnv", "deploy_key_env", "deployKeyEnv", "ssh_key_env", "sshKeyEnv",
		),
	}
	explicitTokenEnv := auth.TokenEnv != ""
	explicitDeployKeyEnv := auth.DeployKeyEnv != ""
	if auth.TokenEnv == "" {
		for _, candidate := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GIT_TOKEN"} {
			if _, ok := env[candidate]; ok {
				auth.TokenEnv = candidate
				break
			}
		}
	}
	if auth.DeployKeyEnv == "" {
		for _, candidate := range []string{"GIT_DEPLOY_KEY", "SSH_DEPLOY_KEY"} {
			if _, ok := env[candidate]; ok {
				auth.DeployKeyEnv = candidate
				break
			}
		}
	}
	if auth.TokenEnv != "" && !isShellIdentifier(auth.TokenEnv) {
		return daytonaGitAuthConfig{}, fmt.Errorf("Daytona runtime git token env %q is not a valid environment variable name", auth.TokenEnv)
	}
	if auth.DeployKeyEnv != "" && !isShellIdentifier(auth.DeployKeyEnv) {
		return daytonaGitAuthConfig{}, fmt.Errorf("Daytona runtime git deploy key env %q is not a valid environment variable name", auth.DeployKeyEnv)
	}
	if explicitTokenEnv && strings.TrimSpace(env[auth.TokenEnv]) == "" {
		return daytonaGitAuthConfig{}, fmt.Errorf("Daytona runtime git token env %s is configured but not available to the sandbox", auth.TokenEnv)
	}
	if explicitDeployKeyEnv && strings.TrimSpace(env[auth.DeployKeyEnv]) == "" {
		return daytonaGitAuthConfig{}, fmt.Errorf("Daytona runtime git deploy key env %s is configured but not available to the sandbox", auth.DeployKeyEnv)
	}
	if auth.Username == "" {
		auth.Username = "x-access-token"
	}
	return auth, nil
}

func applyDaytonaGitAuthEnv(env map[string]string, auth daytonaGitAuthConfig) {
	if env == nil {
		return
	}
	if auth.TokenEnv != "" {
		env["GIT_TERMINAL_PROMPT"] = "0"
		env["GIT_ASKPASS"] = daytonaGitAskpassPath
		env["LOOM_GIT_ASKPASS"] = daytonaGitAskpassPath
		env["LOOM_GIT_USERNAME"] = auth.Username
	}
	if auth.DeployKeyEnv != "" {
		env["GIT_TERMINAL_PROMPT"] = "0"
		env["LOOM_GIT_SSH_KEY"] = daytonaGitSSHKeyPath
		env["GIT_SSH_COMMAND"] = daytonaGitSSHCommand(daytonaGitSSHKeyPath)
	}
}

func applyDaytonaAIAuthEnv(env map[string]string, runtimeMap map[string]any, backend string) error {
	if env == nil {
		return nil
	}
	openAIEnv := stringFromRuntimeMap(runtimeMap, "openai_api_key_env", "openaiApiKeyEnv")
	if openAIEnv == "" {
		if _, ok := env["OPENAI_API_KEY"]; ok {
			openAIEnv = "OPENAI_API_KEY"
		}
	}
	if openAIEnv != "" {
		if !isShellIdentifier(openAIEnv) {
			return fmt.Errorf("Daytona runtime openai api key env %q is not a valid environment variable name", openAIEnv)
		}
		if value := strings.TrimSpace(env[openAIEnv]); value != "" {
			env["OPENAI_API_KEY"] = value
		}
	}

	codexAuthFileEnv := stringFromRuntimeMap(runtimeMap, "codex_auth_file_env", "codexAuthFileEnv")
	if codexAuthFileEnv == "" {
		if _, ok := env["CODEX_AUTH_FILE"]; ok {
			codexAuthFileEnv = "CODEX_AUTH_FILE"
		}
	}
	if codexAuthFileEnv != "" {
		if !isShellIdentifier(codexAuthFileEnv) {
			return fmt.Errorf("Daytona runtime codex auth file env %q is not a valid environment variable name", codexAuthFileEnv)
		}
		authFile := strings.TrimSpace(env[codexAuthFileEnv])
		if authFile == "" {
			if value, ok := os.LookupEnv(codexAuthFileEnv); ok && isHostLocalEnvValue(value) {
				authFile = value
			}
		}
		if authFile != "" {
			cleanAuthFile, err := validateRemoteCodexAuthFilePath(authFile)
			if err != nil {
				return err
			}
			env["CODEX_AUTH_FILE"] = cleanAuthFile
			env["CODEX_HOME"] = path.Dir(cleanAuthFile)
		}
	}

	if strings.TrimSpace(backend) == "codex" &&
		strings.TrimSpace(env["OPENAI_API_KEY"]) == "" &&
		strings.TrimSpace(env["CODEX_HOME"]) == "" {
		return fmt.Errorf("Daytona runtime codex backend requires OPENAI_API_KEY or codex_auth_file_env pointing at a Daytona-provisioned auth.json")
	}
	return nil
}

func validateRemoteCodexAuthFilePath(authFile string) (string, error) {
	authFile = strings.TrimSpace(authFile)
	if authFile == "" {
		return "", nil
	}
	if !path.IsAbs(authFile) {
		return "", fmt.Errorf("Daytona runtime codex auth file path must be an absolute remote path, got %q", authFile)
	}
	clean := path.Clean(authFile)
	if isHostLocalAuthPath(clean) {
		return "", fmt.Errorf("Daytona runtime codex auth file path %q looks host-local; provide a Daytona-provisioned remote auth.json path", authFile)
	}
	if path.Base(clean) != "auth.json" {
		return "", fmt.Errorf("Daytona runtime codex auth file path must point to auth.json, got %q", authFile)
	}
	return clean, nil
}

func isHostLocalAuthPath(authFile string) bool {
	clean := path.Clean(strings.TrimSpace(authFile))
	for _, prefix := range []string{"/Users/", "/private/", "/Volumes/", "/var/folders/"} {
		if strings.HasPrefix(clean, prefix) {
			return true
		}
	}
	return false
}

func daytonaGitSSHCommand(keyPath string) string {
	return "ssh -i " + keyPath + " -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new"
}

func mergeRuntimeEnvVars(env map[string]string, runtimeMap map[string]any) {
	if env == nil {
		return
	}
	for _, key := range []string{"env_vars", "envVars"} {
		value, ok := runtimeMap[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case map[string]string:
			for envKey, envValue := range typed {
				envKey = strings.TrimSpace(envKey)
				if envKey != "" && !isBlockedRemoteEnv(envKey) && !isHostLocalEnvValue(envValue) {
					env[envKey] = envValue
				}
			}
		case map[string]any:
			for envKey, envValue := range typed {
				envKey = strings.TrimSpace(envKey)
				envText := fmt.Sprint(envValue)
				if envKey != "" && !isBlockedRemoteEnv(envKey) && !isHostLocalEnvValue(envText) {
					env[envKey] = envText
				}
			}
		}
	}
}

func (s *Supervisor) setDaytonaRuntimeState(ap *AgentProcess, sandboxID, phase, cleanupState string) {
	if ap == nil {
		return
	}
	ap.Mu.Lock()
	if sandboxID != "" {
		ap.DaytonaSandboxID = sandboxID
	}
	if phase != "" {
		ap.DaytonaRuntimePhase = phase
	}
	if cleanupState != "" {
		ap.DaytonaCleanupState = cleanupState
	}
	ap.Mu.Unlock()
	s.markControlPlaneAgentSessionRuntimeState(ap)
}

func (s *Supervisor) markControlPlaneAgentSessionRuntimeState(ap *AgentProcess) {
	if s.ControlStore == nil || s.WorkspaceID == "" || ap == nil {
		return
	}
	backend := s.GetEffectiveBackend(ap)
	ap.Mu.Lock()
	sessionID := ap.AgentSessionID
	metadata := s.agentSessionMetadataLocked(ap, backend)
	ap.Mu.Unlock()
	if sessionID == "" {
		return
	}
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), controlPlaneOperationTimeout)
	defer cancel()
	if _, err := s.ControlStore.AgentSessions().Update(ctx, s.WorkspaceID, sessionID, store.AgentSessionUpdate{
		LastHeartbeat: &now,
		Metadata:      &metadata,
	}); err != nil {
		slog.Warn("control-plane agent session runtime update failed", "worktree", ap.Entry.Worktree, "session_id", sessionID, "err", err)
	}
}

func (s *Supervisor) remoteAgentRunContext(ap *AgentProcess) (context.Context, context.CancelFunc, func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-s.Shutdown:
			cancel()
		case <-ap.StopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel, func() {
		cancel()
		<-done
	}
}

func remoteAgentCWD(ap *AgentProcess) string {
	return remoteAgentCWDFromMap(ap, nil)
}

func remoteAgentCWDFromMap(ap *AgentProcess, runtimeMap map[string]any) string {
	if ap == nil {
		return "/workspace/project"
	}
	if cwd := strings.TrimSpace(ap.RoleConfig.RuntimeCWD); cwd != "" {
		return cwd
	}
	if cwd := stringFromRuntimeMap(runtimeMap, "cwd", "workdir", "working_dir"); cwd != "" {
		return cwd
	}
	if cwd := stringFromRuntimeMap(ap.RoleConfig.RuntimeDaytona, "cwd", "workdir", "working_dir"); cwd != "" {
		return cwd
	}
	return "/workspace/project"
}

func normalizeRemoteCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	return path.Clean(cwd)
}

func remoteCommandEnv(env []string, grants map[string]bool) map[string]string {
	out := map[string]string{}
	for _, entry := range env {
		idx := strings.IndexByte(entry, '=')
		if idx <= 0 {
			continue
		}
		key, value := entry[:idx], entry[idx+1:]
		if isBlockedRemoteEnv(key) {
			continue
		}
		if (remoteLoomEnvAllowlist[key] || remoteEnvAllowlist[key] || grants[key]) && !isHostLocalEnvValue(value) {
			out[key] = value
		}
	}
	for key := range grants {
		key = strings.TrimSpace(key)
		if key == "" || isBlockedRemoteEnv(key) {
			continue
		}
		if value, ok := os.LookupEnv(key); ok && !isHostLocalEnvValue(value) {
			out[key] = value
		}
	}
	return out
}

var remoteEnvAllowlist = map[string]bool{
	"HTTP_PROXY":          true,
	"HTTPS_PROXY":         true,
	"NO_PROXY":            true,
	"http_proxy":          true,
	"https_proxy":         true,
	"no_proxy":            true,
	"GIT_AUTHOR_NAME":     true,
	"GIT_AUTHOR_EMAIL":    true,
	"GIT_COMMITTER_NAME":  true,
	"GIT_COMMITTER_EMAIL": true,
}

var remoteLoomEnvAllowlist = map[string]bool{
	bootstrap.EnvFleetDBActor:  true,
	bootstrap.EnvFleetDBAPIKey: true,
	bootstrap.EnvFleetDBURL:    true,
	bootstrap.EnvWorkspace:     true,

	"LOOM_AGENT_LEASE_ID":                true,
	"LOOM_AGENT_LEASE_TOKEN":             true,
	"LOOM_AGENT_NAME":                    true,
	"LOOM_AGENT_OWNERSHIP_FENCING_TOKEN": true,
	"LOOM_AGENT_OWNERSHIP_LEASE_ID":      true,
	"LOOM_AGENT_PATH_PATTERNS":           true,
	"LOOM_AGENT_REPO":                    true,
	"LOOM_ALLOWED_TOOLS":                 true,
	"LOOM_ASSIGNED_TASK_ID":              true,
	"LOOM_DENIED_TOOLS":                  true,
	"LOOM_ISSUE_BACKEND":                 true,
	"LOOM_MAX_BUDGET_USD":                true,
	"LOOM_ORCHESTRATOR_SESSION_ID":       true,
	"LOOM_READ_ONLY":                     true,
	"LOOM_ROLE":                          true,
	"LOOM_ROLE_MAX_PRIORITY":             true,
	"LOOM_ROLE_PATH_PATTERNS":            true,
	"LOOM_ROLE_SKILLS":                   true,
	"LOOM_ROLE_TASK_FILTER":              true,
	"LOOM_SESSION_ID":                    true,
	"LOOM_SOURCE_REPOS":                  true,
	"LOOM_TRACE_PARENT":                  true,
	"LOOM_WORKSPACE_ID":                  true,
	"LOOM_WORKTREE_PATH":                 true,
	"LOOM_YIELD_FILE":                    true,
}

var localPathEnvBlocklist = map[string]bool{
	"CODEX_HOME":                 true,
	"HOME":                       true,
	"LOOM_CONFIG_DIR":            true,
	"LOOM_DAEMON_SOCKET":         true,
	"LOOM_EVENTS_DIR":            true,
	"LOOM_SERVER_URL":            true,
	"LOOM_WORKSPACE_RUNTIME_DIR": true,
	"OLDPWD":                     true,
	"PATH":                       true,
	"PWD":                        true,
	"SSH_AUTH_SOCK":              true,
	"TMPDIR":                     true,
	"XDG_CONFIG_HOME":            true,
	"XDG_DATA_HOME":              true,
	"XDG_RUNTIME_DIR":            true,
}

func isBlockedRemoteEnv(key string) bool {
	if localPathEnvBlocklist[key] {
		return true
	}
	return strings.HasPrefix(key, "LOOM_FLEET_DB_REDIS_")
}

func isHostLocalEnvValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lowerValue := strings.ToLower(value)
	for _, prefix := range []string{"file:///users/", "file:///private/", "file:///volumes/", "file:///var/folders/"} {
		if strings.HasPrefix(lowerValue, prefix) {
			return true
		}
	}
	if strings.Contains(value, "://") {
		return false
	}
	for _, segment := range strings.Split(value, ":") {
		segment = strings.TrimSpace(segment)
		for _, prefix := range []string{"/Users/", "/private/", "/Volumes/", "/var/folders/"} {
			if strings.HasPrefix(segment, prefix) {
				return true
			}
		}
	}
	return false
}

func shellQuoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteValue(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteValue(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func isShellIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func cloneRuntimeMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mergeRuntimeMap(dst, src map[string]any) {
	if dst == nil || src == nil {
		return
	}
	for k, v := range src {
		dst[k] = v
	}
}

func stringFromRuntimeMap(in map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := in[key]; ok {
			if s := strings.TrimSpace(fmt.Sprint(value)); s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func stringSliceFromRuntimeMap(in map[string]any, keys ...string) []string {
	var out []string
	for _, key := range keys {
		value, ok := in[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []string:
			for _, item := range typed {
				if item = strings.TrimSpace(item); item != "" {
					out = append(out, item)
				}
			}
		case []any:
			for _, item := range typed {
				if s := strings.TrimSpace(fmt.Sprint(item)); s != "" && s != "<nil>" {
					out = append(out, s)
				}
			}
		case string:
			if s := strings.TrimSpace(typed); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func intFromRuntimeMap(in map[string]any, keys ...string) int {
	for _, key := range keys {
		value, ok := in[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case int:
			return typed
		case int64:
			return int(typed)
		case float64:
			return int(typed)
		case json.Number:
			n, _ := typed.Int64()
			return int(n)
		case string:
			n, _ := strconv.Atoi(strings.TrimSpace(typed))
			return n
		}
	}
	return 0
}

func daytonaRedactionValues(req AgentRuntimeRequest) []string {
	seen := map[string]bool{}
	var values []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || len(value) < 4 || seen[value] {
			return
		}
		seen[value] = true
		values = append(values, value)
	}
	for key, value := range req.Env {
		if isSecretEnvKey(key) {
			add(value)
		}
	}
	for _, key := range daytonaRuntimeSecretEnvNames(req.Daytona) {
		if value, ok := os.LookupEnv(key); ok {
			add(value)
		}
	}
	collectSecretValuesFromRuntimeMap(req.Daytona, add)
	return values
}

func daytonaRuntimeSecretEnvNames(runtimeMap map[string]any) []string {
	var out []string
	for _, key := range []string{
		"api_key_env", "apiKeyEnv",
		"git_token_env", "gitTokenEnv", "github_token_env", "githubTokenEnv", "git_auth_token_env", "gitAuthTokenEnv",
		"git_deploy_key_env", "gitDeployKeyEnv", "deploy_key_env", "deployKeyEnv", "ssh_key_env", "sshKeyEnv",
		"openai_api_key_env", "openaiApiKeyEnv", "codex_auth_file_env", "codexAuthFileEnv",
	} {
		if name := stringFromRuntimeMap(runtimeMap, key); name != "" && isShellIdentifier(name) {
			out = append(out, name)
		}
	}
	if name := stringFromRuntimeMap(runtimeMap, "api_key_env", "apiKeyEnv"); name == "" {
		out = append(out, "DAYTONA_API_KEY")
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(out))
	for _, name := range out {
		if seen[name] {
			continue
		}
		seen[name] = true
		unique = append(unique, name)
	}
	return unique
}

func collectSecretValuesFromRuntimeMap(in map[string]any, add func(string)) {
	for key, value := range in {
		switch typed := value.(type) {
		case map[string]string:
			for envKey, envValue := range typed {
				if isSecretEnvKey(envKey) {
					add(envValue)
				}
			}
		case map[string]any:
			if isSecretEnvKey(key) {
				for _, envValue := range typed {
					add(fmt.Sprint(envValue))
				}
			}
			collectSecretValuesFromRuntimeMap(typed, add)
		case string:
			if isSecretEnvKey(key) {
				add(typed)
			}
		}
	}
}

func isSecretEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, marker := range []string{"TOKEN", "API_KEY", "SECRET", "PASSWORD", "CREDENTIAL", "AUTH", "LEASE_TOKEN"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func redactText(text string, values []string) string {
	for _, value := range values {
		if value != "" {
			text = strings.ReplaceAll(text, value, "[REDACTED]")
		}
	}
	return text
}

func (s *Supervisor) appendRemoteRunOutput(ap *AgentProcess, result AgentRuntimeResult, redactions []string) {
	cfg := s.ConfigSnapshot()
	if cfg == nil || cfg.Daemon.LogDir == "" || result.Stdout == "" && result.Stderr == "" {
		return
	}
	logDir := cfg.Daemon.LogDir
	if !filepath.IsAbs(logDir) {
		logDir = filepath.Join(s.ProjectDir, logDir)
	}
	if s.WorkspaceID != "" {
		logDir = filepath.Join(logDir, s.WorkspaceID)
	}
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return
	}
	logFilePath := filepath.Join(logDir, fmt.Sprintf("%s-%s.log", filepath.Base(ap.Entry.Role), filepath.Base(ap.Entry.Worktree)))
	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // G304: log file path from daemon config
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	ap.Mu.Lock()
	ap.LogFilePath = logFilePath
	ap.Mu.Unlock()
	if result.Stdout != "" {
		stdout := redactText(result.Stdout, redactions)
		_, _ = f.WriteString(stdout)
		if !strings.HasSuffix(result.Stdout, "\n") {
			_, _ = f.WriteString("\n")
		}
	}
	if result.Stderr != "" {
		stderr := redactText(result.Stderr, redactions)
		_, _ = f.WriteString(stderr)
		if !strings.HasSuffix(result.Stderr, "\n") {
			_, _ = f.WriteString("\n")
		}
	}
}

const daytonaProgressPrefix = "__loom_daytona_progress__"

type daytonaProgressWriter struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	pending  string
	progress func(AgentRuntimeProgress)
}

func (w *daytonaProgressWriter) Write(p []byte) (int, error) {
	text := string(p)
	w.mu.Lock()
	combined := w.pending + text
	w.pending = ""
	lines := strings.SplitAfter(combined, "\n")
	if len(lines) > 0 && !strings.HasSuffix(combined, "\n") {
		w.pending = lines[len(lines)-1]
		lines = lines[:len(lines)-1]
	}
	var updates []AgentRuntimeProgress
	for _, line := range lines {
		trimmed := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if strings.HasPrefix(trimmed, daytonaProgressPrefix) {
			var update AgentRuntimeProgress
			if err := json.Unmarshal([]byte(strings.TrimPrefix(trimmed, daytonaProgressPrefix)), &update); err == nil {
				updates = append(updates, update)
			}
			continue
		}
		_, _ = w.buf.WriteString(line)
	}
	progress := w.progress
	w.mu.Unlock()
	if progress != nil {
		for _, update := range updates {
			progress(update)
		}
	}
	return len(p), nil
}

func (w *daytonaProgressWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	text := w.buf.String()
	if w.pending != "" && !strings.HasPrefix(w.pending, daytonaProgressPrefix) {
		text += w.pending
	}
	return text
}

func (r DaytonaAgentRunner) RunAgent(ctx context.Context, req AgentRuntimeRequest) (AgentRuntimeResult, error) {
	nodeBin := strings.TrimSpace(r.NodeBin)
	if nodeBin == "" {
		nodeBin = "node"
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return AgentRuntimeResult{}, fmt.Errorf("marshal Daytona request: %w", err)
	}
	cmd := exec.Command(nodeBin, "--input-type=module", "-e", daytonaAgentRunnerScript) //nolint:gosec // fixed helper script, payload is stdin JSON.
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = os.Environ()
	if r.SDKRoot != "" {
		cmd.Env = append(cmd.Env, "LOOM_DAYTONA_SDK_ROOT="+r.SDKRoot)
	}
	var stdout bytes.Buffer
	stderrProgress := &daytonaProgressWriter{progress: req.Progress}
	cmd.Stdout = &stdout
	cmd.Stderr = stderrProgress
	if err := cmd.Start(); err != nil {
		return AgentRuntimeResult{}, fmt.Errorf("start Daytona runner helper: %w", err)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-waitCh:
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case waitErr = <-waitCh:
		case <-time.After(daytonaRunnerCancelGrace):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			waitErr = <-waitCh
			return AgentRuntimeResult{}, fmt.Errorf("daytona runner helper canceled: %w: %s", ctx.Err(), strings.TrimSpace(stdout.String()+"\n"+stderrProgress.String()))
		}
	}

	var result AgentRuntimeResult
	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) > 0 {
		if err := json.Unmarshal(out, &result); err != nil {
			return AgentRuntimeResult{}, fmt.Errorf("decode Daytona runner result: %w: stdout=%s stderr=%s", err, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderrProgress.String()))
		}
		return result, nil
	}
	if waitErr != nil {
		return AgentRuntimeResult{}, fmt.Errorf("daytona runner helper failed: %w: %s", waitErr, strings.TrimSpace(stderrProgress.String()))
	}
	return AgentRuntimeResult{}, fmt.Errorf("daytona runner helper produced no result")
}

const daytonaAgentRunnerScript = `
import { createRequire } from "node:module";
import fs from "node:fs";
import path from "node:path";

const payload = JSON.parse(fs.readFileSync(0, "utf8"));
const require = createRequire(import.meta.url);

function loadSDK() {
  const roots = [process.env.LOOM_DAYTONA_SDK_ROOT, process.cwd()].filter(Boolean);
  for (const root of roots) {
    for (const packagePath of [
      path.join(root, "node_modules", "@daytona", "sdk"),
      path.join(root, "node_modules", "@daytonaio", "sdk"),
    ]) {
      try {
        return require(packagePath);
      } catch (_) {}
    }
  }
  for (const packageName of ["@daytona/sdk", "@daytonaio/sdk"]) {
    try {
      return require(packageName);
    } catch (_) {}
  }
  return require("@daytona/sdk");
}

function sandboxIdFor(sandbox) {
  return String(
    sandbox?.id ||
      sandbox?.sandboxId ||
      sandbox?.sandbox_id ||
      sandbox?.workspaceId ||
      sandbox?.workspace_id ||
      sandbox?.instanceId ||
      sandbox?.instance_id ||
      "",
  );
}

function cleanCreateOptions(input = {}) {
  const out = { ...input };
  for (const key of [
    "api_key", "apiKey",
    "api_key_env", "apiKeyEnv", "api_url", "apiUrl",
    "branch", "checkout_branch", "checkoutBranch", "checkout_ref", "checkoutRef",
    "clone_timeout", "clone_timeout_seconds", "command_timeout",
    "create_timeout", "createTimeout",
    "delete_after_run", "deleteAfterRun",
    "env", "env_grants", "envGrants",
    "daytona_api_key", "daytonaApiKey",
    "deploy_key", "deployKey",
    "git_auth_token", "gitAuthToken", "git_auth_token_env", "gitAuthTokenEnv",
    "git_branch", "gitBranch",
    "git_deploy_key", "gitDeployKey", "git_deploy_key_env", "gitDeployKeyEnv",
    "git_ref", "gitRef", "git_token", "gitToken", "git_token_env", "gitTokenEnv", "git_url", "gitURL",
    "git_username", "gitUsername", "github_token", "githubToken",
    "health_timeout", "health_timeout_seconds",
    "github_token_env", "githubTokenEnv", "github_username", "githubUsername",
    "id", "sandbox_id", "sandboxId", "workspace_id", "workspaceId", "provider_workspace_id", "providerWorkspaceId",
    "install_commands", "installCommands",
    "keep_sandbox", "keepSandbox",
    "openai_api_key", "openaiApiKey", "openai_api_key_env", "openaiApiKeyEnv",
    "password",
    "ref", "remote_url", "remoteURL", "repo", "repository", "repositories", "repos", "repo_name", "repoName", "repo_url", "repoURL",
    "run_timeout", "runTimeout", "run_timeout_seconds",
    "secret_env", "secretEnv", "secrets", "setup_commands", "setupCommands", "setup_timeout", "setup_timeout_seconds",
    "codex_auth_file_env", "codexAuthFileEnv",
    "deploy_key_env", "deployKeyEnv", "secret", "ssh_key", "sshKey", "ssh_key_env", "sshKeyEnv", "token",
    "build_logs", "buildLogs", "on_snapshot_create_logs", "onSnapshotCreateLogs",
    "target",
    "cwd", "workdir", "working_dir",
  ]) {
    delete out[key];
  }
	for (const [from, to] of [
    ["auto_stop_interval", "autoStopInterval"],
    ["auto_archive_interval", "autoArchiveInterval"],
    ["auto_delete_interval", "autoDeleteInterval"],
    ["network_allow_list", "networkAllowList"],
    ["network_block_all", "networkBlockAll"],
    ["env_vars", "envVars"],
    ["snapshot_name", "snapshotName"],
  ]) {
    if (out[from] !== undefined && out[to] === undefined) out[to] = out[from];
    delete out[from];
  }
  for (const [key, value] of Object.entries(out)) {
    if (value === undefined || value === null || value === "") delete out[key];
  }
  return out;
}

function numberOption(input, ...keys) {
  for (const key of keys) {
    const value = input?.[key];
    if (value == null || value === "") continue;
    const n = Number(value);
    if (Number.isFinite(n) && n > 0) return n;
  }
  return undefined;
}

function daytonaImageForSDK(image, sdk) {
  if (image == null || typeof image === "string") return image;
  if (typeof image !== "object") return image;
  if (typeof image.runCommands === "function" || typeof image.pipInstall === "function" || typeof image.workdir === "function") {
    return image;
  }
  if (!sdk || !sdk.Image) return image;

  const metadata = daytonaImageMetadata(image);
  const base = String(metadata.base || "");
  const dockerfile = String(metadata.dockerfile || "");
  let built;
  if (base && /^debian-slim:/i.test(base) && typeof sdk.Image.debianSlim === "function") {
    built = sdk.Image.debianSlim(base.replace(/^debian-slim:/i, ""));
  } else if (base && typeof sdk.Image.base === "function") {
    built = sdk.Image.base(base);
  } else if (dockerfile && typeof sdk.Image.fromDockerfile === "function") {
    built = sdk.Image.fromDockerfile(dockerfile);
  }
  if (!built) return image;
  for (const step of Array.isArray(metadata.steps) ? metadata.steps : []) {
    built = applyDaytonaImageStep(built, step);
  }
  return built;
}

function daytonaImageMetadata(image) {
  if (image == null || typeof image === "string") return image;
  if (image && typeof image === "object" && image.__loomDaytonaImage) return image.__loomDaytonaImage;
  if (!image || typeof image !== "object") return image;
  if (image.__loomType === "daytona_image" || image.base || image.dockerfile || Array.isArray(image.steps)) {
    return {
      ...(image.base ? { base: image.base } : {}),
      ...(image.dockerfile ? { dockerfile: image.dockerfile } : {}),
      ...(Array.isArray(image.steps) ? { steps: image.steps } : {}),
    };
  }
  return image;
}

function applyDaytonaImageStep(image, step = {}) {
  const op = String(step.op || "").trim();
  if (!op) return image;
  switch (op) {
    case "runCommands":
      return callDaytonaImageMethod(image, "runCommands", flattenStringArgs(step.commands || []));
    case "dockerfileCommands":
      return callDaytonaImageMethod(image, "dockerfileCommands", [stringArray(step.commands || []), String(step.contextDir || "")]);
    case "pipInstall": {
      const packages = stringArray(step.packages || []);
      return callDaytonaImageMethod(image, "pipInstall", [packages.length === 1 ? packages[0] : packages, cleanObject(step.options || {})]);
    }
    case "pipInstallFromRequirements":
      return callDaytonaImageMethod(image, "pipInstallFromRequirements", [String(step.file || ""), cleanObject(step.options || {})]);
    case "pipInstallFromPyproject":
      return callDaytonaImageMethod(image, "pipInstallFromPyproject", [String(step.file || ""), cleanObject(step.options || {})]);
    case "addLocalFile":
      return callDaytonaImageMethod(image, "addLocalFile", [String(step.source || ""), String(step.target || "")]);
    case "addLocalDir":
      return callDaytonaImageMethod(image, "addLocalDir", [String(step.source || ""), String(step.target || "")]);
    case "env":
      return callDaytonaImageMethod(image, "env", [stringRecord(step.vars || {})]);
    case "workdir":
      return callDaytonaImageMethod(image, "workdir", [String(step.dir || "")]);
    case "entrypoint":
      return callDaytonaImageMethod(image, "entrypoint", [Array.isArray(step.value) ? stringArray(step.value) : [String(step.value || "")]]);
    case "cmd":
      return callDaytonaImageMethod(image, "cmd", [Array.isArray(step.value) ? stringArray(step.value) : [String(step.value || "")]]);
    case "user":
      return callDaytonaImageMethod(image, "user", [String(step.value || "")]);
    default:
      return image;
  }
}

function callDaytonaImageMethod(image, method, args = []) {
  if (!image || typeof image[method] !== "function") return image;
  const result = image[method](...args.filter((arg) => arg !== undefined && arg !== ""));
  return result || image;
}

function stringArray(value) {
  if (Array.isArray(value)) return value.map((item) => String(item || "")).filter(Boolean);
  if (value == null || value === "") return [];
  return [String(value)];
}

function flattenStringArgs(args) {
  const out = [];
  for (const arg of Array.isArray(args) ? args : [args]) {
    if (Array.isArray(arg)) out.push(...flattenStringArgs(arg));
    else if (arg !== undefined && arg !== null && arg !== "") out.push(String(arg));
  }
  return out;
}

function stringRecord(value) {
  const out = {};
  for (const [key, item] of Object.entries(value || {})) {
    if (key && item !== undefined && item !== null) out[key] = String(item);
  }
  return out;
}

async function executeSandboxCommand(sandbox, command, cwd, env, timeout) {
  const options = cleanObject({ cwd, env, timeout });
  const request = cleanObject({ command, cwd, env, timeout });
  if (sandbox.process && typeof sandbox.process.executeCommand === "function") {
    return executeWithSignatureFallbacks(sandbox.process.executeCommand.bind(sandbox.process), [
      [command, cwd, env, timeout],
      [command, options],
      [request],
    ]);
  }
  if (sandbox.process && typeof sandbox.process.exec === "function") {
    return executeWithSignatureFallbacks(sandbox.process.exec.bind(sandbox.process), [
      [command, cwd, env, timeout],
      [command, options],
      [request],
    ]);
  }
  if (typeof sandbox.shell === "function") {
    return executeWithSignatureFallbacks(sandbox.shell.bind(sandbox), [
      [command, options],
      [command, cwd, env, timeout],
      [request],
    ]);
  }
  throw new Error("Daytona sandbox does not expose process.executeCommand, process.exec, or shell");
}

async function executeWithSignatureFallbacks(fn, calls) {
  let lastErr;
  for (let i = 0; i < calls.length; i += 1) {
    try {
      return await fn(...calls[i]);
    } catch (err) {
      lastErr = err;
      if (!isSandboxCommandSignatureError(err) || i === calls.length - 1) throw err;
    }
  }
  throw lastErr;
}

function isSandboxCommandSignatureError(err) {
  const message = err && err.message ? String(err.message) : String(err || "");
  return /argument|parameter|signature|cwd|env|timeout|object|options/i.test(message);
}

function responseExitCode(response) {
  const exitCode = Number(response?.exitCode ?? response?.exit_code ?? response?.code ?? 0);
  return Number.isFinite(exitCode) ? exitCode : 0;
}

function responseStdout(response) {
  for (const value of [response?.stdout, response?.output, response?.artifacts?.stdout, response?.result]) {
    if (value === undefined || value === null) continue;
    if (typeof value === "string") return value;
    if (typeof value !== "object") return String(value);
  }
  return "";
}

function responseStderr(response) {
  for (const value of [response?.stderr, response?.artifacts?.stderr, response?.error]) {
    if (value === undefined || value === null) continue;
    if (typeof value === "string") return value;
    if (typeof value !== "object") return String(value);
  }
  return "";
}

async function executeRequiredSandboxCommand(sandbox, step, fallbackCwd, fallbackEnv) {
  if (!step || !step.command) return;
  const response = await executeSandboxCommand(
    sandbox,
    step.command,
    step.cwd || fallbackCwd || "/",
    step.env || fallbackEnv || {},
    step.timeout_seconds || undefined,
  );
  const exitCode = responseExitCode(response);
  if (exitCode !== 0) {
    const detail = responseStderr(response) || responseStdout(response);
    throw new Error((step.name || "setup command") + " failed with exit code " + exitCode + (detail ? ": " + detail : ""));
  }
}

function cleanObject(input) {
  const out = {};
  for (const [key, value] of Object.entries(input || {})) {
    if (value !== undefined && value !== null && value !== "") out[key] = value;
  }
  return out;
}

function shellQuote(value) {
  value = String(value || "");
  if (!value) return "''";
  return "'" + value.replaceAll("'", "'\"'\"'") + "'";
}

async function cleanupGitAuthFiles(sandbox, env = {}) {
  const paths = [env.LOOM_GIT_ASKPASS, env.LOOM_GIT_SSH_KEY].filter(Boolean);
  if (paths.length === 0) return;
  try {
    await executeSandboxCommand(sandbox, "rm -f " + paths.map(shellQuote).join(" "), "/", {}, undefined);
  } catch (_) {}
}

let activeClient = null;
let activeSandbox = null;
let activeSandboxId = "";
let activeDaytona = {};
let activePhase = "provisioning";
let activeCleanupState = "pending";
let finalizing = false;
let resultWritten = false;

async function finalizeSandbox(client, sandbox, daytona = {}, env = {}) {
  if (finalizing) return activeCleanupState;
  finalizing = true;
  if (!sandbox) {
    activeCleanupState = "not_started";
    return activeCleanupState;
  }
  await cleanupGitAuthFiles(sandbox, env);
  const keep = daytona.keep_sandbox === true || daytona.keepSandbox === true || daytona.delete_after_run === false || daytona.deleteAfterRun === false;
  if (keep) {
    activeCleanupState = "retained";
  } else if (client && typeof client.delete === "function") {
    try {
      await client.delete(sandbox, 60);
      activeCleanupState = "deleted";
    } catch (_) {
      activeCleanupState = "failed";
    }
  } else {
    activeCleanupState = "unknown";
  }
  return activeCleanupState;
}

function writeResult(result) {
  if (resultWritten) return;
  resultWritten = true;
  process.stdout.write(JSON.stringify(result));
}

function emitProgress(update) {
  try {
    process.stderr.write("__loom_daytona_progress__" + JSON.stringify(cleanObject(update)) + "\n");
  } catch (_) {}
}

async function handleCancel(signal) {
  activePhase = "stopping";
  const cleanupState = await finalizeSandbox(activeClient, activeSandbox, activeDaytona, payload.env || {});
  emitProgress({
    sandbox_id: activeSandboxId,
    phase: "stopping",
    cleanup_state: cleanupState,
  });
  writeResult({
    sandbox_id: activeSandboxId,
    exit_code: 130,
    stdout: "",
    stderr: "Daytona runner canceled by " + signal,
    phase: "stopping",
    cleanup_state: cleanupState,
  });
  process.exit(0);
}

process.once("SIGTERM", () => {
  void handleCancel("SIGTERM");
});

async function main() {
  const sdk = loadSDK();
  const { Daytona } = sdk;
  const daytona = payload.daytona || {};
  activeDaytona = daytona;
  emitProgress({ phase: activePhase, cleanup_state: activeCleanupState });
  const apiKeyEnv = String(daytona.api_key_env || daytona.apiKeyEnv || "DAYTONA_API_KEY");
  const clientOptions = {};
  if (process.env[apiKeyEnv]) clientOptions.apiKey = process.env[apiKeyEnv];
  if (daytona.api_url || daytona.apiUrl) clientOptions.apiUrl = daytona.api_url || daytona.apiUrl;
  if (daytona.target) clientOptions.target = daytona.target;

  const client = new Daytona(clientOptions);
  activeClient = client;
  const createOptions = cleanCreateOptions(daytona);
  if (createOptions.image !== undefined) createOptions.image = daytonaImageForSDK(createOptions.image, sdk);
  if (daytona.env_vars && !createOptions.envVars) createOptions.envVars = daytona.env_vars;
  delete createOptions.env_vars;
  const createSDKOptions = {};
  const createTimeout = numberOption(daytona, "create_timeout", "createTimeout");
  if (createTimeout) createSDKOptions.timeout = createTimeout;

  const sandbox = await client.create(createOptions, Object.keys(createSDKOptions).length ? createSDKOptions : undefined);
  activeSandbox = sandbox;
  const sandboxId = sandboxIdFor(sandbox);
  activeSandboxId = sandboxId;
  emitProgress({ sandbox_id: sandboxId, phase: activePhase, cleanup_state: activeCleanupState });
  let response;
  let phase = "provisioning";
  let cleanupState = "pending";
  try {
    if (payload.health_check && payload.health_check.command) {
      phase = "setup";
      activePhase = phase;
      emitProgress({ sandbox_id: sandboxId, phase, cleanup_state: activeCleanupState });
      await executeRequiredSandboxCommand(sandbox, payload.health_check, "/", payload.env || {});
    }
    for (const step of payload.setup || []) {
      phase = "setup";
      activePhase = phase;
      emitProgress({ sandbox_id: sandboxId, phase, cleanup_state: activeCleanupState });
      await executeRequiredSandboxCommand(sandbox, step, payload.cwd || "/", payload.env || {});
    }
    phase = "running";
    activePhase = phase;
    emitProgress({ sandbox_id: sandboxId, phase, cleanup_state: activeCleanupState });
    const timeout = payload.timeout_seconds || numberOption(daytona, "run_timeout", "runTimeout", "run_timeout_seconds", "command_timeout");
    response = await executeSandboxCommand(sandbox, payload.command, payload.cwd || "/", payload.env || {}, timeout || undefined);
  } catch (err) {
    response = {
      exitCode: 1,
      stderr: err && err.stack ? String(err.stack) : String(err),
    };
  } finally {
    cleanupState = await finalizeSandbox(client, sandbox, daytona, payload.env || {});
    emitProgress({ sandbox_id: sandboxId, phase, cleanup_state: cleanupState });
  }
  const exitCode = responseExitCode(response);
  writeResult({
    sandbox_id: sandboxId,
    exit_code: Number.isFinite(exitCode) ? exitCode : 0,
    stdout: responseStdout(response),
    stderr: responseStderr(response),
    phase,
    cleanup_state: cleanupState,
  });
}

main().catch((err) => {
  console.error(err && err.stack ? err.stack : String(err));
  process.exit(1);
});
`
