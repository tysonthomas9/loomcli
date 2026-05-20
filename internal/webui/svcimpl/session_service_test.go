package svcimpl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
)

func TestSessionServiceListTaskSessionsUsesControlPlane(t *testing.T) {
	st := memstore.New()
	finishedAt := time.Now().UTC()
	exitCode := 0
	status := domain.AgentSessionCompleted
	finishedAtPtr := &finishedAt
	exitCodePtr := &exitCode

	created, err := st.AgentSessions().Create(t.Context(), store.AgentSessionCreate{
		WorkspaceKey: "WS",
		SessionID:    "sess-1",
		AgentID:      "worker-1",
		TaskID:       "TASK-1",
		Status:       domain.AgentSessionRunning,
		Phase:        "implementation",
		Metadata: map[string]string{
			"backend":         "localdogfood",
			"transcript_path": "/tmp/sess-1/transcript.jsonl",
			"files_changed":   "1",
			"lines_added":     "2",
			"lines_removed":   "3",
			"files_touched":   "file.txt",
		},
	})
	if err != nil {
		t.Fatalf("create control-plane session: %v", err)
	}
	if _, err := st.AgentSessions().Update(t.Context(), "WS", created.SessionID, store.AgentSessionUpdate{
		Status:     &status,
		FinishedAt: &finishedAtPtr,
		ExitCode:   &exitCodePtr,
	}); err != nil {
		t.Fatalf("complete control-plane session: %v", err)
	}

	svc := NewSessionService(st, nil)
	items, err := svc.ListTaskSessions(t.Context(), "WS", "TASK-1")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.SessionID != "sess-1" || item.TaskID != "TASK-1" || item.AgentName != "worker-1" {
		t.Fatalf("item identity = %+v", item.SessionRecord)
	}
	if item.Status != sessions.StatusCompleted || item.IsActive {
		t.Fatalf("item status/isActive = %q/%v, want completed/false", item.Status, item.IsActive)
	}
	if item.Backend != "localdogfood" || !item.HasTranscript {
		t.Fatalf("item backend/transcript = %q/%v", item.Backend, item.HasTranscript)
	}
	if item.FilesChanged != 1 || item.LinesAdded != 2 || item.LinesRemoved != 3 || len(item.FilesTouched) != 1 || item.FilesTouched[0] != "file.txt" {
		t.Fatalf("item diff stats = %+v", item.SessionRecord)
	}
}

func TestSessionServiceListTaskSessionsFallsBackToFileStores(t *testing.T) {
	loomConfigDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomConfigDir)

	ctx := t.Context()
	workspacePath := t.TempDir()
	repoPath := filepath.Join(workspacePath, "repos", "source-repo")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo path: %v", err)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "WS", Name: "source-repo"}); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {
				Path:  workspacePath,
				Repos: map[string]string{"source-repo": repoPath},
			},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	sessStore, err := sessions.NewStore(repoPath)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "file-agent",
		Backend:   "codex",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	srcTranscript := filepath.Join(t.TempDir(), "codex.jsonl")
	if err := os.WriteFile(srcTranscript, []byte("{\"type\":\"session_meta\"}\n"), 0o600); err != nil {
		t.Fatalf("write source transcript: %v", err)
	}
	if err := sessStore.SyncNativeTranscript(sess.SessionID(), srcTranscript); err != nil {
		t.Fatalf("sync native transcript: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{
		TaskID:    "TASK-2",
		ExitCode:  0,
		DiffPatch: "diff --git a/file.txt b/file.txt\n",
		DiffStats: sessions.DiffStats{FilesChanged: 1, LinesAdded: 1},
	}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}

	svc := NewSessionService(st, nil)
	items, err := svc.ListTaskSessions(ctx, "WS", "TASK-2")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	item := items[0]
	if item.SessionID != sess.SessionID() || item.AgentName != "file-agent" || item.Backend != "codex" {
		t.Fatalf("item identity = %+v", item.SessionRecord)
	}
	if !item.HasTranscript || !item.HasDiff {
		t.Fatalf("transcript/diff flags = %v/%v, want true/true", item.HasTranscript, item.HasDiff)
	}
}

