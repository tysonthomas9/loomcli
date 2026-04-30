package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/configlock"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

const (
	workspaceCreateTimeoutEmpty = 10 * time.Second
	workspaceCreateTimeoutClone = 60 * time.Second
)

// WorkspaceServiceConfig holds the dependencies for workspace service construction.
//
// Phase 4 of the loom -> fleet-db migration: when Store is non-nil, the
// service sources its read-side data from Store instead of the legacy
// ConfigFn / ConfigByIDFn closures (which still drive write-side
// helpers + tests). The closures will be removed in Phase 6.
type WorkspaceServiceConfig struct {
	Store          store.Store                              // Fleet-db-backed store; preferred read source when set
	ConfigFn       func() (*ops.WorkspaceData, error)       // Workspace topology supplier (legacy yaml-derived)
	ConfigByIDFn   func(string) (*ops.WorkspaceData, error) // Topology supplier by workspace ID
	MultiPool      *daemon.MultiPool                        // For listing workspaces and existence checks
	CreateFn       WorkspaceCreateFn                        // Already wrapped with registry hooks
	DeleteFn       func(string) error                       // Already wrapped with cleanup hooks
	JobStore       JobStore                                 // For async creation; nil = async unavailable
	SetDefaultFn   func(string) error                       // nil = feature disabled
	ClearDefaultFn func() error                             // nil = feature disabled
}

type workspaceServiceImpl struct {
	store          store.Store
	configFn       func() (*ops.WorkspaceData, error)
	configByIDFn   func(string) (*ops.WorkspaceData, error)
	multiPool      *daemon.MultiPool
	createFn       WorkspaceCreateFn
	deleteFn       func(string) error
	jobStore       JobStore
	setDefaultFn   func(string) error
	clearDefaultFn func() error
}

// NewWorkspaceService creates a new WorkspaceService from the given config.
func NewWorkspaceService(cfg WorkspaceServiceConfig) WorkspaceService {
	return &workspaceServiceImpl{
		store:          cfg.Store,
		configFn:       cfg.ConfigFn,
		configByIDFn:   cfg.ConfigByIDFn,
		multiPool:      cfg.MultiPool,
		createFn:       cfg.CreateFn,
		deleteFn:       cfg.DeleteFn,
		jobStore:       cfg.JobStore,
		setDefaultFn:   cfg.SetDefaultFn,
		clearDefaultFn: cfg.ClearDefaultFn,
	}
}

// loadActiveWorkspace returns the active workspace topology, preferring
// the store when configured. Falls back to configFn during the migration
// window.
func (s *workspaceServiceImpl) loadActiveWorkspace(ctx context.Context) (*ops.WorkspaceData, error) {
	if s.store != nil {
		data, err := storeadapter.BuildActiveWorkspaceData(ctx, s.store)
		if err == nil && data != nil {
			return data, nil
		}
		// Fall through on store miss / no active workspace — legacy path
		// may still know the workspace from yaml.
	}
	if s.configFn != nil {
		return s.configFn()
	}
	return nil, nil
}

// loadWorkspaceByID returns a specific workspace's topology, preferring
// the store when configured.
func (s *workspaceServiceImpl) loadWorkspaceByID(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	if s.store != nil {
		data, err := storeadapter.BuildWorkspaceDataForKey(ctx, s.store, wsID)
		if err == nil && data != nil {
			return data, nil
		}
	}
	if s.configByIDFn != nil {
		return s.configByIDFn(wsID)
	}
	return nil, nil
}

