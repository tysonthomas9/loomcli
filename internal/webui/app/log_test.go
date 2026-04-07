package app

import (
	"bytes"
	"log/slog"
	"testing"
)

func TestInitLogger_DefaultIsSlogDefault(t *testing.T) {
	orig := logger
	defer func() { logger = orig }()

	// Reset to package-init state.
	logger = slog.Default()

	if logger != slog.Default() {
		t.Fatalf("default logger should be slog.Default()")
	}
}

func TestInitLogger_CustomLogger(t *testing.T) {
	orig := logger
	defer func() { logger = orig }()

	var buf bytes.Buffer
	custom := slog.New(slog.NewTextHandler(&buf, nil))

	initLogger(custom)

	if logger != custom {
		t.Fatalf("expected logger to be replaced with the custom logger")
	}

	// Confirm the custom logger actually writes to our buffer.
	logger.Info("test message")
	if buf.Len() == 0 {
		t.Fatalf("expected custom logger to write to buffer, got nothing")
	}
}

func TestInitLogger_NilKeepsExisting(t *testing.T) {
	orig := logger
	defer func() { logger = orig }()

	var buf bytes.Buffer
	custom := slog.New(slog.NewTextHandler(&buf, nil))
	logger = custom

	initLogger(nil) // should be a no-op

	if logger != custom {
		t.Fatalf("expected logger to remain unchanged after initLogger(nil)")
	}
}
