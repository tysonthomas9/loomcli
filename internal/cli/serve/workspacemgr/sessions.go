package workspacemgr

import (
	"log"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// PurgeOldSessions sweeps orphaned sessions and purges sessions older than 30 days.
// Intended to be called as a goroutine during server startup.
func PurgeOldSessions() {
	sessStore, err := sessions.NewStore(cli.GetBeadsDir())
	if err != nil {
		return
	}
	if healed, err := sessStore.SweepOrphans(); err != nil {
		log.Printf("[serve] session orphan sweep error: %v", err)
	} else if healed > 0 {
		log.Printf("[serve] healed %d orphaned sessions on startup", healed)
	}
	count, err := sessStore.PurgeOlderThan(30 * 24 * time.Hour)
	if err != nil {
		log.Printf("[serve] session purge error: %v", err)
	} else if count > 0 {
		log.Printf("[serve] purged %d old sessions", count)
	}
}

// NewSessionStore creates a sessions.Store for the beads directory.
// Returns nil if the directory is empty or if the store cannot be created.
func NewSessionStore() *sessions.Store {
	dir := cli.GetBeadsDir()
	if dir == "" {
		return nil
	}
	s, err := sessions.NewStore(dir)
	if err != nil {
		return nil
	}
	return s
}
