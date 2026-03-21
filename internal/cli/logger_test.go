package cli

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitLogger_TextFormat(t *testing.T) {
	// Use a file so we can inspect output without racing on stderr.
	dir := t.TempDir()
	path := filepath.Join(dir, "text.log")

	if err := InitLogger("text", path); err != nil {
		t.Fatalf("InitLogger(\"text\", %q) error = %v", path, err)
	}

	slog.Info("hello", "key", "value")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	line := string(data)

	// slog.TextHandler produces key=value pairs, not JSON.
	if !strings.Contains(line, "msg=hello") {
		t.Errorf("expected text output containing msg=hello, got %q", line)
	}
	if !strings.Contains(line, "key=value") {
		t.Errorf("expected text output containing key=value, got %q", line)
	}
}

func TestInitLogger_JSONFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "json.log")

	if err := InitLogger("json", path); err != nil {
		t.Fatalf("InitLogger(\"json\", %q) error = %v", path, err)
	}

	slog.Info("hello", "key", "value")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	var m map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(data), &m); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, data)
	}
	if msg, _ := m["msg"].(string); msg != "hello" {
		t.Errorf("JSON msg = %q, want %q", msg, "hello")
	}
	if v, _ := m["key"].(string); v != "value" {
		t.Errorf("JSON key = %q, want %q", v, "value")
	}
}

func TestInitLogger_FileOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.log")

	if err := InitLogger("text", path); err != nil {
		t.Fatalf("InitLogger error: %v", err)
	}

	slog.Info("file-test")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(string(data), "file-test") {
		t.Errorf("log file should contain \"file-test\", got %q", string(data))
	}
}

func TestInitLogger_InvalidFile(t *testing.T) {
	// Use a path whose parent directory does not exist.
	badPath := filepath.Join(t.TempDir(), "no-such-dir", "nested", "log.txt")

	err := InitLogger("text", badPath)
	if err == nil {
		t.Fatal("expected error for invalid file path, got nil")
	}
	if !strings.Contains(err.Error(), "failed to open log file") {
		t.Errorf("error should mention failed to open log file, got %q", err.Error())
	}
}

func TestInitLogger_BridgesLogPrintf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridge.log")

	if err := InitLogger("text", path); err != nil {
		t.Fatalf("InitLogger error: %v", err)
	}

	// log.Printf should be bridged through slog after InitLogger.
	log.Printf("bridged message %s", "here")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(string(data), "bridged message here") {
		t.Errorf("log.Printf output should appear in slog output, got %q", string(data))
	}
}
