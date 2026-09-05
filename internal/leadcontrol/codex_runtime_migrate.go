package leadcontrol

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
)

// renameDir is os.Rename, indirected so tests can exercise the EXDEV branch
// without needing two filesystems.
var renameDir = os.Rename

// migrateLegacyCodexSQLiteHome moves a lead's Codex sqlite store from the old
// per-session cache location (<os.UserCacheDir()>/loom/codex-leads/<workspace>/
// <lead>/<session>/sqlite) to the stable per-lead path used since PUPPET-489.
//
// It is best-effort by design: a lead launch must never fail or stall because
// an old store could not be moved. Anything unexpected — an unreadable legacy
// tree, a destination that already exists, a cross-device rename, an
// app-server still holding the legacy files — is logged and the launch
// continues against the new (possibly empty) store. Nothing is ever deleted
// from the legacy tree, so a failed migration leaves the old data recoverable
// by hand.
func migrateLegacyCodexSQLiteHome(legacyRoot, newSQLiteHome string, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	if legacyRoot == "" || newSQLiteHome == "" {
		return
	}
	if _, err := os.Stat(newSQLiteHome); err == nil {
		// Already migrated, or a fresh store is in place. Never overwrite it.
		return
	} else if !errors.Is(err, fs.ErrNotExist) {
		logger.Warn("codex lead sqlite store not migrated: destination unreadable",
			"destination", newSQLiteHome, "err", err)
		return
	}

	source, ok := newestLegacyCodexSQLiteDir(legacyRoot, logger)
	if !ok {
		return
	}

	if err := os.MkdirAll(filepath.Dir(newSQLiteHome), 0700); err != nil {
		logger.Warn("codex lead sqlite store not migrated: cannot create destination parent",
			"source", source, "destination", newSQLiteHome, "err", err)
		return
	}

	size := dirSizeBytes(source)
	if err := renameDir(source, newSQLiteHome); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			logger.Warn("codex lead sqlite store left in place: legacy cache is on a different filesystem; starting a fresh store",
				"source", source, "destination", newSQLiteHome)
			return
		}
		logger.Warn("codex lead sqlite store not migrated; starting a fresh store",
			"source", source, "destination", newSQLiteHome, "err", err)
		return
	}
	logger.Info("migrated codex lead sqlite store out of the OS cache dir",
		"source", source, "destination", newSQLiteHome, "bytes", size)
}

// newestLegacyCodexSQLiteDir returns the sqlite directory of the legacy
// session subdirectory with the newest mtime, if any exists.
func newestLegacyCodexSQLiteDir(legacyRoot string, logger *slog.Logger) (string, bool) {
	entries, err := os.ReadDir(legacyRoot)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logger.Warn("codex lead sqlite store not migrated: legacy root unreadable",
				"legacy_root", legacyRoot, "err", err)
		}
		return "", false
	}
	var best string
	var bestMod int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(legacyRoot, entry.Name(), "sqlite")
		if info, err := os.Stat(candidate); err != nil || !info.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().UnixNano() > bestMod {
			best = candidate
			bestMod = info.ModTime().UnixNano()
		}
	}
	return best, best != ""
}

func dirSizeBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // best-effort accounting for a log line
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
