package svcimpl

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

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

// sessionServiceImpl is the concrete implementation of SessionService.
type sessionServiceImpl struct {
	store     store.Store
	histStore *sessionhistory.Store
}

// NewSessionService creates a new SessionService implementation.
func NewSessionService(st store.Store, histStore *sessionhistory.Store) service.SessionService {
	return &sessionServiceImpl{store: st, histStore: histStore}
}

// storesForWorkspace returns session stores for all repos in the workspace.
// Agent worktrees store sessions in their own directories, so we need to
// search across all repos to find sessions for a given task.
func (s *sessionServiceImpl) storesForWorkspace(ctx context.Context, wsID string) ([]*sessions.Store, error) {
	if s.store == nil {
		return nil, service.ErrUnavailable("session store not available")
	}
	wsData, err := storeadapter.BuildWorkspaceDataForKey(ctx, s.store, wsID)
	if err != nil || wsData == nil {
		return nil, service.ErrNotFound("workspace not found")
	}
	var stores []*sessions.Store
	// Include workspace root
	if st, err := sessions.NewStore(wsData.Path); err == nil {
		stores = append(stores, st)
	}
	// Include each repo (agent worktrees have their own sessions dir)
	for _, repo := range wsData.Repos {
		if repo.Path == wsData.Path {
			continue // already added
		}
		if st, err := sessions.NewStore(repo.Path); err == nil {
			stores = append(stores, st)
		}
	}
	if len(stores) == 0 {
		return nil, service.ErrInternal("no session stores available", nil)
	}
	return stores, nil
}

// findStoreForSession returns the first store that has metadata for the given session.
func (s *sessionServiceImpl) findStoreForSession(ctx context.Context, wsID, sessionID string) (*sessions.Store, error) {
	stores, err := s.storesForWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	for _, store := range stores {
		if _, err := store.LoadMetadata(sessionID); err == nil {
			return store, nil
		}
	}
	return nil, service.ErrNotFound("session not found")
}

func (s *sessionServiceImpl) ListTaskSessions(ctx context.Context, wsID, taskID string) ([]service.SessionListItem, error) {
	stores, err := s.storesForWorkspace(ctx, wsID)
	if err != nil {
		return nil, err
	}
	if taskID == "" || !validTaskID.MatchString(taskID) {
		return nil, service.ErrValidation("invalid task ID: must match [a-zA-Z0-9._-]+")
	}

	var items []service.SessionListItem
	for _, store := range stores {
		records, err := store.SessionsByTask(taskID)
		if err != nil {
			continue
		}
		for _, rec := range records {
			item := service.SessionListItem{
				SessionRecord: rec,
				IsActive:      rec.Status == sessions.StatusRunning,
			}
			if info, err := os.Stat(store.NativeTranscriptPath(rec.SessionID)); err == nil && info.Size() > 0 {
				item.HasTranscript = true
			}
			if diff, err := store.ReadDiff(rec.SessionID); err == nil && diff != "" {
				item.HasDiff = true
			}
			items = append(items, item)
		}
	}
	return items, nil
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
	homeDir, _ := os.UserHomeDir()
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
