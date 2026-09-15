package usage

import (
	"fmt"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/sessions"
)

// SessionsLedgerName is the path, relative to the runtime directory, of the
// authoritative session ledger. Every real fleet run is finalized into it by
// internal/sessions; the legacy usage.jsonl is only written by `loom auto`.
const SessionsLedgerName = "sessions/index.jsonl"

// ReadSessionUsage reads agent usage from the session index
// (<runtimeDir>/sessions/index.jsonl), the ledger every finalized run writes
// to, and adapts it to the SessionUsage shape shared with the legacy
// usage.jsonl reader. It returns the records, the resolved ledger path (for
// display, so a reader can tell where a number came from) and an error.
//
// Records are read through sessions.Store.Query, which deduplicates the index
// by session ID — the index is append-only and carries one "running" entry plus
// one finalized entry per session, so reading the file directly would roughly
// double every total.
//
// A missing index is not an error: the result is empty and the returned path
// still names the file that was looked for.
func ReadSessionUsage(runtimeDir string, f Filter) ([]SessionUsage, string, error) {
	if runtimeDir == "" {
		runtimeDir = "."
	}
	ledgerPath := filepath.Join(runtimeDir, SessionsLedgerName)

	store, err := sessions.NewStore(runtimeDir)
	if err != nil {
		return nil, ledgerPath, fmt.Errorf("open sessions store: %w", err)
	}

	recs, err := store.Query(sessionsFilter(f))
	if err != nil {
		return nil, ledgerPath, fmt.Errorf("query sessions index: %w", err)
	}

	out := make([]SessionUsage, 0, len(recs))
	for _, rec := range recs {
		out = append(out, adaptSessionRecord(rec))
	}
	return out, ledgerPath, nil
}

// sessionsFilter translates a usage.Filter into the sessions.Filter accepted by
// the session store. Both bound --since/--until against StartedAt, matching the
// legacy reader's semantics closely enough that the same flags mean the same
// thing on either source.
func sessionsFilter(f Filter) sessions.Filter {
	return sessions.Filter{
		TaskID:    f.TaskID,
		EpicID:    f.EpicID,
		AgentName: f.AgentName,
		Backend:   f.Backend,
		Status:    sessions.SessionStatus(f.Status),
		Since:     f.Since,
		Until:     f.Until,
	}
}

// adaptSessionRecord converts one session index record into a SessionUsage.
//
// EstimatedCostUSD is copied through, never derived: the session index only
// carries a cost when the backend reported one, and Loom does not fabricate
// cost from token counts (see SessionUsage).
func adaptSessionRecord(rec sessions.SessionRecord) SessionUsage {
	u := SessionUsage{
		AgentName:        rec.AgentName,
		Backend:          rec.Backend,
		TaskID:           rec.TaskID,
		EpicID:           rec.EpicID,
		InputTokens:      rec.InputTokens,
		OutputTokens:     rec.OutputTokens,
		CacheReadTokens:  rec.CacheReadTokens,
		CacheWriteTokens: rec.CacheWriteTokens,
		EstimatedCostUSD: rec.EstimatedCostUSD,
		StartedAt:        rec.StartedAt,
		ExitCode:         rec.ExitCode,
		Model:            rec.Model,
		SessionID:        rec.SessionID,
		Status:           string(rec.Status),
		DurationS:        rec.DurationS,
	}
	// Running sessions have no end time; leave EndedAt zero rather than
	// inventing one, and let the renderer fall back to DurationS.
	if rec.EndedAt != nil {
		u.EndedAt = *rec.EndedAt
	}
	if u.DurationS == 0 && rec.EndedAt != nil {
		u.DurationS = rec.EndedAt.Sub(rec.StartedAt).Seconds()
	}
	return u
}
