package workspace

import (
	"crypto/sha1" //nolint:gosec // sentinel filename, not a security primitive
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

const (
	centralDirPerm  = 0o700
	centralFilePerm = 0o600
	fallbackWSID    = "_default"
)

// CentralSessionsDir returns ~/.loom/sessions/<wsID>/. Empty wsID falls back
// to LOOM_WORKSPACE_ID then "_default" so legacy/standalone callers land in
// a single shared bucket rather than scattered PWD-relative dirs.
// The directory is not created here — callers (via sessions.NewStoreAt) do so.
func CentralSessionsDir(wsID string) (string, error) {
	return centralKindDir("sessions", wsID)
}

// CentralUsageDir returns ~/.loom/usage/<wsID>/.
func CentralUsageDir(wsID string) (string, error) {
	return centralKindDir("usage", wsID)
}

func centralKindDir(kind, wsID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	id := ResolveWorkspaceID(wsID)
	if id == "" {
		id = fallbackWSID
	}
	return filepath.Join(home, ".loom", kind, id), nil
}

// MigrateLegacySessionsAndUsage moves <legacyBeadsDir>/sessions/** into
// CentralSessionsDir(wsID) and <legacyBeadsDir>/usage.jsonl into
// CentralUsageDir(wsID)/usage.jsonl, if present. On-disk sentinel files
// (.migrated-from-<hash>) skip re-runs across processes. Best-effort: errors
// are logged but not returned — the central stores remain usable even if
// legacy data can't be moved.
func MigrateLegacySessionsAndUsage(wsID, legacyBeadsDir string) {
	if legacyBeadsDir == "" {
		return
	}
	migrateSessionsFromLegacy(wsID, legacyBeadsDir)
	migrateUsageFromLegacy(wsID, legacyBeadsDir)
}

func sentinelName(legacyBeadsDir string) string {
	sum := sha1.Sum([]byte(legacyBeadsDir)) //nolint:gosec // not a security primitive
	return ".migrated-from-" + hex.EncodeToString(sum[:])
}

func migrateSessionsFromLegacy(wsID, legacyBeadsDir string) {
	legacyDir := filepath.Join(legacyBeadsDir, "sessions")
	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		return // no legacy data — nothing to migrate
	}

	centralDir, err := CentralSessionsDir(wsID)
	if err != nil {
		slog.Warn("migrate: resolve central sessions dir", "ws", wsID, "err", err)
		return
	}
	if err := os.MkdirAll(centralDir, centralDirPerm); err != nil {
		slog.Warn("migrate: create central sessions dir", "dir", centralDir, "err", err)
		return
	}

	sentinel := filepath.Join(centralDir, sentinelName(legacyBeadsDir))
	if _, err := os.Stat(sentinel); err == nil {
		return // already migrated from this source
	}

	moved := 0
	for _, entry := range entries {
		if migrateSessionsEntry(entry.Name(), legacyDir, centralDir) {
			moved++
		}
	}
	if err := os.WriteFile(sentinel, []byte(legacyBeadsDir), centralFilePerm); err != nil {
		slog.Warn("migrate: write sentinel", "path", sentinel, "err", err)
	}
	if moved > 0 {
		slog.Info("migrate: moved session dirs", "count", moved, "from", legacyDir, "to", centralDir)
	}
}

// migrateSessionsEntry moves a single legacy sessions/ child into the central
// dir. index.jsonl is merged (append under flock); everything else is a
// rename that skips if the destination already exists. Returns true when the
// child landed in the central store.
func migrateSessionsEntry(name, legacyDir, centralDir string) bool {
	if name == "." || name == ".." {
		return false
	}
	src := filepath.Join(legacyDir, name)
	dst := filepath.Join(centralDir, name)
	if name == "index.jsonl" {
		if err := appendFileWithFlock(src, dst); err != nil {
			slog.Warn("migrate: merge index", "from", src, "to", dst, "err", err)
			return false
		}
		if err := os.Remove(src); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("migrate: remove legacy index", "path", src, "err", err)
		}
		return true
	}
	if _, err := os.Stat(dst); err == nil {
		return false // destination already exists — don't clobber
	}
	if err := os.Rename(src, dst); err != nil {
		slog.Warn("migrate: rename", "from", src, "to", dst, "err", err)
		return false
	}
	return true
}

func migrateUsageFromLegacy(wsID, legacyBeadsDir string) {
	legacyFile := filepath.Join(legacyBeadsDir, "usage.jsonl")
	if _, err := os.Stat(legacyFile); err != nil {
		return
	}

	centralDir, err := CentralUsageDir(wsID)
	if err != nil {
		slog.Warn("migrate: resolve central usage dir", "ws", wsID, "err", err)
		return
	}
	if err := os.MkdirAll(centralDir, centralDirPerm); err != nil {
		slog.Warn("migrate: create central usage dir", "dir", centralDir, "err", err)
		return
	}

	sentinel := filepath.Join(centralDir, sentinelName(legacyBeadsDir))
	if _, err := os.Stat(sentinel); err == nil {
		return
	}

	centralFile := filepath.Join(centralDir, "usage.jsonl")
	if err := appendFileWithFlock(legacyFile, centralFile); err != nil {
		slog.Warn("migrate: append usage", "from", legacyFile, "to", centralFile, "err", err)
		return
	}
	if err := os.Remove(legacyFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("migrate: remove legacy usage", "path", legacyFile, "err", err)
	}
	if err := os.WriteFile(sentinel, []byte(legacyBeadsDir), centralFilePerm); err != nil {
		slog.Warn("migrate: write usage sentinel", "path", sentinel, "err", err)
	}
}

// appendFileWithFlock appends src onto dst under an exclusive flock so a
// concurrent writer doesn't interleave records. Used for both legacy
// usage.jsonl migration and legacy sessions/index.jsonl merging.
func appendFileWithFlock(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src is under legacyBeadsDir from trusted config
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer func() { _ = in.Close() }()

	//nolint:gosec // dst is under CentralSessionsDir/CentralUsageDir
	out, err := os.OpenFile(dst, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open dst: %w", err)
	}
	defer func() { _ = out.Close() }()

	if err := lockfile.FlockExclusiveBlocking(out); err != nil {
		return fmt.Errorf("flock dst: %w", err)
	}
	defer func() { _ = lockfile.FlockUnlock(out) }()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	return nil
}
