package workspacemgr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/agentsbootstrap"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr/admissionstore"
	"github.com/tysonthomas9/loomcli/internal/cli/serve/workspacemgr/agentsbootstrapcomposition"
	"github.com/tysonthomas9/loomcli/internal/domain"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/infra/workspacecatalog"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/sourcecontrol"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/roleprompts"
	storepkg "github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/workspacecoord"
	"github.com/tysonthomas9/loomcli/internal/workspaceerrors"
)

// repositoryCheckoutMaterializer is the only checkout authority Workspace
// admission receives. It intentionally omits the Source Control owner API,
// credential broker, and provider credential types.
type repositoryCheckoutMaterializer interface {
	PrepareRepositoryAdmissionCheckout(
		context.Context,
		sourcecontrol.RepositoryAdmissionCheckoutCommand,
	) (*sourcecontrol.PreparedRepositoryCheckout, error)
}

// BuildStoreBackedCreateWorkspace returns a create function for fleet-db store
// mode. Existing-dir ("empty") creation writes workspace/repo metadata to the
// store as the source of truth and records only local checkout paths in
// ~/.loom/state.json.
func BuildStoreBackedCreateWorkspace(s storepkg.Store) workspacecoord.WorkspaceCreateFn {
	return BuildStoreBackedCreateWorkspaceWithSourceControl(s, nil)
}

// BuildStoreBackedCreateWorkspaceWithSourceControl is the UI/runtime variant.
// Clone requests fail closed without the owner materializer; raw remotes are
// persisted only after Source Control validation and provider credentials
// never cross this boundary.
func BuildStoreBackedCreateWorkspaceWithSourceControl(
	s storepkg.Store,
	materializer repositoryCheckoutMaterializer,
) workspacecoord.WorkspaceCreateFn {
	return BuildStoreBackedCreateWorkspaceWithAdmission(
		s,
		nil,
		nil,
		materializer,
	)
}

// BuildStoreBackedCreateWorkspaceWithAdmission composes the production
// restart-safe repository-admission process. FleetDB reserves the complete
// repository batch before any checkout is published, while the local journal
// binds that admission to this machine's checkout root.
func BuildStoreBackedCreateWorkspaceWithAdmission(
	s storepkg.Store,
	admissions infrafleetdb.RepositoryAdmissionTransport,
	journal *RepositoryAdmissionJournal,
	materializer repositoryCheckoutMaterializer,
) workspacecoord.WorkspaceCreateFn {
	catalog, err := workspacecatalog.New(s.Workspaces(), s.Repos())
	if err != nil {
		return nil
	}
	operations := NewStoreBackedWorkspaceAdmissionOperationsWithWorkspace(
		s,
		catalog,
		admissions,
		journal,
		materializer,
	)
	if operations == nil {
		return nil
	}
	return operations.CreateWorkspace
}

// BuildStoreBackedAddRepos returns a repo attachment function for fleet-db
// store mode. It creates git worktrees, then registers those repos in the
// store and local state cache as one rollback-aware operation.
func BuildStoreBackedAddRepos(s storepkg.Store) workspacecoord.WorkspaceAddReposFn {
	return BuildStoreBackedAddReposWithSourceControl(s, nil)
}

// BuildStoreBackedAddReposWithSourceControl is the UI/runtime variant. Local
// worktree attachment remains Workspace-owned; remote checkout always crosses
// the credential-free Source Control materializer.
func BuildStoreBackedAddReposWithSourceControl(
	s storepkg.Store,
	materializer repositoryCheckoutMaterializer,
) workspacecoord.WorkspaceAddReposFn {
	return BuildStoreBackedAddReposWithAdmission(
		s,
		nil,
		nil,
		materializer,
	)
}

// BuildStoreBackedAddReposWithAdmission composes the same durable batch for
// an existing Workspace. Neither local worktrees nor remote checkouts become
// FleetDB Repo records until one owner-fenced Commit publishes the full set.
func BuildStoreBackedAddReposWithAdmission(
	s storepkg.Store,
	admissions infrafleetdb.RepositoryAdmissionTransport,
	journal *RepositoryAdmissionJournal,
	materializer repositoryCheckoutMaterializer,
) workspacecoord.WorkspaceAddReposFn {
	catalog, err := workspacecatalog.New(s.Workspaces(), s.Repos())
	if err != nil {
		return nil
	}
	operations := NewStoreBackedWorkspaceAdmissionOperationsWithWorkspace(
		s,
		catalog,
		admissions,
		journal,
		materializer,
	)
	if operations == nil {
		return nil
	}
	return operations.AddWorkspaceRepos
}

