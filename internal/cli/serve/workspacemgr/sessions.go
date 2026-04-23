package workspacemgr

import (
	"fmt"
	"log"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/sessions"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// PurgeOldSessions sweeps orphaned sessions and purges sessions older than
// 30 days across every configured workspace's ~/.loom/sessions/<wsID>/ store.
// Intended to be called as a goroutine during server startup.
func PurgeOldSessions() {
	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil {
		return
	}
	for _, ws := range cfg.Workspaces {
		if ws.ID == "" {
			continue
		}
		purgeWorkspaceSessions(ws.ID)
	}
}

func purgeWorkspaceSessions(wsID string) {
	sessStore, err := sessions.NewStoreForWorkspace(wsID)
	if err != nil {
		return
	}
	if healed, err := sessStore.SweepOrphans(); err != nil {
		log.Printf("[serve] session orphan sweep error (ws=%s): %v", wsID, err)
	} else if healed > 0 {
		log.Printf("[serve] healed %d orphaned sessions on startup (ws=%s)", healed, wsID)
	}
	count, err := sessStore.PurgeOlderThan(30 * 24 * time.Hour)
	if err != nil {
		log.Printf("[serve] session purge error (ws=%s): %v", wsID, err)
	} else if count > 0 {
		log.Printf("[serve] purged %d old sessions (ws=%s)", count, wsID)
	}
}

// NewSessionStore returns a central session store for the default workspace.
// Returns nil if no default workspace UUID is configured or the store cannot
// be created.
func NewSessionStore() *sessions.Store {
	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil || cfg.DefaultWorkspaceID == "" {
		return nil
	}
	s, err := sessions.NewStoreForWorkspace(cfg.DefaultWorkspaceID)
	if err != nil {
		return nil
	}
	return s
}

// OpenInitialSessionStore resolves the current workspace's UUID and opens its
// central session store, returning a single combined error for CLI call sites
// that need to bubble it to the user.
func OpenInitialSessionStore() (*sessions.Store, error) {
	wsID, err := ResolveInitialWorkspaceID()
	if err != nil {
		return nil, fmt.Errorf("resolve workspace ID: %w", err)
	}
	return sessions.NewStoreForWorkspace(wsID)
}

// OpenInitialUsageStore is the usage-store counterpart to
// OpenInitialSessionStore.
func OpenInitialUsageStore() (*usage.Store, error) {
	wsID, err := ResolveInitialWorkspaceID()
	if err != nil {
		return nil, fmt.Errorf("resolve workspace ID: %w", err)
	}
	return usage.NewStoreForWorkspace(wsID)
}
