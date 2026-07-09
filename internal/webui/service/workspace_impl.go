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
	"github.com/tysonthomas9/loomcli/internal/localsettings"
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

var workspaceBackendOptions = []string{"claude", defaultWorkspaceBackend, "opencode", "gemini", "cursor", "shell"}

// WorkspaceServiceConfig holds the dependencies for workspace service construction.
type WorkspaceServiceConfig struct {
	Store      store.Store         // FleetDB-backed store; authoritative workspace source
	MultiPool  *daemon.MultiPool   // For daemon-pool stats when local daemons are running
	CreateFn   WorkspaceCreateFn   // Already wrapped with registry hooks
	AddReposFn WorkspaceAddReposFn // Store-backed repo attachment
	DeleteFn   func(string) error  // Already wrapped with cleanup hooks
	JobStore   JobStore            // For async creation; nil = async unavailable

	LocalSettingsDir string // Desktop-local settings dir for per-user UI preferences
}

type workspaceServiceImpl struct {
	store          store.Store
	multiPool      *daemon.MultiPool
	createFn       WorkspaceCreateFn
	addReposFn     WorkspaceAddReposFn
	deleteFn       func(string) error
	jobStore       JobStore
	localSettings  string
	workspaceCache *workspaceDataCache
	orderCacheMu   sync.RWMutex
	orderCache     []string
	orderCacheOK   bool
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
		localSettings:  cfg.LocalSettingsDir,
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
		s.applyWorkspaceOrderToData(data)
		return data, nil
	}
	return s.GetWorkspace(ctx, req.WorkspaceID)
}

func (s *workspaceServiceImpl) RemoveWorkspaceRepo(ctx context.Context, req WorkspaceRemoveRepoRequest) (*ops.WorkspaceData, error) {
	if s.store == nil {
		return nil, ErrUnavailable("workspace store unavailable")
	}
	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	req.RepoName = strings.TrimSpace(req.RepoName)
	if req.WorkspaceID == "" {
		return nil, ErrValidation("workspace ID is required")
	}
	if req.RepoName == "" {
		return nil, ErrValidation("repo name is required")
	}

	ws, serr := s.resolveStoreWorkspaceForDefault(ctx, req.WorkspaceID)
	if serr != nil {
		return nil, serr
	}
	if _, err := s.store.Repos().Get(ctx, ws.Key, req.RepoName); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrNotFound(fmt.Sprintf("repo %q not found in workspace %q", req.RepoName, ws.Key))
		}
		return nil, ErrInternal("failed to load workspace repo", err)
	}
	if err := s.store.Repos().Delete(ctx, ws.Key, req.RepoName); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, ErrNotFound(fmt.Sprintf("repo %q not found in workspace %q", req.RepoName, ws.Key))
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "http 403") || strings.Contains(msg, "forbidden") || strings.Contains(msg, "permission denied") {
			return nil, ErrForbidden("repo removal is not permitted")
		}
		return nil, ErrInternal("failed to remove workspace repo", err)
	}
	s.invalidateWorkspaceCache()

	data, err := s.loadWorkspaceByID(ctx, ws.Key)
	if err != nil {
		return nil, ErrInternal("failed to load workspace data", err)
	}
	normalizeWorkspaceData(data)
	s.applyWorkspaceOrderToData(data)
	return data, nil
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
	s.applyWorkspaceOrderToData(data)
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
			s.applyWorkspaceOrderToList(items)
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
	s.applyWorkspaceOrderToData(data)
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
			s.applyWorkspaceOrderToData(data)
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
		s.applyWorkspaceOrderToData(d)
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
	s.applyWorkspaceOrderToData(data)
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
		s.applyWorkspaceOrderToData(data)
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
	s.applyWorkspaceOrderToData(data)
	return data, nil
}

func (s *workspaceServiceImpl) ReorderWorkspaces(ctx context.Context, order []string) (*ops.WorkspaceData, error) {
	if strings.TrimSpace(s.localSettings) == "" {
		return nil, ErrUnavailable("local settings are not configured")
	}
	if s.store == nil {
		return nil, ErrUnavailable("workspace store unavailable")
	}
	order = normalizeWorkspaceOrder(order)

	settings, err := localsettings.Update(s.localSettings, func(settings *localsettings.Settings) error {
		settings.UIPreferences.WorkspaceOrder = order
		return nil
	})
	if err != nil {
		return nil, ErrInternal("failed to save workspace order", err)
	}
	s.setWorkspaceOrderPreference(settings.UIPreferences.WorkspaceOrder)

	data, err := s.loadActiveWorkspace(ctx)
	if err != nil {
		return nil, ErrInternal("failed to load workspace data", err)
	}
	if data == nil {
		data = &ops.WorkspaceData{}
	}
	normalizeWorkspaceData(data)
	s.applyWorkspaceOrderToData(data)
	return data, nil
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
	s.applyWorkspaceOrderToData(data)
	return data, nil
}