//nolint:cyclop,funlen,gocognit // Orchestrates filesystem, git, and store rollback steps for one workflow.
func createStoreBackedEmptyWorkspace(
	ctx context.Context,
	s admissionstore.Store,
	catalog workspacemodule.API,
	req workspacecoord.WorkspaceCreateRequest,
) (workspacecoord.WorkspaceCreateResult, error) {
	if req.Type != "empty" {
		return workspacecoord.WorkspaceCreateResult{}, fmt.Errorf("unsupported workspace type: %s", req.Type)
	}
	if catalog == nil {
		return workspacecoord.WorkspaceCreateResult{}, workspaceerrors.New(
			workspaceerrors.ConfigFailed,
			"Workspace capability is unavailable",
			workspacemodule.ErrUnavailable,
		)
	}
	if existing, err := catalog.Resolve(ctx, workspacemodule.ResolveQuery{Reference: req.Name}); err == nil && existing != nil {
		return workspacecoord.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), nil)
	} else if err != nil && !errors.Is(err, workspacemodule.ErrNotFound) {
		return workspacecoord.WorkspaceCreateResult{}, fmt.Errorf("check workspace name: %w", err)
	}

	wsPlan, err := resolveWorkspaceDirForCreate(req.Path, req.Name)
	if err != nil {
		return workspacecoord.WorkspaceCreateResult{}, err
	}
	wsDir := wsPlan.path
	var resolved []resolvedRepo
	if len(req.Repos) > 0 {
		resolved, err = resolveRepoPaths(req.Repos)
		if err != nil {
			return workspacecoord.WorkspaceCreateResult{}, err
		}
	}

	branch := req.Branch
	if branch == "" {
		branch = req.Name
	}
	key := workspacecoord.WorkspaceKeyFromName(req.Name)
	if _, err := catalog.Resolve(ctx, workspacemodule.ResolveQuery{Reference: key}); err == nil {
		return workspacecoord.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), nil)
	} else if err != nil && !errors.Is(err, workspacemodule.ErrNotFound) {
		return workspacecoord.WorkspaceCreateResult{}, fmt.Errorf("check workspace key: %w", err)
	}

	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return workspacecoord.WorkspaceCreateResult{}, fmt.Errorf("cannot create workspace directory: %w", err)
	}
	var created []createdWorktree
	var repos []config.RepoConfig
	if len(resolved) > 0 {
		created, repos, err = addWorktrees(ctx, resolved, wsDir, branch)
		if err != nil {
			cleanupWorktrees(wsPlan, created)
			return workspacecoord.WorkspaceCreateResult{}, err
		}
	}

	if _, err := catalog.Create(ctx, workspacemodule.CreateCommand{
		Key:           key,
		Name:          req.Name,
		DefaultBranch: branch,
	}); err != nil {
		cleanupWorktrees(wsPlan, created)
		if errors.Is(err, workspacemodule.ErrConflict) {
			return workspacecoord.WorkspaceCreateResult{}, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("workspace %q already exists", req.Name), err)
		}
		return workspacecoord.WorkspaceCreateResult{}, fmt.Errorf("create workspace in store: %w", err)
	}
	rollbackStore := func() {
		deleteLocalWorkspaceState(key)
		if _, err := catalog.Delete(context.Background(), workspacemodule.DeleteCommand{Reference: key}); err != nil && !errors.Is(err, workspacemodule.ErrNotFound) {
			slog.Warn("failed to rollback store workspace create", "workspace", key, "err", err)
		}
	}
	if err := seedBuiltInRoles(ctx, s, key, wsDir); err != nil {
		rollbackStore()
		cleanupWorktrees(wsPlan, created)
		return workspacecoord.WorkspaceCreateResult{}, fmt.Errorf("seed built-in roles: %w", err)
	}

	for _, r := range repos {
		remoteName := r.Remote
		if remoteName == "" {
			remoteName = "origin"
		}
		remoteURL, err := persistentGitRemoteURL(r.Path, remoteName)
		if err != nil {
			rollbackStore()
			cleanupWorktrees(wsPlan, created)
			return workspacecoord.WorkspaceCreateResult{}, err
		}
		if _, err := catalog.RegisterRepository(ctx, workspacemodule.RegisterRepositoryCommand{
			WorkspaceReference: key,
			Name:               r.Name,
			RemoteURL:          remoteURL,
			Remote:             remoteName,
			DefaultBranch:      branch,
			SourceRepoID:       r.SourceRepoID,
		}); err != nil {
			rollbackStore()
			cleanupWorktrees(wsPlan, created)
			return workspacecoord.WorkspaceCreateResult{}, fmt.Errorf("create repo %q in store: %w", r.Name, err)
		}
	}

	if err := saveLocalWorkspaceState(key, wsDir, repos, true); err != nil {
		rollbackStore()
		cleanupWorktrees(wsPlan, created)
		return workspacecoord.WorkspaceCreateResult{}, err
	}
	if _, err := catalog.SetLifecycle(ctx, workspacemodule.SetLifecycleCommand{Reference: key, State: workspacemodule.StateReady}); err != nil {
		rollbackStore()
		cleanupWorktrees(wsPlan, created)
		return workspacecoord.WorkspaceCreateResult{}, fmt.Errorf("mark workspace ready: %w", err)
	}

	return workspacecoord.WorkspaceCreateResult{WorkspaceID: key, WorkspacePath: wsDir}, nil
}

