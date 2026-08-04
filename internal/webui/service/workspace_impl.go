package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/localnodeconfig"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

const (
	workspaceCreateTimeoutEmpty = 10 * time.Second
	workspaceCreateTimeoutClone = 60 * time.Second
)

const defaultWorkspaceBackend = "codex"

var workspaceBackendOptions = []string{"claude", defaultWorkspaceBackend, "opencode", "gemini", "cursor", "shell"}

// WorkspaceServiceConfig holds the dependencies for workspace service construction.
type WorkspaceServiceConfig struct {
	Store                store.Store                   // FleetDB-backed store; authoritative workspace source
	Workspace            workspacemodule.API           // Workspace-owned catalog commands and queries
	CreateFn             WorkspaceCreateFn             // Already wrapped with registry hooks
	AddReposFn           WorkspaceAddReposFn           // Store-backed repo attachment
	DeleteCleanupFn      func(string) error            // Machine-local cleanup after a Workspace-owned durable delete.
	JobStore             JobStore                      // For async creation; nil = async unavailable
	AdmissionCoordinator WorkspaceAdmissionCoordinator // Optional durable admission seam for async mutations
	SetDefaultFn         func(string) error            // Deprecated compatibility hook; default workspace selection is disabled.
	ClearDefaultFn       func() error                  // Deprecated compatibility hook; default workspace selection is disabled.
	AgentDirectory       WorkspaceAgentDirectory       // Canonical Agents/Role read surface; nil fails closed to an empty roster.
}

// WorkspaceAgentDirectory is the canonical read surface needed to project
// repository-scoped Agents into the workspace shell. The workspace cache never
// stores this projection, so an Agent create/archive is visible on the next
// read without a cache invalidation side channel.
type WorkspaceAgentDirectory interface {
	ListAgents(context.Context, string, agentsmodule.AgentFilter) ([]*agentsmodule.Agent, error)
	ListRoles(context.Context, string) ([]*agentsmodule.Role, error)
}

type workspaceServiceImpl struct {
	store                store.Store
	workspace            workspacemodule.API
	createFn             WorkspaceCreateFn
	addReposFn           WorkspaceAddReposFn
	deleteCleanupFn      func(string) error
	jobStore             JobStore
	admissionCoordinator WorkspaceAdmissionCoordinator
	setDefaultFn         func(string) error
	clearDefaultFn       func() error
	agentDirectory       WorkspaceAgentDirectory
	workspaceCache       *workspaceDataCache
}

// NewWorkspaceService creates a new WorkspaceService from the given config.
func NewWorkspaceService(cfg WorkspaceServiceConfig) WorkspaceService {
	return &workspaceServiceImpl{
		store:                cfg.Store,
		workspace:            cfg.Workspace,
		createFn:             cfg.CreateFn,
		addReposFn:           cfg.AddReposFn,
		deleteCleanupFn:      cfg.DeleteCleanupFn,
		jobStore:             cfg.JobStore,
		admissionCoordinator: cfg.AdmissionCoordinator,
		setDefaultFn:         cfg.SetDefaultFn,
		clearDefaultFn:       cfg.ClearDefaultFn,
		agentDirectory:       cfg.AgentDirectory,
		workspaceCache:       newWorkspaceDataCache(defaultWorkspaceDataCacheTTL),
	}
}

