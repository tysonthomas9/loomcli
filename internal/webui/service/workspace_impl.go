package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

const (
	workspaceCreateTimeoutEmpty = 10 * time.Second
	workspaceCreateTimeoutClone = 60 * time.Second
)

const defaultWorkspaceBackend = "codex"

// workspaceBackendOptions is the set of backends the Settings UI offers and the
// "Talk to Lead" terminal picks from. Seeded with the known built-ins (+ the
// "shell" pseudo-option) as a fallback, then replaced at serve startup from the
// backend registry via SetWorkspaceBackendOptions so the list tracks every
// registered backend (incl. flue) instead of drifting from a hardcoded set.
var (
	workspaceBackendOptionsMu sync.RWMutex
	workspaceBackendOptions   = []string{"claude", defaultWorkspaceBackend, "opencode", "gemini", "cursor", "shell"}
)

// SetWorkspaceBackendOptions replaces the selectable workspace backend list.
// The serve command calls this with cli.ListBackends() (plus "shell") at startup.
func SetWorkspaceBackendOptions(names []string) {
	cp := append([]string(nil), names...)
	workspaceBackendOptionsMu.Lock()
	workspaceBackendOptions = cp
	workspaceBackendOptionsMu.Unlock()
}

func workspaceBackendOptionsList() []string {
	workspaceBackendOptionsMu.RLock()
	defer workspaceBackendOptionsMu.RUnlock()
	return append([]string(nil), workspaceBackendOptions...)
}

// WorkspaceServiceConfig holds the dependencies for workspace service construction.
type WorkspaceServiceConfig struct {
	Store          store.Store         // FleetDB-backed store; authoritative workspace source
	MultiPool      *daemon.MultiPool   // For daemon-pool stats when local daemons are running
	CreateFn       WorkspaceCreateFn   // Already wrapped with registry hooks
	AddReposFn     WorkspaceAddReposFn // Store-backed repo attachment
	DeleteFn       func(string) error  // Already wrapped with cleanup hooks
	JobStore       JobStore            // For async creation; nil = async unavailable
	SetDefaultFn   func(string) error  // Deprecated compatibility hook; default workspace selection is disabled.
	ClearDefaultFn func() error        // Deprecated compatibility hook; default workspace selection is disabled.
}

type workspaceServiceImpl struct {
	store          store.Store
	multiPool      *daemon.MultiPool
	createFn       WorkspaceCreateFn
	addReposFn     WorkspaceAddReposFn
	deleteFn       func(string) error
	jobStore       JobStore
	setDefaultFn   func(string) error
	clearDefaultFn func() error
	workspaceCache *workspaceDataCache
}

// NewWorkspaceService creates a new WorkspaceService from the given config.
func NewWorkspaceService(cfg WorkspaceServiceConfig) WorkspaceService {
	return &workspaceServiceImpl{
		store:          cfg.Store,
		multiPool:      cfg.MultiPool,
		createFn:       cfg.CreateFn,
		addReposFn:     cfg.AddReposFn,
		deleteFn:       cfg.DeleteFn,
		jobStore:       cfg.JobStore,
		setDefaultFn:   cfg.SetDefaultFn,
		clearDefaultFn: cfg.ClearDefaultFn,
		workspaceCache: newWorkspaceDataCache(defaultWorkspaceDataCacheTTL),
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

// loadActiveWorkspace returns the active workspace topology from FleetDB.
func (s *workspaceServiceImpl) loadActiveWorkspace(ctx context.Context) (*ops.WorkspaceData, error) {
	if s.store != nil {
		return storeadapter.BuildActiveWorkspaceData(ctx, s.store)
	}
	return nil, nil
}

// loadWorkspaceByID returns a specific workspace's topology from FleetDB.
func (s *workspaceServiceImpl) loadWorkspaceByID(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	if s.store != nil {
		return s.workspaceCache.get(ctx, wsID, func(ctx context.Context, key string) (*ops.WorkspaceData, error) {
			return storeadapter.BuildWorkspaceDataForKey(ctx, s.store, key)
		})
	}
	return nil, nil
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
	// Store is authoritative: list FleetDB workspaces directly. The multiPool
	// is consulted only to enrich items with daemon-pool stats.
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
				if s.multiPool != nil {
					if p := s.multiPool.PoolForWorkspace(ws.Key); p != nil {
						ps := poolStatsFromDaemon(p.Stats())
						item.Pool = &ps
					}
				}
				items = append(items, item)
			}
			return items, nil
		}
		return nil, ErrInternal("failed to list workspaces from store", err)
	}

	if s.multiPool != nil {
		ids := s.multiPool.WorkspaceIDs()
		items := make([]WorkspaceListItem, 0, len(ids))
		for _, id := range ids {
			item := WorkspaceListItem{
				ID:   id,
				Name: id,
			}
			if p := s.multiPool.PoolForWorkspace(id); p != nil {
				ps := poolStatsFromDaemon(p.Stats())
				item.Pool = &ps
			}
			items = append(items, item)
		}
		return items, nil
	}

	return []WorkspaceListItem{}, nil
}

