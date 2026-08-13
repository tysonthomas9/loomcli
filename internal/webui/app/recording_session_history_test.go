package app

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/tysonthomas9/loomcli/internal/webui/sessionhistory"
	"github.com/tysonthomas9/loomcli/internal/webui/tabmeta"
	"github.com/tysonthomas9/loomcli/internal/webui/terminal"
)

func TestRecordingLifecycleCompletesExistingSessionHistory(t *testing.T) {
	mr := miniredis.RunT(t)
	tabClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	historyClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = tabClient.Close()
		_ = historyClient.Close()
	})

	const (
		workspace = "ws"
		session   = "agent-1"
		issue     = "loom-123"
	)
	tabs := tabmeta.NewStore(tabClient, slog.Default())
	now := time.Now().UTC()
	if err := tabs.Set(t.Context(), &tabmeta.TabMetadata{
		SessionName: session,
		Workspace:   workspace,
		Label:       "Agent",
		IssueID:     issue,
		Backend:     "codex",
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		t.Fatalf("seed tab metadata: %v", err)
	}

	history := sessionhistory.NewStore(historyClient, slog.Default())
	recordingRoot := t.TempDir()
	recordings := terminal.NewRecordingStore(recordingRoot, tabClient)
	app := &Server{
		recordings:          recordings,
		tabMetaStore:        tabs,
		sessionHistoryStore: history,
	}
	wireRecordingSessionHistory(app)

	key := terminal.SessionKey{Workspace: workspace, Name: session}
	recorder, err := recordings.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	// Scroll "durable output" off the 24-row screen so a durable line
	// commits: the started lifecycle hook is deferred until a recording
	// proves non-trivial, and a trivial one is discarded without any
	// session-history record.
	recorder.Append([]byte("durable output\r\n" + strings.Repeat("filler\r\n", 24)))
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	waitForSessionHistory(t, func() bool {
		records, listErr := history.List(t.Context(), workspace, issue)
		return listErr == nil && len(records) == 1 && records[0].Status == "completed"
	})
	records, err := history.List(t.Context(), workspace, issue)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %#v, want one", records)
	}
	record := records[0]
	if record.Status != "completed" || record.EndedAt == nil || record.Backend != "codex" {
		t.Fatalf("record = %#v", record)
	}
	meta, _, _, err := recordings.Meta(t.Context(), key)
	if err != nil {
		t.Fatalf("recording Meta: %v", err)
	}
	wantScrollbackPath := filepath.Join(recordingRoot, workspace, session, "generations", meta.Generation, "lines.jsonl")
	if record.ScrollbackPath != wantScrollbackPath {
		t.Fatalf("scrollback path = %q, want %q", record.ScrollbackPath, wantScrollbackPath)
	}
}

