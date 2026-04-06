//go:build ignore

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
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
	if err := os.WriteFile(path, []byte("version: 2\nbackend: claude\n"), 0644); err != nil {
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

func TestMigrateV1ToV2_AddsUUIDs(t *testing.T) {
	data := map[string]interface{}{
		"version": 1,
		"workspaces": map[string]interface{}{
			"alpha": map[string]interface{}{
				"path": "/tmp/alpha",
			},
			"beta": map[string]interface{}{
				"path": "/tmp/beta",
			},
		},
	}
	result, err := migrateV1ToV2(data)
	if err != nil {
		t.Fatalf("migrateV1ToV2() error = %v", err)
	}
	if v := getConfigVersion(result); v != 2 {
		t.Errorf("version = %d, want 2", v)
	}
	workspaces := result["workspaces"].(map[string]interface{})
	for _, name := range []string{"alpha", "beta"} {
		ws := workspaces[name].(map[string]interface{})
		idVal, ok := ws["id"]
		if !ok {
			t.Fatalf("workspace %q missing id field", name)
		}
		idStr, ok := idVal.(string)
		if !ok {
			t.Fatalf("workspace %q id is not a string: %T", name, idVal)
		}
		if _, err := uuid.Parse(idStr); err != nil {
			t.Errorf("workspace %q id %q is not a valid UUID: %v", name, idStr, err)
		}
	}
	// Ensure the two UUIDs are different
	alphaID := workspaces["alpha"].(map[string]interface{})["id"].(string)
	betaID := workspaces["beta"].(map[string]interface{})["id"].(string)
	if alphaID == betaID {
		t.Errorf("alpha and beta got the same UUID: %s", alphaID)
	}
}

func TestMigrateV1ToV2_PreservesExistingID(t *testing.T) {
	existingID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	data := map[string]interface{}{
		"version": 1,
		"workspaces": map[string]interface{}{
			"hasid": map[string]interface{}{
				"path": "/tmp/hasid",
				"id":   existingID,
			},
			"noid": map[string]interface{}{
				"path": "/tmp/noid",
			},
		},
	}
	result, err := migrateV1ToV2(data)
	if err != nil {
		t.Fatalf("migrateV1ToV2() error = %v", err)
	}
	workspaces := result["workspaces"].(map[string]interface{})

	// Existing ID should be preserved
	hasidWs := workspaces["hasid"].(map[string]interface{})
	if hasidWs["id"] != existingID {
		t.Errorf("existing id changed: got %q, want %q", hasidWs["id"], existingID)
	}

	// Missing ID should get a new valid UUID
	noidWs := workspaces["noid"].(map[string]interface{})
	newID, ok := noidWs["id"].(string)
	if !ok {
		t.Fatal("workspace 'noid' missing id after migration")
	}
	if _, err := uuid.Parse(newID); err != nil {
		t.Errorf("workspace 'noid' id %q is not a valid UUID: %v", newID, err)
	}
	if newID == existingID {
		t.Error("new UUID should differ from the preserved one")
	}
}

func TestMigrateV1ToV2_NoWorkspaces(t *testing.T) {
	data := map[string]interface{}{
		"version": 1,
		"backend": "claude",
	}
	result, err := migrateV1ToV2(data)
	if err != nil {
		t.Fatalf("migrateV1ToV2() error = %v", err)
	}
	if v := getConfigVersion(result); v != 2 {
		t.Errorf("version = %d, want 2", v)
	}
	if result["backend"] != "claude" {
		t.Error("existing fields should be preserved")
	}
}

func TestMigrateV1ToV2_EmptyWorkspaces(t *testing.T) {
	data := map[string]interface{}{
		"version":    1,
		"workspaces": map[string]interface{}{},
	}
	result, err := migrateV1ToV2(data)
	if err != nil {
		t.Fatalf("migrateV1ToV2() error = %v", err)
	}
	if v := getConfigVersion(result); v != 2 {
		t.Errorf("version = %d, want 2", v)
	}
}

func TestMigrationChain_V0ToV2(t *testing.T) {
	data := map[string]interface{}{
		"backend": "claude",
		"workspaces": map[string]interface{}{
			"ws1": map[string]interface{}{
				"path": "/tmp/ws1",
			},
			"ws2": map[string]interface{}{
				"path": "/tmp/ws2",
			},
		},
	}
	result, err := MigrateConfigData(data)
	if err != nil {
		t.Fatalf("MigrateConfigData() error = %v", err)
	}
	if v := getConfigVersion(result); v != 2 {
		t.Errorf("version = %d, want 2", v)
	}
	workspaces := result["workspaces"].(map[string]interface{})
	for _, name := range []string{"ws1", "ws2"} {
		ws := workspaces[name].(map[string]interface{})
		idStr, ok := ws["id"].(string)
		if !ok {
			t.Fatalf("workspace %q missing id after full migration chain", name)
		}
		if _, err := uuid.Parse(idStr); err != nil {
			t.Errorf("workspace %q id %q is not a valid UUID: %v", name, idStr, err)
		}
	}
}

func TestAutoMigrateConfigData_AllNonDestructive(t *testing.T) {
	data := map[string]interface{}{
		"default_workspace": "myws",
		"backend":           "claude",
		"workspaces": map[string]interface{}{
			"ws1": map[string]interface{}{
				"path": "/tmp/ws1",
			},
		},
	}
	result, reachedVersion, err := AutoMigrateConfigData(data)
	if err != nil {
		t.Fatalf("AutoMigrateConfigData() error = %v", err)
	}
	if reachedVersion != CurrentConfigVersion {
		t.Errorf("reachedVersion = %d, want %d", reachedVersion, CurrentConfigVersion)
	}
	if v := getConfigVersion(result); v != CurrentConfigVersion {
		t.Errorf("data version = %d, want %d", v, CurrentConfigVersion)
	}
	if result["default_workspace"] != "myws" {
		t.Error("existing fields should be preserved")
	}
	// Check UUID was added
	workspaces := result["workspaces"].(map[string]interface{})
	ws := workspaces["ws1"].(map[string]interface{})
	if _, ok := ws["id"]; !ok {
		t.Error("workspace ws1 should have an id after auto-migration")
	}
}

func TestAutoMigrateConfigData_AlreadyCurrent(t *testing.T) {
	data := map[string]interface{}{
		"version": CurrentConfigVersion,
		"backend": "claude",
	}
	result, reachedVersion, err := AutoMigrateConfigData(data)
	if err != nil {
		t.Fatalf("AutoMigrateConfigData() error = %v", err)
	}
	if reachedVersion != CurrentConfigVersion {
		t.Errorf("reachedVersion = %d, want %d", reachedVersion, CurrentConfigVersion)
	}
	if result["backend"] != "claude" {
		t.Error("data should be unchanged")
	}
}

func TestAutoMigrateConfigData_FutureVersion(t *testing.T) {
	data := map[string]interface{}{
		"version": 99,
		"backend": "claude",
	}
	result, reachedVersion, err := AutoMigrateConfigData(data)
	if err != nil {
		t.Fatalf("AutoMigrateConfigData() error = %v", err)
	}
	if reachedVersion != 99 {
		t.Errorf("reachedVersion = %d, want 99", reachedVersion)
	}
	if result["backend"] != "claude" {
		t.Error("data should be unchanged")
	}
}

func TestAutoMigrateConfigData_StopsAtDestructive(t *testing.T) {
	// Save and restore the original migrations map
	origMigrations := migrations
	defer func() { migrations = origMigrations }()

	// Set up: v0→v1 is non-destructive, v1→v2 is destructive
	migrations = map[int]Migration{
		0: {Fn: migrateV0ToV1, Destructive: false},
		1: {Fn: migrateV1ToV2, Destructive: true},
	}

	data := map[string]interface{}{
		"default_workspace": "myws",
		"backend":           "claude",
	}
	result, reachedVersion, err := AutoMigrateConfigData(data)
	if err != nil {
		t.Fatalf("AutoMigrateConfigData() error = %v", err)
	}
	if reachedVersion != 1 {
		t.Errorf("reachedVersion = %d, want 1 (should stop before destructive v1→v2)", reachedVersion)
	}
	if getConfigVersion(result) != 1 {
		t.Errorf("data version = %d, want 1", getConfigVersion(result))
	}
}

func TestAutoMigrateConfigData_FirstMigrationDestructive(t *testing.T) {
	origMigrations := migrations
	defer func() { migrations = origMigrations }()

	// Both migrations destructive
	migrations = map[int]Migration{
		0: {Fn: migrateV0ToV1, Destructive: true},
		1: {Fn: migrateV1ToV2, Destructive: true},
	}

	data := map[string]interface{}{
		"backend": "claude",
	}
	result, reachedVersion, err := AutoMigrateConfigData(data)
	if err != nil {
		t.Fatalf("AutoMigrateConfigData() error = %v", err)
	}
	if reachedVersion != 0 {
		t.Errorf("reachedVersion = %d, want 0 (no migrations applied)", reachedVersion)
	}
	if getConfigVersion(result) != 0 {
		t.Errorf("data version = %d, want 0", getConfigVersion(result))
	}
}

func TestAutoMigrateConfigData_NilMap(t *testing.T) {
	// nil data should be handled gracefully
	data := map[string]interface{}(nil)
	if data == nil {
		data = make(map[string]interface{})
	}
	result, reachedVersion, err := AutoMigrateConfigData(data)
	if err != nil {
		t.Fatalf("AutoMigrateConfigData() error = %v", err)
	}
	if reachedVersion != CurrentConfigVersion {
		t.Errorf("reachedVersion = %d, want %d", reachedVersion, CurrentConfigVersion)
	}
	if getConfigVersion(result) != CurrentConfigVersion {
		t.Errorf("data version = %d, want %d", getConfigVersion(result), CurrentConfigVersion)
	}
}

func TestMigrateConfigData_UnchangedBehavior(t *testing.T) {
	// Verify MigrateConfigData still applies all migrations (including destructive)
	origMigrations := migrations
	defer func() { migrations = origMigrations }()

	migrations = map[int]Migration{
		0: {Fn: migrateV0ToV1, Destructive: false},
		1: {Fn: migrateV1ToV2, Destructive: true}, // destructive but MigrateConfigData should apply it
	}

	data := map[string]interface{}{
		"backend": "claude",
	}
	result, err := MigrateConfigData(data)
	if err != nil {
		t.Fatalf("MigrateConfigData() error = %v", err)
	}
	if v := getConfigVersion(result); v != CurrentConfigVersion {
		t.Errorf("version = %d, want %d (MigrateConfigData should apply all, including destructive)", v, CurrentConfigVersion)
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

func TestPendingMigrations_FromV0(t *testing.T) {
	pending := PendingMigrations(0)
	if len(pending) != 2 {
		t.Fatalf("PendingMigrations(0) returned %d entries, want 2", len(pending))
	}

	// First: v0 → v1
	if pending[0].FromVersion != 0 || pending[0].ToVersion != 1 {
		t.Errorf("pending[0] = v%d→v%d, want v0→v1", pending[0].FromVersion, pending[0].ToVersion)
	}
	if pending[0].Description == "" {
		t.Error("pending[0].Description should not be empty")
	}
	if pending[0].Destructive {
		t.Error("pending[0] should not be destructive")
	}

	// Second: v1 → v2
	if pending[1].FromVersion != 1 || pending[1].ToVersion != 2 {
		t.Errorf("pending[1] = v%d→v%d, want v1→v2", pending[1].FromVersion, pending[1].ToVersion)
	}
	if pending[1].Description == "" {
		t.Error("pending[1].Description should not be empty")
	}
	if pending[1].Destructive {
		t.Error("pending[1] should not be destructive")
	}
}

func TestPendingMigrations_AlreadyCurrent(t *testing.T) {
	pending := PendingMigrations(CurrentConfigVersion)
	if len(pending) != 0 {
		t.Errorf("PendingMigrations(CurrentConfigVersion) returned %d entries, want 0", len(pending))
	}
}

func TestPendingMigrations_FutureVersion(t *testing.T) {
	pending := PendingMigrations(CurrentConfigVersion + 5)
	if len(pending) != 0 {
		t.Errorf("PendingMigrations(CurrentConfigVersion+5) returned %d entries, want 0", len(pending))
	}
}

func TestPendingMigrations_Partial(t *testing.T) {
	pending := PendingMigrations(1)
	if len(pending) != 1 {
		t.Fatalf("PendingMigrations(1) returned %d entries, want 1", len(pending))
	}
	if pending[0].FromVersion != 1 || pending[0].ToVersion != 2 {
		t.Errorf("pending[0] = v%d→v%d, want v1→v2", pending[0].FromVersion, pending[0].ToVersion)
	}
}

func TestPendingMigrations_DestructiveFlag(t *testing.T) {
	// Save and restore the original migrations map
	origMigrations := migrations
	defer func() { migrations = origMigrations }()

	migrations = map[int]Migration{
		0: {Fn: migrateV0ToV1, Destructive: false, Description: "step one"},
		1: {Fn: migrateV1ToV2, Destructive: true, Description: "step two"},
	}

	pending := PendingMigrations(0)
	if len(pending) != 2 {
		t.Fatalf("PendingMigrations(0) returned %d entries, want 2", len(pending))
	}
	if pending[0].Destructive {
		t.Error("pending[0] should not be destructive")
	}
	if !pending[1].Destructive {
		t.Error("pending[1] should be destructive")
	}
}

func TestMigrationDescriptions_NotEmpty(t *testing.T) {
	for version, m := range migrations {
		if m.Description == "" {
			t.Errorf("migration v%d→v%d has empty Description", version, version+1)
		}
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
