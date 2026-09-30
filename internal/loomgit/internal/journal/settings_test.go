package journal

import (
	"context"
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
