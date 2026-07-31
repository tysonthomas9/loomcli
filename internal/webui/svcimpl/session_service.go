package svcimpl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript"
	"github.com/tysonthomas9/loomcli/internal/sessions/transcript/backends"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

// Compile-time check.
var _ service.SessionService = (*sessionServiceImpl)(nil)
var _ service.AgentSessionTranscriptService = (*sessionServiceImpl)(nil)

var userHomeDir = os.UserHomeDir

// errNoUsableSessionStores distinguishes an absent or unreadable local session
// store from unrelated control-plane failures. Transcript and diff reads may
// recover from this condition through their durable control-plane artifacts.
var errNoUsableSessionStores = errors.New("no usable local session stores")

func sessionControlPlaneReadError(message string, err error) error {
	switch {
	case errors.Is(err, domain.ErrRateLimited):
		return service.NewServiceError(
			service.KindRateLimited,
			message,
			err,
		)
	case errors.Is(err, domain.ErrUnavailable):
		return service.NewServiceError(
			service.KindUnavailable,
			message,
			err,
		)
	default:
		return service.ErrInternal(message, err)
	}
}

// sessionServiceImpl is the concrete implementation of SessionService.
type sessionServiceImpl struct {
	store      store.Store
	histStore  *sessionhistory.Store
	runtimeDir string
}

// NewSessionService creates a new SessionService implementation.
func NewSessionService(st store.Store, histStore *sessionhistory.Store) service.SessionService {
	return NewSessionServiceWithRuntimeDir(st, histStore, "")
}

// NewSessionServiceWithRuntimeDir creates a SessionService that also searches
// the daemon/runtime session store used by local desktop mode.
func NewSessionServiceWithRuntimeDir(st store.Store, histStore *sessionhistory.Store, runtimeDir string) service.SessionService {
	return &sessionServiceImpl{store: st, histStore: histStore, runtimeDir: runtimeDir}
}

// storesForWorkspace returns session stores for all repos in the workspace.
// Agent worktrees store sessions in their own directories, so we need to
// search across all repos to find sessions for a given task.
func (s *sessionServiceImpl) storesForWorkspace(ctx context.Context, wsID string) ([]*sessions.Store, error) {
	collection := newSessionStoreCollection()
	s.addRuntimeSessionStoreCandidates(wsID, collection)
	if err := s.addWorkspaceSessionStores(ctx, wsID, collection); err != nil {
		return nil, err
	}
	if len(collection.stores) == 0 {
		return nil, service.ErrInternal("no session stores available", errNoUsableSessionStores)
	}
	return collection.stores, nil
}

type sessionStoreCollection struct {
	stores []*sessions.Store
	seen   map[string]struct{}
}

func newSessionStoreCollection() *sessionStoreCollection {
	return &sessionStoreCollection{seen: make(map[string]struct{})}
}

func (c *sessionStoreCollection) add(runtimeDir string) {
	if runtimeDir == "" {
		return
	}
	key := filepath.Clean(runtimeDir)
	if abs, err := filepath.Abs(runtimeDir); err == nil {
		key = filepath.Clean(abs)
	}
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	st, err := sessions.NewStore(runtimeDir)
	if err != nil {
		logger.Warn("failed to open local session store", "runtime_dir", runtimeDir, "err", err)
		return
	}
	c.stores = append(c.stores, st)
}

// addExistingRuntimeDir adds the server-configured runtime directory only when
// it already exists and is a directory. Session reads must not create an
// arbitrary directory from configuration while resolving a user-supplied
// session ID. sessions.Store validates and bounds the session ID beneath its
// sessions directory before it reads metadata.
func (c *sessionStoreCollection) addExistingRuntimeDir(runtimeDir string) {
	if runtimeDir == "" {
		return
	}
	absRuntimeDir, err := filepath.Abs(runtimeDir)
	if err != nil {
		logger.Warn("failed to resolve local session runtime directory", "runtime_dir", runtimeDir, "err", err)
		return
	}
	info, err := os.Stat(absRuntimeDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.Warn("failed to inspect local session runtime directory", "runtime_dir", absRuntimeDir, "err", err)
		}
		return
	}
	if !info.IsDir() {
		logger.Warn("local session runtime path is not a directory", "runtime_dir", absRuntimeDir)
		return
	}
	c.add(absRuntimeDir)
}