func TestSessionServiceListTaskSessionsSearchesRuntimeDir(t *testing.T) {
	loomConfigDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomConfigDir)

	ctx := t.Context()
	workspacePath := t.TempDir()
	runtimeDir := t.TempDir()

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {Path: workspacePath},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}

	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("new runtime session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "desktopqa",
		Backend:   "codex",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "DESKTOP-QA-3", ExitCode: 0}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}

	svc := NewSessionServiceWithRuntimeDir(st, nil, runtimeDir)
	items, err := svc.ListTaskSessions(ctx, "WS", "DESKTOP-QA-3")
	if err != nil {
		t.Fatalf("ListTaskSessions: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].SessionID != sess.SessionID() || items[0].TaskID != "DESKTOP-QA-3" || items[0].AgentName != "desktopqa" {
		t.Fatalf("item identity = %+v", items[0].SessionRecord)
	}
}

func TestSessionServiceStoresForWorkspaceAvailabilityBranches(t *testing.T) {
	ctx := t.Context()

	unavailableSvc := NewSessionServiceWithRuntimeDir(nil, nil, "").(*sessionServiceImpl)
	if _, err := unavailableSvc.storesForWorkspace(ctx, "WS"); err == nil {
		t.Fatal("storesForWorkspace without store or runtime dir returned nil error")
	}

	runtimeDir := t.TempDir()
	runtimeSvc := NewSessionServiceWithRuntimeDir(nil, nil, runtimeDir).(*sessionServiceImpl)
	stores, err := runtimeSvc.storesForWorkspace(ctx, "WS")
	if err != nil {
		t.Fatalf("storesForWorkspace runtime-only: %v", err)
	}
	if len(stores) != 1 {
		t.Fatalf("stores = %d, want 1", len(stores))
	}

	withMissingWorkspace := NewSessionServiceWithRuntimeDir(memstore.New(), nil, runtimeDir).(*sessionServiceImpl)
	stores, err = withMissingWorkspace.storesForWorkspace(ctx, "MISSING")
	if err != nil {
		t.Fatalf("storesForWorkspace with runtime fallback: %v", err)
	}
	if len(stores) != 1 {
		t.Fatalf("stores with missing workspace = %d, want runtime fallback only", len(stores))
	}
}

func TestSessionServiceAdditionalErrorAndMetadataBranches(t *testing.T) {
	ctx := t.Context()

	missingWorkspaceSvc := NewSessionServiceWithRuntimeDir(memstore.New(), nil, "").(*sessionServiceImpl)
	if _, err := missingWorkspaceSvc.storesForWorkspace(ctx, "MISSING"); err == nil {
		t.Fatal("storesForWorkspace missing workspace without runtime returned nil error")
	}
	if _, err := missingWorkspaceSvc.findStoreForSession(ctx, "MISSING", "missing-session"); err == nil {
		t.Fatal("findStoreForSession missing workspace returned nil error")
	}
	if items, err := (&sessionServiceImpl{}).controlPlaneTaskSessions(ctx, "WS", "TASK-1"); err != nil || items != nil {
		t.Fatalf("controlPlaneTaskSessions without store = %+v, %v", items, err)
	}

	runtimeDir := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if err := bootstrap.SaveStateCache(&bootstrap.StateCache{
		Workspaces: map[string]bootstrap.WorkspaceLocalState{
			"WS": {Path: runtimeDir},
		},
	}); err != nil {
		t.Fatalf("save state cache: %v", err)
	}
	duplicateSvc := NewSessionServiceWithRuntimeDir(st, nil, runtimeDir).(*sessionServiceImpl)
	stores, err := duplicateSvc.storesForWorkspace(ctx, "WS")
	if err != nil {
		t.Fatalf("storesForWorkspace duplicate runtime: %v", err)
	}
	if len(stores) != 1 {
		t.Fatalf("stores = %d, want duplicate runtime/workspace path to be skipped", len(stores))
	}

	finishedAt := time.Now().UTC()
	exitCode := 7
	rec := sessionRecordFromAgentSession(&domain.AgentSession{
		SessionID:  "sess-meta",
		AgentID:    "agent",
		CreatedAt:  finishedAt.Add(-time.Minute),
		FinishedAt: &finishedAt,
		ExitCode:   &exitCode,
		Status:     domain.AgentSessionExpired,
		Metadata: map[string]string{
			"task_id":       "TASK-META",
			"backend":       "codex",
			"files_changed": "2",
		},
	})
	if rec.TaskID != "TASK-META" || rec.Backend != "codex" || rec.Status != sessions.StatusAborted || rec.ExitCode != exitCode || rec.DurationS <= 0 {
		t.Fatalf("sessionRecordFromAgentSession metadata fallback = %+v", rec)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	hist := sessionhistory.NewStore(rdb, nil)
	historySvc := NewSessionService(nil, hist)
	if _, err := historySvc.ListSessionHistory(ctx, "WS", "bad/id"); err == nil {
		t.Fatal("ListSessionHistory invalid issue ID returned nil error")
	}

	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{AgentName: "worker", Backend: "claude", Phase: "implementation"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "TASK-CORRUPT", ExitCode: 0}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}
	subPath := sessStore.SubagentTranscriptPath(sess.SessionID(), "badjson")
	if err := os.MkdirAll(filepath.Dir(subPath), 0o755); err != nil {
		t.Fatalf("mkdir subagent path: %v", err)
	}
	if err := os.Mkdir(subPath, 0o755); err != nil {
		t.Fatalf("create unreadable subagent transcript path: %v", err)
	}
	if _, err := NewSessionServiceWithRuntimeDir(nil, nil, runtimeDir).GetSessionSubagentTranscript(ctx, "WS", "TASK-CORRUPT", sess.SessionID(), "badjson"); err == nil {
		t.Fatal("corrupt subagent transcript returned nil error")
	}
}

