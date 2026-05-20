package cleanup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestCleanupUsageDryRunAndPurge(t *testing.T) {
	runtimeDir := t.TempDir()
	store, err := usage.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("usage.NewStore: %v", err)
	}

	now := time.Now().UTC()
	oldEnded := usage.SessionUsage{AgentName: "old-ended", StartedAt: now.Add(-72 * time.Hour), EndedAt: now.Add(-71 * time.Hour)}
	oldStarted := usage.SessionUsage{AgentName: "old-started", StartedAt: now.Add(-70 * time.Hour)}
	recent := usage.SessionUsage{AgentName: "recent", StartedAt: now.Add(-time.Hour), EndedAt: now.Add(-30 * time.Minute)}
	for _, rec := range []usage.SessionUsage{oldEnded, oldStarted, recent} {
		if err := store.Append(rec); err != nil {
			t.Fatalf("append usage: %v", err)
		}
	}

	wouldPurge, err := cleanupUsage(runtimeDir, 24*time.Hour, true)
	if err != nil {
		t.Fatalf("cleanupUsage dry-run: %v", err)
	}
	if wouldPurge != 2 {
		t.Fatalf("dry-run purged = %d, want 2", wouldPurge)
	}

	purged, err := cleanupUsage(runtimeDir, 24*time.Hour, false)
	if err != nil {
		t.Fatalf("cleanupUsage purge: %v", err)
	}
	if purged != 2 {
		t.Fatalf("purged = %d, want 2", purged)
	}
	remaining, err := store.Read(usage.Filter{})
	if err != nil {
		t.Fatalf("read remaining usage: %v", err)
	}
	if len(remaining) != 1 || remaining[0].AgentName != "recent" {
		t.Fatalf("remaining usage = %#v", remaining)
	}
}

func TestCleanupSessionsDryRunCountsOldCompletedAndDuplicateIndex(t *testing.T) {
	runtimeDir := t.TempDir()
	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		t.Fatalf("sessions.NewStore: %v", err)
	}
	now := time.Now().UTC()
	oldEnd := now.Add(-72 * time.Hour)
	recentEnd := now.Add(-time.Hour)

	writeSessionRecord(t, store, sessions.SessionRecord{
		SchemaVersion: sessions.CurrentSchemaVersion,
		SessionID:     "old-session",
		AgentName:     "worker",
		Backend:       "codex",
		StartedAt:     oldEnd.Add(-time.Hour),
		EndedAt:       &oldEnd,
		Status:        sessions.StatusCompleted,
	})
	writeSessionRecord(t, store, sessions.SessionRecord{
		SchemaVersion: sessions.CurrentSchemaVersion,
		SessionID:     "old-session",
		AgentName:     "worker",
		Backend:       "codex",
		StartedAt:     oldEnd.Add(-time.Hour),
		EndedAt:       &oldEnd,
		Status:        sessions.StatusCompleted,
	})
	writeSessionRecord(t, store, sessions.SessionRecord{
		SchemaVersion: sessions.CurrentSchemaVersion,
		SessionID:     "recent-session",
		AgentName:     "worker",
		Backend:       "codex",
		StartedAt:     recentEnd.Add(-time.Hour),
		EndedAt:       &recentEnd,
		Status:        sessions.StatusCompleted,
	})
	writeSessionRecord(t, store, sessions.SessionRecord{
		SchemaVersion: sessions.CurrentSchemaVersion,
		SessionID:     "running-session",
		AgentName:     "worker",
		Backend:       "codex",
		StartedAt:     oldEnd.Add(-time.Hour),
		Status:        sessions.StatusRunning,
	})

	wouldPurge, wouldCompact, err := cleanupSessionsDryRun(store, 24*time.Hour)
	if err != nil {
		t.Fatalf("cleanupSessionsDryRun: %v", err)
	}
	if wouldPurge != 2 {
		t.Fatalf("wouldPurge = %d, want 2", wouldPurge)
	}
	if wouldCompact != 2 {
		t.Fatalf("wouldCompact = %d, want 2", wouldCompact)
	}

	purged, compacted, err := cleanupSessions(runtimeDir, 24*time.Hour, true)
	if err != nil {
		t.Fatalf("cleanupSessions dry-run: %v", err)
	}
	if purged != wouldPurge || compacted != wouldCompact {
		t.Fatalf("cleanupSessions dry-run = purge %d compact %d", purged, compacted)
	}

	purged, compacted, err = cleanupSessions(runtimeDir, 24*time.Hour, false)
	if err != nil {
		t.Fatalf("cleanupSessions purge: %v", err)
	}
	if purged != 2 {
		t.Fatalf("cleanupSessions purge count = %d, want 2", purged)
	}
	if compacted == 0 {
		t.Fatalf("cleanupSessions compacted = %d, want non-zero", compacted)
	}
}

func TestRunCleanupValidationAndEventOnlySuccess(t *testing.T) {
	oldSessionsAge, oldUsageAge, oldEventsAge, oldDryRun := cleanupSessionsAge, cleanupUsageAge, cleanupEventsAge, cleanupDryRun
	t.Cleanup(func() {
		cleanupSessionsAge, cleanupUsageAge, cleanupEventsAge, cleanupDryRun = oldSessionsAge, oldUsageAge, oldEventsAge, oldDryRun
	})

	cleanupSessionsAge = "not-a-duration"
	if err := runCleanup(nil, nil); err == nil || !strings.Contains(err.Error(), "invalid --sessions-older-than") {
		t.Fatalf("runCleanup invalid sessions age err = %v", err)
	}
}

func writeSessionRecord(t *testing.T, store *sessions.Store, rec sessions.SessionRecord) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(store.Dir(), rec.SessionID), 0o700); err != nil {
		t.Fatalf("create session dir: %v", err)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal session record: %v", err)
	}
	indexPath := filepath.Join(store.Dir(), "index.jsonl")
	f, err := os.OpenFile(indexPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatalf("write index: %v", err)
	}
}
