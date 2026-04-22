package workspacemgr

import (
	"log"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// PurgeOldSessions sweeps orphaned sessions and purges sessions older than
// 30 days across every configured workspace's ~/.loom/sessions/<wsID>/ store.
// Intended to be called as a goroutine during server startup.
func PurgeOldSessions() {
	for _, wsID := range workspaceIDsForPurge() {
		purgeWorkspaceSessions(wsID)
	}
}

func purgeWorkspaceSessions(wsID string) {
	sessStore, err := sessions.NewStoreForWorkspace(wsID, cli.GetBeadsDir())
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

// workspaceIDsForPurge returns every configured workspace UUID, plus an
// empty-string sentinel so pre-migration (or fallback) buckets still get swept.
func workspaceIDsForPurge() []string {
	cfg, err := config.LoadConfig()
	if err != nil || cfg == nil {
		return []string{""}
	}
	ids := make([]string, 0, len(cfg.Workspaces)+1)
	for _, ws := range cfg.Workspaces {
		if ws.ID != "" {
			ids = append(ids, ws.ID)
		}
	}
	ids = append(ids, "")
	return ids
}

// NewSessionStore returns a central session store for the default workspace.
// Returns nil if the store cannot be created.
func NewSessionStore() *sessions.Store {
	wsID := ""
	if cfg, err := config.LoadConfig(); err == nil && cfg != nil {
		wsID = cfg.DefaultWorkspaceID
	}
	s, err := sessions.NewStoreForWorkspace(wsID, cli.GetBeadsDir())
	if err != nil {
		return nil
	}
	return s
}
