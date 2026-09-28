package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	cp := &Checkpoint{
		AgentName:  "falcon",
		TaskID:     "loom-123",
		EpicID:     "loom-epic1",
		CaptureRef: "diff --git a/main.go\n+added line",
		ExitCode:   1,
		ErrorClass: "RateLimited",
		Timestamp:  time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
	}

	if err := SaveCheckpoint(tmpDir, cp); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	loaded, err := LoadCheckpoint(tmpDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadCheckpoint returned nil")
	}

	if loaded.AgentName != cp.AgentName {
		t.Errorf("AgentName: got %q, want %q", loaded.AgentName, cp.AgentName)
	}
	if loaded.TaskID != cp.TaskID {
		t.Errorf("TaskID: got %q, want %q", loaded.TaskID, cp.TaskID)
	}
	if loaded.EpicID != cp.EpicID {
		t.Errorf("EpicID: got %q, want %q", loaded.EpicID, cp.EpicID)
	}
	if loaded.CaptureRef != cp.CaptureRef {
		t.Errorf("CaptureRef: got %q, want %q", loaded.CaptureRef, cp.CaptureRef)
	}
	if loaded.ExitCode != cp.ExitCode {
		t.Errorf("ExitCode: got %d, want %d", loaded.ExitCode, cp.ExitCode)
	}
	if loaded.ErrorClass != cp.ErrorClass {
		t.Errorf("ErrorClass: got %q, want %q", loaded.ErrorClass, cp.ErrorClass)
	}
	if !loaded.Timestamp.Equal(cp.Timestamp) {
		t.Errorf("Timestamp: got %v, want %v", loaded.Timestamp, cp.Timestamp)
	}
}

func TestLoadCheckpointNotExists(t *testing.T) {
	tmpDir := t.TempDir()

	cp, err := LoadCheckpoint(tmpDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint should not error for missing file: %v", err)
	}
	if cp != nil {
		t.Error("LoadCheckpoint should return nil for missing file")
	}
}

func TestClearCheckpoint(t *testing.T) {
	tmpDir := t.TempDir()

	// Save then clear
	cp := &Checkpoint{
		AgentName: "falcon",
		TaskID:    "loom-456",
		ExitCode:  1,
		Timestamp: time.Now(),
	}
	if err := SaveCheckpoint(tmpDir, cp); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	if err := ClearCheckpoint(tmpDir); err != nil {
		t.Fatalf("ClearCheckpoint failed: %v", err)
	}

	loaded, err := LoadCheckpoint(tmpDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint after clear failed: %v", err)
	}
	if loaded != nil {
		t.Error("LoadCheckpoint should return nil after clear")
	}
}

func TestClearCheckpointNotExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Clearing a non-existent checkpoint should not error
	if err := ClearCheckpoint(tmpDir); err != nil {
		t.Fatalf("ClearCheckpoint should not error for missing file: %v", err)
	}
}

func TestSaveCheckpointAtomicity(t *testing.T) {
	tmpDir := t.TempDir()

	cp := &Checkpoint{
		AgentName: "test",
		TaskID:    "loom-789",
		ExitCode:  137,
		Timestamp: time.Now(),
	}

	if err := SaveCheckpoint(tmpDir, cp); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	// Verify no .tmp file left behind
	tmpPath := filepath.Join(tmpDir, CheckpointFileName+".tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("Temp file should not exist after successful save")
	}

	// Verify the checkpoint file is valid JSON
	data, err := os.ReadFile(filepath.Join(tmpDir, CheckpointFileName))
	if err != nil {
		t.Fatalf("Failed to read checkpoint file: %v", err)
	}
	var loaded Checkpoint
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Checkpoint file is not valid JSON: %v", err)
	}
}

func TestSaveAndLoadCheckpoint_WithYieldReason(t *testing.T) {
	tmpDir := t.TempDir()

	cp := &Checkpoint{
		AgentName:   "falcon",
		TaskID:      "loom-yield-1",
		EpicID:      "loom-epic1",
		CaptureRef:  "+yielded change",
		ExitCode:    0,
		ErrorClass:  "Yielded",
		YieldReason: "config_removed",
		Timestamp:   time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
	}

	if err := SaveCheckpoint(tmpDir, cp); err != nil {
		t.Fatalf("SaveCheckpoint failed: %v", err)
	}

	loaded, err := LoadCheckpoint(tmpDir)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadCheckpoint returned nil")
	}

	if loaded.YieldReason != "config_removed" {
		t.Errorf("YieldReason: got %q, want %q", loaded.YieldReason, "config_removed")
	}
	if loaded.ErrorClass != "Yielded" {
		t.Errorf("ErrorClass: got %q, want %q", loaded.ErrorClass, "Yielded")
	}
	if loaded.ExitCode != 0 {
		t.Errorf("ExitCode: got %d, want 0", loaded.ExitCode)
	}
	if loaded.AgentName != cp.AgentName {
		t.Errorf("AgentName: got %q, want %q", loaded.AgentName, cp.AgentName)
	}
	if loaded.TaskID != cp.TaskID {
		t.Errorf("TaskID: got %q, want %q", loaded.TaskID, cp.TaskID)
	}
	if loaded.CaptureRef != cp.CaptureRef {
		t.Errorf("CaptureRef: got %q, want %q", loaded.CaptureRef, cp.CaptureRef)
	}
}