func TestSessionServiceAdditionalControlPlaneAndHistoryErrorBranches(t *testing.T) {
	ctx := t.Context()

	listErr := errors.New("list sessions failed")
	controlErrSvc := NewSessionService(&sessionServiceStoreOverride{
		Store:    memstore.New(),
		sessions: agentSessionListStore{err: listErr},
	}, nil).(*sessionServiceImpl)
	if _, err := controlErrSvc.controlPlaneTaskSessions(ctx, "WS", "TASK-1"); !errors.Is(err, listErr) {
		t.Fatalf("controlPlaneTaskSessions err = %v, want %v", err, listErr)
	}

	later := time.Now().UTC()
	earlier := later.Add(-time.Minute)
	controlSvc := NewSessionService(&sessionServiceStoreOverride{
		Store: memstore.New(),
		sessions: agentSessionListStore{records: []*domain.AgentSession{
			nil,
			{
				WorkspaceKey: "WS",
				SessionID:    "older",
				AgentID:      "a",
				TaskID:       "TASK-1",
				Status:       domain.AgentSessionRunning,
				CreatedAt:    earlier,
				Metadata:     map[string]string{"diff_path": "/tmp/diff.patch"},
			},
			{
				WorkspaceKey: "WS",
				SessionID:    "newer",
				AgentID:      "b",
				TaskID:       "TASK-1",
				Status:       domain.AgentSessionCompleted,
				StartedAt:    later,
				CreatedAt:    later,
				Metadata:     map[string]string{"transcript_path": "/tmp/transcript.jsonl"},
			},
		}},
	}, nil).(*sessionServiceImpl)
	items, err := controlSvc.controlPlaneTaskSessions(ctx, "WS", "TASK-1")
	if err != nil {
		t.Fatalf("controlPlaneTaskSessions sorted: %v", err)
	}
	if len(items) != 2 || items[0].SessionID != "newer" || !items[0].HasTranscript || !items[1].HasDiff {
		t.Fatalf("control-plane items = %+v", items)
	}

	missingWorkspaceSvc := NewSessionServiceWithRuntimeDir(memstore.New(), nil, "").(*sessionServiceImpl)
	if _, err := missingWorkspaceSvc.ListTaskSessions(ctx, "MISSING", "TASK-1"); err == nil {
		t.Fatal("ListTaskSessions missing workspace returned nil error")
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	hist := sessionhistory.NewStore(rdb, nil)
	historySvc := NewSessionService(nil, hist)
	_ = rdb.Close()
	if _, err := historySvc.ListSessionHistory(ctx, "WS", "TASK-1"); err == nil {
		t.Fatal("ListSessionHistory with closed redis returned nil error")
	}
	if _, err := historySvc.GetSessionScrollback(ctx, "WS", "TASK-1", "record"); err == nil {
		t.Fatal("GetSessionScrollback with closed redis returned nil error")
	}
}

type sessionServiceStoreOverride struct {
	store.Store
	sessions store.AgentSessionStore
}

func (s *sessionServiceStoreOverride) AgentSessions() store.AgentSessionStore {
	if s.sessions != nil {
		return s.sessions
	}
	return s.Store.AgentSessions()
}

type agentSessionListStore struct {
	store.AgentSessionStore
	records []*domain.AgentSession
	err     error
}

func (s agentSessionListStore) List(context.Context, string, store.AgentSessionFilter) ([]*domain.AgentSession, error) {
	return s.records, s.err
}

func TestReadScrollbackFileReturnsInternalWhenHomeDirUnavailable(t *testing.T) {
	oldUserHomeDir := userHomeDir
	userHomeDir = func() (string, error) {
		return "", errors.New("home unavailable")
	}
	t.Cleanup(func() { userHomeDir = oldUserHomeDir })

	_, err := readScrollbackFile("/.loom/session-scrollback/session.log")
	if err == nil {
		t.Fatal("expected error")
	}
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("error = %T %v, want ServiceError", err, err)
	}
	if svcErr.Kind != service.KindInternal {
		t.Fatalf("error kind = %q, want %q", svcErr.Kind, service.KindInternal)
	}
}