func (s *workspaceServiceImpl) AddWorkspaceRepos(ctx context.Context, req WorkspaceAddReposRequest) (*ops.WorkspaceData, error) {
	if s.addReposFn == nil {
		return nil, ErrUnavailable("workspace repo attachment is not available")
	}
	req = normalizeWorkspaceAddReposRequest(req)
	if err := validateWorkspaceAddReposRequest(&req); err != nil {
		return nil, err
	}

	timeout := workspaceCreateTimeoutEmpty
	if len(req.CloneURLs) > 0 {
		timeout = workspaceCreateTimeoutClone
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	result, err := s.addReposFn(ctx, req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrTimeout("repo attachment timed out")
		}
		slog.Warn("workspace repo attachment failed", "workspace", req.WorkspaceID, "err", err)
		return nil, classifyWorkspaceCreateError(err)
	}
	if s.store != nil && result.WorkspaceID != "" {
		s.invalidateWorkspaceCache()
		data, buildErr := s.loadWorkspaceByID(ctx, result.WorkspaceID)
		if buildErr != nil {
			return nil, ErrInternal("failed to load workspace data", buildErr)
		}
		normalizeWorkspaceData(data)
		return data, nil
	}
	return s.GetWorkspace(ctx, req.WorkspaceID)
}

func (s *workspaceServiceImpl) StartAsyncAddRepos(ctx context.Context, req WorkspaceAddReposRequest) (string, error) {
	if s.addReposFn == nil {
		return "", ErrUnavailable("workspace repo attachment is not available")
	}
	req = normalizeWorkspaceAddReposRequest(req)
	if err := validateWorkspaceAddReposRequest(&req); err != nil {
		return "", err
	}
	if len(req.CloneURLs) == 0 {
		return "", ErrValidation("async repo attachment requires at least one clone URL")
	}
	if s.jobStore == nil {
		return "", ErrUnavailable("async workspace repo attachment not available")
	}

	run := func(ctx context.Context, normalized WorkspaceAddReposRequest) (WorkspaceCreateResult, error) {
		result, err := s.addReposFn(ctx, normalized)
		if err == nil {
			s.invalidateWorkspaceCache()
		}
		return result, err
	}
	var jobID string
	if s.admissionCoordinator != nil {
		var err error
		jobID, err = s.admissionCoordinator.PrepareAddRepos(ctx, req)
		if err != nil {
			return "", classifyWorkspaceCreateError(err)
		}
		if strings.TrimSpace(jobID) == "" {
			return "", ErrInternal("workspace admission coordinator returned an empty job ID", nil)
		}
		jobID = s.jobStore.StartPreparedAddRepos(jobID, req, run)
	} else {
		jobID = s.jobStore.StartAddRepos(req, run)
	}
	return jobID, nil
}

