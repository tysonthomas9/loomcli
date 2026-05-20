package localredis

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestLoadBackupAndErrorBranches(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	snapPath := filepath.Join(dir, "snapshot.json")

	backup := snapshot{
		SchemaVersion: currentSchemaVersion,
		DumpedAt:      time.Now(),
		Entries: []snapshotEntry{{
			Key:    "terminal:meta:ws1:from-backup",
			Type:   "hash",
			TTLMs:  -1,
			Hash:   map[string]string{"label": "backup"},
			String: "ignored",
		}},
	}
	backupData, err := json.Marshal(backup)
	if err != nil {
		t.Fatalf("marshal backup: %v", err)
	}
	if err := os.WriteFile(snapPath+".bak", backupData, 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	m, err := NewManager("", false, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()
	m.snapshotPath = snapPath
	if err := m.load(); err != nil {
		t.Fatalf("load from backup: %v", err)
	}
	label, err := m.Client().HGet(ctx, "terminal:meta:ws1:from-backup", "label").Result()
	if err != nil || label != "backup" {
		t.Fatalf("backup label = %q, %v", label, err)
	}
	if !m.lastDumpSet {
		t.Fatal("load did not seed lastDumpSet")
	}

	newer := snapshot{SchemaVersion: currentSchemaVersion + 1}
	newerData, err := json.Marshal(newer)
	if err != nil {
		t.Fatalf("marshal newer: %v", err)
	}
	if err := os.WriteFile(snapPath, newerData, 0o644); err != nil {
		t.Fatalf("write newer: %v", err)
	}
	if err := m.load(); err == nil {
		t.Fatal("load newer schema error = nil")
	}

	if err := os.WriteFile(snapPath, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("write corrupt primary: %v", err)
	}
	if err := os.WriteFile(snapPath+".bak", []byte("{also-bad"), 0o644); err != nil {
		t.Fatalf("write corrupt backup: %v", err)
	}
	if err := m.load(); err == nil {
		t.Fatal("load corrupt primary+backup error = nil")
	}

	noPermPath := filepath.Join(dir, "subdir")
	if err := os.Mkdir(noPermPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	m.snapshotPath = noPermPath
	if err := m.load(); err == nil {
		t.Fatal("load directory path error = nil")
	}
}

func TestReplaySkipsEmptyUnknownAndAppliesTTL(t *testing.T) {
	m, err := NewManager("", false, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	snap := &snapshot{
		SchemaVersion: currentSchemaVersion,
		DumpedAt:      time.Now(),
		Entries: []snapshotEntry{
			{Key: "terminal:meta:empty-hash", Type: "hash", TTLMs: -1},
			{Key: "terminal:set:empty", Type: "set", TTLMs: -1},
			{Key: "terminal:list:empty", Type: "list", TTLMs: -1},
			{Key: "terminal:zset:empty", Type: "zset", TTLMs: -1},
			{Key: "terminal:stream:empty", Type: "stream", TTLMs: -1},
			{Key: "terminal:unknown", Type: "geo", TTLMs: -1},
			{Key: "terminal:meta:ttl", Type: "hash", TTLMs: int64(time.Hour / time.Millisecond), Hash: map[string]string{"label": "ttl"}},
		},
	}
	if err := m.replay(snap); err != nil {
		t.Fatalf("replay: %v", err)
	}

	ctx := context.Background()
	if exists, err := m.Client().Exists(ctx, "terminal:meta:empty-hash", "terminal:unknown").Result(); err != nil || exists != 0 {
		t.Fatalf("empty/unknown exists = %d, %v", exists, err)
	}
	ttl, err := m.Client().PTTL(ctx, "terminal:meta:ttl").Result()
	if err != nil {
		t.Fatalf("PTTL: %v", err)
	}
	if ttl <= 0 || ttl > time.Hour {
		t.Fatalf("ttl = %v, want (0, 1h]", ttl)
	}

	future := &snapshot{
		SchemaVersion: currentSchemaVersion,
		DumpedAt:      time.Now().Add(time.Hour),
		Entries: []snapshotEntry{{
			Key:    "terminal:meta:future",
			Type:   "hash",
			TTLMs:  int64(time.Minute / time.Millisecond),
			Hash:   map[string]string{"label": "future"},
			String: "ignored",
		}},
	}
	if err := m.replay(future); err != nil {
		t.Fatalf("future replay: %v", err)
	}
	if got, err := m.Client().HGet(ctx, "terminal:meta:future", "label").Result(); err != nil || got != "future" {
		t.Fatalf("future replay label = %q, %v", got, err)
	}
}

func TestReadEntrySkipsExpiredOrMissingKey(t *testing.T) {
	m, err := NewManager("", false, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	entry, err := m.readEntry(context.Background(), "terminal:meta:missing")
	if err != nil {
		t.Fatalf("readEntry missing: %v", err)
	}
	if entry != nil {
		t.Fatalf("missing entry = %+v, want nil", entry)
	}
}

func TestDumpNoopsWhenSnapshotContentUnchanged(t *testing.T) {
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	m, err := NewManager(snapPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	if err := m.Client().HSet(context.Background(), "terminal:meta:ws1:s1", "label", "same").Err(); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	if err := m.Dump(); err != nil {
		t.Fatalf("first Dump: %v", err)
	}
	info, err := os.Stat(snapPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	firstMod := info.ModTime()
	if err := m.Dump(); err != nil {
		t.Fatalf("second Dump: %v", err)
	}
	info, err = os.Stat(snapPath)
	if err != nil {
		t.Fatalf("stat second: %v", err)
	}
	if !info.ModTime().Equal(firstMod) {
		t.Fatalf("unchanged dump rewrote snapshot: first=%s second=%s", firstMod, info.ModTime())
	}
}

func TestManagerCollectDumpReloadsAllPersistedTypes(t *testing.T) {
	ctx := context.Background()
	snapPath := filepath.Join(t.TempDir(), "snapshot.json")
	m, err := NewManager(snapPath, true, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	client := m.Client()
	if err := client.HSet(ctx, "terminal:meta:ws1:tab1", "label", "Lead").Err(); err != nil {
		t.Fatalf("HSet: %v", err)
	}
	if err := client.Set(ctx, "fleet-db:string", "value", 0).Err(); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := client.SAdd(ctx, "ws:WS:issue:tabs:set", "b", "a").Err(); err != nil {
		t.Fatalf("SAdd: %v", err)
	}
	if err := client.RPush(ctx, "ws:WS:list", "first", "second").Err(); err != nil {
		t.Fatalf("RPush: %v", err)
	}
	if err := client.ZAdd(ctx, "fleet-db:zset", redis.Z{Member: "later", Score: 2}, redis.Z{Member: "earlier", Score: 1}).Err(); err != nil {
		t.Fatalf("ZAdd: %v", err)
	}
	if _, err := client.XAdd(ctx, &redis.XAddArgs{
		Stream: "fleet-db:stream",
		ID:     "*",
		Values: map[string]any{"kind": "created", "n": 3},
	}).Result(); err != nil {
		t.Fatalf("XAdd: %v", err)
	}
	if err := client.Set(ctx, "ignored:key", "skip", 0).Err(); err != nil {
		t.Fatalf("Set ignored: %v", err)
	}
	if err := client.Set(ctx, "terminal:ui-state:ttl", "short", time.Minute).Err(); err != nil {
		t.Fatalf("Set ttl: %v", err)
	}

	entries, err := m.collectEntries(ctx)
	if err != nil {
		t.Fatalf("collectEntries: %v", err)
	}
	byType := map[string]bool{}
	for _, entry := range entries {
		if entry.Key == "ignored:key" {
			t.Fatal("collectEntries persisted an ignored key")
		}
		byType[entry.Type] = true
	}
	for _, typ := range []string{"hash", "string", "set", "list", "zset", "stream"} {
		if !byType[typ] {
			t.Fatalf("missing persisted type %q in %+v", typ, entries)
		}
	}

	m.Start(ctx)
	m.Start(ctx)
	if err := m.Dump(); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if err := client.HSet(ctx, "terminal:meta:ws1:tab1", "label", "Lead Updated").Err(); err != nil {
		t.Fatalf("HSet update: %v", err)
	}
	if err := m.Dump(); err != nil {
		t.Fatalf("second Dump: %v", err)
	}
	if _, err := os.Stat(snapPath + ".bak"); err != nil {
		t.Fatalf("backup snapshot was not written: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	reloaded, err := NewManager(snapPath, true, nil)
	if err != nil {
		t.Fatalf("reload NewManager: %v", err)
	}
	defer reloaded.Close()
	got, err := reloaded.Client().HGet(ctx, "terminal:meta:ws1:tab1", "label").Result()
	if err != nil || got != "Lead Updated" {
		t.Fatalf("reloaded hash = %q, %v", got, err)
	}
	stream, err := reloaded.Client().XRange(ctx, "fleet-db:stream", "-", "+").Result()
	if err != nil || len(stream) != 1 || stream[0].Values["kind"] != "created" || stream[0].Values["n"] != "3" {
		t.Fatalf("reloaded stream = %+v, %v", stream, err)
	}
}

func TestReplaySkipsExpiredEntries(t *testing.T) {
	m, err := NewManager("", false, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	snap := &snapshot{
		SchemaVersion: currentSchemaVersion,
		DumpedAt:      time.Now().Add(-time.Hour),
		Entries: []snapshotEntry{{
			Key:   "terminal:meta:expired",
			Type:  "hash",
			TTLMs: 1,
			Hash:  map[string]string{"label": "expired"},
		}},
	}
	if err := m.replay(snap); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if exists, err := m.Client().Exists(context.Background(), "terminal:meta:expired").Result(); err != nil || exists != 0 {
		t.Fatalf("expired key exists = %d, %v", exists, err)
	}
}

func TestManagerStartLoadAndDumpErrorBranches(t *testing.T) {
	corruptPath := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(corruptPath, []byte("{bad-json"), 0o644); err != nil {
		t.Fatalf("write corrupt snapshot: %v", err)
	}
	m, err := NewManager(corruptPath, false, nil)
	if err != nil {
		t.Fatalf("NewManager with corrupt snapshot: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("close corrupt snapshot manager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m, err = NewManager(filepath.Join(t.TempDir(), "snapshot.json"), false, nil)
	if err != nil {
		t.Fatalf("NewManager start ctx: %v", err)
	}
	m.Start(ctx)
	select {
	case <-m.stoppedCh:
	case <-time.After(time.Second):
		t.Fatal("periodic dump goroutine did not stop after context cancellation")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("close canceled manager: %v", err)
	}

	fileDataDir := filepath.Join(t.TempDir(), "not-dir")
	if err := os.WriteFile(fileDataDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write not-dir: %v", err)
	}
	m, err = NewManager(filepath.Join(fileDataDir, "snapshot.json"), false, nil)
	if err != nil {
		t.Fatalf("NewManager bad snapshot dir: %v", err)
	}
	if err := m.Dump(); err == nil || !strings.Contains(err.Error(), "mkdir snapshot dir") {
		t.Fatalf("Dump mkdir error = %v", err)
	}
	if err := m.Close(); err == nil || !strings.Contains(err.Error(), "mkdir snapshot dir") {
		t.Fatalf("Close final dump error = %v", err)
	}

	m, err = NewManager(filepath.Join(t.TempDir(), "snapshot.json"), false, nil)
	if err != nil {
		t.Fatalf("NewManager closed client: %v", err)
	}
	if err := m.client.Close(); err != nil {
		t.Fatalf("close redis client: %v", err)
	}
	if _, err := m.collectEntries(context.Background()); err == nil || !strings.Contains(err.Error(), "scan") {
		t.Fatalf("collectEntries closed client err = %v", err)
	}
	if _, err := m.readEntry(context.Background(), "terminal:meta:missing"); err == nil {
		t.Fatal("readEntry closed client error = nil")
	}
	if err := m.Dump(); err == nil || !strings.Contains(err.Error(), "collect entries") {
		t.Fatalf("Dump closed client err = %v", err)
	}
	m.mr.Close()
}