func TestReadScrollbackFileReturnsInternalForEmptyHomeDir(t *testing.T) {
	oldUserHomeDir := userHomeDir
	userHomeDir = func() (string, error) { return " ", nil }
	t.Cleanup(func() { userHomeDir = oldUserHomeDir })

	_, err := readScrollbackFile("/tmp/scrollback.log")
	if err == nil {
		t.Fatal("expected error")
	}
	var svcErr *service.ServiceError
	if !errors.As(err, &svcErr) || svcErr.Kind != service.KindInternal {
		t.Fatalf("error = %T %v, want internal ServiceError", err, err)
	}
}

func TestSessionServiceDetailTranscriptDiffAndSubagents(t *testing.T) {
	runtimeDir := t.TempDir()
	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "worker",
		Backend:   "codex",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{
		TaskID:    "TASK-4",
		ExitCode:  0,
		DiffPatch: "diff --git a/a.txt b/a.txt\n",
	}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(sessStore.SubagentTranscriptPath(sess.SessionID(), "abc123")), 0o755); err != nil {
		t.Fatalf("mkdir subagents: %v", err)
	}
	if err := os.WriteFile(sessStore.SubagentTranscriptPath(sess.SessionID(), "abc123"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write subagent: %v", err)
	}

	svc := NewSessionServiceWithRuntimeDir(nil, nil, runtimeDir)
	detail, err := svc.GetSession(t.Context(), "WS", "TASK-4", sess.SessionID())
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if detail.SessionID != sess.SessionID() || detail.IsActive {
		t.Fatalf("detail = %+v", detail)
	}
	events, err := svc.GetSessionTranscript(t.Context(), "WS", "TASK-4", sess.SessionID())
	if err != nil {
		t.Fatalf("GetSessionTranscript: %v", err)
	}
	if events == nil {
		t.Fatal("transcript events should be an empty slice, not nil")
	}
	diff, err := svc.GetSessionDiff(t.Context(), "WS", "TASK-4", sess.SessionID())
	if err != nil || !strings.Contains(diff, "diff --git") {
		t.Fatalf("diff = %q err=%v", diff, err)
	}
	subagents, err := svc.ListSessionSubagents(t.Context(), "WS", "TASK-4", sess.SessionID())
	if err != nil {
		t.Fatalf("ListSessionSubagents: %v", err)
	}
	if len(subagents) != 1 || subagents[0] != "abc123" {
		t.Fatalf("subagents = %+v", subagents)
	}
	subEvents, err := svc.GetSessionSubagentTranscript(t.Context(), "WS", "TASK-4", sess.SessionID(), "abc123")
	if err != nil {
		t.Fatalf("GetSessionSubagentTranscript: %v", err)
	}
	if subEvents == nil {
		t.Fatal("subagent transcript events should be an empty slice, not nil")
	}
	if _, err := svc.GetSessionSubagentTranscript(t.Context(), "WS", "TASK-4", sess.SessionID(), ""); err == nil {
		t.Fatal("empty subagent id returned nil error")
	}
	if _, err := svc.GetSessionSubagentTranscript(t.Context(), "WS", "TASK-4", sess.SessionID(), "bad/id"); err == nil {
		t.Fatal("invalid subagent id returned nil error")
	}
	if _, err := svc.GetSessionSubagentTranscript(t.Context(), "WS", "TASK-4", sess.SessionID(), "missing"); err == nil {
		t.Fatal("missing subagent transcript returned nil error")
	}
}

