package localredis

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestDumpLoad_RoundTripV2Types verifies that set, list, zset, and
// stream entries round-trip through dump+load. Required for embedded
// fleet-db mode — fleet-db uses SETs for indexes, ZSETs for priority
// queues, and STREAMs for the event log; without these the read model
// would be lost on restart.
//
// Uses fleetKeys=true so the test can exercise fleet-db: prefixed keys
// (the only way these types currently get persisted).
func TestDumpLoad_RoundTripV2Types(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	ctx := context.Background()

	m1, err := NewManager(snapPath, true /* fleetKeys */, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// SET — workspace registry shape.
	if err := m1.Client().SAdd(ctx, "fleet-db:workspaces", "WSA", "WSB").Err(); err != nil {
		t.Fatalf("SAdd: %v", err)
	}
	// LIST — comments shape.
	if err := m1.Client().RPush(ctx, "fleet-db:WSA:comments:I-1", "first", "second").Err(); err != nil {
		t.Fatalf("RPush: %v", err)
	}
	// ZSET — priority queue shape.
	if err := m1.Client().ZAdd(ctx, "fleet-db:WSA:queue:ready",
		redis.Z{Score: 2.0, Member: "I-1"},
		redis.Z{Score: 1.0, Member: "I-2"},
	).Err(); err != nil {
		t.Fatalf("ZAdd: %v", err)
	}
	// STREAM — event log shape with two entries; preserve IDs.
	if _, err := m1.Client().XAdd(ctx, &redis.XAddArgs{
		Stream: "fleet-db:WSA:events",
		ID:     "1-0",
		Values: map[string]any{"action": "issue.create", "entity": "I-1", "actor": "alice"},
	}).Result(); err != nil {
		t.Fatalf("XAdd 1-0: %v", err)
	}
	if _, err := m1.Client().XAdd(ctx, &redis.XAddArgs{
		Stream: "fleet-db:WSA:events",
		ID:     "2-0",
		Values: map[string]any{"action": "issue.update", "entity": "I-1"},
	}).Result(); err != nil {
		t.Fatalf("XAdd 2-0: %v", err)
	}

	if err := m1.Close(); err != nil {
		t.Fatalf("Close m1: %v", err)
	}

	// Reload + assert each type came back intact.
	m2, err := NewManager(snapPath, true, nil)
	if err != nil {
		t.Fatalf("NewManager m2: %v", err)
	}
	defer m2.Close()

	got, err := m2.Client().SMembers(ctx, "fleet-db:workspaces").Result()
	if err != nil {
		t.Fatalf("SMembers: %v", err)
	}
	sort.Strings(got)
	if len(got) != 2 || got[0] != "WSA" || got[1] != "WSB" {
		t.Errorf("set = %v, want [WSA WSB]", got)
	}

	list, err := m2.Client().LRange(ctx, "fleet-db:WSA:comments:I-1", 0, -1).Result()
	if err != nil {
		t.Fatalf("LRange: %v", err)
	}
	if len(list) != 2 || list[0] != "first" || list[1] != "second" {
		t.Errorf("list = %v, want [first second]", list)
	}

	zs, err := m2.Client().ZRangeWithScores(ctx, "fleet-db:WSA:queue:ready", 0, -1).Result()
	if err != nil {
		t.Fatalf("ZRangeWithScores: %v", err)
	}
	if len(zs) != 2 {
		t.Fatalf("zset len = %d, want 2", len(zs))
	}
	// Ascending by score: I-2 (1.0) then I-1 (2.0).
	if m, _ := zs[0].Member.(string); m != "I-2" || zs[0].Score != 1.0 {
		t.Errorf("zs[0] = %+v, want {I-2, 1.0}", zs[0])
	}
	if m, _ := zs[1].Member.(string); m != "I-1" || zs[1].Score != 2.0 {
		t.Errorf("zs[1] = %+v, want {I-1, 2.0}", zs[1])
	}

	msgs, err := m2.Client().XRange(ctx, "fleet-db:WSA:events", "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("stream len = %d, want 2", len(msgs))
	}
	if msgs[0].ID != "1-0" || msgs[1].ID != "2-0" {
		t.Errorf("stream IDs = [%s, %s], want [1-0, 2-0]", msgs[0].ID, msgs[1].ID)
	}
	if msgs[0].Values["action"] != "issue.create" || msgs[1].Values["action"] != "issue.update" {
		t.Errorf("stream values mismatch: %+v", msgs)
	}
}
