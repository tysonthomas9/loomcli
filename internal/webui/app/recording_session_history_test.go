package app

import (
	"log/slog"
	"path/filepath"
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
	recorder.Append([]byte("durable output\r\n"))
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

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
