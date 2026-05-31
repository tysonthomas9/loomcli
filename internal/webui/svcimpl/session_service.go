package svcimpl

import (
	"context"
	"errors"
	"io"
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

var userHomeDir = os.UserHomeDir

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
	var stores []*sessions.Store
	seen := make(map[string]struct{})
	addStore := func(runtimeDir string) {
		if runtimeDir == "" {
			return
		}
		key := filepath.Clean(runtimeDir)
		if abs, err := filepath.Abs(runtimeDir); err == nil {
			key = filepath.Clean(abs)
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		if st, err := sessions.NewStore(runtimeDir); err == nil {
			stores = append(stores, st)
		}
	}

	addStore(s.runtimeDir)

	if s.store != nil {
		wsData, err := storeadapter.BuildWorkspaceDataForKey(ctx, s.store, wsID)
		if err != nil || wsData == nil {
			if len(stores) == 0 {
				return nil, service.ErrNotFound("workspace not found")
			}
		} else {
			// Include workspace root.
			addStore(wsData.Path)
			// Include each repo (agent worktrees may have their own sessions dir).
			for _, repo := range wsData.Repos {
				addStore(repo.Path)
			}
		}
	} else if len(stores) == 0 {
		return nil, service.ErrUnavailable("session store not available")
	}

	if len(stores) == 0 {
		return nil, service.ErrInternal("no session stores available", nil)
	}
	return stores, nil
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

func (s *sessionServiceImpl) controlPlaneTaskSessions(ctx context.Context, wsID, taskID string) ([]service.SessionListItem, error) {
	if s.store == nil {
		return nil, nil
	}
	records, err := s.store.AgentSessions().List(ctx, wsID, store.AgentSessionFilter{TaskID: taskID})
	if err != nil {
		return nil, err
	}
	// Resolve local stores once so artifact presence reflects on-disk truth, not the
	// metadata keys (stamped at creation/completion). Best-effort: a remote-only
	// deployment may have no local store, in which case we fall back to metadata.
	var stores []*sessions.Store
	if len(records) > 0 {
		stores, _ = s.storesForWorkspace(ctx, wsID)
	}
	items := make([]service.SessionListItem, 0, len(records))
	for _, rec := range records {
		if rec == nil {
			continue
		}
		item := service.SessionListItem{
			SessionRecord: sessionRecordFromAgentSession(rec),
			IsActive:      isActiveAgentSession(rec.Status),
		}
		fillControlPlaneArtifactFlags(&item, stores, rec)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].StartedAt.After(items[j].StartedAt)
	})
	return items, nil
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
		if diff, err := st.ReadDiff(rec.SessionID); err == nil && diff != "" {
			item.HasDiff = true
		}
		return
	}
	if rec.Metadata != nil {
		item.HasTranscript = rec.Metadata["transcript_path"] != ""
		item.HasDiff = rec.Metadata["diff_path"] != "" || rec.Metadata["diff_artifact_id"] != ""
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
		backend = rec.Metadata["backend"]
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
	store, err := s.findStoreForSession(ctx, wsID, sessionID)
	if err != nil {
		return nil, err
	}

	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, service.ErrNotFound("session not found")
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

func (s *sessionServiceImpl) GetSessionTranscript(ctx context.Context, wsID, taskID, sessionID string) ([]transcript.Event, error) {
	store, _, err := s.authorizedSessionStore(ctx, wsID, taskID, sessionID)
	if err != nil {
		return nil, err
	}
	events, loadErr := store.LoadNativeEvents(sessionID)
	if loadErr != nil {
		logger.Error("failed to load native transcript", "session_id", sessionID, "err", loadErr)
		return nil, service.ErrInternal("failed to load transcript", loadErr)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
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
	for _, name := range names {
		// Format: agent-<id>.jsonl
		stripped := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
		if stripped != "" {
			ids = append(ids, stripped)
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

func (s *sessionServiceImpl) GetSessionDiff(ctx context.Context, wsID, taskID, sessionID string) (string, error) {
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return "", service.ErrValidation("invalid task ID")
	}
	if sessionID == "" || !validSessionID.MatchString(sessionID) {
		return "", service.ErrValidation("invalid session ID")
	}
	store, err := s.findStoreForSession(ctx, wsID, sessionID)
	if err != nil {
		return "", err
	}

	// Enforce task ownership
	meta, err := store.LoadMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", service.ErrNotFound("session not found")
		}
		logger.Error("failed to load session metadata", "session_id", sessionID, "err", err)
		return "", service.ErrInternal("failed to load session", err)
	}
	if meta.TaskID != taskID {
		return "", service.ErrNotFound("session not found")
	}

	diff, diffErr := store.ReadDiff(sessionID)
	if diffErr != nil {
		if errors.Is(diffErr, os.ErrNotExist) {
			return "", service.ErrNotFound("diff not found")
		}
		logger.Error("failed to read diff", "session_id", sessionID, "err", diffErr)
		return "", service.ErrInternal("failed to read diff", diffErr)
	}
	return diff, nil
}

func (s *sessionServiceImpl) ListSessionHistory(ctx context.Context, wsID, issueID string) ([]sessionhistory.SessionRecord, error) {
	if s.histStore == nil {
		return nil, service.ErrUnavailable("session history not available (no Redis)")
	}
	if err := sessionhistory.ValidateIssueID(issueID); err != nil {
		return nil, service.ErrValidation(err.Error())
	}

	records, err := s.histStore.List(ctx, wsID, issueID)
	if err != nil {
		logger.Error("failed to list session history", "issue_id", issueID, "err", err)
		return nil, service.ErrInternal("failed to list session history", err)
	}
	return records, nil
}

func (s *sessionServiceImpl) GetSessionScrollback(ctx context.Context, wsID, issueID, recordID string) (*service.SessionScrollbackResult, error) {
	if s.histStore == nil {
		return nil, service.ErrUnavailable("session history not available (no Redis)")
	}
	if err := sessionhistory.ValidateIssueID(issueID); err != nil {
		return nil, service.ErrValidation(err.Error())
	}
	if recordID == "" {
		return nil, service.ErrValidation("record ID is required")
	}

	records, err := s.histStore.List(ctx, wsID, issueID)
	if err != nil {
		logger.Error("failed to get session history for scrollback", "issue_id", issueID, "err", err)
		return nil, service.ErrInternal("failed to get session history", err)
	}

	found := findSessionRecord(records, recordID)
	if found == nil {
		return nil, service.ErrNotFound("session record not found")
	}

	if found.ScrollbackPath == "" {
		return nil, service.ErrNotFound("no scrollback available for this session")
	}

	return readScrollbackFile(found.ScrollbackPath)
}

// findSessionRecord returns the record with the given ID, or nil if not found.
func findSessionRecord(records []sessionhistory.SessionRecord, id string) *sessionhistory.SessionRecord {
	for i := range records {
		if records[i].ID == id {
			return &records[i]
		}
	}
	return nil
}

// readScrollbackFile validates the path, reads the file, and returns the result.
func readScrollbackFile(scrollbackPath string) (*service.SessionScrollbackResult, error) {
	homeDir, err := userHomeDir()
	if err != nil {
		return nil, service.ErrInternal("resolve home directory", err)
	}
	if strings.TrimSpace(homeDir) == "" {
		return nil, service.ErrInternal("resolve home directory", errors.New("empty home directory"))
	}
	expectedPrefix := filepath.Clean(homeDir+"/.loom/session-scrollback") + string(os.PathSeparator)
	cleanPath := filepath.Clean(scrollbackPath)
	if !strings.HasPrefix(cleanPath+string(os.PathSeparator), expectedPrefix) {
		return nil, service.ErrValidation("invalid scrollback path")
	}

	f, err := os.Open(cleanPath) //nolint:gosec // path cleaned and prefix-validated above
	if err != nil {
		if os.IsNotExist(err) {
			return nil, service.ErrNotFound("scrollback file not found")
		}
		logger.Error("failed to open scrollback file", "path", scrollbackPath, "err", err)
		return nil, service.ErrInternal("failed to read scrollback", err)
	}
	defer f.Close()

	content, err := io.ReadAll(f)
	if err != nil {
		logger.Error("failed to read scrollback file", "path", scrollbackPath, "err", err)
		return nil, service.ErrInternal("failed to read scrollback", err)
	}

	text := string(content)
	lines := 0
	if text != "" {
		lines = strings.Count(text, "\n") + 1
	}
	return &service.SessionScrollbackResult{Content: text, Lines: lines}, nil
}
