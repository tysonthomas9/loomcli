package svcimpl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
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
	addStore(workspaceRuntimeRoot(wsID))

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

// workspaceRuntimeRoot returns the conventional workspace runtime directory
// (~/.loom/workspaces/<wsID>) where daemon/desktop workers write their session
// stores, or "" when the directory does not exist. The existence check keeps
// storesForWorkspace from creating runtime dirs for unknown workspace IDs.
func workspaceRuntimeRoot(wsID string) string {
	if wsID == "" || wsID == "." || wsID == ".." || strings.ContainsAny(wsID, "/\\") {
		return ""
	}
	loomDir := bootstrap.LoomDir()
	if loomDir == "" {
		return ""
	}
	// Same convention as config.GetWorkspaceDir (webui must not import internal/cli).
	dir := filepath.Join(loomDir, "workspaces", wsID)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
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
			enrichSessionUsageFromTranscript(sessStore, &item.SessionRecord)
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
			enrichSessionUsageFromTranscript(sessStore, &item.SessionRecord)
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

// enrichSessionUsageFromTranscript backfills token fields from the on-disk native
// transcript when session metadata still has zeros (common for completed Go Codex
// daemon runs whose collector finalize never persisted usage).
func enrichSessionUsageFromTranscript(store *sessions.Store, rec *sessions.SessionRecord) {
	if localHasUsage(*rec) {
		return
	}
	u := store.TranscriptUsage(rec.SessionID)
	if u.IsZero() {
		return
	}
	rec.InputTokens = u.InputTokens
	rec.OutputTokens = u.OutputTokens
	rec.CacheReadTokens = u.CacheReadTokens
	rec.CacheWriteTokens = u.CacheWriteTokens
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
	store, err := s.findStoreForSession(ctx, wsID, sessionID)
	if err != nil {
		// Control-plane (flue task-run) sessions live in the agent-session store, not a file
		// store. ListTaskSessions and GetSessionTranscript already resolve them; GetSession must
		// too, else the single-session GET 404s on a session the list endpoint returned.
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
		return nil, service.ErrInternal("failed to load session", err)
	}
	if rec.TaskID != taskID && (rec.Metadata == nil || rec.Metadata["task_id"] != taskID) {
		return nil, service.ErrNotFound("session not found")
	}
	return rec, nil
}

// controlPlaneSession resolves a session detail from the agent-session (control-plane) store,
// the fallback for flue task-run sessions that have no file-store metadata. It reuses the
// AgentSession->SessionRecord mapping ListTaskSessions uses, so list/get/transcript agree.
func (s *sessionServiceImpl) controlPlaneSession(ctx context.Context, wsID, taskID, sessionID string) (*service.SessionDetailData, error) {
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
	store, _, err := s.authorizedSessionStore(ctx, wsID, taskID, sessionID)
	if err != nil {
		if !serviceErrorNotFound(err) {
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
		if cpEvents, cpErr := s.controlPlaneSessionTranscript(ctx, wsID, taskID, sessionID); cpErr == nil {
			return cpEvents, nil
		}
		logger.Error("failed to load native transcript", "session_id", sessionID, "err", loadErr)
		return nil, service.ErrInternal("failed to load transcript", loadErr)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

func (s *sessionServiceImpl) controlPlaneSessionTranscript(ctx context.Context, wsID, taskID, sessionID string) ([]transcript.Event, error) {
	rec, err := s.controlPlaneSessionRecord(ctx, wsID, taskID, sessionID)
	if err != nil {
		return nil, err
	}
	transcriptRef := ""
	if rec.Metadata != nil {
		transcriptRef = strings.TrimSpace(rec.Metadata["transcript_ref"])
	}
	if transcriptRef == "" {
		return nil, service.ErrNotFound("transcript not found")
	}
	data, err := s.readTranscriptRef(ctx, wsID, transcriptRef)
	if err != nil {
		return nil, service.ErrInternal("failed to load transcript", err)
	}
	events, err := parseCanonicalTranscriptBytes(data)
	if err != nil {
		return nil, service.ErrInternal("failed to parse transcript", err)
	}
	if events == nil {
		events = []transcript.Event{}
	}
	return events, nil
}

const maxControlPlaneTranscriptBytes = 16 << 20

func (s *sessionServiceImpl) readTranscriptRef(ctx context.Context, wsID, ref string) ([]byte, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("empty transcript ref")
	}
	if strings.HasPrefix(ref, "artifact://") {
		artifactID := strings.TrimSpace(strings.TrimPrefix(ref, "artifact://"))
		if artifactID == "" {
			return nil, errors.New("empty artifact transcript ref")
		}
		if s.store == nil {
			return nil, errors.New("artifact store unavailable")
		}
		if reader, ok := s.store.Artifacts().(store.ArtifactContentReader); ok {
			data, err := reader.ReadContent(ctx, wsID, artifactID)
			if err == nil {
				return data, nil
			}
			if !errors.Is(err, domain.ErrNotFound) {
				return nil, err
			}
		}
		artifact, err := s.store.Artifacts().Get(ctx, wsID, artifactID)
		if err != nil {
			return nil, err
		}
		return readTranscriptURI(ctx, artifact.URI)
	}
	return readTranscriptURI(ctx, ref)
}

func readTranscriptURI(ctx context.Context, rawURI string) ([]byte, error) {
	rawURI = strings.TrimSpace(rawURI)
	switch {
	case strings.HasPrefix(rawURI, "file://"):
		parsed, err := url.Parse(rawURI)
		if err != nil {
			return nil, err
		}
		path := parsed.Path
		if path == "" {
			path = parsed.Host
		}
		if path == "" {
			return nil, errors.New("empty file transcript ref")
		}
		return os.ReadFile(path) //nolint:gosec // refs are emitted by the trusted runner/control-plane path.
	case strings.HasPrefix(rawURI, "http://"), strings.HasPrefix(rawURI, "https://"):
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURI, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, errors.New("transcript ref returned non-success status")
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxControlPlaneTranscriptBytes+1))
		if err != nil {
			return nil, err
		}
		if len(body) > maxControlPlaneTranscriptBytes {
			return nil, errors.New("transcript is too large")
		}
		return body, nil
	default:
		return nil, errors.New("unsupported transcript ref")
	}
}

func parseCanonicalTranscriptBytes(data []byte) ([]transcript.Event, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return []transcript.Event{}, nil
	}
	if trimmed[0] == '[' {
		var events []transcript.Event
		if err := json.Unmarshal(trimmed, &events); err != nil {
			return nil, err
		}
		return events, nil
	}
	lines := bytes.Split(trimmed, []byte("\n"))
	events := make([]transcript.Event, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event transcript.Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func serviceErrorNotFound(err error) bool {
	var svcErr *service.ServiceError
	return errors.As(err, &svcErr) && svcErr.Kind == service.KindNotFound
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

func (s *sessionServiceImpl) GetSessionDiff(ctx context.Context, wsID, taskID, sessionID string) (string, error) {
	store, _, err := s.authorizedSessionStore(ctx, wsID, taskID, sessionID)
	if err != nil {
		if !serviceErrorNotFound(err) {
			return "", err
		}
		return s.controlPlaneSessionDiff(ctx, wsID, taskID, sessionID)
	}

	diff, diffErr := store.ReadDiff(sessionID)
	if diffErr != nil {
		if errors.Is(diffErr, os.ErrNotExist) {
			cpDiff, cpErr := s.controlPlaneSessionDiff(ctx, wsID, taskID, sessionID)
			if cpErr == nil {
				return cpDiff, nil
			}
			if serviceErrorNotFound(cpErr) {
				return "", service.ErrNotFound("diff not found")
			}
			return "", cpErr
		}
		logger.Error("failed to read diff", "session_id", sessionID, "err", diffErr)
		return "", service.ErrInternal("failed to read diff", diffErr)
	}
	if diff == "" {
		if cpDiff, cpErr := s.controlPlaneSessionDiff(ctx, wsID, taskID, sessionID); cpErr == nil {
			return cpDiff, nil
		}
	}
	return diff, nil
}

func (s *sessionServiceImpl) controlPlaneSessionDiff(ctx context.Context, wsID, taskID, sessionID string) (string, error) {
	rec, err := s.controlPlaneSessionRecord(ctx, wsID, taskID, sessionID)
	if err != nil {
		return "", err
	}
	artifactID := ""
	if rec.Metadata != nil {
		artifactID = controlPlaneDiffArtifactRef(rec.Metadata)
	}
	if artifactID == "" && rec.Metadata != nil {
		artifactID, err = s.diffArtifactIDForTaskRun(ctx, wsID, rec.Metadata["task_run_id"])
		if err != nil {
			return "", err
		}
	}
	if artifactID == "" {
		return "", service.ErrNotFound("diff not found")
	}
	diff, err := s.readControlPlaneArtifactText(ctx, wsID, artifactID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", service.ErrNotFound("diff not found")
		}
		return "", service.ErrInternal("failed to read diff", err)
	}
	return diff, nil
}

func controlPlaneDiffArtifactRef(metadata map[string]string) string {
	if metadata == nil {
		return ""
	}
	return normalizeArtifactRef(firstNonEmptySessionValue(
		metadata["patch_artifact_id"],
		metadata["diff_artifact_id"],
		metadata["patch_ref"],
		metadata["diff_ref"],
	))
}

func normalizeArtifactRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "artifact://") {
		ref = strings.TrimSpace(strings.TrimPrefix(ref, "artifact://"))
	}
	return ref
}