func TestSessionServiceDetailOwnershipAndMissingArtifacts(t *testing.T) {
	runtimeDir := t.TempDir()
	sessStore, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sess, err := sessStore.CreateSession(sessions.CreateOptions{
		AgentName: "worker",
		Backend:   "codex",
		Phase:     "implementation",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sess.Finalize(sessions.FinalizeOptions{TaskID: "TASK-6", ExitCode: 0}); err != nil {
		t.Fatalf("finalize session: %v", err)
	}

	svc := NewSessionServiceWithRuntimeDir(nil, nil, runtimeDir)
	if _, err := svc.GetSession(t.Context(), "WS", "TASK-OTHER", sess.SessionID()); err == nil {
		t.Fatal("GetSession for wrong task returned nil error")
	}
	if _, err := svc.GetSession(t.Context(), "WS", "TASK-6", "missing-session"); err == nil {
		t.Fatal("GetSession for missing session returned nil error")
	}
	if _, err := svc.GetSessionTranscript(t.Context(), "WS", "TASK-OTHER", sess.SessionID()); err == nil {
		t.Fatal("GetSessionTranscript for wrong task returned nil error")
	}
	if _, err := svc.GetSessionTranscript(t.Context(), "WS", "TASK-6", "bad/session"); err == nil {
		t.Fatal("GetSessionTranscript for invalid session returned nil error")
	}
	if _, err := svc.ListSessionSubagents(t.Context(), "WS", "TASK-OTHER", sess.SessionID()); err == nil {
		t.Fatal("ListSessionSubagents for wrong task returned nil error")
	}
	if _, err := svc.GetSessionDiff(t.Context(), "WS", "TASK-OTHER", sess.SessionID()); err == nil {
		t.Fatal("GetSessionDiff for wrong task returned nil error")
	}
	if _, err := svc.GetSessionDiff(t.Context(), "WS", "TASK-6", sess.SessionID()); err == nil {
		t.Fatal("GetSessionDiff without diff returned nil error")
	}
	if _, err := svc.GetSessionDiff(t.Context(), "WS", "TASK-6", "bad/session"); err == nil {
		t.Fatal("GetSessionDiff for invalid session returned nil error")
	}
}

func TestSessionServiceValidationAndScrollbackHelpers(t *testing.T) {
	svc := NewSessionServiceWithRuntimeDir(nil, nil, t.TempDir())
	if _, err := svc.ListTaskSessions(t.Context(), "WS", "../bad"); err == nil {
		t.Fatal("invalid task id returned nil error")
	}
	if _, err := svc.GetSession(t.Context(), "WS", "TASK", "../bad"); err == nil {
		t.Fatal("invalid session id returned nil error")
	}
	if _, err := svc.GetSession(t.Context(), "WS", "../bad", "sess"); err == nil {
		t.Fatal("invalid get task id returned nil error")
	}
	if _, err := svc.GetSessionTranscript(t.Context(), "WS", "../bad", "sess"); err == nil {
		t.Fatal("invalid transcript task id returned nil error")
	}
	if _, err := svc.ListSessionSubagents(t.Context(), "WS", "../bad", "sess"); err == nil {
		t.Fatal("invalid subagent list task id returned nil error")
	}
	if _, err := svc.GetSessionDiff(t.Context(), "WS", "../bad", "sess"); err == nil {
		t.Fatal("invalid diff task id returned nil error")
	}
	if _, err := svc.GetSessionDiff(t.Context(), "WS", "TASK", ""); err == nil {
		t.Fatal("empty diff session id returned nil error")
	}
	if _, err := svc.GetSessionScrollback(t.Context(), "WS", "bad/id", "record"); err == nil {
		t.Fatal("nil history should return unavailable before validation")
	}
	if _, err := svc.ListSessionHistory(t.Context(), "WS", "TASK-5"); err == nil {
		t.Fatal("nil history store should return unavailable")
	}
	records := []sessionhistory.SessionRecord{{ID: "one"}, {ID: "two"}}
	if got := findSessionRecord(records, "two"); got == nil || got.ID != "two" {
		t.Fatalf("findSessionRecord = %+v", got)
	}
	if got := findSessionRecord(records, "missing"); got != nil {
		t.Fatalf("find missing = %+v", got)
	}

	home := t.TempDir()
	oldUserHomeDir := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = oldUserHomeDir })
	scrollDir := filepath.Join(home, ".loom", "session-scrollback")
	if err := os.MkdirAll(scrollDir, 0o755); err != nil {
		t.Fatalf("mkdir scrollback: %v", err)
	}
	path := filepath.Join(scrollDir, "session.log")
	if err := os.WriteFile(path, []byte("one\ntwo"), 0o600); err != nil {
		t.Fatalf("write scrollback: %v", err)
	}
	got, err := readScrollbackFile(path)
	if err != nil {
		t.Fatalf("read scrollback: %v", err)
	}
	if got.Content != "one\ntwo" || got.Lines != 2 {
		t.Fatalf("scrollback = %+v", got)
	}
	if _, err := readScrollbackFile(filepath.Join(home, "outside.log")); err == nil {
		t.Fatal("outside scrollback path returned nil error")
	}
	if _, err := readScrollbackFile(filepath.Join(scrollDir, "missing.log")); err == nil {
		t.Fatal("missing scrollback path returned nil error")
	}
}

