package sessions

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQueryFiltersDedupesAndSkipsBadIndexLines(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	now := time.Now().UTC()
	records := []SessionRecord{
		{
			SessionID: "s1",
			TaskID:    "task-1",
			EpicID:    "epic-1",
			AgentName: "nova",
			Backend:   "codex",
			StartedAt: now.Add(-time.Hour),
			Status:    StatusRunning,
		},
		{
			SessionID: "s2",
			TaskID:    "task-2",
			EpicID:    "epic-1",
			AgentName: "spark",
			Backend:   "claude",
			StartedAt: now.Add(-30 * time.Minute),
			Status:    StatusCompleted,
		},
		{
			SessionID: "s1",
			TaskID:    "task-1",
			EpicID:    "epic-1",
			AgentName: "nova",
			Backend:   "codex",
			StartedAt: now.Add(-time.Hour),
			Status:    StatusCompleted,
			ExitCode:  0,
		},
	}

	indexPath := filepath.Join(store.dir, "index.jsonl")
	f, err := os.Create(indexPath)
	if err != nil {
		t.Fatalf("create index: %v", err)
	}
	if _, err := f.WriteString("\n{bad-json\n"); err != nil {
		t.Fatalf("write corrupt index line: %v", err)
	}
	for _, rec := range records {
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal record: %v", err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			t.Fatalf("write record: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close index: %v", err)
	}

	got, err := store.Query(Filter{
		TaskID:    "task-1",
		EpicID:    "epic-1",
		AgentName: "nova",
		Backend:   "codex",
		Status:    StatusCompleted,
		Since:     now.Add(-2 * time.Hour),
		Until:     now,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != "s1" || got[0].Status != StatusCompleted {
		t.Fatalf("filtered records = %+v", got)
	}

	none, err := store.Query(Filter{Backend: "claude", Since: now})
	if err != nil {
		t.Fatalf("Query none: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("future query records = %+v", none)
	}
}

func TestReadDiffSuccessMissingAndInvalidSessionID(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	sessionDir := filepath.Join(store.dir, "s1")
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "diff.patch"), []byte("diff --git a/a b/a\n"), 0600); err != nil {
		t.Fatalf("write diff: %v", err)
	}

	diff, err := store.ReadDiff("s1")
	if err != nil {
		t.Fatalf("ReadDiff: %v", err)
	}
	if diff != "diff --git a/a b/a\n" {
		t.Fatalf("diff = %q", diff)
	}
	if _, err := store.ReadDiff("missing"); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing ReadDiff err = %v", err)
	}
	if _, err := store.ReadDiff("../bad"); err == nil {
		t.Fatal("invalid session ID err = nil")
	}
}