func (s *workspaceServiceImpl) invalidateWorkspaceCache() {
	if s.workspaceCache != nil {
		s.workspaceCache.invalidateAll()
	}
}

func (s *workspaceServiceImpl) loadWorkspaceOrderPreference() []string {
	if strings.TrimSpace(s.localSettings) == "" {
		return nil
	}
	if order, ok := s.cachedWorkspaceOrderPreference(); ok {
		return order
	}
	settings, err := localsettings.Load(s.localSettings)
	if err != nil {
		slog.Warn("failed to load workspace order preference", "err", err)
		return nil
	}
	order := normalizeWorkspaceOrder(settings.UIPreferences.WorkspaceOrder)
	s.setWorkspaceOrderPreference(order)
	return order
}

func (s *workspaceServiceImpl) cachedWorkspaceOrderPreference() ([]string, bool) {
	s.orderCacheMu.RLock()
	defer s.orderCacheMu.RUnlock()
	if !s.orderCacheOK {
		return nil, false
	}
	return append([]string(nil), s.orderCache...), true
}

func (s *workspaceServiceImpl) setWorkspaceOrderPreference(order []string) {
	s.orderCacheMu.Lock()
	defer s.orderCacheMu.Unlock()
	s.orderCache = append([]string(nil), normalizeWorkspaceOrder(order)...)
	s.orderCacheOK = true
}

func (s *workspaceServiceImpl) applyWorkspaceOrderToData(data *ops.WorkspaceData) {
	if data == nil {
		return
	}
	order := s.loadWorkspaceOrderPreference()
	if len(order) == 0 {
		data.WorkspaceOrder = nil
		return
	}
	data.Workspaces = orderWorkspaceSummaries(data.Workspaces, order)
	data.WorkspaceOrder = workspaceSummaryIDs(data.Workspaces)
}

func (s *workspaceServiceImpl) applyWorkspaceOrderToList(items []WorkspaceListItem) {
	order := s.loadWorkspaceOrderPreference()
	if len(order) == 0 {
		return
	}
	ordered := orderWorkspaceList(items, order)
	copy(items, ordered)
}

func normalizeWorkspaceOrder(order []string) []string {
	if len(order) == 0 {
		return nil
	}
	out := make([]string, 0, len(order))
	seen := make(map[string]bool, len(order))
	for _, id := range order {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func orderWorkspaceSummaries(workspaces []ops.WorkspaceSummary, order []string) []ops.WorkspaceSummary {
	if len(workspaces) == 0 || len(order) == 0 {
		return workspaces
	}
	byID := make(map[string]ops.WorkspaceSummary, len(workspaces))
	for _, ws := range workspaces {
		byID[ws.ID] = ws
	}
	out := make([]ops.WorkspaceSummary, 0, len(workspaces))
	used := make(map[string]bool, len(workspaces))
	for _, id := range order {
		ws, ok := byID[id]
		if !ok {
			continue
		}
		out = append(out, ws)
		used[id] = true
	}
	for _, ws := range workspaces {
		if used[ws.ID] {
			continue
		}
		out = append(out, ws)
	}
	return out
}

func orderWorkspaceList(items []WorkspaceListItem, order []string) []WorkspaceListItem {
	if len(items) == 0 || len(order) == 0 {
		return items
	}
	byID := make(map[string]WorkspaceListItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	out := make([]WorkspaceListItem, 0, len(items))
	used := make(map[string]bool, len(items))
	for _, id := range order {
		item, ok := byID[id]
		if !ok {
			continue
		}
		out = append(out, item)
		used[id] = true
	}
	for _, item := range items {
		if used[item.ID] {
			continue
		}
		out = append(out, item)
	}
	return out
}

func workspaceSummaryIDs(workspaces []ops.WorkspaceSummary) []string {
	if len(workspaces) == 0 {
		return nil
	}
	ids := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws.ID != "" {
			ids = append(ids, ws.ID)
		}
	}
	return ids
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