func TestSessionServiceHistoryAndScrollback(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	hist := sessionhistory.NewStore(rdb, nil)

	home := t.TempDir()
	oldUserHomeDir := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = oldUserHomeDir })

	scrollDir := filepath.Join(home, ".loom", "session-scrollback")
	if err := os.MkdirAll(scrollDir, 0o755); err != nil {
		t.Fatalf("mkdir scrollback: %v", err)
	}
	scrollback := filepath.Join(scrollDir, "record.log")
	if err := os.WriteFile(scrollback, []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatalf("write scrollback: %v", err)
	}

	now := time.Now().UTC()
	records := []sessionhistory.SessionRecord{
		{ID: "without-scrollback", SessionName: "sess-1", IssueID: "TASK-5", Status: "completed", StartedAt: now.Add(-time.Hour)},
		{ID: "with-scrollback", SessionName: "sess-2", IssueID: "TASK-5", Status: "completed", StartedAt: now, ScrollbackPath: scrollback},
	}
	for _, rec := range records {
		if err := hist.Add(t.Context(), "WS", rec); err != nil {
			t.Fatalf("add history %s: %v", rec.ID, err)
		}
	}

	svc := NewSessionService(nil, hist)
	listed, err := svc.ListSessionHistory(t.Context(), "WS", "TASK-5")
	if err != nil {
		t.Fatalf("ListSessionHistory: %v", err)
	}
	if len(listed) != 2 || listed[0].ID != "with-scrollback" {
		t.Fatalf("history = %+v, want most recent first", listed)
	}

	got, err := svc.GetSessionScrollback(t.Context(), "WS", "TASK-5", "with-scrollback")
	if err != nil {
		t.Fatalf("GetSessionScrollback: %v", err)
	}
	if got.Content != "alpha\nbeta\n" || got.Lines != 3 {
		t.Fatalf("scrollback = %+v", got)
	}
	if _, err := svc.GetSessionScrollback(t.Context(), "WS", "bad/id", "with-scrollback"); err == nil {
		t.Fatal("invalid issue ID returned nil error")
	}
	if _, err := svc.GetSessionScrollback(t.Context(), "WS", "TASK-5", ""); err == nil {
		t.Fatal("empty record ID returned nil error")
	}
	if _, err := svc.GetSessionScrollback(t.Context(), "WS", "TASK-5", "missing"); err == nil {
		t.Fatal("missing record returned nil error")
	}
	if _, err := svc.GetSessionScrollback(t.Context(), "WS", "TASK-5", "without-scrollback"); err == nil {
		t.Fatal("record without scrollback returned nil error")
	}
}

func TestAgentSessionStatusMappings(t *testing.T) {
	cases := []struct {
		status domain.AgentSessionStatus
		want   sessions.SessionStatus
		active bool
	}{
		{domain.AgentSessionCompleted, sessions.StatusCompleted, false},
		{domain.AgentSessionFailed, sessions.StatusFailed, false},
		{domain.AgentSessionCancelled, sessions.StatusAborted, false},
		{domain.AgentSessionExpired, sessions.StatusAborted, false},
		{domain.AgentSessionRunning, sessions.StatusRunning, true},
	}
	for _, tt := range cases {
		if got := sessionStatusFromAgentSession(tt.status); got != tt.want {
			t.Fatalf("sessionStatusFromAgentSession(%q) = %q, want %q", tt.status, got, tt.want)
		}
		if got := isActiveAgentSession(tt.status); got != tt.active {
			t.Fatalf("isActiveAgentSession(%q) = %t, want %t", tt.status, got, tt.active)
		}
	}
}