// loadActiveWorkspace returns the active workspace topology from FleetDB.
func (s *workspaceServiceImpl) loadActiveWorkspace(ctx context.Context) (*ops.WorkspaceData, error) {
	if s.store != nil {
		data, err := storeadapter.BuildActiveWorkspaceData(ctx, s.store)
		if err != nil || data == nil {
			return data, err
		}
		if err := s.projectCanonicalAgents(ctx, data); err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, nil
}

// loadWorkspaceByID returns a specific workspace's topology from FleetDB.
func (s *workspaceServiceImpl) loadWorkspaceByID(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	if s.store != nil {
		data, err := s.workspaceCache.get(ctx, wsID, func(ctx context.Context, key string) (*ops.WorkspaceData, error) {
			return storeadapter.BuildWorkspaceDataForKey(ctx, s.store, key)
		})
		if err != nil || data == nil {
			return data, err
		}
		if err := s.projectCanonicalAgents(ctx, data); err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, nil
}

func (s *workspaceServiceImpl) projectCanonicalAgents(ctx context.Context, data *ops.WorkspaceData) error {
	data.Agents = []ops.WorkspaceAgentInfo{}
	if s.agentDirectory == nil {
		return nil
	}
	agents, err := s.agentDirectory.ListAgents(ctx, data.ID, agentsmodule.AgentFilter{})
	if err != nil {
		return fmt.Errorf("list canonical workspace agents: %w", err)
	}
	roles, err := s.agentDirectory.ListRoles(ctx, data.ID)
	if err != nil {
		return fmt.Errorf("list canonical workspace roles: %w", err)
	}
	rolesByName := make(map[string]*agentsmodule.Role, len(roles))
	for _, role := range roles {
		if role == nil || role.WorkspaceKey != data.ID || role.Name == "" {
			return fmt.Errorf("invalid canonical workspace Role: %w", agentsmodule.ErrInvalidPersistedState)
		}
		rolesByName[role.Name] = role
	}
	for _, agent := range agents {
		if agent == nil || agent.WorkspaceKey != data.ID || agent.AgentID == "" {
			return fmt.Errorf("invalid canonical workspace Agent: %w", agentsmodule.ErrInvalidPersistedState)
		}
		if agent.Behavior.RoleName == "" {
			continue
		}
		role := rolesByName[agent.Behavior.RoleName]
		if role == nil {
			return fmt.Errorf("canonical Agent %q references missing Role %q: %w", agent.AgentID, agent.Behavior.RoleName, agentsmodule.ErrInvalidPersistedState)
		}
		runtime, parseErr := agentsmodule.ParseRuntimeMetadata(agent.Metadata)
		if parseErr != nil {
			return fmt.Errorf("canonical Agent %q runtime metadata: %w", agent.AgentID, parseErr)
		}
		roleKind := runtime.RoleKind
		if roleKind == "" {
			roleKind = role.Kind
		}
		backend := runtime.Backend
		if backend == "" {
			backend = role.Backend
		}
		data.Agents = append(data.Agents, ops.WorkspaceAgentInfo{
			Name: agent.AgentID, Kind: roleKind, RoleName: agent.Behavior.RoleName,
			Backend: backend, Repos: append([]string{}, runtime.Repos...),
			RepoGroups: append([]string{}, runtime.RepoGroups...), CrossRepo: runtime.CrossRepo,
		})
	}
	sort.Slice(data.Agents, func(i, j int) bool { return data.Agents[i].Name < data.Agents[j].Name })
	return nil
}

func (s *workspaceServiceImpl) GetActiveWorkspace(ctx context.Context) (*ops.WorkspaceData, error) {
	if s.store == nil {
		return &ops.WorkspaceData{
			Repos:      []ops.WorkspaceRepo{},
			Groups:     []string{},
			Agents:     []ops.WorkspaceAgentInfo{},
			Workspaces: []ops.WorkspaceSummary{},
		}, nil
	}

	data, err := s.loadActiveWorkspace(ctx)
	if err != nil {
		return nil, ErrInternal("failed to load workspace data", err)
	}
	if data == nil {
		data = &ops.WorkspaceData{}
	}
	normalizeWorkspaceData(data)
	for i := range data.Repos {
		if b := readGitHeadBranch(data.Repos[i].Path); b != "" {
			data.Repos[i].CurrentBranch = b
		}
	}
	return data, nil
}

// readGitHeadBranch reads the current branch from .git/HEAD.
// Returns empty string on error or detached HEAD.
func readGitHeadBranch(repoPath string) string {
	data, err := os.ReadFile(filepath.Join(repoPath, ".git", "HEAD")) //nolint:gosec // repoPath comes from workspace config, not user input
	if err != nil {
		return ""
	}
	after, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "ref: refs/heads/")
	if !ok {
		return ""
	}
	return after
}

//nolint:gocognit,cyclop,funlen // Aggregates workspace, repo, agent, and local-state views for the UI.
func (s *workspaceServiceImpl) ListWorkspaces(ctx context.Context) ([]WorkspaceListItem, error) {
	if s.workspace != nil {
		values, err := s.workspace.List(ctx, workspacemodule.ListQuery{})
		if err != nil {
			return nil, classifyWorkspaceCapabilityError("failed to list workspaces", err)
		}
		activeKey := ""
		if s.store != nil {
			activeKey, _ = bootstrap.ResolveActiveWorkspaceKey(ctx, s.store.Workspaces())
		}
		items := make([]WorkspaceListItem, 0, len(values))
		for _, value := range values {
			items = append(items, WorkspaceListItem{
				ID: value.Key, Name: value.Name, Path: storeadapter.ResolveWorkspacePath(value.Key),
				Active: value.Key == activeKey, IsDefault: false,
			})
		}
		return items, nil
	}
	// Store is authoritative for workspace discovery.
	if s.store != nil {
		wsList, err := s.store.Workspaces().List(ctx)
		if err == nil {
			activeKey, _ := bootstrap.ResolveActiveWorkspaceKey(ctx, s.store.Workspaces())
			items := make([]WorkspaceListItem, 0, len(wsList))
			for _, ws := range wsList {
				item := WorkspaceListItem{
					ID:        ws.Key,
					Name:      ws.Name,
					Path:      storeadapter.ResolveWorkspacePath(ws.Key),
					Active:    ws.Key == activeKey,
					IsDefault: false,
				}
				items = append(items, item)
			}
			return items, nil
		}
		return nil, ErrInternal("failed to list workspaces from store", err)
	}

	return []WorkspaceListItem{}, nil
}

func (s *workspaceServiceImpl) GetWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	if data, ok, err := s.lookupWorkspace(ctx, wsID); err != nil {
		return nil, err
	} else if ok {
		return data, nil
	}
	return nil, ErrNotFound("workspace not found: " + wsID)
}

