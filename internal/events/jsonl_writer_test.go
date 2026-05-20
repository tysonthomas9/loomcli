package events

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONLWriter_WritesValidJSONL(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONLWriter(dir, defaultMaxSize, defaultMaxBackups)
	defer w.Close()

	ts := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		e, _ := NewEvent(TaskClaimed, "a1", "task", "", TaskClaimedData{TaskID: "t1"})
		e.Timestamp = ts
		if err := w.Write(e); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := filepath.Join(dir, "events-2026-03-04.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("line %d invalid JSON: %v", count, err)
		}
		if e.Type != TaskClaimed {
			t.Errorf("line %d: Type = %q", count, e.Type)
		}
		count++
	}
	if count != 10 {
		t.Errorf("got %d lines, want 10", count)
	}
}

func TestJSONLWriter_DayRotation(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONLWriter(dir, defaultMaxSize, defaultMaxBackups)
	defer w.Close()

	day1 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

	e1, _ := NewEvent(AgentStarted, "a1", "task", "", AgentStartedData{PID: 1})
	e1.Timestamp = day1
	if err := w.Write(e1); err != nil {
		t.Fatalf("Write day1: %v", err)
	}

	e2, _ := NewEvent(AgentStopped, "a1", "task", "", AgentStoppedData{PID: 1})
	e2.Timestamp = day2
	if err := w.Write(e2); err != nil {
		t.Fatalf("Write day2: %v", err)
	}
	w.Close()

	for _, name := range []string{"events-2026-03-01.jsonl", "events-2026-03-02.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected file %s: %v", name, err)
		}
	}
}

func TestJSONLWriter_SizeRotation(t *testing.T) {
	dir := t.TempDir()
	// Small maxSize to trigger rotation quickly
	w := NewJSONLWriter(dir, 200, 3)
	defer w.Close()

	ts := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		e, _ := NewEvent(TaskClaimed, "a1", "task", "", TaskClaimedData{TaskID: "t1", Title: "Some task title"})
		e.Timestamp = ts
		if err := w.Write(e); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	w.Close()

	// Check that backup files were created
	base := filepath.Join(dir, "events-2026-03-04.jsonl")
	if _, err := os.Stat(base); err != nil {
		t.Errorf("base file missing: %v", err)
	}
	if _, err := os.Stat(base + ".1"); err != nil {
		t.Errorf("backup .1 missing: %v", err)
	}
}

func TestJSONLWriter_LazyOpen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir")
	w := NewJSONLWriter(dir, defaultMaxSize, defaultMaxBackups)

	// Dir should not exist yet
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("dir should not exist before first write")
	}

	ts := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	e, _ := NewEvent(AgentStarted, "a1", "task", "", AgentStartedData{PID: 1})
	e.Timestamp = ts
	if err := w.Write(e); err != nil {
		t.Fatalf("Write: %v", err)
	}
	w.Close()

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dir should exist after write: %v", err)
	}
}

func TestJSONLWriter_Close_FlushesBuffer(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONLWriter(dir, defaultMaxSize, defaultMaxBackups)

	ts := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	e, _ := NewEvent(TaskStarted, "a1", "task", "", TaskStartedData{TaskID: "t1"})
	e.Timestamp = ts
	if err := w.Write(e); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Before close, data may be buffered
	w.Close()

	// After close, file should have content
	path := filepath.Join(dir, "events-2026-03-04.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("file should have content after close")
	}
}

func TestJSONLWriter_CloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONLWriter(dir, defaultMaxSize, defaultMaxBackups)

	ts := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	e, _ := NewEvent(TaskStarted, "a1", "task", "", TaskStartedData{TaskID: "t1"})
	e.Timestamp = ts
	w.Write(e)

	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close should be no-op: %v", err)
	}
}

func TestJSONLWriter_WriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONLWriter(dir, defaultMaxSize, defaultMaxBackups)
	w.Close()

	e, _ := NewEvent(TaskStarted, "a1", "task", "", TaskStartedData{TaskID: "t1"})
	e.Timestamp = time.Now()
	err := w.Write(e)
	if err == nil {
		t.Error("expected error writing after close")
	}
}

func TestJSONLWriter_WriteErrorBranches(t *testing.T) {
	w := NewJSONLWriter(t.TempDir(), defaultMaxSize, defaultMaxBackups)
	err := w.Write(Event{Timestamp: time.Now(), Data: json.RawMessage("{bad")})
	if err == nil || !strings.Contains(err.Error(), "marshaling event") {
		t.Fatalf("invalid JSON data err = %v, want marshaling event", err)
	}

	dir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(dir, []byte("file"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	w = NewJSONLWriter(dir, defaultMaxSize, defaultMaxBackups)
	err = w.Write(Event{Timestamp: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "creating events dir") {
		t.Fatalf("file-as-dir err = %v, want creating events dir", err)
	}

	dir = t.TempDir()
	blockedPath := filepath.Join(dir, "events-2026-03-04.jsonl")
	if err := os.Mkdir(blockedPath, 0750); err != nil {
		t.Fatalf("Mkdir blocked events path: %v", err)
	}
	w = NewJSONLWriter(dir, defaultMaxSize, defaultMaxBackups)
	err = w.Write(Event{Timestamp: time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)})
	if err == nil || !strings.Contains(err.Error(), "opening events file") {
		t.Fatalf("blocked events path err = %v, want opening events file", err)
	}
}

func TestJSONLWriter_Flush(t *testing.T) {
	dir := t.TempDir()
	w := NewJSONLWriter(dir, defaultMaxSize, defaultMaxBackups)
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush before write: %v", err)
	}

	e, _ := NewEvent(TaskStarted, "a1", "task", "", TaskStartedData{TaskID: "t1"})
	e.Timestamp = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	if err := w.Write(e); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush after write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "events-2026-03-04.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("Flush did not write buffered data")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