// addRuntimeSessionStoreCandidates adds the active runtime and, only for the
// conventional <root>/workspaces/<workspace> layout, the requested workspace's
// direct sibling. This lets a local serve running in LOCALMODE read a session
// owned by another local workspace without asking FleetDB to enumerate the
// workspace topology.
func (s *sessionServiceImpl) addRuntimeSessionStoreCandidates(wsID string, collection *sessionStoreCollection) {
	collection.addExistingRuntimeDir(s.runtimeDir)
	if siblingRuntimeDir, ok := siblingWorkspaceRuntimeDir(s.runtimeDir, wsID); ok {
		collection.addExistingRuntimeDir(siblingRuntimeDir)
	}
}

// siblingWorkspaceRuntimeDir returns <runtime parent>/<wsID> only when the
// runtime's parent is literally named "workspaces" and wsID is one safe path
// component. It must not turn a workspace key into an arbitrary filesystem
// path.
func siblingWorkspaceRuntimeDir(runtimeDir, wsID string) (string, bool) {
	if runtimeDir == "" || wsID == "" || wsID == "." || wsID == ".." ||
		filepath.Clean(wsID) != wsID || filepath.Base(wsID) != wsID ||
		strings.ContainsAny(wsID, "/\\") {
		return "", false
	}
	absRuntimeDir, err := filepath.Abs(runtimeDir)
	if err != nil {
		return "", false
	}
	workspacesDir := filepath.Dir(absRuntimeDir)
	if filepath.Base(workspacesDir) != "workspaces" {
		return "", false
	}
	return filepath.Join(workspacesDir, wsID), true
}

func (s *sessionServiceImpl) addWorkspaceSessionStores(
	ctx context.Context,
	wsID string,
	collection *sessionStoreCollection,
) error {
	if s.store == nil {
		if len(collection.stores) == 0 {
			return service.ErrUnavailable("session store not available")
		}
		return nil
	}
	wsData, err := storeadapter.BuildWorkspaceDataForKey(ctx, s.store, wsID)
	if err != nil {
		if len(collection.stores) > 0 {
			return nil
		}
		if errors.Is(err, domain.ErrNotFound) {
			return service.ErrNotFound("workspace not found")
		}
		return sessionControlPlaneReadError(
			"failed to resolve session stores",
			err,
		)
	}
	if wsData == nil {
		if len(collection.stores) == 0 {
			return service.ErrNotFound("workspace not found")
		}
		return nil
	}
	// Include the workspace root and every repo, since agent worktrees may have
	// their own sessions directory.
	collection.add(wsData.Path)
	for _, repo := range wsData.Repos {
		collection.add(repo.Path)
	}
	return nil
}

// storeOwningSession returns the first store whose metadata exists for
// sessionID (i.e. the store that owns the session on disk), or nil.
func storeOwningSession(stores []*sessions.Store, sessionID string) *sessions.Store {
	for _, st := range stores {
		if _, err := st.LoadMetadata(sessionID); err == nil {
			return st
		}
	}
	return nil
}

// findStoreForSession returns the first store that has metadata for the given session.
func (s *sessionServiceImpl) findStoreForSession(ctx context.Context, wsID, sessionID string) (*sessions.Store, error) {
	// The active runtime or requested workspace's sibling runtime can own the
	// session even when FleetDB is unavailable or its workspace topology is
	// stale. Check those local candidates first so loading a transcript, diff,
	// or detail does not depend on a control-plane fanout.
	if validSessionID.MatchString(sessionID) {
		collection := newSessionStoreCollection()
		s.addRuntimeSessionStoreCandidates(wsID, collection)
		if st := storeOwningSession(collection.stores, sessionID); st != nil {
			return st, nil
		}
	}

	stores, err := s.storesForWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	if st := storeOwningSession(stores, sessionID); st != nil {
		return st, nil
	}
	return nil, service.ErrNotFound("session not found")
}