// lookupWorkspace resolves a workspace UUID via the store. Returns
// (data, true, nil) when a match is found, (nil, false, nil) when the ID is
// unknown, or (nil, false, err) on a load error.
func (s *workspaceServiceImpl) lookupWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, bool, error) {
	if s.store != nil {
		// FleetDB workspace keys are canonical uppercase identifiers. Routes may
		// still receive a display name immediately after creation or from a
		// hand-authored URL, so never pass that unvalidated value into a storage
		// key builder.
		key := WorkspaceKeyFromName(wsID)
		if s.workspace != nil {
			resolved, err := s.workspace.Resolve(ctx, workspacemodule.ResolveQuery{Reference: wsID})
			if errors.Is(err, workspacemodule.ErrNotFound) {
				return nil, false, nil
			}
			if err != nil {
				return nil, false, classifyWorkspaceCapabilityError("failed to resolve workspace", err)
			}
			key = resolved.Key
		}
		data, err := s.loadWorkspaceByID(ctx, key)
		if err == nil && data != nil {
			normalizeWorkspaceData(data)
			for i := range data.Repos {
				if b := readGitHeadBranch(data.Repos[i].Path); b != "" {
					data.Repos[i].CurrentBranch = b
				}
			}
			for i := range data.Workspaces {
				data.Workspaces[i].Active = data.Workspaces[i].ID == key
			}
			return data, true, nil
		}
		if errors.Is(err, domain.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, ErrInternal("failed to load workspace data", err)
	}
	return nil, false, nil
}

func (s *workspaceServiceImpl) CreateWorkspace(ctx context.Context, req WorkspaceCreateRequest) (*ops.WorkspaceData, []string, error) {
	if s.createFn == nil {
		return nil, nil, ErrUnavailable("workspace creation is not available")
	}

	if err := validateWorkspaceCreateRequest(&req); err != nil {
		return nil, nil, err
	}

	timeout := workspaceCreateTimeoutClone
	if req.Type == "empty" {
		timeout = workspaceCreateTimeoutEmpty
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ctx = WithCreateWarnings(ctx)

	result, err := s.createFn(ctx, req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, nil, ErrTimeout("workspace creation timed out")
		}
		slog.Warn("workspace creation failed", "name", req.Name, "type", req.Type, "err", err)
		return nil, nil, classifyWorkspaceCreateError(err)
	}

	var data *ops.WorkspaceData
	if s.store != nil && result.WorkspaceID != "" {
		s.invalidateWorkspaceCache()
		d, buildErr := s.loadWorkspaceByID(ctx, result.WorkspaceID)
		if buildErr != nil {
			return nil, nil, ErrInternal("failed to load created workspace data", buildErr)
		}
		normalizeWorkspaceData(d)
		data = d
	}
	warnings := GetCreateWarnings(ctx)
	return data, warnings, nil
}

func (s *workspaceServiceImpl) StartAsyncCreate(ctx context.Context, req WorkspaceCreateRequest) (string, error) {
	if err := validateWorkspaceCreateRequest(&req); err != nil {
		return "", err
	}

	if s.jobStore == nil {
		return "", ErrUnavailable("async workspace creation not available")
	}

	var jobID string
	if s.admissionCoordinator != nil {
		var err error
		jobID, err = s.admissionCoordinator.PrepareCreate(ctx, req)
		if err != nil {
			return "", classifyWorkspaceCreateError(err)
		}
		if strings.TrimSpace(jobID) == "" {
			return "", ErrInternal("workspace admission coordinator returned an empty job ID", nil)
		}
		jobID = s.jobStore.StartPrepared(jobID, req, s.createFn)
	} else {
		jobID = s.jobStore.Start(req, s.createFn)
	}
	return jobID, nil
}

func (s *workspaceServiceImpl) GetWorkspaceJob(ctx context.Context, jobID string) (*WorkspaceJob, error) {
	if s.jobStore != nil {
		if job := s.jobStore.Get(jobID); job != nil {
			return job, nil
		}
	}
	if s.admissionCoordinator != nil {
		job, found, err := s.admissionCoordinator.LookupJob(ctx, jobID)
		if err != nil {
			return nil, err
		}
		if found {
			if job == nil {
				return nil, ErrInternal("workspace admission coordinator returned an empty job", nil)
			}
			return job, nil
		}
	}
	if s.store != nil {
		return s.workspaceJobFromStore(ctx, jobID)
	}
	return nil, ErrNotFound("job not found")
}

func (s *workspaceServiceImpl) workspaceJobFromStore(ctx context.Context, key string) (*WorkspaceJob, error) {
	ws, err := s.store.Workspaces().Get(ctx, key)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrNotFound("job not found")
		}
		return nil, ErrInternal("failed to load workspace job", err)
	}
	job := &WorkspaceJob{ID: key, WorkspaceID: key}
	switch ws.State {
	case domain.WorkspaceStateCreating:
		job.Status = JobStatusRunning
		job.Progress = "creating workspace..."
	case domain.WorkspaceStateCloning:
		job.Status = JobStatusRunning
		job.Progress = "cloning repository..."
	case domain.WorkspaceStateInitializing:
		job.Status = JobStatusRunning
		job.Progress = "initializing workspace..."
	case domain.WorkspaceStateError:
		job.Status = JobStatusFailed
		job.Error = ws.ErrorMessage
		if job.Error == "" {
			job.Error = "workspace creation failed"
		}
		job.CompletedAt = ws.UpdatedAt
	default:
		job.Status = JobStatusDone
		job.CompletedAt = ws.UpdatedAt
	}
	return job, nil
}

