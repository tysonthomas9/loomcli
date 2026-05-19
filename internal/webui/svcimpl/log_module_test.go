package svcimpl

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
)

func TestNewLogModuleAndSetLogger(t *testing.T) {
	module := NewLogModule(nil)
	if module == nil {
		t.Fatal("NewLogModule returned nil")
	}
	module.Register(http.NewServeMux())

	oldLogger := logger
	t.Cleanup(func() { logger = oldLogger })

	SetLogger(nil)
	if logger != oldLogger {
		t.Fatal("SetLogger(nil) changed logger")
	}
	next := slog.New(slog.NewTextHandler(io.Discard, nil))
	SetLogger(next)
	if logger != next {
		t.Fatal("SetLogger did not install provided logger")
	}
}
