package sessioncoord

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/query/runcapture"
	"github.com/tysonthomas9/loomcli/internal/domain"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/artifacts/transcript"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/apperrors"
	"github.com/tysonthomas9/loomcli/internal/webui/storeadapter"
)

// Compile-time check.
var _ SessionService = (*sessionServiceImpl)(nil)
var _ AgentSessionTranscriptService = (*sessionServiceImpl)(nil)

var userHomeDir = os.UserHomeDir

// errNoUsableSessionStores distinguishes an absent or unreadable local session
// store from unrelated control-plane failures. Transcript and diff reads may
// recover from this condition through their durable control-plane artifacts.
var errNoUsableSessionStores = errors.New("no usable local session stores")

func sessionControlPlaneReadError(message string, err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, artifactsmodule.ErrNotFound):
		return apperrors.NewServiceError(
			apperrors.KindNotFound,
			message,
			err,
		)
	case errors.Is(err, domain.ErrRateLimited):
		return apperrors.NewServiceError(
			apperrors.KindRateLimited,
			message,
			err,
		)
	case errors.Is(err, domain.ErrUnavailable), errors.Is(err, artifactsmodule.ErrUnavailable):
		return apperrors.NewServiceError(
			apperrors.KindUnavailable,
			message,
			err,
		)
	default:
		return apperrors.ErrInternal(message, err)
	}
}

// sessionServiceImpl is the concrete implementation of SessionService.
type sessionServiceImpl struct {
	store      ProjectionReader
	artifacts  artifactsmodule.QueryAPI
	captures   runcapture.API
	histStore  HistoryReader
	runtimeDir string
}

// ProjectionReader is the exact persisted read surface needed to compose task
// and interactive session evidence. It deliberately excludes mutation stores.
type ProjectionReader interface {
	storeadapter.WorkspaceTopologyReader
	TaskRuns() store.TaskRunStore
	AgentSessions() store.AgentSessionStore
}

// ProjectionReaderWithArtifactQueries is the convenience composition surface
// used by tests and narrow callers that hold a concrete persistence adapter.
// Runtime Web UI composition injects the Artifacts capability QueryAPI
// directly instead of routing it through the horizontal Store interface.
type ProjectionReaderWithArtifactQueries interface {
	ProjectionReader
	ArtifactQueries() artifactsmodule.QueryStore
}

// NewSessionService creates a new SessionService implementation.
func NewSessionService(st ProjectionReaderWithArtifactQueries, histStore HistoryReader) SessionService {
	return NewSessionServiceWithRuntimeDir(st, histStore, "")
}

// NewSessionServiceWithRuntimeDir creates a SessionService that also searches
// the daemon/runtime session store used by local desktop mode.
func NewSessionServiceWithRuntimeDir(st ProjectionReaderWithArtifactQueries, histStore HistoryReader, runtimeDir string) SessionService {
	return NewSessionServiceWithArtifactQueries(st, histStore, runtimeDir, composeArtifactQueries(st))
}

// NewSessionServiceWithArtifactQueries composes session UI projections over
// the Artifacts owner query surface. Production and boundary tests can inject
// the capability directly; the convenience constructors derive it from the
// owner-owned query port exposed by the projection reader.
func NewSessionServiceWithArtifactQueries(
	st ProjectionReader,
	histStore HistoryReader,
	runtimeDir string,
	artifactQueries artifactsmodule.QueryAPI,
) SessionService {
	return NewSessionServiceWithRunCaptures(st, histStore, runtimeDir, artifactQueries, nil)
}

// NewSessionServiceWithRunCaptures composes durable transcript reads through
// the owner-validated Run Capture projection. The remaining parameters are
// transitional until the rest of the legacy session UI is removed.
func NewSessionServiceWithRunCaptures(
	st ProjectionReader,
	histStore HistoryReader,
	runtimeDir string,
	artifactQueries artifactsmodule.QueryAPI,
	captures runcapture.API,
) SessionService {
	return &sessionServiceImpl{
		store: st, artifacts: artifactQueries, captures: captures,
		histStore: histStore, runtimeDir: runtimeDir,
	}
}

func composeArtifactQueries(st ProjectionReaderWithArtifactQueries) artifactsmodule.QueryAPI {
	if st == nil {
		return nil
	}
	queries, err := artifactsmodule.NewQuery(st.ArtifactQueries())
	if err != nil {
		return nil
	}
	return queries
}

// storesForWorkspace returns session stores for all repos in the workspace.
// Agent worktrees store sessions in their own directories, so we need to
// search across all repos to find sessions for a given task.
func (s *sessionServiceImpl) storesForWorkspace(ctx context.Context, wsID string) ([]*sessions.Store, error) {
	collection := newSessionStoreCollection(ctx)
	s.addRuntimeSessionStoreCandidates(wsID, collection)
	if err := s.addWorkspaceSessionStores(ctx, wsID, collection); err != nil {
		return nil, err
	}
	if len(collection.stores) == 0 {
		return nil, apperrors.ErrInternal("no session stores available", errNoUsableSessionStores)
	}
	return collection.stores, nil
}

