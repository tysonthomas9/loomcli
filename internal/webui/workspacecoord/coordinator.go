package workspacecoord

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/app/query/operationalview"
	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	workspacemodule "github.com/tysonthomas9/loomcli/internal/modules/workspace"
	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
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
	Topology             storeadapter.WorkspaceTopologyReader  // Narrow Workspace and Repository read projection.
	Workspace            workspacemodule.API                   // Workspace-owned catalog commands and queries
	CreateFn             WorkspaceCreateFn                     // Synchronous empty-workspace creation.
	AddReposFn           WorkspaceAddReposFn                   // Store-backed repo attachment
	DeleteCleanupFn      func(string) error                    // Machine-local cleanup after a Workspace-owned durable delete.
	AdmissionCoordinator WorkspaceAdmissionCoordinator         // Durable admission seam for async repository mutations.
	AgentDirectory       operationalview.WorkspaceAgentRecords // Canonical Agents/Role read surface; nil fails closed to an empty roster.
}

type workspaceServiceImpl struct {
	topology             storeadapter.WorkspaceTopologyReader
	workspace            workspacemodule.API
	createFn             WorkspaceCreateFn
	addReposFn           WorkspaceAddReposFn
	deleteCleanupFn      func(string) error
	admissionCoordinator WorkspaceAdmissionCoordinator
	agentDirectory       operationalview.WorkspaceAgentRecords
	workspaceCache       *workspaceDataCache
}

// NewWorkspaceService creates a new WorkspaceService from the given config.
func NewWorkspaceService(cfg WorkspaceServiceConfig) WorkspaceService {
	return &workspaceServiceImpl{
		topology:             cfg.Topology,
		workspace:            cfg.Workspace,
		createFn:             cfg.CreateFn,
		addReposFn:           cfg.AddReposFn,
		deleteCleanupFn:      cfg.DeleteCleanupFn,
		admissionCoordinator: cfg.AdmissionCoordinator,
		agentDirectory:       cfg.AgentDirectory,
		workspaceCache:       newWorkspaceDataCache(defaultWorkspaceDataCacheTTL),
	}
}