func (s *workspaceServiceImpl) GetWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	// Primary existence check: the multiPool registry. In daemon-backed mode the
	// pool owns the authoritative list of live workspaces. In fleet mode the
	// multiPool is intentionally empty (no local issue daemon), so the lookup
	// path below resolves against the store/config data source.
	poolKnown := s.multiPool != nil && s.multiPool.PoolForWorkspace(wsID) != nil
	if !poolKnown {
		if data, ok, err := s.lookupWorkspace(ctx, wsID); err != nil {
			return nil, err
		} else if ok {
			return data, nil
		}
		return nil, ErrNotFound("workspace not found: " + wsID)
	}

	data, err := s.loadWorkspaceByID(ctx, wsID)
	if err != nil || data == nil {
		return nil, ErrInternal("failed to load workspace data", err)
	}

	normalizeWorkspaceData(data)
	for i := range data.Repos {
		if b := readGitHeadBranch(data.Repos[i].Path); b != "" {
			data.Repos[i].CurrentBranch = b
		}
	}
	for i := range data.Workspaces {
		data.Workspaces[i].Active = data.Workspaces[i].ID == wsID
	}
	return data, nil
}

// lookupWorkspace resolves a workspace UUID via the store. Returns
// (data, true, nil) when a match is found, (nil, false, nil) when the ID is
// unknown, or (nil, false, err) on a load error.
func (s *workspaceServiceImpl) lookupWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, bool, error) {
	if s.store != nil {
		data, err := s.loadWorkspaceByID(ctx, wsID)
		if err == nil && data != nil {
			normalizeWorkspaceData(data)
			for i := range data.Repos {
				if b := readGitHeadBranch(data.Repos[i].Path); b != "" {
					data.Repos[i].CurrentBranch = b
				}
			}
			for i := range data.Workspaces {
				data.Workspaces[i].Active = data.Workspaces[i].ID == wsID
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

func (s *workspaceServiceImpl) StartAsyncCreate(_ context.Context, req WorkspaceCreateRequest) (string, error) {
	if err := validateWorkspaceCreateRequest(&req); err != nil {
		return "", err
	}

	if s.jobStore == nil {
		return "", ErrUnavailable("async workspace creation not available")
	}

	jobID := s.jobStore.Start(req, s.createFn)
	return jobID, nil
}

func (s *workspaceServiceImpl) GetWorkspaceJob(ctx context.Context, jobID string) (*WorkspaceJob, error) {
	if s.jobStore != nil {
		if job := s.jobStore.Get(jobID); job != nil {
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
	if s.deleteFn == nil {
		return nil, ErrUnavailable("workspace deletion not available")
	}
	if s.store == nil {
		return nil, ErrUnavailable("workspace store unavailable")
	}

	key := wsID
	if _, err := s.store.Workspaces().Get(ctx, key); err != nil {
		if ws, byNameErr := s.store.Workspaces().GetByName(ctx, wsID); byNameErr == nil && ws != nil {
			key = ws.Key
		} else {
			return nil, ErrNotFound(fmt.Sprintf("workspace with ID %q not found", wsID))
		}
	}
	if err := s.deleteFn(key); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") {
			return nil, ErrNotFound(errMsg)
		}
		if strings.Contains(errMsg, "has running agents") {
			return nil, ErrConflict(errMsg)
		}
		return nil, ErrInternal(errMsg, err)
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

func (s *workspaceServiceImpl) RenameWorkspace(ctx context.Context, wsID string, newName string) (*ops.WorkspaceData, error) {
	if err := validateWorkspaceName(newName); err != nil {
		return nil, err
	}
	if s.store == nil {
		return nil, ErrUnavailable("workspace store unavailable")
	}
	ws, serr := s.resolveStoreWorkspaceForDefault(ctx, wsID)
	if serr != nil {
		return nil, serr
	}
	if ws.Name == newName {
		data, err := s.loadWorkspaceByID(ctx, ws.Key)
		if err != nil {
			return nil, ErrInternal("failed to load workspace data", err)
		}
		normalizeWorkspaceData(data)
		return data, nil
	}
	if existing, err := s.store.Workspaces().GetByName(ctx, newName); err == nil && existing.Key != ws.Key {
		return nil, ErrConflict("workspace name already exists")
	}
	updated, err := s.store.Workspaces().Update(ctx, ws.Key, store.WorkspaceUpdate{Name: &newName})
	if err != nil {
		return nil, ErrInternal("failed to rename workspace", err)
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
	profile, err := s.store.Daemon().Get(ctx, ws.Key)
	if err != nil {
		return nil, ErrInternal("failed to load workspace backend config", err)
	}

	backend := ""
	source := "default"
	if profile != nil && strings.TrimSpace(profile.AgentBackend) != "" {
		backend = strings.TrimSpace(profile.AgentBackend)
		source = "fleetdb"
	}
	if backend == "" {
		backend = defaultWorkspaceBackend
	}

	agents, err := s.store.Agents().List(ctx, ws.Key)
	if err != nil {
		return nil, ErrInternal("failed to load agent backend overrides", err)
	}
	overrides := make([]AgentBackendOverride, 0, len(agents))
	for _, agent := range agents {
		overrides = append(overrides, AgentBackendOverride{
			Worktree: agent.Name,
			Role:     agent.RoleName,
			Backend:  agent.Backend,
		})
	}

	available := workspaceBackendOptionsList()
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
	profile, err := s.store.Daemon().Get(ctx, ws.Key)
	if err != nil {
		return nil, ErrInternal("failed to load workspace backend config", err)
	}
	if profile == nil {
		profile = &domain.DaemonProfile{WorkspaceKey: ws.Key}
	}
	profile.WorkspaceKey = ws.Key
	profile.AgentBackend = backend
	if _, err := s.store.Daemon().Upsert(ctx, profile); err != nil {
		return nil, ErrInternal("failed to save workspace backend config", err)
	}
	s.invalidateWorkspaceCache()
	data, err := s.loadWorkspaceByID(ctx, ws.Key)
	if err != nil {
		return nil, ErrInternal("failed to load workspace data", err)
	}
	normalizeWorkspaceData(data)
	return data, nil
}

func (s *workspaceServiceImpl) invalidateWorkspaceCache() {
	if s.workspaceCache != nil {
		s.workspaceCache.invalidateAll()
	}
}

func poolStatsFromDaemon(d daemon.PoolStats) PoolStats {
	return PoolStats{
		Size:      d.Size,
		Created:   d.Created,
		Active:    d.Active,
		Available: d.Available,
		Closed:    d.Closed,
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