func (s *workspaceServiceImpl) DeleteWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	if s.store == nil {
		return nil, ErrUnavailable("workspace store unavailable")
	}

	key, deleteErr := s.deleteDurableWorkspace(ctx, wsID)
	if deleteErr != nil {
		return nil, deleteErr
	}
	if s.deleteCleanupFn != nil {
		if err := s.deleteCleanupFn(key); err != nil {
			slog.Warn("workspace deleted but machine-local cleanup failed", "workspace", key, "err", err)
		}
	}
	s.invalidateWorkspaceCache()

	data, err := storeadapter.BuildActiveWorkspaceData(ctx, s.store)
	if err != nil {
		return nil, ErrInternal("failed to load workspace data", err)
	}
	if data == nil {
		data = &ops.WorkspaceData{}
	}
	normalizeWorkspaceData(data)
	return data, nil
}

func (s *workspaceServiceImpl) deleteDurableWorkspace(ctx context.Context, reference string) (string, *ServiceError) {
	if s.workspace == nil {
		return "", ErrUnavailable("Workspace capability unavailable")
	}
	deleted, err := s.workspace.Delete(ctx, workspacemodule.DeleteCommand{Reference: reference})
	if err != nil {
		return "", classifyWorkspaceCapabilityError("failed to delete workspace", err)
	}
	if deleted == nil || strings.TrimSpace(deleted.Key) == "" {
		return "", ErrInternal("Workspace delete returned an invalid reference", nil)
	}
	return deleted.Key, nil
}

