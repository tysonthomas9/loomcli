package agent

import (
	"testing"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// newTestSessionStore creates a sessions store in a temp dir with one session
// whose metadata TaskID is taskID (possibly empty).
func newTestSessionStore(t *testing.T, taskID string) (*sessions.Store, string) {
	t.Helper()
	store, err := sessions.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore error: %v", err)
	}
	sess, err := store.CreateSession(sessions.CreateOptions{
		AgentName: "nova",
		Backend:   "claude",
		TaskID:    taskID,
		Prompt:    "test prompt",
	})
	if err != nil {
		t.Fatalf("CreateSession error: %v", err)
	}
	return store, sess.SessionID()
}

func TestStampAssignedTaskID_StampsWhenEmpty(t *testing.T) {
	store, sid := newTestSessionStore(t, "")
	t.Setenv("LOOM_ASSIGNED_TASK_ID", "TASK-99")

	stampAssignedTaskID(store, sid)

	meta, err := store.LoadMetadata(sid)
	if err != nil {
		t.Fatalf("LoadMetadata error: %v", err)
	}
	if meta.TaskID != "TASK-99" {
		t.Errorf("TaskID = %q, want %q", meta.TaskID, "TASK-99")
	}
}

func TestStampAssignedTaskID_NoopWhenEnvUnset(t *testing.T) {
	store, sid := newTestSessionStore(t, "")
	t.Setenv("LOOM_ASSIGNED_TASK_ID", "")

	stampAssignedTaskID(store, sid)

	meta, err := store.LoadMetadata(sid)
	if err != nil {
		t.Fatalf("LoadMetadata error: %v", err)
	}
	if meta.TaskID != "" {
		t.Errorf("TaskID = %q, want empty", meta.TaskID)
	}
}

func TestStampAssignedTaskID_NoopWhenAlreadySet(t *testing.T) {
	store, sid := newTestSessionStore(t, "TASK-ORIG")
	t.Setenv("LOOM_ASSIGNED_TASK_ID", "TASK-NEW")

	stampAssignedTaskID(store, sid)

	meta, err := store.LoadMetadata(sid)
	if err != nil {
		t.Fatalf("LoadMetadata error: %v", err)
	}
	if meta.TaskID != "TASK-ORIG" {
		t.Errorf("TaskID = %q, want %q (must not overwrite)", meta.TaskID, "TASK-ORIG")
	}
}
