package skills

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

func TestWriteSkillErrorLogsExpectedClientFailuresBelowError(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	writeSkillError(httptest.NewRecorder(), &domain.SkillPreconditionError{Stored: "current"})
	if got := logs.String(); !strings.Contains(got, "level=DEBUG") || strings.Contains(got, "level=ERROR") {
		t.Fatalf("expected client failure log = %q, want DEBUG and no ERROR", got)
	}

	logs.Reset()
	writeSkillError(httptest.NewRecorder(), errors.New("unexpected store failure"))
	if got := logs.String(); !strings.Contains(got, "level=ERROR") {
		t.Fatalf("unexpected failure log = %q, want ERROR", got)
	}
}