//nolint:cyclop,funlen // Coordinates local git worktrees, fleet-db repo records, and local state rollback.
func addReposToStoreBackedWorkspace(
	ctx context.Context,
	catalog workspacemodule.API,
	req workspacecoord.WorkspaceAddReposRequest,
	materializer repositoryCheckoutMaterializer,
) (workspacecoord.WorkspaceCreateResult, error) {
	key, ws, err := resolveWorkspaceForAddRepos(ctx, catalog, req.WorkspaceID)
	if err != nil {
		return workspacecoord.WorkspaceCreateResult{}, err
	}
	wsDir, err := prepareWorkspaceDir(key)
	if err != nil {
		return workspacecoord.WorkspaceCreateResult{}, err
	}

	resolved, err := resolveRequestRepos(req.Repos)
	if err != nil {
		return workspacecoord.WorkspaceCreateResult{}, err
	}
	seen, err := dedupAddReposAgainstExisting(ctx, catalog, key, resolved)
	if err != nil {
		return workspacecoord.WorkspaceCreateResult{}, err
	}

	branch := pickAddReposBranch(req.Branch, ws, key)

	created, repos, err := materializeAddReposWorktrees(ctx, resolved, wsDir, branch, req.Branch)
	if err != nil {
		return workspacecoord.WorkspaceCreateResult{}, err
	}
	clonedRepos, clonesToCleanup, err := materializeAddReposClones(
		ctx,
		key,
		nil,
		req.CloneURLs,
		wsDir,
		seen,
		created,
		req.Branch,
		materializer,
		nil,
	)
	if err != nil {
		return workspacecoord.WorkspaceCreateResult{}, err
	}
	allRepos := append(append([]config.RepoConfig(nil), repos...), clonedRepos...)

	if err := persistAddReposRecords(
		ctx,
		catalog,
		key,
		wsDir,
		branch,
		repos,
		allRepos,
		created,
		clonedRepos,
		clonesToCleanup,
	); err != nil {
		return workspacecoord.WorkspaceCreateResult{}, err
	}
	return workspacecoord.WorkspaceCreateResult{WorkspaceID: key, WorkspacePath: wsDir}, nil
}

