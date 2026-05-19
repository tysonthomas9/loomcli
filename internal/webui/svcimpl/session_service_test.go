package svcimpl

import (
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

func TestSessionServiceValidationAndScrollbackHelpers(t *testing.T) {
	svc := NewSessionServiceWithRuntimeDir(nil, nil, t.TempDir())
	if _, err := svc.ListTaskSessions(t.Context(), "WS", "../bad"); err == nil {
		t.Fatal("invalid task id returned nil error")
	}
	if _, err := svc.GetSession(t.Context(), "WS", "TASK", "../bad"); err == nil {
		t.Fatal("invalid session id returned nil error")
	}
	if _, err := svc.GetSessionDiff(t.Context(), "WS", "../bad", "sess"); err == nil {
		t.Fatal("invalid diff task id returned nil error")
	}
	if _, err := svc.GetSessionScrollback(t.Context(), "WS", "bad/id", "record"); err == nil {
		t.Fatal("nil history should return unavailable before validation")
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