func (s *workspaceServiceImpl) RenameWorkspace(ctx context.Context, wsID string, newName string) (*ops.WorkspaceData, error) {
	if s.workspace == nil {
		return nil, ErrUnavailable("Workspace capability unavailable")
	}
	updated, err := s.workspace.Rename(ctx, workspacemodule.RenameCommand{Reference: wsID, Name: newName})
	if err != nil {
		return nil, classifyWorkspaceCapabilityError("failed to rename workspace", err)
	}
	s.invalidateWorkspaceCache()
	data, err := s.loadWorkspaceByID(ctx, updated.Key)
	if err != nil {
		return nil, ErrInternal("failed to load workspace data", err)
	}
	normalizeWorkspaceData(data)
	return data, nil
}

func (s *workspaceServiceImpl) ReorderWorkspaces(_ context.Context, order []string) (*ops.WorkspaceData, error) {
	_ = order
	return nil, ErrNotImplemented("workspace ordering is not implemented in FleetDB")
}

func (s *workspaceServiceImpl) SetDefaultWorkspace(ctx context.Context, name string) (*ops.WorkspaceData, error) {
	_ = ctx
	_ = name
	return nil, ErrUnavailable("default workspace selection has been removed; use explicit workspace routes, --workspace, or LOOM_WORKSPACE")
}

func (s *workspaceServiceImpl) ClearDefaultWorkspace(ctx context.Context) (*ops.WorkspaceData, error) {
	_ = ctx
	return nil, ErrUnavailable("default workspace selection has been removed; use explicit workspace routes, --workspace, or LOOM_WORKSPACE")
}

func (s *workspaceServiceImpl) resolveStoreWorkspaceForDefault(ctx context.Context, name string) (*domain.Workspace, *ServiceError) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrValidation("workspace name is required")
	}
	ws, err := s.store.Workspaces().Get(ctx, name)
	if err == nil {
		return ws, nil
	}
	if !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, domain.ErrInvalid) {
		return nil, ErrInternal("failed to load workspace", err)
	}
	ws, err = s.store.Workspaces().GetByName(ctx, name)
	if err == nil {
		return ws, nil
	}
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrNotFound("workspace not found: " + name)
	}
	return nil, ErrInternal("failed to load workspace", err)
}

func (s *workspaceServiceImpl) GetWorkspaceBackend(ctx context.Context, wsID string) (*BackendConfigData, error) {
	if s.store == nil {
		return nil, ErrUnavailable("workspace store unavailable")
	}
	ws, serr := s.resolveStoreWorkspaceForDefault(ctx, wsID)
	if serr != nil {
		return nil, serr
	}
	backend := ""
	source := "default"
	backend, err := localnodeconfig.RuntimeProvider(ws.Key)
	if err != nil {
		return nil, ErrInternal("failed to load local node backend config", err)
	}
	if backend != "" {
		source = "local_node"
	}
	if backend == "" {
		backend = defaultWorkspaceBackend
	}

	overrides := []AgentBackendOverride{}
	if s.agentDirectory != nil {
		agents, err := s.agentDirectory.ListAgents(ctx, ws.Key, agentsmodule.AgentFilter{})
		if err != nil {
			return nil, ErrInternal("failed to load canonical agent backend overrides", err)
		}
		overrides = make([]AgentBackendOverride, 0, len(agents))
		for _, agent := range agents {
			if agent == nil {
				continue
			}
			runtime, err := agentsmodule.ParseRuntimeMetadata(agent.Metadata)
			if err != nil {
				return nil, ErrInternal("failed to load canonical agent runtime metadata", err)
			}
			overrides = append(overrides, AgentBackendOverride{
				Worktree: agent.AgentID,
				Role:     agent.Behavior.RoleName,
				Backend:  runtime.Backend,
			})
		}
	}

	available := append([]string(nil), workspaceBackendOptions...)
	return &BackendConfigData{
		Backend:   backend,
		Source:    source,
		Available: available,
		Agents:    overrides,
	}, nil
}