func (s *sessionServiceImpl) diffArtifactIDForTaskRun(ctx context.Context, wsID, taskRunID string) (string, error) {
	taskRunID = strings.TrimSpace(taskRunID)
	if taskRunID == "" || s.store == nil {
		return "", nil
	}
	artifacts, err := s.store.Artifacts().List(ctx, wsID, store.ArtifactFilter{
		OwnerType: "task_run",
		OwnerID:   taskRunID,
		Type:      "patch",
		Status:    "finalized",
		Limit:     1,
	})
	if err != nil {
		return "", service.ErrInternal("failed to list patch artifacts", err)
	}
	for _, artifact := range artifacts {
		if artifact != nil && strings.TrimSpace(artifact.ArtifactID) != "" {
			return artifact.ArtifactID, nil
		}
	}
	return "", nil
}

func (s *sessionServiceImpl) readControlPlaneArtifactText(ctx context.Context, wsID, artifactID string) (string, error) {
	artifactID = normalizeArtifactRef(artifactID)
	if artifactID == "" {
		return "", errors.New("empty artifact ref")
	}
	if isSupportedControlPlaneURI(artifactID) {
		data, err := readTranscriptURI(ctx, artifactID)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if s.store == nil {
		return "", errors.New("artifact store unavailable")
	}
	if reader, ok := s.store.Artifacts().(store.ArtifactContentReader); ok {
		data, err := reader.ReadContent(ctx, wsID, artifactID)
		if err == nil {
			return string(data), nil
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return "", err
		}
	}
	artifact, err := s.store.Artifacts().Get(ctx, wsID, artifactID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(artifact.URI) == "" {
		return "", domain.ErrNotFound
	}
	data, err := readTranscriptURI(ctx, artifact.URI)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func isSupportedControlPlaneURI(ref string) bool {
	return strings.HasPrefix(ref, "file://") || strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://")
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
