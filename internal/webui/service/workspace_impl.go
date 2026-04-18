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

	"github.com/tysonthomas9/loomcli/internal/configlock"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

const (
	workspaceCreateTimeoutEmpty = 10 * time.Second
	workspaceCreateTimeoutClone = 60 * time.Second
)

// WorkspaceServiceConfig holds the dependencies for workspace service construction.
type WorkspaceServiceConfig struct {
	ConfigFn       func() (*ops.WorkspaceData, error)       // Workspace topology supplier
	ConfigByIDFn   func(string) (*ops.WorkspaceData, error) // Topology supplier by workspace ID
	MultiPool      *daemon.MultiPool                        // For listing workspaces and existence checks
	CreateFn       WorkspaceCreateFn                        // Already wrapped with registry hooks
	DeleteFn       func(string) error                       // Already wrapped with cleanup hooks
	JobStore       JobStore                                 // For async creation; nil = async unavailable
	SetDefaultFn   func(string) error                       // nil = feature disabled
	ClearDefaultFn func() error                             // nil = feature disabled
}

type workspaceServiceImpl struct {
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

func (s *workspaceServiceImpl) GetActiveWorkspace(_ context.Context) (*ops.WorkspaceData, error) {
	if s.configFn == nil {
		return &ops.WorkspaceData{
			Repos:      []ops.WorkspaceRepo{},
			Groups:     []string{},
			Agents:     []ops.WorkspaceAgentInfo{},
			Workspaces: []ops.WorkspaceSummary{},
		}, nil
	}

	data, err := s.configFn()
	if err != nil {
		return nil, ErrInternal("failed to load workspace config", err)
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

func (s *workspaceServiceImpl) ListWorkspaces(_ context.Context) ([]WorkspaceListItem, error) {
	if s.multiPool == nil {
		return []WorkspaceListItem{}, nil
	}

	ids := s.multiPool.WorkspaceIDs()

	// Build workspace metadata maps from config if available.
	var wsMetaByName map[string]ops.WorkspaceSummary
	var wsMetaByID map[string]ops.WorkspaceSummary
	if s.configFn != nil {
		if data, err := s.configFn(); err == nil && data != nil {
			wsMetaByName = make(map[string]ops.WorkspaceSummary, len(data.Workspaces))
			wsMetaByID = make(map[string]ops.WorkspaceSummary, len(data.Workspaces))
			for _, ws := range data.Workspaces {
				wsMetaByName[ws.Name] = ws
				if ws.ID != "" {
					wsMetaByID[ws.ID] = ws
				}
			}
		}
	}

	items := make([]WorkspaceListItem, 0, len(ids))
	for _, id := range ids {
		item := WorkspaceListItem{
			ID:   id,
			Name: id,
		}

		// Enrich with config metadata if available.
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

		if p := s.multiPool.PoolForWorkspace(id); p != nil {
			ps := poolStatsFromDaemon(p.Stats())
			item.Pool = &ps
		}

		items = append(items, item)
	}

	return items, nil
}

func (s *workspaceServiceImpl) GetWorkspace(_ context.Context, wsID string) (*ops.WorkspaceData, error) {
	if s.multiPool == nil || s.multiPool.PoolForWorkspace(wsID) == nil {
		return nil, ErrNotFound("workspace not found: " + wsID)
	}

	if s.configByIDFn == nil {
		return nil, ErrInternal("workspace config not available", nil)
	}

	data, err := s.configByIDFn(wsID)
	if err != nil || data == nil {
		return nil, ErrInternal("failed to load workspace config", err)
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

func (s *workspaceServiceImpl) DeleteWorkspace(_ context.Context, wsID string) (*ops.WorkspaceData, error) {
	if s.deleteFn == nil {
		return nil, ErrUnavailable("workspace deletion not available")
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
