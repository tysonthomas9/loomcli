package events

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadJSONLDirIncludesRotatedBackupsChronologically(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	t1 := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	backup := `{"type":"agent.started","timestamp":"` + t1.Format(time.RFC3339Nano) + `","agent":"agent-1"}` + "\n"
	current := "not-json\n" + `{"type":"agent.stopped","timestamp":"` + t2.Format(time.RFC3339Nano) + `","agent":"agent-1"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events-2026-08-14.jsonl.1"), []byte(backup), 0600); err != nil {
		t.Fatalf("write backup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events-2026-08-14.jsonl"), []byte(current), 0600); err != nil {
		t.Fatalf("write current: %v", err)
	}

	got, err := ReadJSONLDir(dir)
	if err != nil {
		t.Fatalf("ReadJSONLDir: %v", err)
	}
	if len(got) != 2 || got[0].Type != AgentStarted || got[1].Type != AgentStopped {
		t.Fatalf("events = %+v", got)
	}
}

func TestBusEmitIsImmediatelyReadableByAuditService(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bus := NewBus(dir)
	t.Cleanup(func() { _ = bus.Close() })
	event, err := NewEvent(AgentStarted, "agent-1", "task", "", AgentStartedData{PID: 42})
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if err := bus.Emit(event); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	got, err := ReadJSONLDir(dir)
	if err != nil {
		t.Fatalf("ReadJSONLDir: %v", err)
	}
	if len(got) != 1 || got[0].Agent != "agent-1" || got[0].Type != AgentStarted {
		t.Fatalf("events = %+v", got)
	}
}