func (s *sessionServiceImpl) ListTaskSessions(ctx context.Context, wsID, taskID string) ([]service.SessionListItem, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, service.ErrValidation("invalid task ID: must match [a-zA-Z0-9._-]+")
	}
	if items, err := s.controlPlaneTaskSessions(ctx, wsID, taskID); err == nil && len(items) > 0 {
		s.enrichSessionListItemsFromFileStores(ctx, wsID, taskID, items)
		return items, nil
	}

	stores, err := s.storesForWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}

	var items []service.SessionListItem
	for _, sessStore := range stores {
		records, err := sessStore.SessionsByTask(taskID)
		if err != nil {
			continue
		}
		for _, rec := range records {
			item := service.SessionListItem{
				SessionRecord: rec,
				IsActive:      rec.Status == sessions.StatusRunning,
			}
			if info, err := os.Stat(sessStore.NativeTranscriptPath(rec.SessionID)); err == nil && info.Size() > 0 {
				item.HasTranscript = true
			}
			if !item.HasTranscript && eventStoreHasTranscript(sessStore, rec.SessionID) {
				item.HasTranscript = true // F3: event store as a has_transcript source (native fallback above)
			}
			if diff, err := sessStore.ReadDiff(rec.SessionID); err == nil && diff != "" {
				item.HasDiff = true
			}
			items = append(items, item)
		}
	}
	return items, nil
}

func (s *sessionServiceImpl) enrichSessionListItemsFromFileStores(ctx context.Context, wsID, taskID string, items []service.SessionListItem) {
	stores, err := s.storesForWorkspace(ctx, wsID)
	if err != nil {
		return
	}
	for i := range items {
		item := &items[i]
		for _, sessStore := range stores {
			meta, err := sessStore.LoadMetadata(item.SessionID)
			if err != nil || meta.TaskID != taskID {
				continue
			}
			enrichSessionRecordFromLocal(&item.SessionRecord, meta.SessionRecord)
			if info, err := os.Stat(sessStore.NativeTranscriptPath(item.SessionID)); err == nil && info.Size() > 0 {
				item.HasTranscript = true
			}
			if diff, err := sessStore.ReadDiff(item.SessionID); err == nil && diff != "" {
				item.HasDiff = true
			}
			break
		}
	}
}

func enrichSessionRecordFromLocal(rec *sessions.SessionRecord, local sessions.SessionRecord) {
	if rec.TaskID == "" {
		rec.TaskID = local.TaskID
	}
	if rec.EpicID == "" {
		rec.EpicID = local.EpicID
	}
	if rec.Backend == "" {
		rec.Backend = local.Backend
	}
	if rec.Model == "" {
		rec.Model = local.Model
	}
	if rec.Phase == "" {
		rec.Phase = local.Phase
	}
	if localHasUsage(local) {
		rec.InputTokens = local.InputTokens
		rec.OutputTokens = local.OutputTokens
		rec.CacheReadTokens = local.CacheReadTokens
		rec.CacheWriteTokens = local.CacheWriteTokens
		rec.EstimatedCostUSD = local.EstimatedCostUSD
	}
	if local.FilesChanged != 0 {
		rec.FilesChanged = local.FilesChanged
	}
	if local.LinesAdded != 0 {
		rec.LinesAdded = local.LinesAdded
	}
	if local.LinesRemoved != 0 {
		rec.LinesRemoved = local.LinesRemoved
	}
	if len(local.FilesTouched) > 0 {
		rec.FilesTouched = local.FilesTouched
	}
	if rec.ErrorClass == "" {
		rec.ErrorClass = local.ErrorClass
	}
}

func localHasUsage(rec sessions.SessionRecord) bool {
	return rec.InputTokens != 0 ||
		rec.OutputTokens != 0 ||
		rec.CacheReadTokens != 0 ||
		rec.CacheWriteTokens != 0 ||
		rec.EstimatedCostUSD != 0
}

func firstNonEmptySessionValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

//nolint:funlen // Keep canonical TaskRun projection, legacy compatibility merge, and evidence enrichment in one deterministic query.
func (s *sessionServiceImpl) controlPlaneTaskSessions(ctx context.Context, wsID, taskID string) ([]service.SessionListItem, error) {
	if s.store == nil {
		return nil, nil
	}
	taskRuns, err := s.store.TaskRuns().List(ctx, wsID, store.TaskRunFilter{TaskID: taskID})
	if err != nil {
		return nil, err
	}
	records, interactionErr := s.store.AgentSessions().List(ctx, wsID, store.AgentSessionFilter{TaskID: taskID})
	if interactionErr != nil && len(taskRuns) == 0 {
		return nil, interactionErr
	}
	// Resolve local stores once so artifact presence reflects on-disk truth, not the
	// metadata keys (stamped at creation/completion). Best-effort: a remote-only
	// deployment may have no local store, in which case we fall back to metadata.
	var stores []*sessions.Store
	if len(taskRuns) > 0 || len(records) > 0 {
		stores, _ = s.storesForWorkspace(ctx, wsID)
	}
	artifactKinds := s.taskRunArtifactKinds(ctx, wsID, taskID)
	items := make([]service.SessionListItem, 0, len(taskRuns)+len(records))
	representedTaskRuns := make(map[string]struct{}, len(taskRuns))
	representedSessionIDs := make(map[string]struct{}, len(taskRuns))
	for _, run := range taskRuns {
		if run == nil || run.WorkspaceKey != wsID || run.TaskID != taskID {
			continue
		}
		item := service.SessionListItem{
			SessionRecord: sessionRecordFromTaskRun(run),
			IsActive:      isActiveTaskRun(run.Status),
		}
		fillExecutionTaskRunEvidence(&item, run, artifactKinds)
		items = append(items, item)
		representedTaskRuns[run.TaskRunID] = struct{}{}
		representedSessionIDs[item.SessionID] = struct{}{}
	}
	for _, rec := range records {
		if rec == nil {
			continue
		}
		if _, represented := representedTaskRuns[legacyAgentSessionTaskRunID(rec)]; represented {
			continue
		}
		if _, represented := representedSessionIDs[rec.SessionID]; represented {
			continue
		}
		item := service.SessionListItem{
			SessionRecord: sessionRecordFromAgentSession(rec),
			IsActive:      isActiveAgentSession(rec.Status),
		}
		fillExecutionEvidence(&item, rec.Metadata)
		fillControlPlaneArtifactFlags(&item, stores, rec)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].StartedAt.After(items[j].StartedAt)
	})
	return items, nil
}

func fillExecutionEvidence(item *service.SessionListItem, metadata map[string]string) {
	if item == nil || metadata == nil {
		return
	}
	item.RuntimeStrategy = metadata["runtime_strategy"]
	item.DeliveryMode = metadata["delivery"]
	item.PatchBackStatus = metadata["patch_back_status"]
	item.LogsRef = metadata["logs_ref"]
	item.LocalBranch = metadata["local_branch"]
	item.HeadSHA = firstNonEmptySessionValue(metadata["head_sha"], metadata["github_head_sha"], metadata["patch_back_head_sha"])
	item.GitHubBranch = metadata["github_branch"]
	item.GitHubPRURL = metadata["github_pr_url"]
}