// resolveWorkspaceForAddRepos looks up the workspace by ID, then falls back
// to lookup-by-name so callers can pass either. Returns the canonical key
// (which may differ from the input when matched by name).
func resolveWorkspaceForAddRepos(ctx context.Context, catalog workspacemodule.API, workspaceID string) (string, *workspacemodule.Reference, error) {
	key := strings.TrimSpace(workspaceID)
	if key == "" {
		return "", nil, workspaceerrors.New(workspaceerrors.PathNotFound, "workspace ID is required", nil)
	}
	if catalog == nil {
		return "", nil, workspaceerrors.New(workspaceerrors.ConfigFailed, "Workspace capability is unavailable", workspacemodule.ErrUnavailable)
	}
	workspace, err := catalog.Resolve(ctx, workspacemodule.ResolveQuery{Reference: key})
	if err != nil {
		return "", nil, fmt.Errorf("load workspace %q: %w", workspaceID, err)
	}
	return workspace.Key, workspace, nil
}

// prepareWorkspaceDir resolves the workspace's on-disk path, validates it,
// and ensures the directory exists.
func prepareWorkspaceDir(key string) (string, error) {
	wsDir, err := localWorkspacePath(key)
	if err != nil {
		return "", err
	}
	if err := validateWorkspacePath(wsDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create workspace directory: %w", err)
	}
	return wsDir, nil
}

func resolveRequestRepos(reqRepos []string) ([]resolvedRepo, error) {
	if len(reqRepos) == 0 {
		return nil, nil
	}
	return resolveRepoPaths(reqRepos)
}

// dedupAddReposAgainstExisting builds the set of repo names already present
// in the workspace, then verifies the requested repos don't collide. Returns
// the merged seen-set so downstream clone steps can extend it.
func dedupAddReposAgainstExisting(ctx context.Context, catalog workspacemodule.API, key string, resolved []resolvedRepo) (map[string]bool, error) {
	if catalog == nil {
		return nil, workspaceerrors.New(workspaceerrors.ConfigFailed, "Workspace capability is unavailable", workspacemodule.ErrUnavailable)
	}
	existing, err := catalog.ListRepositories(ctx, workspacemodule.ListRepositoriesQuery{WorkspaceReference: key})
	if err != nil {
		return nil, fmt.Errorf("list workspace repos: %w", err)
	}
	seen := make(map[string]bool, len(existing)+len(resolved))
	for _, r := range existing {
		seen[r.Name] = true
	}
	for _, r := range resolved {
		if seen[r.name] {
			return nil, workspaceerrors.New(workspaceerrors.AlreadyExists, fmt.Sprintf("repo %q already exists in workspace %q", r.name, key), nil)
		}
		seen[r.name] = true
	}
	return seen, nil
}

// pickAddReposBranch resolves the target branch using the precedence
// request → workspace default → workspace name → workspace key. Same fallbacks
// the inline code used; lifted out so the outer function isn't paying the
// cognitive cost of three sequential ifs.
func pickAddReposBranch(reqBranch string, ws *workspacemodule.Reference, key string) string {
	if reqBranch != "" {
		return reqBranch
	}
	if ws.DefaultBranch != "" {
		return ws.DefaultBranch
	}
	if ws.Name != "" {
		return ws.Name
	}
	return key
}

// materializeAddReposWorktrees attaches a worktree for each resolved repo,
// rolling back partially-attached worktrees on failure.
func materializeAddReposWorktrees(
	ctx context.Context,
	resolved []resolvedRepo,
	wsDir, branch, defaultBranchOverride string,
) ([]createdWorktree, []config.RepoConfig, error) {
	if len(resolved) == 0 {
		return nil, nil, nil
	}
	warningCtx := workspacecoord.WithCreateWarnings(ctx)
	created, repos, err := addWorktreesWithRepoDefault(warningCtx, resolved, wsDir, branch, defaultBranchOverride)
	if err != nil {
		cleanupAttachedWorktrees(created)
		return nil, nil, err
	}
	if len(created) != len(resolved) {
		cleanupAttachedWorktrees(created)
		message := "one or more local repository checkouts could not be materialized"
		if warnings := workspacecoord.GetCreateWarnings(warningCtx); len(warnings) > 0 {
			message = strings.Join(warnings, "; ")
		}
		return nil, nil, workspaceerrors.New(workspaceerrors.GitFailed, "attach local repository checkout failed", errors.New(message))
	}
	return created, repos, nil
}

