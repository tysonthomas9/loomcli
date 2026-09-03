package skills

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// captureLogs installs a Debug-level handler for the duration of the test and
// returns the records it collects, one per line.
func captureLogs(t *testing.T) func() []string {
	t.Helper()
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return func() []string {
		lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
		if len(lines) == 1 && lines[0] == "" {
			return nil
		}
		return lines
	}
}

func TestWriteSkillErrorLogsExpectedClientFailuresBelowError(t *testing.T) {
	records := captureLogs(t)

	writeSkillError(httptest.NewRecorder(), &domain.SkillPreconditionError{Stored: "current"})
	if got := strings.Join(records(), "\n"); !strings.Contains(got, "level=DEBUG") || strings.Contains(got, "level=ERROR") {
		t.Fatalf("expected client failure log = %q, want DEBUG and no ERROR", got)
	}
}

// Every failure must be logged exactly once. writeSkillError used to log on
// entry and then hand unmapped errors to HandleServiceError, which logs
// everything it writes — so an unclassified failure landed in the log twice at
// Error level. Counting records, not just matching levels, is what pins that.
func TestWriteSkillErrorLogsEachFailureExactlyOnce(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantLevel string
	}{
		{
			name:      "mapped client error",
			err:       &domain.SkillPreconditionError{Stored: "current"},
			wantLevel: "level=DEBUG",
		},
		{
			name:      "mapped service error",
			err:       &service.ServiceError{Kind: service.KindNotFound, Message: "no such skill"},
			wantLevel: "level=DEBUG",
		},
		{
			name:      "unclassified error",
			err:       errors.New("unexpected store failure"),
			wantLevel: "level=ERROR",
		},
		{
			name:      "internal service error",
			err:       &service.ServiceError{Kind: service.KindInternal, Message: "boom"},
			wantLevel: "level=ERROR",
		},
		{
			name:      "unmapped service error kind",
			err:       &service.ServiceError{Kind: service.KindStarting, Message: "still starting"},
			wantLevel: "level=ERROR",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := captureLogs(t)
			writeSkillError(httptest.NewRecorder(), tt.err)
			got := records()
			if len(got) != 1 {
				t.Fatalf("logged %d records, want exactly 1:\n%s", len(got), strings.Join(got, "\n"))
			}
			if !strings.Contains(got[0], tt.wantLevel) {
				t.Fatalf("record = %q, want %s", got[0], tt.wantLevel)
			}
		})
	}
}
