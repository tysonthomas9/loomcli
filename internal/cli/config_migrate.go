package cli

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// CurrentConfigVersion is the latest config schema version.
// Bump this when adding new migrations.
const CurrentConfigVersion = 1

// MigrationFunc transforms config data from one version to the next.
type MigrationFunc func(data map[string]interface{}) (map[string]interface{}, error)

// migrations maps source version to the function that migrates it to source+1.
var migrations = map[int]MigrationFunc{
	0: migrateV0ToV1,
}

// getConfigVersion reads the version field from raw config data.
// Returns 0 if the field is missing or not a recognized numeric type.
func getConfigVersion(data map[string]interface{}) int {
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
	version := getConfigVersion(data)
	if version > CurrentConfigVersion {
		return nil, fmt.Errorf("config version %d is newer than supported version %d; please upgrade loom", version, CurrentConfigVersion)
	}
	for version < CurrentConfigVersion {
		fn, ok := migrations[version]
		if !ok {
			return nil, fmt.Errorf("no migration defined for version %d", version)
		}
		var err error
		data, err = fn(data)
		if err != nil {
			return nil, fmt.Errorf("migrating from version %d: %w", version, err)
		}
		version = getConfigVersion(data)
	}
	return data, nil
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

	oldVersion = getConfigVersion(data)
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

	if err := os.WriteFile(path, out, 0644); err != nil {
		return oldVersion, backupPath, fmt.Errorf("writing migrated config: %w", err)
	}

	return oldVersion, backupPath, nil
}

// migrateV0ToV1 adds the version field to a legacy config.
func migrateV0ToV1(data map[string]interface{}) (map[string]interface{}, error) {
	data["version"] = 1
	return data, nil
}