// fillControlPlaneArtifactFlags sets HasTranscript/HasDiff from on-disk truth when a
// local store owns the session. The control-plane metadata keys (transcript_path is
// stamped at creation, diff_path at completion) only record expected paths and don't
// prove the artifact exists, so they're used only as a fallback for remote-only
// sessions that have no local store.
func fillControlPlaneArtifactFlags(item *service.SessionListItem, stores []*sessions.Store, rec *domain.AgentSession) {
	if st := storeOwningSession(stores, rec.SessionID); st != nil {
		if info, err := os.Stat(st.NativeTranscriptPath(rec.SessionID)); err == nil && info.Size() > 0 {
			item.HasTranscript = true
		}
		if !item.HasTranscript && eventStoreHasTranscript(st, rec.SessionID) {
			item.HasTranscript = true // F3: event store as a has_transcript source (native fallback above)
		}
		if diff, err := st.ReadDiff(rec.SessionID); err == nil && diff != "" {
			item.HasDiff = true
		}
		return
	}
	if rec.Metadata != nil {
		item.HasTranscript = rec.Metadata["transcript_path"] != "" || rec.Metadata["transcript_ref"] != ""
		item.HasDiff = rec.Metadata["diff_path"] != "" || controlPlaneDiffArtifactRef(rec.Metadata) != ""
	}
}

func sessionRecordFromAgentSession(rec *domain.AgentSession) sessions.SessionRecord {
	startedAt := rec.StartedAt
	if startedAt.IsZero() {
		startedAt = rec.CreatedAt
	}
	taskID := rec.TaskID
	backend := ""
	if rec.Metadata != nil {
		if taskID == "" {
			taskID = rec.Metadata["task_id"]
		}
		backend = firstNonEmptySessionValue(rec.Metadata["backend"], rec.Metadata["runtime"])
	}
	diffMeta := sessions.DecodeDiffStatsMetadata(rec.Metadata)
	out := sessions.SessionRecord{
		SchemaVersion: sessions.CurrentSchemaVersion,
		SessionID:     rec.SessionID,
		TaskID:        taskID,
		AgentName:     rec.AgentID,
		Backend:       backend,
		Phase:         rec.Phase,
		StartedAt:     startedAt,
		Status:        sessionStatusFromAgentSession(rec.Status),
		AttemptNum:    rec.Attempt,
		ErrorClass:    rec.ErrorClass,
		FilesChanged:  diffMeta.FilesChanged,
		LinesAdded:    diffMeta.LinesAdded,
		LinesRemoved:  diffMeta.LinesRemoved,
		FilesTouched:  diffMeta.FilesTouched,
	}
	if rec.FinishedAt != nil {
		out.EndedAt = rec.FinishedAt
		if !startedAt.IsZero() {
			out.DurationS = rec.FinishedAt.Sub(startedAt).Seconds()
		}
	}
	if rec.ExitCode != nil {
		out.ExitCode = *rec.ExitCode
	}
	return out
}

func sessionStatusFromAgentSession(status domain.AgentSessionStatus) sessions.SessionStatus {
	switch status {
	case domain.AgentSessionCompleted:
		return sessions.StatusCompleted
	case domain.AgentSessionFailed:
		return sessions.StatusFailed
	case domain.AgentSessionCancelled, domain.AgentSessionExpired:
		return sessions.StatusAborted
	default:
		return sessions.StatusRunning
	}
}

func isActiveAgentSession(status domain.AgentSessionStatus) bool {
	switch status {
	case domain.AgentSessionCompleted, domain.AgentSessionFailed, domain.AgentSessionCancelled, domain.AgentSessionExpired:
		return false
	default:
		return true
	}
}

