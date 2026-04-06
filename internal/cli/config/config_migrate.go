package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/tysonthomas9/loomcli/internal/atomicfile"
	"github.com/tysonthomas9/loomcli/internal/configlock"
)

// CurrentConfigVersion is the latest config schema version.
// Bump this when adding new migrations.
const CurrentConfigVersion = 2

// MigrationFunc transforms config data from one version to the next.
type MigrationFunc func(data map[string]interface{}) (map[string]interface{}, error)

// Migration describes a single version migration step.
type Migration struct {
	Fn          MigrationFunc
	Destructive bool   // If true, requires explicit 'loom config migrate' — not auto-applied by LoadConfig
	Description string // Human-readable summary shown in `loom config migrate` output
}

// migrations maps source version to the function that migrates it to source+1.
var migrations = map[int]Migration{
	0: {Fn: migrateV0ToV1, Description: "Add schema version field"},
	1: {Fn: migrateV1ToV2, Description: "Add stable UUIDs to workspace entries"},
}

// getConfigVersion reads the version field from raw config data.
// Returns 0 if the field is missing or not a recognized numeric type.
func GetConfigVersion(data map[string]interface{}) int {
	v, ok := data["version"]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	default:
		return 0
	}
}

// MigrateConfigData applies all needed migrations sequentially to reach CurrentConfigVersion.
// Returns the data unchanged if already at the current version.
func MigrateConfigData(data map[string]interface{}) (map[string]interface{}, error) {
	version := GetConfigVersion(data)
	if version > CurrentConfigVersion {
		return nil, fmt.Errorf("config version %d is newer than supported version %d; please upgrade loom", version, CurrentConfigVersion)
	}
	for version < CurrentConfigVersion {
		m, ok := migrations[version]
		if !ok {
			return nil, fmt.Errorf("no migration defined for version %d", version)
		}
		var err error
		data, err = m.Fn(data)
		if err != nil {
			return nil, fmt.Errorf("migrating from version %d: %w", version, err)
		}
		version = GetConfigVersion(data)
	}
	return data, nil
}

// AutoMigrateConfigData applies non-destructive migrations from the data's current version
// toward CurrentConfigVersion, stopping before the first destructive migration.
// Returns the (possibly migrated) data and the version it reached.
// If no migrations were needed or applied, returns the original data and its version.
func AutoMigrateConfigData(data map[string]interface{}) (map[string]interface{}, int, error) {
	version := GetConfigVersion(data)
	if version >= CurrentConfigVersion {
		return data, version, nil
	}
	for version < CurrentConfigVersion {
		m, ok := migrations[version]
		if !ok {
			return data, version, fmt.Errorf("no migration defined for version %d", version)
		}
		if m.Destructive {
			break // stop before destructive migration
		}
		var err error
		data, err = m.Fn(data)
		if err != nil {
			return nil, version, fmt.Errorf("auto-migrating from version %d: %w", version, err)
		}
		version = GetConfigVersion(data)
	}
	return data, version, nil
}

// PendingMigration describes a migration that would be applied to reach CurrentConfigVersion.
type PendingMigration struct {
	FromVersion int
	ToVersion   int
	Description string
	Destructive bool
}

// PendingMigrations returns the ordered list of migrations needed to go from
// currentVersion to CurrentConfigVersion. Returns nil if already current or ahead.
func PendingMigrations(currentVersion int) []PendingMigration {
	var pending []PendingMigration
	for v := currentVersion; v < CurrentConfigVersion; v++ {
		m, ok := migrations[v]
		if !ok {
			break // gap in migration chain — stop here
		}
		pending = append(pending, PendingMigration{
			FromVersion: v,
			ToVersion:   v + 1,
			Description: m.Description,
			Destructive: m.Destructive,
		})
	}
	return pending
}