func (s *workspaceServiceImpl) GetActiveWorkspace(ctx context.Context) (*ops.WorkspaceData, error) {
	if s.store == nil && s.configFn == nil {
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

func (s *workspaceServiceImpl) ListWorkspaces(ctx context.Context) ([]WorkspaceListItem, error) {
	// Store is authoritative when set: list its workspaces directly so
	// the API surface reflects fleet-db's actual contents — not whatever
	// the legacy multiPool/yaml closures believed at startup. The
	// multiPool is consulted only to enrich items with beads-pool stats.
	if s.store != nil {
		wsList, err := s.store.Workspaces().List(ctx)
		if err == nil {
			activeKey, _ := bootstrap.ResolveActiveWorkspaceKey(ctx, s.store.Workspaces())
			items := make([]WorkspaceListItem, 0, len(wsList))
			for _, ws := range wsList {
				item := WorkspaceListItem{
					ID:     ws.Key,
					Name:   ws.Name,
					Active: ws.Key == activeKey,
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
		slog.Warn("store list failed, falling through to legacy", "err", err)
	}

	// Legacy path: multiPool first (beads), else yaml-derived configFn.
	var wsMetaByName map[string]ops.WorkspaceSummary
	var wsMetaByID map[string]ops.WorkspaceSummary
	var configWorkspaces []ops.WorkspaceSummary
	if data, err := s.loadActiveWorkspace(ctx); err == nil && data != nil {
		configWorkspaces = data.Workspaces
		wsMetaByName = make(map[string]ops.WorkspaceSummary, len(data.Workspaces))
		wsMetaByID = make(map[string]ops.WorkspaceSummary, len(data.Workspaces))
		for _, ws := range data.Workspaces {
			wsMetaByName[ws.Name] = ws
			if ws.ID != "" {
				wsMetaByID[ws.ID] = ws
			}
		}
	}

	var ids []string
	if s.multiPool != nil {
		ids = s.multiPool.WorkspaceIDs()
	}
	if len(ids) == 0 && len(configWorkspaces) > 0 {
		ids = make([]string, 0, len(configWorkspaces))
		for _, ws := range configWorkspaces {
			id := ws.ID
			if id == "" {
				id = ws.Name
			}
			if id != "" {
				ids = append(ids, id)
			}
		}
	}

	items := make([]WorkspaceListItem, 0, len(ids))
	for _, id := range ids {
		item := WorkspaceListItem{
			ID:   id,
			Name: id,
		}
		if meta, ok := wsMetaByID[id]; ok {
			item.Name = meta.Name
			item.ID = meta.ID
			item.Path = meta.Path
			item.Active = meta.Active
		} else if meta, ok := wsMetaByName[id]; ok {
			if meta.ID != "" {
				item.ID = meta.ID
			}
			item.Path = meta.Path
			item.Active = meta.Active
		}
		if s.multiPool != nil {
			if p := s.multiPool.PoolForWorkspace(id); p != nil {
				ps := poolStatsFromDaemon(p.Stats())
				item.Pool = &ps
			}
		}
		items = append(items, item)
	}

	return items, nil
}

func (s *workspaceServiceImpl) GetWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	// Primary existence check: the multiPool registry. In beads mode the
	// daemon pool owns the authoritative list of live workspaces. In fleet
	// mode the multiPool is intentionally empty (no beads daemon), so the
	// multiPool check alone would 404 every lookup — fall through to a
	// store/config-based lookup instead.
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

// lookupWorkspace resolves a workspace UUID via the store (preferred)
// or the legacy config closures. Returns (data, true, nil) when a match
// is found, (nil, false, nil) when the ID is unknown, or (nil, false,
// err) on a load error. Used by GetWorkspace as the fallback when the
// multiPool registry has no entry for the workspace.
func (s *workspaceServiceImpl) lookupWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, bool, error) {
	// Prefer the store — single Get call, no full-config scan.
	if s.store != nil {
		if data, err := storeadapter.BuildWorkspaceDataForKey(ctx, s.store, wsID); err == nil && data != nil {
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
	}
	// Legacy: by-ID supplier (cheaper than full config scan).
	if s.configByIDFn != nil {
		if data, err := s.configByIDFn(wsID); err == nil && data != nil {
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
	}

	// Final fallback: scan the full config for a matching UUID.
	if s.configFn == nil {
		return nil, false, nil
	}
	cfgData, err := s.configFn()
	if err != nil {
		return nil, false, ErrInternal("failed to load workspace config", err)
	}
	if cfgData == nil {
		return nil, false, nil
	}
	for _, ws := range cfgData.Workspaces {
		if ws.ID != wsID {
			continue
		}
		result := &ops.WorkspaceData{
			ID:               ws.ID,
			Name:             ws.Name,
			Path:             ws.Path,
			Repos:            cfgData.Repos,
			Groups:           cfgData.Groups,
			Agents:           cfgData.Agents,
			Workspaces:       cfgData.Workspaces,
			WorkspaceOrder:   cfgData.WorkspaceOrder,
			DefaultWorkspace: cfgData.DefaultWorkspace,
		}
		normalizeWorkspaceData(result)
		for i := range result.Repos {
			if b := readGitHeadBranch(result.Repos[i].Path); b != "" {
				result.Repos[i].CurrentBranch = b
			}
		}
		for i := range result.Workspaces {
			result.Workspaces[i].Active = result.Workspaces[i].ID == wsID
		}
		return result, true, nil
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
	_ = result // used by wrapWorkspaceCreateFn for registration

	var data *ops.WorkspaceData
	if s.configFn != nil {
		d, cfgErr := s.configFn()
		if cfgErr == nil && d != nil {
			normalizeWorkspaceData(d)
			data = d
		}
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

func (s *workspaceServiceImpl) GetWorkspaceJob(_ context.Context, jobID string) (*WorkspaceJob, error) {
	if s.jobStore == nil {
		return nil, ErrNotFound("job not found")
	}

	job := s.jobStore.Get(jobID)
	if job == nil {
		return nil, ErrNotFound("job not found")
	}
	return job, nil
}

func (s *workspaceServiceImpl) DeleteWorkspace(ctx context.Context, wsID string) (*ops.WorkspaceData, error) {
	if s.deleteFn == nil {
		return nil, ErrUnavailable("workspace deletion not available")
	}

	if s.store != nil {
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
		return s.refreshWorkspaceData()
	}

	name, err := s.resolveWorkspaceNameByUUID(wsID)
	if err != nil {
		return nil, ErrInternal("failed to resolve workspace", err)
	}
	if name == "" {
		return nil, ErrNotFound(fmt.Sprintf("workspace with ID %q not found", wsID))
	}

	if err := s.deleteFn(name); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") {
			return nil, ErrNotFound(errMsg)
		}
		if strings.Contains(errMsg, "has running agents") {
			return nil, ErrConflict(errMsg)
		}
		return nil, ErrInternal(errMsg, err)
	}

	return s.refreshWorkspaceData()
}

func (s *workspaceServiceImpl) RenameWorkspace(_ context.Context, wsID string, newName string) (*ops.WorkspaceData, error) {
	if err := validateWorkspaceName(newName); err != nil {
		return nil, err
	}

	dir := loomConfigDir()
	unlock, lockErr := configlock.ConfigLock(dir)
	if lockErr != nil {
		return nil, ErrInternal("failed to acquire config lock", lockErr)
	}

	cfg, err := loadLoomConfigUnlocked()
	if err != nil {
		unlock()
		return nil, ErrInternal("failed to load config", err)
	}
	if cfg == nil {
		unlock()
		return nil, ErrNotFound("no config found")
	}

	oldName, ws, found := resolveWorkspaceNameByID(cfg, wsID)
	if !found {
		unlock()
		return nil, ErrNotFound(fmt.Sprintf("workspace with ID %q not found", wsID))
	}

	if oldName == newName {
		unlock()
		return s.refreshWorkspaceData()
	}

	if _, exists := cfg.Workspaces[newName]; exists {
		unlock()
		return nil, ErrConflict("workspace name already exists")
	}

	applyWorkspaceRename(cfg, oldName, newName, ws)

	if err := saveLoomConfigUnlocked(cfg); err != nil {
		unlock()
		return nil, ErrInternal("failed to save config", err)
	}
	unlock() // Release before refreshWorkspaceData which also acquires the lock

	return s.refreshWorkspaceData()
}

func (s *workspaceServiceImpl) ReorderWorkspaces(_ context.Context, order []string) (*ops.WorkspaceData, error) {
	dir := loomConfigDir()
	unlock, lockErr := configlock.ConfigLock(dir)
	if lockErr != nil {
		return nil, ErrInternal("failed to acquire config lock", lockErr)
	}

	cfg, err := loadLoomConfigUnlocked()
	if err != nil {
		unlock()
		return nil, ErrInternal("failed to load config", err)
	}
	if cfg == nil {
		unlock()
		return nil, ErrNotFound("no config found")
	}

	// Resolve UUIDs to names and filter unknown entries. Deduplicate.
	validOrder := make([]string, 0, len(order))
	seen := make(map[string]bool, len(order))
	for _, entry := range order {
		var name string
		if _, ok := cfg.Workspaces[entry]; ok {
			name = entry
		} else if resolved, _, found := resolveWorkspaceNameByID(cfg, entry); found {
			name = resolved
		}
		if name != "" && !seen[name] {
			validOrder = append(validOrder, name)
			seen[name] = true
		}
	}
	cfg.WorkspaceOrder = validOrder

	if err := saveLoomConfigUnlocked(cfg); err != nil {
		unlock()
		return nil, ErrInternal("failed to save config", err)
	}
	unlock() // Release before refreshWorkspaceData which also acquires the lock

	return s.refreshWorkspaceData()
}

func (s *workspaceServiceImpl) SetDefaultWorkspace(_ context.Context, name string) (*ops.WorkspaceData, error) {
	if s.setDefaultFn == nil {
		return nil, ErrUnavailable("set default workspace not available")
	}

	if err := s.setDefaultFn(name); err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "not found") {
			return nil, ErrNotFound(errMsg)
		}
		return nil, ErrInternal(errMsg, err)
	}

	return s.refreshWorkspaceData()
}

func (s *workspaceServiceImpl) ClearDefaultWorkspace(_ context.Context) (*ops.WorkspaceData, error) {
	if s.clearDefaultFn == nil {
		return nil, ErrUnavailable("clear default workspace not available")
	}

	if err := s.clearDefaultFn(); err != nil {
		return nil, ErrInternal(err.Error(), err)
	}

	return s.refreshWorkspaceData()
}

func (s *workspaceServiceImpl) PatchWorkspaceBackend(_ context.Context, wsID string, backend string) (*ops.WorkspaceData, error) {
	dir := loomConfigDir()
	unlock, lockErr := configlock.ConfigLock(dir)
	if lockErr != nil {
		return nil, ErrInternal("failed to acquire config lock", lockErr)
	}

	cfg, err := loadLoomConfigForBackendUnlocked()
	if err != nil {
		unlock()
		return nil, ErrInternal("failed to load config", err)
	}
	if cfg == nil {
		unlock()
		return nil, ErrNotFound("no config found")
	}

	name, ws, found := resolveWorkspaceNameByIDForBackend(cfg, wsID)
	if !found {
		unlock()
		return nil, ErrNotFound(fmt.Sprintf("workspace with ID %q not found", wsID))
	}

	ws.Backend = backend
	cfg.Workspaces[name] = ws

	if err := saveLoomConfigForBackendUnlocked(cfg); err != nil {
		unlock()
		return nil, ErrInternal("failed to save config", err)
	}
	unlock() // Release before refreshWorkspaceData which also acquires the lock

	return s.refreshWorkspaceData()
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

// --- Internal helpers ---

func (s *workspaceServiceImpl) refreshWorkspaceData() (*ops.WorkspaceData, error) {
	if s.configFn == nil {
		return nil, nil
	}
	data, err := s.configFn()
	if err != nil {
		return nil, nil // non-fatal: data refresh failure after successful mutation
	}
	if data != nil {
		normalizeWorkspaceData(data)
	}
	return data, nil
}

func (s *workspaceServiceImpl) resolveWorkspaceNameByUUID(wsID string) (string, error) {
	if s.configFn == nil {
		return "", nil
	}
	data, err := s.configFn()
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", nil
	}
	for _, ws := range data.Workspaces {
		if ws.ID == wsID {
			return ws.Name, nil
		}
	}
	return "", nil
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

// FindWorkspacePathByID scans workspace summaries for a matching UUID and
// returns its filesystem path. Returns empty string if not found.
func FindWorkspacePathByID(wsData *ops.WorkspaceData, id string) string {
	if wsData == nil {
		return ""
	}
	for _, ws := range wsData.Workspaces {
		if ws.ID == id {
			return ws.Path
		}
	}
	return ""
}

// ResolveWorkspacePath loads config and resolves a workspace UUID to its
// filesystem path. Returns empty string on any failure.
func ResolveWorkspacePath(configFn func() (*ops.WorkspaceData, error), workspaceID string) string {
	if configFn == nil {
		return ""
	}
	wsData, err := configFn()
	if err != nil || wsData == nil {
		return ""
	}
	return FindWorkspacePathByID(wsData, workspaceID)
}
