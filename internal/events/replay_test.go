package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestJSONL(t *testing.T, dir string, events []Event) string {
	t.Helper()
	path := filepath.Join(dir, "events.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	return path
}

func TestReplayFromFile_ValidJSONL(t *testing.T) {
	ms := newTestStore()
	dir := t.TempDir()

	e1, _ := NewEvent(TaskCompleted, "agent1", "dev", "epic1", TaskCompletedData{
		TaskID: "t1", Duration: Duration{5 * time.Minute}, LinesAdded: 50,
	})
	e1.Timestamp = ms.now().Add(-10 * time.Minute)

	e2, _ := NewEvent(TaskFailed, "agent1", "dev", "", TaskFailedData{
		TaskID: "t2", Error: "err",
	})
	e2.Timestamp = ms.now().Add(-5 * time.Minute)

	path := writeTestJSONL(t, dir, []Event{e1, e2})

	count, err := ms.ReplayFromFile(path)
	if err != nil {
		t.Fatalf("ReplayFromFile: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}

	snap := ms.Snapshot()
	if snap.TotalTasksCompleted != 1 || snap.TotalTasksFailed != 1 {
		t.Errorf("unexpected totals: completed=%d failed=%d", snap.TotalTasksCompleted, snap.TotalTasksFailed)
	}
}

func TestReplayFromFile_MalformedLines(t *testing.T) {
	ms := newTestStore()
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	e, _ := NewEvent(TaskCompleted, "agent1", "dev", "", TaskCompletedData{
		TaskID: "t1", Duration: Duration{time.Minute},
	})
	e.Timestamp = ms.now()
	raw, _ := json.Marshal(e)

	content := string(raw) + "\n{bad json\n" + string(raw) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := ms.ReplayFromFile(path)
	if err != nil {
		t.Fatalf("ReplayFromFile: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2 (malformed line skipped)", count)
	}
}

func TestReplayFromFile_EmptyFile(t *testing.T) {
	ms := newTestStore()
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := ms.ReplayFromFile(path)
	if err != nil {
		t.Fatalf("ReplayFromFile: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestReplayFromFile_NonexistentFile(t *testing.T) {
	ms := newTestStore()
	_, err := ms.ReplayFromFile("/nonexistent/path/events.jsonl")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}