// autoMigrateFile checks if the config at path (with rawBytes content) needs non-destructive
// migration. If so, applies the migrations, creates a backup, writes the migrated config to
// disk, and returns the new bytes. If no migration is needed or only destructive migrations
// remain, returns the original bytes unchanged and shows the version warning for the
// destructive case.
func autoMigrateFile(path string, rawBytes []byte) ([]byte, error) {
	var rawMap map[string]interface{}
	if err := yaml.Unmarshal(rawBytes, &rawMap); err != nil {
		// Can't parse for version check — return original bytes.
		// Normal LoadConfig flow will report the parse error.
		return rawBytes, nil
	}
	if rawMap == nil {
		rawMap = make(map[string]interface{})
	}

	origVersion := GetConfigVersion(rawMap)
	if origVersion >= CurrentConfigVersion {
		return rawBytes, nil
	}

	migrated, reachedVersion, err := AutoMigrateConfigData(rawMap)
	if err != nil {
		return nil, fmt.Errorf("auto-migration failed for %s: %w", path, err)
	}

	if reachedVersion > origVersion {
		// Non-destructive migrations were applied — persist to disk
		out, marshalErr := yaml.Marshal(migrated)
		if marshalErr != nil {
			slog.Warn("auto-migration: could not marshal migrated config", "path", path, "error", marshalErr)
			return rawBytes, nil
		}

		// Create backup of original file
		backupPath := fmt.Sprintf("%s.bak.%s", path, time.Now().Format("20060102T150405.000000000"))
		if backupErr := os.WriteFile(backupPath, rawBytes, 0644); backupErr != nil {
			slog.Warn("auto-migration: could not create backup", "path", path, "error", backupErr)
			// Continue anyway — migration is non-destructive
		}

		// Write migrated config using atomic write
		if writeErr := atomicfile.WriteFile(path, out, 0644); writeErr != nil {
			slog.Warn("auto-migration: could not persist migrated config", "path", path, "error", writeErr)
			// Use migrated bytes in-memory anyway
			return out, nil
		}

		slog.Info("auto-migrated config", "path", path, "from_version", origVersion, "to_version", reachedVersion)
		rawBytes = out
	}

	// If destructive migrations still needed, show the warning
	if reachedVersion < CurrentConfigVersion {
		configVersionWarnOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "Warning: config %s is at version %d (current: %d). Run 'loom config migrate' for remaining upgrades.\n",
				path, reachedVersion, CurrentConfigVersion)
		})
	}

	return rawBytes, nil
}

// BackupConfig creates a timestamped backup copy of the config file.
// Returns the backup file path.
func BackupConfig(path string) (string, error) {
	content, err := os.ReadFile(path) //nolint:gosec // path is from CLI arg or resolved config path
	if err != nil {
		return "", fmt.Errorf("reading config for backup: %w", err)
	}
	backupPath := fmt.Sprintf("%s.bak.%s", path, time.Now().Format("20060102T150405"))
	if err := os.WriteFile(backupPath, content, 0644); err != nil {
		return "", fmt.Errorf("writing backup: %w", err)
	}
	return backupPath, nil
}

// MigrateConfigFile reads, migrates, and writes back a config file.
// Creates a timestamped backup before modifying. Returns the old version
// and backup path. If already at the current version, no backup is created.
func MigrateConfigFile(path string) (oldVersion int, backupPath string, err error) {
	unlock, lockErr := configlock.ConfigLock(filepath.Dir(path))
	if lockErr != nil {
		return 0, "", fmt.Errorf("config lock: %w", lockErr)
	}
	defer unlock()

	content, err := os.ReadFile(path) //nolint:gosec // path is from CLI arg or resolved config path
	if err != nil {
		return 0, "", fmt.Errorf("reading config: %w", err)
	}

	var data map[string]interface{}
	if err := yaml.Unmarshal(content, &data); err != nil {
		return 0, "", fmt.Errorf("parsing config: %w", err)
	}
	if data == nil {
		data = make(map[string]interface{})
	}

	oldVersion = GetConfigVersion(data)
	if oldVersion == CurrentConfigVersion {
		return oldVersion, "", nil
	}

	backupPath, err = BackupConfig(path)
	if err != nil {
		return oldVersion, "", err
	}

	data, err = MigrateConfigData(data)
	if err != nil {
		return oldVersion, backupPath, err
	}

	out, err := yaml.Marshal(data)
	if err != nil {
		return oldVersion, backupPath, fmt.Errorf("marshaling migrated config: %w", err)
	}

	if err := atomicfile.WriteFile(path, out, 0644); err != nil {
		return oldVersion, backupPath, fmt.Errorf("writing migrated config: %w", err)
	}

	return oldVersion, backupPath, nil
}

// migrateV0ToV1 adds the version field to a legacy config.
func migrateV0ToV1(data map[string]interface{}) (map[string]interface{}, error) {
	data["version"] = 1
	return data, nil
}

// migrateV1ToV2 adds a stable UUID (id field) to each workspace entry.
func migrateV1ToV2(data map[string]interface{}) (map[string]interface{}, error) {
	workspaces, _ := data["workspaces"].(map[string]interface{})
	for name, entry := range workspaces {
		ws, ok := entry.(map[string]interface{})
		if !ok {
			slog.Warn("skipping non-map workspace entry during v1→v2 migration", "workspace", name)
			continue
		}
		if _, hasID := ws["id"]; !hasID {
			ws["id"] = uuid.New().String()
		}
		workspaces[name] = ws
	}
	data["version"] = 2
	return data, nil
}