func TestRecordingLifecycleUsesExactGenerationAndPersistsIssue(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	const (
		workspace = "ws"
		session   = "agent-reused"
		issue     = "loom-456"
	)
	tabs := tabmeta.NewStore(client, slog.Default())
	now := time.Now().UTC()
	if err := tabs.Set(t.Context(), &tabmeta.TabMetadata{
		SessionName: session, Workspace: workspace, IssueID: issue,
		Backend: "codex", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed tab metadata: %v", err)
	}
	history := sessionhistory.NewStore(client, slog.Default())
	if err := history.Add(t.Context(), workspace, sessionhistory.SessionRecord{
		ID: session + ":1", SessionName: session, IssueID: issue,
		Status: "active", StartedAt: time.UnixMilli(1).UTC(),
	}); err != nil {
		t.Fatalf("seed stale history: %v", err)
	}
	recordingRoot := t.TempDir()
	recordings := terminal.NewRecordingStore(recordingRoot, client)
	wireRecordingSessionHistory(&Server{
		recordings: recordings, tabMetaStore: tabs, sessionHistoryStore: history,
	})

	key := terminal.SessionKey{Workspace: workspace, Name: session}
	recorder, err := recordings.StartRecording(key, 80, 24)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	// Commit durable lines immediately so the deferred started hook fires
	// and the "active" record awaited below is created.
	recorder.Append([]byte(strings.Repeat("seed\r\n", 25)))
	meta, _, _, err := recordings.Meta(t.Context(), key)
	if err != nil {
		t.Fatalf("recording Meta: %v", err)
	}
	recordingDir := filepath.Join(recordingRoot, workspace, session, "generations", meta.Generation)
	wantID := session + ":" + fmt.Sprint(meta.StartedAt)
	wantPath := filepath.Join(recordingDir, "lines.jsonl")
	waitForSessionHistory(t, func() bool {
		records, listErr := history.List(t.Context(), workspace, issue)
		if listErr != nil || len(records) != 2 {
			return false
		}
		for _, record := range records {
			if record.ID == wantID {
				return record.Status == "active" && record.ScrollbackPath == wantPath
			}
		}
		return false
	})

	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitForSessionHistory(t, func() bool {
		records, listErr := history.List(t.Context(), workspace, issue)
		if listErr != nil {
			return false
		}
		statuses := make(map[string]string, len(records))
		for _, record := range records {
			statuses[record.ID] = record.Status
		}
		return statuses[session+":1"] == "active" && statuses[wantID] == "completed"
	})

	metaData, err := os.ReadFile(filepath.Join(recordingDir, "meta.json"))
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	var metaJSON map[string]any
	if err := json.Unmarshal(metaData, &metaJSON); err != nil {
		t.Fatalf("decode meta.json: %v", err)
	}
	if metaJSON["issueId"] != issue {
		t.Fatalf("meta issueId = %#v, want %q", metaJSON["issueId"], issue)
	}
}

func TestRecordingCompletionAfterRestartUsesPersistedIssue(t *testing.T) {
	testRecordingRestartCompletion(t, false)
}

func TestRecordingStartupSweepCompletesClosedHistory(t *testing.T) {
	testRecordingRestartCompletion(t, true)
}

func testRecordingRestartCompletion(t *testing.T, leaveClosed bool) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	const (
		workspace = "ws"
		session   = "agent-restart"
		issue     = "loom-789"
	)
	recordingRoot := t.TempDir()
	seedStore := terminal.NewRecordingStore(recordingRoot, nil)
	key := terminal.SessionKey{Workspace: workspace, Name: session}
	recorder, err := seedStore.StartRecording(key, 8, 2)
	if err != nil {
		t.Fatalf("seed StartRecording: %v", err)
	}
	recorder.Append([]byte("restart output\r\n"))
	if err := recorder.Close(); err != nil {
		t.Fatalf("seed Close: %v", err)
	}
	recordingMeta, _, _, err := seedStore.Meta(t.Context(), key)
	if err != nil {
		t.Fatalf("seed Meta: %v", err)
	}
	recordingDir := filepath.Join(recordingRoot, workspace, session, "generations", recordingMeta.Generation)
	recordID := session + ":" + fmt.Sprint(recordingMeta.StartedAt)
	metaPath := filepath.Join(recordingDir, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read seed meta: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("decode seed meta: %v", err)
	}
	meta["issueId"] = issue
	if !leaveClosed {
		meta["closed"] = false
	}
	data, err = json.Marshal(meta)
	if err != nil {
		t.Fatalf("encode seed meta: %v", err)
	}
	if err := os.WriteFile(metaPath, data, 0o600); err != nil {
		t.Fatalf("rewrite seed meta: %v", err)
	}

	history := sessionhistory.NewStore(client, slog.Default())
	if err := history.Add(t.Context(), workspace, sessionhistory.SessionRecord{
		ID: recordID, SessionName: session, IssueID: issue, Status: "active",
		StartedAt:      time.UnixMilli(recordingMeta.StartedAt).UTC(),
		ScrollbackPath: filepath.Join(recordingDir, "lines.jsonl"),
	}); err != nil {
		t.Fatalf("seed session history: %v", err)
	}
	restarted := terminal.NewRecordingStore(recordingRoot, client)
	wireRecordingSessionHistory(&Server{
		recordings: restarted, tabMetaStore: tabmeta.NewStore(client, slog.Default()),
		sessionHistoryStore: history,
	})
	if !leaveClosed {
		if _, _, _, err := restarted.Meta(t.Context(), key); err != nil {
			t.Fatalf("recover recording: %v", err)
		}
	}
	waitForSessionHistory(t, func() bool {
		records, listErr := history.List(t.Context(), workspace, issue)
		return listErr == nil && len(records) == 1 && records[0].Status == "completed"
	})
}

func waitForSessionHistory(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for session history state")
}
