package placement

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

type leadBootPlan struct {
	prep      LeadBootPrep
	checkout  string
	backend   string
	agentRole string
}

const (
	// leadBootstrapBinaryDest is the in-sandbox install path for the
	// download-at-boot loom binary. It matches the PATH entry the snapshot's
	// boot hook resolves `loom` through, so overwriting it before the PTY
	// starts makes the lead boot the freshly served binary.
	leadBootstrapBinaryDest = "/usr/local/bin/loom"
	// leadBootstrapBinaryMode makes the installed binary executable.
	leadBootstrapBinaryMode = "0755"
)

func (p leadBootPlan) needsPrep() bool {
	return p.prep.Repo != nil || p.prep.PromptText != "" || len(p.prep.Files) > 0 || p.prep.BootstrapBinary != nil
}

func (b *Broker) resolveLeadBootPlan(ctx context.Context, req ProvisionRequest, logEmptyRepo bool) (leadBootPlan, error) {
	backend, agentRole := b.resolveLeadEnvValues(ctx, req)
	plan := leadBootPlan{
		backend:   backend,
		agentRole: agentRole,
	}
	plan.prep.Timeout = b.effectiveLeadBootPrepTimeout()
	plan.prep.Files = append([]SandboxFile(nil), req.SeedFiles...)
	plan.prep.BootstrapBinary = b.leadBootstrapBinarySpec()
	if req.PromptText != "" {
		promptPath := promptPathFromCommand(effectiveLeadCommand(req))
		if promptPath == "" {
			promptPath = defaultLeadPromptPath
		}
		if !strings.HasPrefix(strings.TrimSpace(promptPath), "/") {
			return leadBootPlan{}, fmt.Errorf("lead prompt path must be absolute: %w", domain.ErrInvalid)
		}
		plan.prep.PromptPath = promptPath
		plan.prep.PromptText = req.PromptText
	}

	repo, err := b.resolveLeadRepo(ctx, req, logEmptyRepo)
	if err != nil || repo == nil {
		return plan, err
	}
	checkout, err := leadRepoCheckoutPath(repo.Name)
	if err != nil {
		return leadBootPlan{}, err
	}
	_, host, err := NormalizeRepoCloneRemote(repo.RemoteURL)
	if err != nil {
		return leadBootPlan{}, fmt.Errorf("resolve lead repo clone remote for %q: %w", repo.Name, err)
	}
	if err := enforceCloneHostAllowlist(req.NetworkDomainAllowlist, host); err != nil {
		return leadBootPlan{}, err
	}
	plan.checkout = checkout
	plan.prep.Repo = &RepoClone{
		Name:      repo.Name,
		RemoteURL: repo.RemoteURL,
		Ref:       strings.TrimSpace(repo.DefaultBranch),
		Checkout:  checkout,
	}
	plan.prep.GitToken = req.GitToken
	return plan, nil
}

// leadBootstrapBinarySpec returns the download-at-boot install request, or nil
// when the feature is off or no public serve origin is configured to serve the
// binary from. A missing base URL disables it rather than emitting a relative
// URL the sandbox could never resolve.
func (b *Broker) leadBootstrapBinarySpec() *BootstrapBinarySpec {
	if !b.leadBootstrapEnabled || b.leadAPIBaseURL == "" {
		return nil
	}
	return &BootstrapBinarySpec{
		URL:  strings.TrimRight(b.leadAPIBaseURL, "/") + BootstrapLoomPath,
		Dest: leadBootstrapBinaryDest,
		Mode: leadBootstrapBinaryMode,
	}
}

func (b *Broker) resolveLeadEnvValues(ctx context.Context, req ProvisionRequest) (backend string, agentRole string) {
	backend = strings.TrimSpace(req.Backend)
	agentRole = strings.TrimSpace(req.AgentRole)
	if backend != "" && agentRole != "" {
		return backend, agentRole
	}
	agent, err := b.store.Agents().Get(ctx, req.WorkspaceKey, req.AgentName)
	if err != nil || agent == nil {
		return backend, agentRole
	}
	if backend == "" {
		backend = strings.TrimSpace(agent.Backend)
	}
	if agentRole == "" {
		agentRole = strings.TrimSpace(agent.RoleName)
	}
	return backend, agentRole
}

func (b *Broker) resolveLeadRepo(ctx context.Context, req ProvisionRequest, logEmpty bool) (*domain.Repo, error) {
	repos, err := b.store.Repos().List(ctx, req.WorkspaceKey)
	if err != nil {
		return nil, err
	}
	repos = nonNilRepos(repos)
	switch len(repos) {
	case 0:
		if logEmpty {
			slog.InfoContext(ctx, "lead placement has no repos; booting without checkout",
				"workspace", req.WorkspaceKey,
				"agent", req.AgentName)
		}
		return nil, nil
	case 1:
		return repos[0], nil
	default:
		return selectNamedLeadRepo(repos, req.RepoName)
	}
}