func (s *sessionServiceImpl) GetSession(ctx context.Context, wsID, taskID, sessionID string) (*service.SessionDetailData, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, service.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, service.ErrValidation("invalid session ID")
	}
	// Execution owns batch-attempt identity. Resolve it before consulting the
	// legacy file store so a stale local session with the same route ID cannot
	// shadow the canonical TaskRun lifecycle or evidence.
	if run, runErr := s.executionTaskRunForSession(ctx, wsID, taskID, sessionID); runErr == nil {
		return &service.SessionDetailData{
			SessionMetadata: sessions.SessionMetadata{SessionRecord: sessionRecordFromTaskRun(run)},
			IsActive:        isActiveTaskRun(run.Status),
		}, nil
	} else if !serviceErrorNotFound(runErr) {
		return nil, runErr
	}
	store, err := s.findStoreForSession(ctx, wsID, sessionID)
	if err != nil {
		// Execution TaskRuns and legacy control-plane task sessions do not
		// necessarily have file-store metadata. List/get/transcript must resolve
		// the same durable projection so a row returned by the list endpoint
		// remains navigable.
		if serviceErrorNotFound(err) {
			return s.controlPlaneSession(ctx, wsID, taskID, sessionID)
		}
		return nil, err
	}

	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s.controlPlaneSession(ctx, wsID, taskID, sessionID)
		}
		logger.Error("failed to load session", "session_id", sessionID, "err", err)
		return nil, service.ErrInternal("failed to load session", err)
	}

	// Enforce task ownership — session must belong to the requested task.
	if meta.TaskID != taskID {
		return nil, service.ErrNotFound("session not found")
	}

	return &service.SessionDetailData{
		SessionMetadata: *meta,
		IsActive:        meta.Status == sessions.StatusRunning,
	}, nil
}

func (s *sessionServiceImpl) controlPlaneSessionRecord(ctx context.Context, wsID, taskID, sessionID string) (*domain.AgentSession, error) {
	if s.store == nil {
		return nil, service.ErrNotFound("session not found")
	}
	rec, err := s.store.AgentSessions().Get(ctx, wsID, sessionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, service.ErrNotFound("session not found")
		}
		logger.Error("failed to load control-plane session", "workspace_id", wsID, "task_id", taskID, "session_id", sessionID, "err", err)
		return nil, sessionControlPlaneReadError(
			"failed to load session",
			err,
		)
	}
	if rec.TaskID != taskID && (rec.Metadata == nil || rec.Metadata["task_id"] != taskID) {
		return nil, service.ErrNotFound("session not found")
	}
	return rec, nil
}

// controlPlaneSession resolves batch attempts from Execution TaskRun first.
// AgentSession remains only as a compatibility fallback for historical rows
// and for task-associated Interaction sessions.
func (s *sessionServiceImpl) controlPlaneSession(ctx context.Context, wsID, taskID, sessionID string) (*service.SessionDetailData, error) {
	run, runErr := s.executionTaskRunForSession(ctx, wsID, taskID, sessionID)
	if runErr == nil {
		return &service.SessionDetailData{
			SessionMetadata: sessions.SessionMetadata{SessionRecord: sessionRecordFromTaskRun(run)},
			IsActive:        isActiveTaskRun(run.Status),
		}, nil
	}
	if !serviceErrorNotFound(runErr) {
		return nil, runErr
	}
	rec, err := s.controlPlaneSessionRecord(ctx, wsID, taskID, sessionID)
	if err != nil {
		return nil, err
	}
	return &service.SessionDetailData{
		SessionMetadata: sessions.SessionMetadata{SessionRecord: sessionRecordFromAgentSession(rec)},
		IsActive:        isActiveAgentSession(rec.Status),
	}, nil
}