// seedBuiltInRoles creates the domain.BuiltinRoleNames set — the two lists
// must stay in step, since delete guards consult the domain list. The
// task-running roles (plan/task) are seeded with a TS-contract prompt body on
// disk so the prompt-agent → local-task-runner lane can reuse them by name
// (without a body, role-get returns "" and prompt-agent fails closed with
// prompt_agent_missing_prompt). Writing the prompt file is best-effort: a write
// failure logs and leaves PromptFile empty, and EnsureBuiltinRolePrompts
// materializes it at the next serve start.
func seedBuiltInRoles(ctx context.Context, s admissionstore.Store, key, wsDir string) error { //nolint:funlen // Built-in role seeding preserves exact definitions and per-role prompt repair behavior.
	roles := []storepkg.RoleCreate{
		{
			WorkspaceKey: key,
			Name:         "plan",
			Description:  "Planning agent",
			TaskFilter:   "needs_plan",
			ReadOnly:     true,
		},
		{
			WorkspaceKey: key,
			Name:         "task",
			Description:  "Task implementation agent",
			TaskFilter:   "has_design",
		},
		{
			WorkspaceKey: key,
			Name:         "lead",
			Kind:         string(domain.RoleKindInteractive),
			Description:  "Lead/orchestrator terminal",
		},
	}
	commands, err := agentsbootstrapcomposition.NewManagedCommands(
		s.Roles(),
		s.AgentServices(),
	)
	if err != nil {
		return err
	}
	for _, role := range roles {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		role.PromptFile = seedRolePromptFile(wsDir, role.Name)
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		if _, err := commands.EnsureRole(ctx, agents.EnsureRoleCommand{
			RequestID:    "workspace-bootstrap:" + key + ":" + role.Name,
			WorkspaceKey: key,
			Role: agents.RoleDefinition{
				Name: role.Name, Kind: role.Kind, Description: role.Description,
				Prompt: role.Prompt, PromptFile: role.PromptFile, Model: role.Model,
				TaskFilter: role.TaskFilter, Backend: role.Backend, Effort: role.Effort,
				PathPatterns: role.PathPatterns, Skills: role.Skills,
				MaxPriority: role.MaxPriority, MaxConcurrency: role.MaxConcurrency,
				ReadOnly: role.ReadOnly, AllowedTools: role.AllowedTools,
				DeniedTools: role.DeniedTools, MaxBudgetUSD: role.MaxBudgetUSD,
			},
		}); err != nil {
			return fmt.Errorf("create role %q: %w", role.Name, err)
		}
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
	}
	return nil
}

// seedRolePromptFile materializes the default TS-contract prompt body for a
// builtin role into the workspace and returns its absolute path (empty on
// failure or for a role with no default body — the caller then seeds the role
// with no PromptFile and the serve-start backfill retries).
func seedRolePromptFile(wsDir, roleName string) string {
	body, ok := roleprompts.DefaultPromptBody(roleName)
	if !ok {
		return ""
	}
	path, err := roleprompts.WritePromptFile(wsDir, roleName, "", body)
	if err != nil {
		slog.Warn("failed to seed builtin role prompt; backfill will retry", "role", roleName, "workspace_dir", wsDir, "err", err)
		return ""
	}
	return path
}

// EnsureBuiltinRolePrompts backfills the TS-contract prompt body for the builtin
// task-running roles (plan/task) in every locally-materialized workspace. It is
// idempotent and NON-destructive: it materializes the default body ONLY when the
// role's PromptFile is empty, so an operator-customized prompt is never
// clobbered. Run once at serve start. Best-effort: a workspace without a local
// path, a missing role, or a write/update failure is logged and skipped without
// aborting the sweep.
func EnsureBuiltinRolePrompts(ctx context.Context, s storepkg.Store) error {
	if s == nil {
		return nil
	}
	commands, err := agentsbootstrapcomposition.NewManagedCommands(
		s.Roles(),
		s.AgentServices(),
	)
	if err != nil {
		return err
	}
	workspaces, err := s.Workspaces().List(ctx)
	if err != nil {
		return fmt.Errorf("list workspaces for role prompt backfill: %w", err)
	}
	for _, ws := range workspaces {
		if ws == nil || strings.TrimSpace(ws.Key) == "" {
			continue
		}
		wsDir, err := localWorkspacePath(ws.Key)
		if err != nil || strings.TrimSpace(wsDir) == "" {
			// Not open on this machine — nothing to write to; skip quietly.
			continue
		}
		for _, roleName := range roleprompts.BuiltinPromptRoleNames() {
			ensureBuiltinRolePromptWithCommands(ctx, s, commands, ws.Key, wsDir, roleName)
		}
	}
	return nil
}