func nonNilRepos(in []*domain.Repo) []*domain.Repo {
	// Allocates rather than filtering in place: the input is the store's
	// return value, and mutating its backing array would corrupt any store
	// that hands out an internal slice.
	out := make([]*domain.Repo, 0, len(in))
	for _, repo := range in {
		if repo != nil {
			out = append(out, repo)
		}
	}
	return out
}

func selectNamedLeadRepo(repos []*domain.Repo, name string) (*domain.Repo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("repo name required when workspace has %d repos: %w", len(repos), domain.ErrInvalid)
	}
	for _, repo := range repos {
		if strings.TrimSpace(repo.Name) == name {
			return repo, nil
		}
	}
	return nil, fmt.Errorf("repo %q not found among %d workspace repos: %w", name, len(repos), domain.ErrNotFound)
}

func leadRepoCheckoutPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("repo name %q cannot form a lead checkout path: %w", name, domain.ErrInvalid)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("repo name %q cannot form a lead checkout path: %w", name, domain.ErrInvalid)
		}
	}
	return path.Join(leadCheckoutRoot, name), nil
}

func enforceCloneHostAllowlist(allowlist []string, host string) error {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || len(allowlist) == 0 {
		return nil
	}
	for _, entry := range allowlist {
		if strings.EqualFold(strings.TrimSpace(entry), host) {
			return nil
		}
	}
	return fmt.Errorf("lead repo clone host %q is not in network domain allowlist: %w", host, domain.ErrInvalid)
}

func providerCreateRequest(req ProvisionRequest, nodeID, token, deploymentID, leadAPIBaseURL string, bootPlan leadBootPlan) CreateRequest {
	labels := copyMap(req.Labels)
	labels[PlacementLabelKey] = nodeID
	labels[EnvironmentLabelKey] = deploymentID
	labels["loom-workspace"] = req.WorkspaceKey
	labels["loom-agent"] = req.AgentName
	env := leadEnv(req.Env, req.WorkspaceKey, req.AgentName, nodeID, token, leadAPIBaseURL, bootPlan)
	return CreateRequest{
		WorkspaceKey:           req.WorkspaceKey,
		AgentName:              req.AgentName,
		SnapshotRef:            req.SnapshotRef,
		Name:                   sandboxNameForPlacement(nodeID),
		Labels:                 labels,
		Env:                    env,
		Resource:               req.Resource,
		NetworkDomainAllowlist: append([]string(nil), req.NetworkDomainAllowlist...),
	}
}

func processSpec(req ProvisionRequest, node *domain.Node, token, leadAPIBaseURL string, bootPlan leadBootPlan) ProcessSpec {
	spec := req.Process
	spec.SessionID = LeadPTYSessionID
	spec.Command = effectiveLeadCommand(req)
	if bootPlan.prep.PromptPath != "" && !commandHasPrompt(spec.Command) {
		spec.Command = append(spec.Command, "--prompt", bootPlan.prep.PromptPath)
	}
	if bootPlan.checkout != "" {
		spec.WorkingDir = bootPlan.checkout
	}
	spec.Env = leadEnv(spec.Env, req.WorkspaceKey, req.AgentName, node.NodeID, token, leadAPIBaseURL, bootPlan)
	spec.TTY = true
	return spec
}

func effectiveLeadCommand(req ProvisionRequest) []string {
	command := append([]string(nil), req.Process.Command...)
	if len(command) == 0 {
		return []string{"loom", "--workspace", req.WorkspaceKey, "lead"}
	}
	return command
}

func commandHasPrompt(command []string) bool {
	return promptPathFromCommand(command) != ""
}

func promptPathFromCommand(command []string) string {
	for i, arg := range command {
		arg = strings.TrimSpace(arg)
		if arg == "--prompt" && i+1 < len(command) {
			return strings.TrimSpace(command[i+1])
		}
		if value, ok := strings.CutPrefix(arg, "--prompt="); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func leadEnv(base map[string]string, workspace, agent, nodeID, token, leadAPIBaseURL string, bootPlan leadBootPlan) map[string]string {
	env := copyMap(base)
	env["LOOM_WORKSPACE"] = workspace
	env["LOOM_AGENT_NAME"] = agent
	env["LOOM_LEAD_PLACEMENT_ID"] = nodeID
	env[OccupantTokenEnv] = token
	if strings.TrimSpace(leadAPIBaseURL) != "" {
		env["LOOM_LEAD_API_URL"] = strings.TrimSpace(leadAPIBaseURL)
	}
	env["TERM"] = "xterm-256color"
	if bootPlan.backend != "" {
		env["LOOM_BACKEND"] = bootPlan.backend
	}
	if bootPlan.agentRole != "" {
		env["LOOM_AGENT_ROLE"] = bootPlan.agentRole
	}
	return env
}

func nodeLabels(req ProvisionRequest) []string {
	return []string{
		"loom-lead-placement",
		"loom-workspace=" + req.WorkspaceKey,
		"loom-agent=" + req.AgentName,
	}
}
