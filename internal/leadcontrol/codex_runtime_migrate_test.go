package leadcontrol

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func newMigrateTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// seedLegacySession writes a legacy <session>/sqlite store and returns its path.
func seedLegacySession(t *testing.T, legacyRoot, session string, modTime time.Time) string {
	t.Helper()
	sqliteDir := filepath.Join(legacyRoot, session, "sqlite")
	if err := os.MkdirAll(sqliteDir, 0700); err != nil {
		t.Fatalf("seed legacy session: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sqliteDir, "memories_1.sqlite"), []byte(session), 0600); err != nil {
		t.Fatalf("seed legacy sqlite file: %v", err)
	}
	if err := os.Chtimes(filepath.Join(legacyRoot, session), modTime, modTime); err != nil {
		t.Fatalf("set legacy session mtime: %v", err)
	}
	return sqliteDir
}

func TestMigrateLegacyCodexSQLiteHomeMovesNewestSessionOnce(t *testing.T) {
	base := t.TempDir()
	legacyRoot := filepath.Join(base, "legacy", "workspace", "lead")
	now := time.Now()
	seedLegacySession(t, legacyRoot, "old-session", now.Add(-2*time.Hour))
	seedLegacySession(t, legacyRoot, "new-session", now)
	newSQLiteHome := filepath.Join(base, "runtime", ".loom", "lead-sessions", "workspace", "lead", "sqlite")

	logger, logs := newMigrateTestLogger()
	migrateLegacyCodexSQLiteHome(legacyRoot, newSQLiteHome, logger)

	body, err := os.ReadFile(filepath.Join(newSQLiteHome, "memories_1.sqlite"))
	if err != nil {
		t.Fatalf("read migrated sqlite file: %v", err)
	}
	if string(body) != "new-session" {
		t.Fatalf("migrated store came from %q, want the newest legacy session", body)
	}
	if !bytes.Contains(logs.Bytes(), []byte("migrated codex lead sqlite store")) {
		t.Fatalf("migration was not logged:\n%s", logs.String())
	}
	if !bytes.Contains(logs.Bytes(), []byte("bytes=11")) {
		t.Fatalf("migration log missing byte count:\n%s", logs.String())
	}

	// The legacy tree is never deleted: the older session and the moved-from
	// session directory both survive.
	if _, err := os.Stat(filepath.Join(legacyRoot, "old-session", "sqlite")); err != nil {
		t.Fatalf("legacy sibling session was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(legacyRoot, "new-session")); err != nil {
		t.Fatalf("legacy session directory was removed: %v", err)
	}

	// A second call is a no-op: the destination now exists.
	logger2, logs2 := newMigrateTestLogger()
	migrateLegacyCodexSQLiteHome(legacyRoot, newSQLiteHome, logger2)
	if logs2.Len() != 0 {
		t.Fatalf("second migration was not silent:\n%s", logs2.String())
	}
	body, err = os.ReadFile(filepath.Join(newSQLiteHome, "memories_1.sqlite"))
	if err != nil || string(body) != "new-session" {
		t.Fatalf("second call disturbed the store: %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(legacyRoot, "old-session", "sqlite")); err != nil {
		t.Fatalf("second call consumed the older legacy session: %v", err)
	}
}

func TestMigrateLegacyCodexSQLiteHomeKeepsExistingDestination(t *testing.T) {
	base := t.TempDir()
	legacyRoot := filepath.Join(base, "legacy", "workspace", "lead")
	seedLegacySession(t, legacyRoot, "session-a", time.Now())
	newSQLiteHome := filepath.Join(base, "runtime", "sqlite")
	if err := os.MkdirAll(newSQLiteHome, 0700); err != nil {
		t.Fatalf("create destination: %v", err)
	}
	if err := os.WriteFile(filepath.Join(newSQLiteHome, "memories_1.sqlite"), []byte("current"), 0600); err != nil {
		t.Fatalf("seed destination: %v", err)
	}

	logger, logs := newMigrateTestLogger()
	migrateLegacyCodexSQLiteHome(legacyRoot, newSQLiteHome, logger)

	body, err := os.ReadFile(filepath.Join(newSQLiteHome, "memories_1.sqlite"))
	if err != nil || string(body) != "current" {
		t.Fatalf("existing destination was overwritten: %q, %v", body, err)
	}
	if logs.Len() != 0 {
		t.Fatalf("no-op migration logged:\n%s", logs.String())
	}
	if _, err := os.Stat(filepath.Join(legacyRoot, "session-a", "sqlite")); err != nil {
		t.Fatalf("legacy tree was touched: %v", err)
	}
}

func TestMigrateLegacyCodexSQLiteHomeMissingLegacyRootIsSilent(t *testing.T) {
	base := t.TempDir()
	logger, logs := newMigrateTestLogger()
	migrateLegacyCodexSQLiteHome(filepath.Join(base, "absent"), filepath.Join(base, "runtime", "sqlite"), logger)
	if logs.Len() != 0 {
		t.Fatalf("missing legacy root logged:\n%s", logs.String())
	}
	if _, err := os.Stat(filepath.Join(base, "runtime", "sqlite")); !os.IsNotExist(err) {
		t.Fatalf("migration created a destination it should not have: %v", err)
	}
}

func TestMigrateLegacyCodexSQLiteHomeUnreadableLegacyRootContinues(t *testing.T) {
	base := t.TempDir()
	// A file where a directory is expected: ReadDir fails with ENOTDIR.
	legacyRoot := filepath.Join(base, "legacy")
	if err := os.WriteFile(legacyRoot, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("seed unreadable legacy root: %v", err)
	}
	newSQLiteHome := filepath.Join(base, "runtime", "sqlite")

	logger, logs := newMigrateTestLogger()
	migrateLegacyCodexSQLiteHome(legacyRoot, newSQLiteHome, logger)

	if !bytes.Contains(logs.Bytes(), []byte("legacy root unreadable")) {
		t.Fatalf("unreadable legacy root was not logged:\n%s", logs.String())
	}
	if _, err := os.Stat(newSQLiteHome); !os.IsNotExist(err) {
		t.Fatalf("migration created a destination it should not have: %v", err)
	}
}

func TestMigrateLegacyCodexSQLiteHomeCrossDeviceRenameContinues(t *testing.T) {
	base := t.TempDir()
	legacyRoot := filepath.Join(base, "legacy", "workspace", "lead")
	source := seedLegacySession(t, legacyRoot, "session-a", time.Now())
	newSQLiteHome := filepath.Join(base, "runtime", "sqlite")

	original := renameDir
	t.Cleanup(func() { renameDir = original })
	renameDir = func(oldpath, newpath string) error {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
	}

	logger, logs := newMigrateTestLogger()
	migrateLegacyCodexSQLiteHome(legacyRoot, newSQLiteHome, logger)

	if !bytes.Contains(logs.Bytes(), []byte("different filesystem")) {
		t.Fatalf("EXDEV was not reported:\n%s", logs.String())
	}
	for _, want := range []string{source, newSQLiteHome} {
		if !bytes.Contains(logs.Bytes(), []byte(want)) {
			t.Fatalf("EXDEV log missing %q:\n%s", want, logs.String())
		}
	}
	if _, err := os.Stat(filepath.Join(source, "memories_1.sqlite")); err != nil {
		t.Fatalf("legacy store was disturbed by a failed rename: %v", err)
	}
	if _, err := os.Stat(newSQLiteHome); !os.IsNotExist(err) {
		t.Fatalf("failed rename left a destination behind: %v", err)
	}
}