// ensureBuiltinRolePrompt materializes one builtin role's default prompt body
// when (and only when) its PromptFile is empty.
func ensureBuiltinRolePrompt(ctx context.Context, s storepkg.Store, key, wsDir, roleName string) {
	commands, err := agentsbootstrapcomposition.NewManagedCommands(
		s.Roles(),
		s.AgentServices(),
	)
	if err != nil {
		slog.Warn("failed to compose Agents role prompt repair", "role", roleName, "workspace", key, "err", err)
		return
	}
	ensureBuiltinRolePromptWithCommands(ctx, s, commands, key, wsDir, roleName)
}

func ensureBuiltinRolePromptWithCommands(
	ctx context.Context,
	s storepkg.Store,
	commands agentsbootstrap.ManagedCommands,
	key, wsDir, roleName string,
) {
	role, err := s.Roles().Get(ctx, key, roleName)
	if err != nil {
		// Role not seeded (older workspace shape); leave it to seedBuiltInRoles.
		return
	}
	if strings.TrimSpace(role.PromptFile) != "" {
		return // never clobber an operator-customized (or already-seeded) prompt
	}
	body, ok := roleprompts.DefaultPromptBody(roleName)
	if !ok {
		return
	}
	path, err := roleprompts.WritePromptFile(wsDir, roleName, "", body)
	if err != nil {
		slog.Warn("failed to backfill builtin role prompt", "role", roleName, "workspace", key, "err", err)
		return
	}
	if _, _, err := commands.RepairRolePromptFile(ctx, agents.RepairManagedRolePromptFileCommand{
		RequestID:    "builtin-role-prompt-backfill:" + key + ":" + roleName,
		WorkspaceKey: key,
		RoleName:     roleName,
		PromptFile:   path,
	}); err != nil {
		slog.Warn("failed to set builtin role PromptFile after backfill write", "role", roleName, "workspace", key, "err", err)
	}
}

func saveLocalWorkspaceState(key, wsDir string, repos []config.RepoConfig, makeActive bool) error {
	if key == "" {
		return errors.New("save local workspace state: key must not be empty")
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		if sc.Workspaces == nil {
			sc.Workspaces = make(map[string]bootstrap.WorkspaceLocalState)
		}
		local := sc.Workspaces[key]
		local.Path = wsDir
		if local.Repos == nil {
			local.Repos = make(map[string]string, len(repos))
		}
		for _, r := range repos {
			local.Repos[r.Name] = r.Path
		}
		if local.Agents == nil {
			local.Agents = make(map[string]bootstrap.AgentLocalState)
		}
		sc.Workspaces[key] = local
		if makeActive {
			sc.LastWorkspace = key
		}
		return nil
	}); err != nil {
		return fmt.Errorf("save local workspace state: %w", err)
	}
	return nil
}

func deleteLocalWorkspaceState(key string) {
	if key == "" {
		return
	}
	if err := bootstrap.MutateStateCache(func(sc *bootstrap.StateCache) error {
		delete(sc.Workspaces, key)
		if sc.LastWorkspace == key {
			sc.LastWorkspace = ""
		}
		return nil
	}); err != nil {
		slog.Warn("failed to rollback local workspace state", "workspace", key, "err", err)
	}
}

func removeLocalRepoState(key, repositoryName string) {
	if key == "" || repositoryName == "" {
		return
	}
	if err := bootstrap.MutateWorkspaceLocalState(
		key,
		func(local *bootstrap.WorkspaceLocalState) error {
			delete(local.Repos, repositoryName)
			return nil
		},
	); err != nil {
		slog.Warn(
			"failed to rollback local repository state",
			"workspace",
			key,
			"repo",
			repositoryName,
			"err",
			err,
		)
	}
}
