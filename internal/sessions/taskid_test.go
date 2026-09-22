package sessions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateSessionStampsTaskID(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}

	sess, err := store.CreateSession(CreateOptions{
		AgentName: "nova",
		Backend:   "claude",
		TaskID:    "TASK-42",
		Prompt:    "do the thing",
	})
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}
	if sess.Meta.TaskID != "TASK-42" {
		t.Errorf("Meta.TaskID = %q, want %q", sess.Meta.TaskID, "TASK-42")
	}

	metaPath := filepath.Join(dir, "sessions", sess.SessionID(), "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read metadata.json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal metadata.json: %v", err)
	}
	if got, _ := raw["task_id"].(string); got != "TASK-42" {
		t.Errorf("metadata.json task_id = %q, want %q", got, "TASK-42")
	}
}

func TestCreateSessionWithoutTaskID(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}

	sess, err := store.CreateSession(CreateOptions{
		AgentName: "nova",
		Backend:   "claude",
		Prompt:    "no task",
	})
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}
	if sess.Meta.TaskID != "" {
		t.Errorf("Meta.TaskID = %q, want empty", sess.Meta.TaskID)
	}

	meta, err := store.LoadMetadata(sess.SessionID())
	if err != nil {
		t.Fatalf("LoadMetadata error: %v", err)
	}
	if meta.TaskID != "" {
		t.Errorf("persisted TaskID = %q, want empty", meta.TaskID)
	}
}