func (s *workspaceServiceImpl) AddWorkspaceRepos(ctx context.Context, req WorkspaceAddReposRequest) (*operationalview.Workspace, error) {
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
	if s.topology != nil && result.WorkspaceID != "" {
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
	if s.admissionCoordinator == nil {
		return "", ErrUnavailable("async workspace repo attachment not available")
	}
	jobID, err := s.admissionCoordinator.StartAddRepos(ctx, req)
	if err != nil {
		return "", classifyWorkspaceCreateError(err)
	}
	if strings.TrimSpace(jobID) == "" {
		return "", ErrInternal("workspace admission coordinator returned an empty job ID", nil)
	}
	s.invalidateWorkspaceCache()
	return jobID, nil
}

// loadActiveWorkspace returns the active workspace topology from FleetDB.
func (s *workspaceServiceImpl) loadActiveWorkspace(ctx context.Context) (*operationalview.Workspace, error) {
	if s.topology != nil {
		data, err := storeadapter.BuildActiveWorkspaceData(ctx, s.topology)
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
func (s *workspaceServiceImpl) loadWorkspaceByID(ctx context.Context, wsID string) (*operationalview.Workspace, error) {
	if s.topology != nil {
		data, err := s.workspaceCache.get(ctx, wsID, func(ctx context.Context, key string) (*operationalview.Workspace, error) {
			return storeadapter.BuildWorkspaceDataForKey(ctx, s.topology, key)
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

func (s *workspaceServiceImpl) projectCanonicalAgents(ctx context.Context, data *operationalview.Workspace) error {
	return operationalview.NewWorkspaceRosterQuery(s.agentDirectory).Project(ctx, data)
}

func (s *workspaceServiceImpl) GetActiveWorkspace(ctx context.Context) (*operationalview.Workspace, error) {
	if s.topology == nil {
		return &operationalview.Workspace{
			Repos:      []operationalview.Repository{},
			Groups:     []string{},
			Agents:     []operationalview.Agent{},
			Workspaces: []operationalview.Summary{},
		}, nil
	}

	data, err := s.loadActiveWorkspace(ctx)
	if err != nil {
		return nil, ErrInternal("failed to load workspace data", err)
	}
	if data == nil {
		data = &operationalview.Workspace{}
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

func (s *workspaceServiceImpl) GetWorkspace(ctx context.Context, wsID string) (*operationalview.Workspace, error) {
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
func (s *workspaceServiceImpl) lookupWorkspace(ctx context.Context, wsID string) (*operationalview.Workspace, bool, error) {
	if s.topology != nil {
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
		if errors.Is(err, persistence.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, ErrInternal("failed to load workspace data", err)
	}
	return nil, false, nil
}

func (s *workspaceServiceImpl) CreateWorkspace(ctx context.Context, req WorkspaceCreateRequest) (*operationalview.Workspace, []string, error) {
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

	var data *operationalview.Workspace
	if s.topology != nil && result.WorkspaceID != "" {
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

	if s.admissionCoordinator == nil {
		return "", ErrUnavailable("async workspace creation not available")
	}
	jobID, err := s.admissionCoordinator.StartCreate(ctx, req)
	if err != nil {
		return "", classifyWorkspaceCreateError(err)
	}
	if strings.TrimSpace(jobID) == "" {
		return "", ErrInternal("workspace admission coordinator returned an empty job ID", nil)
	}
	return jobID, nil
}

func (s *workspaceServiceImpl) GetWorkspaceJob(ctx context.Context, jobID string) (*WorkspaceJob, error) {
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
	return nil, ErrNotFound("job not found")
}

func (s *workspaceServiceImpl) DeleteWorkspace(ctx context.Context, wsID string) (*operationalview.Workspace, error) {
	if s.topology == nil {
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

	data, err := storeadapter.BuildActiveWorkspaceData(ctx, s.topology)
	if err != nil {
		return nil, ErrInternal("failed to load workspace data", err)
	}
	if data == nil {
		data = &operationalview.Workspace{}
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

func (s *workspaceServiceImpl) resolveStoreWorkspaceForDefault(ctx context.Context, name string) (*workspacemodule.Workspace, *ServiceError) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrValidation("workspace name is required")
	}
	ws, err := s.topology.Workspaces().Get(ctx, name)
	if err == nil {
		return ws, nil
	}
	if !errors.Is(err, persistence.ErrNotFound) && !errors.Is(err, persistence.ErrInvalid) {
		return nil, ErrInternal("failed to load workspace", err)
	}
	ws, err = s.topology.Workspaces().GetByName(ctx, name)
	if err == nil {
		return ws, nil
	}
	if errors.Is(err, persistence.ErrNotFound) {
		return nil, ErrNotFound("workspace not found: " + name)
	}
	return nil, ErrInternal("failed to load workspace", err)
}

func (s *workspaceServiceImpl) GetWorkspaceBackend(ctx context.Context, wsID string) (*BackendConfigData, error) {
	if s.topology == nil {
		return nil, ErrUnavailable("workspace store unavailable")
	}
	ws, serr := s.resolveStoreWorkspaceForDefault(ctx, wsID)
	if serr != nil {
		return nil, serr
	}
	backend := ""
	source := "default"
	backend, err := bootstrap.RuntimeProvider(ws.Key)
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

func (s *workspaceServiceImpl) PatchWorkspaceBackend(ctx context.Context, wsID string, backend string) (*operationalview.Workspace, error) {
	if s.topology == nil {
		return nil, ErrUnavailable("workspace store unavailable")
	}
	ws, serr := s.resolveStoreWorkspaceForDefault(ctx, wsID)
	if serr != nil {
		return nil, serr
	}
	if err := bootstrap.SetRuntimeProvider(ws.Key, backend); err != nil {
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
func normalizeWorkspaceData(data *operationalview.Workspace) {
	if data.Repos == nil {
		data.Repos = []operationalview.Repository{}
	}
	if data.Groups == nil {
		data.Groups = []string{}
	}
	if data.Agents == nil {
		data.Agents = []operationalview.Agent{}
	}
	if data.Workspaces == nil {
		data.Workspaces = []operationalview.Summary{}
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
