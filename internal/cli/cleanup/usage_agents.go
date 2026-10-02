package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

// agentUsageKinds are the agent_events kinds that place an Agent API agent's
// activity in time; usage rows also carry its tokens.
var agentUsageKinds = []string{"turn.started", "usage", "turn.completed"}

// usageTokens is the token part of a usage event's payload. Every field is
// optional and a per-step delta, so summing a session's usage rows gives
// its totals; a usage row without them counts as zero.
type usageTokens struct {
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	CostUSD          float64 `json:"costUsd"`
}

// readAgentUsage returns one usage record per Agent API agent in dataDir's
// agents.db (workspace "" means every workspace) that was active inside f's
// date range. An agent's tokens are the sum of its usage events in the range;
// an agent with none is listed with zero. A missing agents.db is no data.
func readAgentUsage(ctx context.Context, dataDir, workspace string, f usage.Filter) ([]usage.SessionUsage, error) {
	path := filepath.Join(dataDir, "agents.db")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	st, err := loomstore.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()

	agents, _, err := st.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: workspace,
		Name: f.AgentName, Harness: f.Backend, IncludeArchived: true, IncludeDeleted: true})
	if err != nil {
		return nil, err
	}
	var out []usage.SessionUsage
	for _, a := range agents {
		rec, ok, err := agentSessionUsage(ctx, st, a, f)
		if err != nil {
			return nil, err
		}
		if ok && matchesFilterIDs(rec, f) {
			out = append(out, rec)
		}
	}
	return out, nil
}

// agentSessionUsage folds a's events inside f's range into one record. ok is
// false when a neither started nor had an event in the range.
func agentSessionUsage(ctx context.Context, st *loomstore.Store, a loomstore.Agent, f usage.Filter) (usage.SessionUsage, bool, error) {
	rec := agentRecord(a)
	seen := false
	note := func(t time.Time) {
		if !seen || t.Before(rec.StartedAt) {
			rec.StartedAt = t
		}
		if !seen || t.After(rec.EndedAt) {
			rec.EndedAt = t
		}
		seen = true
	}
	if t, err := time.Parse(time.RFC3339Nano, a.CreatedAt); err == nil && inRange(t, f) {
		note(t)
	}
	q := loomstore.EventQuery{AgentID: a.AgentID, Kinds: agentUsageKinds, Limit: 1000}
	for {
		page, err := st.ListEvents(ctx, q)
		if err != nil {
			return rec, false, err
		}
		for _, e := range page.Events {
			t, err := time.Parse(time.RFC3339Nano, e.CreatedAt)
			if err != nil || !inRange(t, f) {
				continue
			}
			note(t)
			if e.Kind != "usage" {
				continue
			}
			var u usageTokens
			_ = json.Unmarshal(e.Payload, &u)
			rec.InputTokens += u.InputTokens
			rec.OutputTokens += u.OutputTokens
			rec.CacheReadTokens += u.CacheReadTokens
			rec.CacheWriteTokens += u.CacheWriteTokens
			rec.EstimatedCostUSD += u.CostUSD
		}
		if !page.More {
			break
		}
		q.After, q.Snapshot = page.Next, page.SnapshotSeq
	}
	rec.StartedAt, rec.EndedAt = rec.StartedAt.Local(), rec.EndedAt.Local()
	return rec, seen, nil
}

// agentRecord is a's usage record before any tokens or times are added.
func agentRecord(a loomstore.Agent) usage.SessionUsage {
	rec := usage.SessionUsage{AgentName: a.Name, Backend: a.Harness, SessionID: a.AgentID}
	if a.Model != nil {
		rec.Model = *a.Model
	}
	if a.SubjectType != nil && a.SubjectID != nil {
		switch *a.SubjectType {
		case "task":
			rec.TaskID = *a.SubjectID
		case "epic":
			rec.EpicID = *a.SubjectID
		}
	}
	return rec
}

func inRange(t time.Time, f usage.Filter) bool {
	return (f.Since.IsZero() || !t.Before(f.Since)) && (f.Until.IsZero() || !t.After(f.Until))
}

func matchesFilterIDs(rec usage.SessionUsage, f usage.Filter) bool {
	return (f.TaskID == "" || rec.TaskID == f.TaskID) && (f.EpicID == "" || rec.EpicID == f.EpicID)
}

// joinUsage appends the Agent API records to the v5 ones, dropping any agent
// a v5 record already counts (same session id), so nothing is counted twice.
func joinUsage(v5, agents []usage.SessionUsage) []usage.SessionUsage {
	counted := make(map[string]bool, len(v5))
	for _, r := range v5 {
		if r.SessionID != "" {
			counted[r.SessionID] = true
		}
	}
	out := v5
	for _, r := range agents {
		if !counted[r.SessionID] {
			out = append(out, r)
		}
	}
	return out
}
