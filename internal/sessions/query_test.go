package sessions

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// seedKnownAgentsIndex writes, for each agent, one session that has both a
// `running` and a later `completed` row — the shape a stray writer leaves
// behind, and the shape that proves filtering happens before dedup.
func seedKnownAgentsIndex(t *testing.T, store *Store, agents map[string]string) {
	t.Helper()
	now := time.Now().UTC()
	for agent, id := range agents {
		if err := os.MkdirAll(filepath.Join(store.Dir(), id), 0o700); err != nil {
			t.Fatal(err)
		}
		started := now.Add(-10 * time.Minute)
		if err := store.ReIndex(SessionRecord{
			SessionID: id,
			AgentName: agent,
			Status:    StatusRunning,
			StartedAt: started,
		}); err != nil {
			t.Fatal(err)
		}
		ended := now.Add(-time.Minute)
		if err := store.ReIndex(SessionRecord{
			SessionID: id,
			AgentName: agent,
			Status:    StatusCompleted,
			StartedAt: started,
			EndedAt:   &ended,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func agentNamesOf(records []SessionRecord) []string {
	names := make([]string, 0, len(records))
	for _, rec := range records {
		names = append(names, rec.AgentName)
	}
	sort.Strings(names)
	return names
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestQueryKnownAgents(t *testing.T) {
	tests := []struct {
		name        string
		knownAgents []string
		want        []string
	}{
		{
			// A bug here would silently report zero runs during a real
			// outage, so the no-op case is asserted explicitly.
			name:        "nil is a no-op",
			knownAgents: nil,
			want:        []string{"fixture-writer", "worker", "worker-2"},
		},
		{
			name:        "empty slice is a no-op",
			knownAgents: []string{},
			want:        []string{"fixture-writer", "worker", "worker-2"},
		},
		{
			name:        "populated allowlist drops unknown names",
			knownAgents: []string{"worker", "worker-2"},
			want:        []string{"worker", "worker-2"},
		},
		{
			name:        "allowlist naming no present agent yields nothing",
			knownAgents: []string{"nobody"},
			want:        []string{},
		},
		{
			name:        "unrelated names in the allowlist are harmless",
			knownAgents: []string{"worker", "retired-agent"},
			want:        []string{"worker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			seedKnownAgentsIndex(t, store, map[string]string{
				"worker":         "sess-worker",
				"worker-2":       "sess-worker-2",
				"fixture-writer": "sess-fixture",
			})

			got, err := store.Query(Filter{KnownAgents: tt.knownAgents})
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			if names := agentNamesOf(got); !equalStrings(names, tt.want) {
				t.Errorf("Query agents = %v, want %v", names, tt.want)
			}

			// readDedupedIndex is the chokepoint every other consumer
			// (Snapshot included) reads through; it must agree with Query.
			deduped, err := store.readDedupedIndex(Filter{KnownAgents: tt.knownAgents})
			if err != nil {
				t.Fatalf("readDedupedIndex: %v", err)
			}
			if names := agentNamesOf(deduped); !equalStrings(names, tt.want) {
				t.Errorf("readDedupedIndex agents = %v, want %v", names, tt.want)
			}
		})
	}
}

// TestQueryKnownAgentsDropsBothRows checks that filtering happens BEFORE
// dedup: an excluded session must leave no row at all, not a surviving
// `running` row whose finalized version was filtered out.
func TestQueryKnownAgentsDropsBothRows(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	seedKnownAgentsIndex(t, store, map[string]string{
		"worker":         "sess-worker",
		"fixture-writer": "sess-fixture",
	})

	got, err := store.Query(Filter{KnownAgents: []string{"worker"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, rec := range got {
		if rec.SessionID == "sess-fixture" {
			t.Fatalf("excluded session still present with status %q", rec.Status)
		}
	}
	if len(got) != 1 || got[0].Status != StatusCompleted {
		t.Fatalf("got %d record(s), want the single finalized worker row: %+v", len(got), got)
	}
}

// TestQueryKnownAgentsWithOtherFilters checks the allowlist composes with the
// existing filter fields rather than replacing them.
func TestQueryKnownAgentsWithOtherFilters(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	seedKnownAgentsIndex(t, store, map[string]string{
		"worker":   "sess-worker",
		"worker-2": "sess-worker-2",
	})

	got, err := store.Query(Filter{AgentName: "worker-2", KnownAgents: []string{"worker"}})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d record(s), want 0 — AgentName and KnownAgents must both apply", len(got))
	}
}