type sessionStoreCollection struct {
	ctx    context.Context
	stores []*sessions.Store
	seen   map[string]struct{}
}

func newSessionStoreCollection(ctx context.Context) *sessionStoreCollection {
	return &sessionStoreCollection{ctx: ctx, seen: make(map[string]struct{})}
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
	st, err := sessions.NewStore(c.ctx, runtimeDir)
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

// addRuntimeSessionStoreCandidates adds only the requested workspace's runtime
// in the conventional <root>/workspaces/<workspace> layout. A local serve often
// runs from LOCALMODE while serving other workspaces; adding the active runtime
// unconditionally would let a request for one workspace discover another one's
// sessions. Non-conventional single-workspace layouts retain the configured
// runtime fallback.
func (s *sessionServiceImpl) addRuntimeSessionStoreCandidates(wsID string, collection *sessionStoreCollection) {
	if runtimeDirUsesWorkspaceLayout(s.runtimeDir) {
		if siblingRuntimeDir, ok := siblingWorkspaceRuntimeDir(s.runtimeDir, wsID); ok {
			collection.addExistingRuntimeDir(siblingRuntimeDir)
		}
		return
	}
	collection.addExistingRuntimeDir(s.runtimeDir)
}

func runtimeDirUsesWorkspaceLayout(runtimeDir string) bool {
	if runtimeDir == "" {
		return false
	}
	absRuntimeDir, err := filepath.Abs(runtimeDir)
	if err != nil {
		return false
	}
	return filepath.Base(filepath.Dir(absRuntimeDir)) == "workspaces"
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
			return apperrors.ErrUnavailable("session store not available")
		}
		return nil
	}
	wsData, err := storeadapter.BuildWorkspaceDataForKey(ctx, s.store, wsID)
	if err != nil {
		if len(collection.stores) > 0 {
			return nil
		}
		if errors.Is(err, domain.ErrNotFound) {
			return apperrors.ErrNotFound("workspace not found")
		}
		return sessionControlPlaneReadError(
			"failed to resolve session stores",
			err,
		)
	}
	if wsData == nil {
		if len(collection.stores) == 0 {
			return apperrors.ErrNotFound("workspace not found")
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
		collection := newSessionStoreCollection(ctx)
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
	return nil, apperrors.ErrNotFound("session not found")
}

func (s *sessionServiceImpl) ListTaskSessions(ctx context.Context, wsID, taskID string) ([]SessionListItem, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, apperrors.ErrValidation("invalid task ID: must match [a-zA-Z0-9._-]+")
	}
	if items, err := s.controlPlaneTaskSessions(ctx, wsID, taskID); err == nil && len(items) > 0 {
		s.enrichSessionListItemsFromFileStores(ctx, wsID, taskID, items)
		return items, nil
	}

	stores, err := s.storesForWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}

	var items []SessionListItem
	for _, sessStore := range stores {
		records, err := sessStore.SessionsByTask(taskID)
		if err != nil {
			continue
		}
		for _, rec := range records {
			item := SessionListItem{
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

func (s *sessionServiceImpl) enrichSessionListItemsFromFileStores(ctx context.Context, wsID, taskID string, items []SessionListItem) {
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

//nolint:funlen // Keep canonical Execution and Interaction projections plus evidence enrichment in one deterministic query.
func (s *sessionServiceImpl) controlPlaneTaskSessions(ctx context.Context, wsID, taskID string) ([]SessionListItem, error) {
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
	items := make([]SessionListItem, 0, len(taskRuns)+len(records))
	representedSessionIDs := make(map[string]struct{}, len(taskRuns))
	for _, run := range taskRuns {
		if run == nil || run.WorkspaceKey != wsID || run.TaskID != taskID {
			continue
		}
		item := SessionListItem{
			SessionRecord: sessionRecordFromTaskRun(run),
			IsActive:      isActiveTaskRun(run.Status),
		}
		fillExecutionTaskRunEvidence(&item, run, artifactKinds)
		items = append(items, item)
		representedSessionIDs[item.SessionID] = struct{}{}
	}
	for _, rec := range records {
		if rec == nil {
			continue
		}
		if _, represented := representedSessionIDs[rec.SessionID]; represented {
			continue
		}
		item := SessionListItem{
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

func fillExecutionEvidence(item *SessionListItem, metadata map[string]string) {
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
func fillControlPlaneArtifactFlags(item *SessionListItem, stores []*sessions.Store, rec *domain.AgentSession) {
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

func (s *sessionServiceImpl) GetSession(ctx context.Context, wsID, taskID, sessionID string) (*SessionDetailData, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, apperrors.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, apperrors.ErrValidation("invalid session ID")
	}
	// Execution owns batch-attempt identity. Resolve it before consulting the
	// local interactive-session store so a colliding local ID cannot shadow the
	// canonical TaskRun lifecycle or evidence.
	if run, runErr := s.executionTaskRunForSession(ctx, wsID, taskID, sessionID); runErr == nil {
		return &SessionDetailData{
			SessionMetadata: sessions.SessionMetadata{SessionRecord: sessionRecordFromTaskRun(run)},
			IsActive:        isActiveTaskRun(run.Status),
		}, nil
	} else if !serviceErrorNotFound(runErr) {
		return nil, runErr
	}
	store, err := s.findStoreForSession(ctx, wsID, sessionID)
	if err != nil {
		// Execution TaskRuns and Interaction sessions do not
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
		return nil, apperrors.ErrInternal("failed to load session", err)
	}

	// Enforce task ownership — session must belong to the requested task.
	if meta.TaskID != taskID {
		return nil, apperrors.ErrNotFound("session not found")
	}

	return &SessionDetailData{
		SessionMetadata: *meta,
		IsActive:        meta.Status == sessions.StatusRunning,
	}, nil
}

func (s *sessionServiceImpl) controlPlaneSessionRecord(ctx context.Context, wsID, taskID, sessionID string) (*domain.AgentSession, error) {
	if s.store == nil {
		return nil, apperrors.ErrNotFound("session not found")
	}
	rec, err := s.store.AgentSessions().Get(ctx, wsID, sessionID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, apperrors.ErrNotFound("session not found")
		}
		logger.Error("failed to load control-plane session", "workspace_id", wsID, "task_id", taskID, "session_id", sessionID, "err", err)
		return nil, sessionControlPlaneReadError(
			"failed to load session",
			err,
		)
	}
	if rec.TaskID != taskID && (rec.Metadata == nil || rec.Metadata["task_id"] != taskID) {
		return nil, apperrors.ErrNotFound("session not found")
	}
	return rec, nil
}

// controlPlaneSession resolves batch attempts from Execution TaskRun first and
// task-associated interactive sessions from Interaction second.
func (s *sessionServiceImpl) controlPlaneSession(ctx context.Context, wsID, taskID, sessionID string) (*SessionDetailData, error) {
	run, runErr := s.executionTaskRunForSession(ctx, wsID, taskID, sessionID)
	if runErr == nil {
		return &SessionDetailData{
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
	return &SessionDetailData{
		SessionMetadata: sessions.SessionMetadata{SessionRecord: sessionRecordFromAgentSession(rec)},
		IsActive:        isActiveAgentSession(rec.Status),
	}, nil
}

func (s *sessionServiceImpl) GetSessionTranscript(ctx context.Context, wsID, taskID, sessionID string) ([]transcript.Event, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, apperrors.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, apperrors.ErrValidation("invalid session ID")
	}
	// TaskRun artifacts are the canonical transcript for batch attempts. A
	// same-named local interaction session must not win.
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
		var svcErr *apperrors.ServiceError
		if errors.As(cpErr, &svcErr) &&
			(svcErr.Kind == apperrors.KindNotFound || svcErr.Kind == apperrors.KindUnavailable) {
			return nil, cpErr
		}
		logger.Error("failed to load native transcript", "session_id", sessionID, "err", loadErr)
		return nil, apperrors.ErrInternal("failed to load transcript", loadErr)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

func serviceErrorNotFound(err error) bool {
	var svcErr *apperrors.ServiceError
	return errors.As(err, &svcErr) && svcErr.Kind == apperrors.KindNotFound
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
		return nil, apperrors.ErrInternal("failed to list subagents", err)
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
		return nil, apperrors.ErrValidation("subagent ID is required")
	}
	if !sessions.SubagentIDPattern.MatchString(subagentID) {
		return nil, apperrors.ErrValidation("invalid subagent ID")
	}
	// F3: serve the subagent from the event store when enabled + populated, else
	// fall back to the native subagent transcript.
	if evs, ok := eventStoreSubagentEvents(store, sessionID, subagentID); ok {
		return evs, nil
	}
	path := store.SubagentTranscriptPath(sessionID, subagentID)
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, apperrors.ErrNotFound("subagent transcript not found")
		}
		return nil, apperrors.ErrInternal("stat subagent transcript", statErr)
	}
	events, parseErr := sessions.ParseNativeEventsFromFile(meta.Backend, path)
	if parseErr != nil {
		return nil, apperrors.ErrInternal("parse subagent transcript", parseErr)
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
		return nil, nil, apperrors.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return nil, nil, apperrors.ErrValidation("invalid session ID")
	}
	store, err := s.findStoreForSession(ctx, wsID, sessionID)
	if err != nil {
		return nil, nil, err
	}
	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, apperrors.ErrNotFound("session not found")
		}
		logger.Error("failed to load session metadata", "session_id", sessionID, "err", err)
		return nil, nil, apperrors.ErrInternal("failed to load session", err)
	}
	if meta.TaskID != taskID {
		return nil, nil, apperrors.ErrNotFound("session not found")
	}
	return store, meta, nil
}