func (s *workspaceServiceImpl) PatchWorkspaceBackend(ctx context.Context, wsID string, backend string) (*ops.WorkspaceData, error) {
	if s.store == nil {
		return nil, ErrUnavailable("workspace store unavailable")
	}
	ws, serr := s.resolveStoreWorkspaceForDefault(ctx, wsID)
	if serr != nil {
		return nil, serr
	}
	if err := localnodeconfig.SetRuntimeProvider(ws.Key, backend); err != nil {
		return nil, ErrInternal("failed to save local node backend config", err)
	}
	s.invalidateWorkspaceCache()
	data, err := s.loadWorkspaceByID(ctx, ws.Key)
	if err != nil {
		return nil, ErrInternal("failed to load workspace data", err)
	}
	normalizeWorkspaceData(data)
	return data, nil
}

func (s *workspaceServiceImpl) PatchWorkspaceDesignFormat(ctx context.Context, wsID string, designFormat string) (*ops.WorkspaceData, error) {
	if s.workspace == nil {
		return nil, ErrUnavailable("Workspace capability unavailable")
	}
	updated, err := s.workspace.SetDesignFormat(ctx, workspacemodule.SetDesignFormatCommand{
		Reference: wsID,
		Format:    designFormat,
	})
	if err != nil {
		return nil, classifyWorkspaceCapabilityError("failed to save workspace design format", err)
	}
	s.invalidateWorkspaceCache()
	data, err := s.loadWorkspaceByID(ctx, updated.Key)
	if err != nil {
		return nil, ErrInternal("failed to load workspace data", err)
	}
	normalizeWorkspaceData(data)
	return data, nil
}

func classifyWorkspaceCapabilityError(message string, err error) *ServiceError {
	switch {
	case errors.Is(err, workspacemodule.ErrNotFound):
		return ErrNotFound(err.Error())
	case errors.Is(err, workspacemodule.ErrInvalid):
		return ErrValidation(err.Error())
	case errors.Is(err, workspacemodule.ErrConflict):
		return ErrConflict(err.Error())
	case errors.Is(err, workspacemodule.ErrUnavailable):
		return ErrUnavailable(err.Error())
	default:
		return ErrInternal(message, err)
	}
}

func (s *workspaceServiceImpl) invalidateWorkspaceCache() {
	if s.workspaceCache != nil {
		s.workspaceCache.invalidateAll()
	}
}

// normalizeWorkspaceData ensures all slice fields are non-nil so JSON marshals as [] not null.
func normalizeWorkspaceData(data *ops.WorkspaceData) {
	if data.Repos == nil {
		data.Repos = []ops.WorkspaceRepo{}
	}
	if data.Groups == nil {
		data.Groups = []string{}
	}
	if data.Agents == nil {
		data.Agents = []ops.WorkspaceAgentInfo{}
	}
	if data.Workspaces == nil {
		data.Workspaces = []ops.WorkspaceSummary{}
	}
	for i := range data.Repos {
		if data.Repos[i].Groups == nil {
			data.Repos[i].Groups = []string{}
		}
	}
	for i := range data.Agents {
		if data.Agents[i].Repos == nil {
			data.Agents[i].Repos = []string{}
		}
		if data.Agents[i].RepoGroups == nil {
			data.Agents[i].RepoGroups = []string{}
		}
	}
}
