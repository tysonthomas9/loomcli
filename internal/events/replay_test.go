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

func TestReplayFromFile_OldUnderscoreFormat(t *testing.T) {
	ms := newTestStore()
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// Write raw JSON lines with old underscore-format type strings.
	// We cannot use NewEvent here because it would produce new dot-notation types.
	rawLines := `{"type":"task_completed","timestamp":"2026-01-01T00:00:00Z","agent":"agent1","role":"dev","epic_id":"epic1","data":{"task_id":"t1","duration":"5m0s","files_changed":3,"lines_added":100,"lines_removed":20}}
{"type":"task_failed","timestamp":"2026-01-01T00:00:00Z","agent":"agent2","role":"dev","epic_id":"epic1","data":{"task_id":"t2","error":"build failed"}}
{"type":"agent_started","timestamp":"2026-01-01T00:00:00Z","agent":"agent1","data":{"pid":1234}}
{"type":"agent_restarted","timestamp":"2026-01-01T00:00:00Z","agent":"agent1","data":{"pid":1235,"restart_count":1}}
{"type":"agent_stopped","timestamp":"2026-01-01T00:00:00Z","agent":"agent1","data":{"pid":1234,"exit_code":0}}
`
	if err := os.WriteFile(path, []byte(rawLines), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := ms.ReplayFromFile(path)
	if err != nil {
		t.Fatalf("ReplayFromFile: %v", err)
	}
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}

	snap := ms.Snapshot()
	if snap.TotalTasksCompleted != 1 {
		t.Errorf("TotalTasksCompleted = %d, want 1", snap.TotalTasksCompleted)
	}
	if snap.TotalTasksFailed != 1 {
		t.Errorf("TotalTasksFailed = %d, want 1", snap.TotalTasksFailed)
	}
	if snap.TotalRestarts != 1 {
		t.Errorf("TotalRestarts = %d, want 1", snap.TotalRestarts)
	}
}

func TestReplayFromFile_NewDotNotationFormat(t *testing.T) {
	ms := newTestStore()
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// Write raw JSON lines with new dot-notation format type strings.
	rawLines := `{"type":"task.completed","timestamp":"2026-01-01T00:00:00Z","agent":"agent1","role":"dev","epic_id":"epic1","data":{"task_id":"t1","duration":"3m0s","files_changed":2,"lines_added":50,"lines_removed":10}}
{"type":"task.failed","timestamp":"2026-01-01T00:00:00Z","agent":"agent2","role":"dev","data":{"task_id":"t2","error":"timeout"}}
{"type":"agent.started","timestamp":"2026-01-01T00:00:00Z","agent":"agent1","data":{"pid":5678}}
`
	if err := os.WriteFile(path, []byte(rawLines), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := ms.ReplayFromFile(path)
	if err != nil {
		t.Fatalf("ReplayFromFile: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}

	snap := ms.Snapshot()
	if snap.TotalTasksCompleted != 1 {
		t.Errorf("TotalTasksCompleted = %d, want 1", snap.TotalTasksCompleted)
	}
	if snap.TotalTasksFailed != 1 {
		t.Errorf("TotalTasksFailed = %d, want 1", snap.TotalTasksFailed)
	}
}
