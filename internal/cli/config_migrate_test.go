package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetConfigVersion(t *testing.T) {
	tests := []struct {
		name string
		data map[string]interface{}
		want int
	}{
		{name: "present int", data: map[string]interface{}{"version": 1}, want: 1},
		{name: "present float64", data: map[string]interface{}{"version": float64(2)}, want: 2},
		{name: "missing", data: map[string]interface{}{"backend": "claude"}, want: 0},
		{name: "non-numeric string", data: map[string]interface{}{"version": "abc"}, want: 0},
		{name: "empty map", data: map[string]interface{}{}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getConfigVersion(tt.data); got != tt.want {
				t.Errorf("getConfigVersion() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMigrateConfigData_V0ToV1(t *testing.T) {
	data := map[string]interface{}{
		"default_workspace": "myws",
		"backend":           "claude",
	}
	result, err := MigrateConfigData(data)
	if err != nil {
		t.Fatalf("MigrateConfigData() error = %v", err)
	}
	if v := getConfigVersion(result); v != CurrentConfigVersion {
		t.Errorf("version = %d, want %d", v, CurrentConfigVersion)
	}
	if result["default_workspace"] != "myws" {
		t.Error("existing fields should be preserved")
	}
}

func TestMigrateConfigData_AlreadyCurrent(t *testing.T) {
	data := map[string]interface{}{
		"version": CurrentConfigVersion,
		"backend": "claude",
	}
	result, err := MigrateConfigData(data)
	if err != nil {
		t.Fatalf("MigrateConfigData() error = %v", err)
	}
	if result["backend"] != "claude" {
		t.Error("data should be unchanged")
	}
}

func TestMigrateConfigData_FutureVersion(t *testing.T) {
	data := map[string]interface{}{
		"version": CurrentConfigVersion + 1,
	}
	_, err := MigrateConfigData(data)
	if err == nil {
		t.Fatal("expected error for future version")
	}
	if !strings.Contains(err.Error(), "newer than supported") {
		t.Errorf("error = %q, want mention of 'newer than supported'", err)
	}
}

func TestBackupConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("backend: claude\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	backupPath, err := BackupConfig(path)
	if err != nil {
		t.Fatalf("BackupConfig() error = %v", err)
	}
	if !strings.HasPrefix(backupPath, path+".bak.") {
		t.Errorf("backup path %q should start with %q", backupPath, path+".bak.")
	}

	backupContent, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("reading backup: %v", err)
	}
	if string(backupContent) != string(content) {
		t.Errorf("backup content = %q, want %q", backupContent, content)
	}
}

func TestBackupConfig_MissingFile(t *testing.T) {
	_, err := BackupConfig(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestMigrateConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("backend: claude\ndefault_workspace: myws\n"), 0644); err != nil {
		t.Fatal(err)
	}

	oldVersion, backupPath, err := MigrateConfigFile(path)
	if err != nil {
		t.Fatalf("MigrateConfigFile() error = %v", err)
	}
	if oldVersion != 0 {
		t.Errorf("oldVersion = %d, want 0", oldVersion)
	}
	if backupPath == "" {
		t.Fatal("backupPath should not be empty")
	}

	// Verify the migrated file has version field
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "version:") {
		t.Errorf("migrated file should contain 'version:' field, got:\n%s", content)
	}

	// Verify backup exists
	if _, err := os.Stat(backupPath); err != nil {
		t.Errorf("backup file should exist at %s", backupPath)
	}
}

func TestMigrateConfigFile_AlreadyCurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nbackend: claude\n"), 0644); err != nil {
		t.Fatal(err)
	}

	oldVersion, backupPath, err := MigrateConfigFile(path)
	if err != nil {
		t.Fatalf("MigrateConfigFile() error = %v", err)
	}
	if oldVersion != CurrentConfigVersion {
		t.Errorf("oldVersion = %d, want %d", oldVersion, CurrentConfigVersion)
	}
	if backupPath != "" {
		t.Errorf("backupPath = %q, want empty (no backup for current version)", backupPath)
	}
}

func TestMigrateConfigFile_MissingFile(t *testing.T) {
	_, _, err := MigrateConfigFile(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestMigrateV0ToV1(t *testing.T) {
	data := map[string]interface{}{"backend": "claude"}
	result, err := migrateV0ToV1(data)
	if err != nil {
		t.Fatalf("migrateV0ToV1() error = %v", err)
	}
	if v, ok := result["version"]; !ok || v != 1 {
		t.Errorf("version = %v, want 1", v)
	}
	if result["backend"] != "claude" {
		t.Error("existing fields should be preserved")
	}
}

func TestMigrationChain_Idempotent(t *testing.T) {
	data := map[string]interface{}{"backend": "claude"}
	first, err := MigrateConfigData(data)
	if err != nil {
		t.Fatalf("first migration error = %v", err)
	}

	second, err := MigrateConfigData(first)
	if err != nil {
		t.Fatalf("second migration error = %v", err)
	}

	if getConfigVersion(first) != getConfigVersion(second) {
		t.Error("version should be the same after re-migration")
	}
	if first["backend"] != second["backend"] {
		t.Error("data should be identical after re-migration")
	}
}

func TestMigrateConfigFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	oldVersion, backupPath, err := MigrateConfigFile(path)
	if err != nil {
		t.Fatalf("MigrateConfigFile() error = %v", err)
	}
	if oldVersion != 0 {
		t.Errorf("oldVersion = %d, want 0", oldVersion)
	}
	if backupPath == "" {
		t.Fatal("backupPath should not be empty")
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "version:") {
		t.Errorf("migrated empty file should contain 'version:', got:\n%s", content)
	}
}