func (s *sessionServiceImpl) GetSessionTranscript(ctx context.Context, wsID, taskID, sessionID string) ([]transcript.Event, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, service.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, service.ErrValidation("invalid session ID")
	}
	// TaskRun artifacts are the canonical transcript for batch attempts. A
	// same-named local session is only compatibility data and must not win.
	if run, runErr := s.executionTaskRunForSession(ctx, wsID, taskID, sessionID); runErr == nil {
		return s.executionTaskRunTranscript(ctx, wsID, run)
	} else if !serviceErrorNotFound(runErr) {
		return nil, runErr
	}
	store, _, err := s.authorizedSessionStore(ctx, wsID, taskID, sessionID)
	if err != nil {
		if !sessionStoreAllowsControlPlaneFallback(err) {
			return nil, err
		}
		return s.controlPlaneSessionTranscript(ctx, wsID, taskID, sessionID)
	}
	// F3: serve from the event store when enabled + populated, else fall back to
	// the native reader (transitional — transcripts never disappear mid-rollout).
	if evs, ok := eventStoreParentEvents(store, sessionID); ok {
		return evs, nil
	}
	events, loadErr := store.LoadNativeEvents(sessionID)
	if loadErr != nil {
		cpEvents, cpErr := s.controlPlaneSessionTranscript(ctx, wsID, taskID, sessionID)
		if cpErr == nil {
			return cpEvents, nil
		}
		// Preserve the control plane's actionable failure kind rather than
		// collapsing managed-content absence/outage into the native reader's
		// generic failure.
		var svcErr *service.ServiceError
		if errors.As(cpErr, &svcErr) &&
			(svcErr.Kind == service.KindNotFound || svcErr.Kind == service.KindUnavailable) {
			return nil, cpErr
		}
		logger.Error("failed to load native transcript", "session_id", sessionID, "err", loadErr)
		return nil, service.ErrInternal("failed to load transcript", loadErr)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

func serviceErrorNotFound(err error) bool {
	var svcErr *service.ServiceError
	return errors.As(err, &svcErr) && svcErr.Kind == service.KindNotFound
}

func sessionStoreAllowsControlPlaneFallback(err error) bool {
	return serviceErrorNotFound(err) || errors.Is(err, errNoUsableSessionStores)
}

func (s *sessionServiceImpl) ListSessionSubagents(ctx context.Context, wsID, taskID, sessionID string) ([]string, error) {
	store, _, err := s.authorizedSessionStore(ctx, wsID, taskID, sessionID)
	if err != nil {
		return nil, err
	}
	names, err := store.ListSubagentTranscripts(sessionID)
	if err != nil {
		logger.Error("failed to list subagent transcripts", "session_id", sessionID, "err", err)
		return nil, service.ErrInternal("failed to list subagents", err)
	}
	ids := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		// Format: agent-<id>.jsonl
		stripped := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
		if stripped != "" {
			seen[stripped] = struct{}{}
			ids = append(ids, stripped)
		}
	}
	if eventIDs, ok := eventStoreSubagentIDs(store, sessionID); ok {
		for _, id := range eventIDs {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *sessionServiceImpl) GetSessionSubagentTranscript(ctx context.Context, wsID, taskID, sessionID, subagentID string) ([]transcript.Event, error) {
	store, meta, err := s.authorizedSessionStore(ctx, wsID, taskID, sessionID)
	if err != nil {
		return nil, err
	}
	if subagentID == "" {
		return nil, service.ErrValidation("subagent ID is required")
	}
	if !sessions.SubagentIDPattern.MatchString(subagentID) {
		return nil, service.ErrValidation("invalid subagent ID")
	}
	// F3: serve the subagent from the event store when enabled + populated, else
	// fall back to the native subagent transcript.
	if evs, ok := eventStoreSubagentEvents(store, sessionID, subagentID); ok {
		return evs, nil
	}
	path := store.SubagentTranscriptPath(sessionID, subagentID)
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, service.ErrNotFound("subagent transcript not found")
		}
		return nil, service.ErrInternal("stat subagent transcript", statErr)
	}
	events, parseErr := backends.ParseEventsFromFile(meta.Backend, path)
	if parseErr != nil {
		return nil, service.ErrInternal("parse subagent transcript", parseErr)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

// authorizedSessionStore looks up the session's store and metadata, enforces
// task ownership, and validates the IDs. Shared by transcript and diff
// endpoints to cut boilerplate.
func (s *sessionServiceImpl) authorizedSessionStore(ctx context.Context, wsID, taskID, sessionID string) (*sessions.Store, *sessions.SessionMetadata, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, nil, service.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, nil, service.ErrValidation("invalid session ID")
	}
	store, err := s.findStoreForSession(ctx, wsID, sessionID)
	if err != nil {
		return nil, nil, err
	}
	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, service.ErrNotFound("session not found")
		}
		logger.Error("failed to load session metadata", "session_id", sessionID, "err", err)
		return nil, nil, service.ErrInternal("failed to load session", err)
	}
	if meta.TaskID != taskID {
		return nil, nil, service.ErrNotFound("session not found")
	}
	return store, meta, nil
}
