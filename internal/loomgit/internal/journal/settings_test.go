package journal

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestNewWorkspaceSettingsAllowLeadPublishApproval(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetAutoCommit(context.Background(), "ws", false); err != nil {
		t.Fatal(err)
	}
	var enabled int
	if err := store.db.QueryRow("SELECT lead_may_approve_publish FROM workspace_settings WHERE workspace = ?", "ws").Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatalf("lead publish approval default = %d, want 1", enabled)
	}
}

func TestDeliveryModeAndFeatureFlagMigrateExistingJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE workspace_settings (
		workspace TEXT PRIMARY KEY, auto_commit INTEGER NOT NULL DEFAULT 1,
		lead_may_approve_publish INTEGER NOT NULL DEFAULT 1);
		CREATE TABLE change_publications (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, repo TEXT NOT NULL,
		branch TEXT NOT NULL, trunk TEXT NOT NULL, slug TEXT NOT NULL,
		head_sha TEXT NOT NULL, phase TEXT NOT NULL, pr_number INTEGER NOT NULL DEFAULT 0,
		pr_url TEXT NOT NULL DEFAULT '', PRIMARY KEY(workspace,change_id));`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	mode, err := store.DeliveryMode(context.Background(), "ws")
	if err != nil || mode != "stack" {
		t.Fatalf("migrated mode = %q, %v", mode, err)
	}
	publication := Publication{Workspace: "ws", Change: "C", Repo: "repo", Branch: "branch", Trunk: "main",
		Slug: "owner/repo", Head: "head", FeatureFlag: "new_checkout"}
	if err := store.BeginPublication(context.Background(), publication); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.Publication(context.Background(), "ws", "C")
	if err != nil || !found || got.FeatureFlag != "new_checkout" {
		t.Fatalf("migrated publication = %+v, %t, %v", got, found, err)
	}
}

func TestDeliveryModeDefaultsToStackAndPersistsTrunk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mode, err := store.DeliveryMode(ctx, "ws")
	if err != nil || mode != "stack" {
		t.Fatalf("default delivery mode = %q, %v", mode, err)
	}
	if err := store.SetDeliveryMode(ctx, "ws", "trunk"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	mode, err = store.DeliveryMode(ctx, "ws")
	if err != nil || mode != "trunk" {
		t.Fatalf("persisted delivery mode = %q, %v", mode, err)
	}
}
