// Package localredis provides an in-process miniredis instance with
// JSON snapshot persistence. It powers the terminal-state stores
// (tabmeta, issuetabs, sessionhistory, terminal:ui-state) when loom serve
// is run without an external Redis address.
//
// The snapshot file lives under ~/.loom/terminal-state/snapshot.json and
// is rewritten atomically every 30 seconds and on shutdown. The previous
// snapshot is retained as snapshot.json.bak so a partial write during a
// hard kill cannot wipe user state.
package localredis

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const (
	// currentSchemaVersion is the snapshot format version this code reads
	// AND writes. Bump when adding new snapshotEntry types so snapshots
	// written by this version cannot be loaded by older binaries
	// (older binaries reject snap.SchemaVersion > currentSchemaVersion).
	//
	// v1: hash, string
	// v2: + set, list, zset, stream  (required for embedded fleet-db)
	currentSchemaVersion = 2
	defaultInterval      = 30 * time.Second
	dumpTimeout          = 5 * time.Second

	// maxStreamEntriesPerKey caps how many entries from a single Redis
	// Stream are serialized into the snapshot. Fleet-db's compaction
	// keeps streams bounded, but this is a defense against runaway
	// growth that would otherwise balloon the snapshot file (and the
	// in-process JSON marshal). Newest entries are kept; older ones
	// stay in the live miniredis but don't survive restarts.
	maxStreamEntriesPerKey = 10000
)

// includedPrefixes are key patterns always persisted.
var includedPrefixes = []string{
	"terminal:meta:",
	"terminal:ui-state", // matches per-workspace "terminal:ui-state:{wsID}" hashes via HasPrefix
	"ws:",               // matches ws:{wsID}:issue:tabs:* and ws:{wsID}:issue:sessions:*
}

// fleetPrefixes are key patterns persisted only when fleet mode is on.
// Includes fleet-db's entire keyspace so the embedded-fleet-db CLI flow
// (internal/bootstrap/embedded.go) survives across CLI invocations —
// each `loom <cmd>` boots a fresh fleet-db subprocess that connects to
// this in-process miniredis, and the snapshot must round-trip the
// workspace/repo/agent/role/issue data fleet-db wrote.
var fleetPrefixes = []string{
	"fleet:jwt-signing-key:",
	"fleet-db:",
}

// Manager owns the miniredis lifecycle and snapshot persistence.
type Manager struct {
	mr           *miniredis.Miniredis
	client       *redis.Client
	snapshotPath string
	fleetKeys    bool
	logger       *slog.Logger

	stopCh    chan struct{}
	stoppedCh chan struct{}
	started   bool
	mu        sync.Mutex
	closeOnce sync.Once

	// lastDumpHash is the SHA-256 of the most recently written snapshot
	// payload. Subsequent Dump calls compare against this to skip
	// disk writes when the keyspace hasn't changed. Empty until the
	// first successful dump; cleared on the rare error path so the
	// next attempt always writes.
	lastDumpHash [sha256.Size]byte
	lastDumpSet  bool
}

// NewManager starts an in-process miniredis and returns a Manager.
// If snapshotPath is non-empty, the Manager loads an existing snapshot
// from disk but does NOT start the periodic dump goroutine — call Start
// for that. Pass an empty snapshotPath to disable persistence entirely
// (tests).
func NewManager(snapshotPath string, fleetKeys bool, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}
	mr, err := miniredis.Run()
	if err != nil {
		return nil, fmt.Errorf("start miniredis: %w", err)
	}
	m := &Manager{
		mr:           mr,
		client:       redis.NewClient(&redis.Options{Addr: mr.Addr()}),
		snapshotPath: snapshotPath,
		fleetKeys:    fleetKeys,
		logger:       logger,
		stopCh:       make(chan struct{}),
		stoppedCh:    make(chan struct{}),
	}
	if snapshotPath != "" {
		if err := m.load(); err != nil {
			// Corrupt or missing snapshots are non-fatal — start empty.
			logger.Warn("failed to load redis snapshot, starting empty", "path", snapshotPath, "err", err)
		}
	}
	return m, nil
}

// Addr returns the listening address of the in-process miniredis, e.g. "127.0.0.1:45678".
func (m *Manager) Addr() string { return m.mr.Addr() }

// Client returns a shared *redis.Client connected to the in-process miniredis.
// Callers should NOT close this client — Manager owns it.
func (m *Manager) Client() *redis.Client { return m.client }

// Start launches the periodic snapshot goroutine. Idempotent: safe to
// call multiple times. The goroutine stops when ctx is cancelled OR
// when Close is called. If snapshotPath is empty, Start is a no-op.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started || m.snapshotPath == "" {
		return
	}
	m.started = true
	go m.run(ctx)
}

// run is the periodic-dump loop. It uses context.Background() for the
// dump call itself so shutdown dumps still go through after the parent
// ctx is cancelled.
func (m *Manager) run(ctx context.Context) {
	defer close(m.stoppedCh)
	ticker := time.NewTicker(defaultInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			if err := m.Dump(); err != nil {
				m.logger.Warn("periodic snapshot dump failed", "err", err)
			}
		}
	}
}

// Dump writes the current keyspace to the snapshot file. Safe to call
// from any goroutine; the miniredis SCAN is serialized internally.
// Uses context.Background() with a short timeout, so this keeps working
// even when the parent ctx has been cancelled at shutdown.
//
// Skips the disk write when the entries hash matches the previous dump
// (no-op churn detector). DumpedAt is intentionally NOT included in the
// hash so the dump is content-addressed, not time-addressed.
func (m *Manager) Dump() error {
	if m.snapshotPath == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), dumpTimeout)
	defer cancel()

	entries, err := m.collectEntries(ctx)
	if err != nil {
		return fmt.Errorf("collect entries: %w", err)
	}
	// Hash the entries (excluding DumpedAt timestamp) so an idle
	// keyspace doesn't cause a write every tick. JSON-marshaled entries
	// are deterministic because collectEntries sorts keys.
	entriesJSON, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("hash snapshot entries: %w", err)
	}
	hash := sha256.Sum256(entriesJSON)
	m.mu.Lock()
	if m.lastDumpSet && m.lastDumpHash == hash {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	snap := snapshot{
		SchemaVersion: currentSchemaVersion,
		DumpedAt:      time.Now().UTC(),
		Entries:       entries,
	}
	data, err := json.MarshalIndent(&snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(m.snapshotPath), 0o755); err != nil {
		return fmt.Errorf("mkdir snapshot dir: %w", err)
	}
	// Rotate current → .bak before overwriting. Tolerate missing source
	// (first run). On subsequent runs this gives us one fallback copy if
	// a later write is corrupted by a hard kill.
	backupPath := m.snapshotPath + ".bak"
	if _, statErr := os.Stat(m.snapshotPath); statErr == nil {
		_ = os.Rename(m.snapshotPath, backupPath)
	}
	tmpPath := m.snapshotPath + ".tmp"
	// Snapshot may contain the fleet JWT signing key when fleetKeys is on,
	// so lock down perms in that case. Plain UI state stays 0o644.
	perm := os.FileMode(0o644)
	if m.fleetKeys {
		perm = 0o600
	}
	// #nosec G306 — file mode varies by snapshot sensitivity, see above
	if err := os.WriteFile(tmpPath, data, perm); err != nil {
		return fmt.Errorf("write snapshot tmp: %w", err)
	}
	if err := os.Rename(tmpPath, m.snapshotPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename snapshot: %w", err)
	}
	m.mu.Lock()
	m.lastDumpHash = hash
	m.lastDumpSet = true
	m.mu.Unlock()
	return nil
}

// Close stops the periodic dump goroutine, writes a final snapshot, and
// shuts down the miniredis server. Safe to call multiple times.
func (m *Manager) Close() error {
	var closeErr error
	m.closeOnce.Do(func() {
		m.mu.Lock()
		running := m.started
		m.mu.Unlock()
		if running {
			close(m.stopCh)
			<-m.stoppedCh
		}
		if err := m.Dump(); err != nil {
			m.logger.Warn("final snapshot dump failed", "err", err)
			closeErr = err
		}
		_ = m.client.Close()
		m.mr.Close()
	})
	return closeErr
}

// --- snapshot format ---

type snapshot struct {
	SchemaVersion int             `json:"schema_version"`
	DumpedAt      time.Time       `json:"dumped_at"`
	Entries       []snapshotEntry `json:"entries"`
}

type snapshotEntry struct {
	Key    string            `json:"key"`
	Type   string            `json:"type"`             // "hash"|"string"|"set"|"list"|"zset"|"stream"
	TTLMs  int64             `json:"ttl_ms"`           // -1 for no expiry
	Hash   map[string]string `json:"hash,omitempty"`   // type=hash
	String string            `json:"string,omitempty"` // type=string
	Set    []string          `json:"set,omitempty"`    // type=set (order not significant)
	List   []string          `json:"list,omitempty"`   // type=list (head-to-tail)
	ZSet   []zEntry          `json:"zset,omitempty"`   // type=zset
	Stream []streamEntry     `json:"stream,omitempty"` // type=stream (oldest-first; IDs preserved)
}

// zEntry pairs a sorted-set member with its score. Score is float64 to
// match Redis semantics; lossy roundtrip across JSON is acceptable for
// loom's use cases (priority queues use integer scores).
type zEntry struct {
	Member string  `json:"member"`
	Score  float64 `json:"score"`
}

// streamEntry mirrors a Redis Stream entry. ID is preserved verbatim so
// downstream consumers' cursors remain valid across snapshot reload.
type streamEntry struct {
	ID     string            `json:"id"`
	Values map[string]string `json:"values"`
}

func (m *Manager) shouldPersist(key string) bool {
	for _, p := range includedPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	if m.fleetKeys {
		for _, p := range fleetPrefixes {
			if strings.HasPrefix(key, p) {
				return true
			}
		}
	}
	return false
}

// collectEntries walks the miniredis keyspace and returns all persistable
// entries in deterministic order (sorted by key) for stable snapshots.
//
// Uses SCAN rather than KEYS to avoid the canonical "blocks the entire
// server" issue — even though miniredis is in-process, the dump runs on
// a 30s timer alongside live traffic.
func (m *Manager) collectEntries(ctx context.Context) ([]snapshotEntry, error) {
	var keys []string
	var cursor uint64
	for {
		batch, next, err := m.client.Scan(ctx, cursor, "*", 1000).Result()
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	sort.Strings(keys)
	entries := make([]snapshotEntry, 0, len(keys))
	for _, key := range keys {
		if !m.shouldPersist(key) {
			continue
		}
		entry, err := m.readEntry(ctx, key)
		if err != nil {
			m.logger.Warn("failed to read key for snapshot", "key", key, "err", err)
			continue
		}
		if entry == nil {
			continue
		}
		entries = append(entries, *entry)
	}
	return entries, nil
}

func (m *Manager) readEntry(ctx context.Context, key string) (*snapshotEntry, error) {
	typ, err := m.client.Type(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	ttl, err := m.client.PTTL(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	ttlMs := int64(-1)
	switch {
	case ttl == -2*time.Millisecond:
		// Key expired between Keys() and here — skip.
		return nil, nil
	case ttl > 0:
		ttlMs = ttl.Milliseconds()
	}
	switch typ {
	case "hash":
		fields, err := m.client.HGetAll(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		if len(fields) == 0 {
			return nil, nil
		}
		return &snapshotEntry{Key: key, Type: "hash", TTLMs: ttlMs, Hash: fields}, nil
	case "string":
		value, err := m.client.Get(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		return &snapshotEntry{Key: key, Type: "string", TTLMs: ttlMs, String: value}, nil
	case "set":
		members, err := m.client.SMembers(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		if len(members) == 0 {
			return nil, nil
		}
		sort.Strings(members) // determinism for tests + diffs
		return &snapshotEntry{Key: key, Type: "set", TTLMs: ttlMs, Set: members}, nil
	case "list":
		values, err := m.client.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			return nil, nil
		}
		return &snapshotEntry{Key: key, Type: "list", TTLMs: ttlMs, List: values}, nil
	case "zset":
		zs, err := m.client.ZRangeWithScores(ctx, key, 0, -1).Result()
		if err != nil {
			return nil, err
		}
		if len(zs) == 0 {
			return nil, nil
		}
		entries := make([]zEntry, 0, len(zs))
		for _, z := range zs {
			member, _ := z.Member.(string)
			entries = append(entries, zEntry{Member: member, Score: z.Score})
		}
		return &snapshotEntry{Key: key, Type: "zset", TTLMs: ttlMs, ZSet: entries}, nil
	case "stream":
		// Read the newest maxStreamEntriesPerKey via XREVRANGE; reverse
		// to oldest-first so replay preserves ordering. Older entries
		// beyond the cap are NOT in the snapshot — they remain in the
		// running miniredis but won't survive a restart.
		msgs, err := m.client.XRevRangeN(ctx, key, "+", "-", maxStreamEntriesPerKey).Result()
		if err != nil {
			return nil, err
		}
		if len(msgs) == 0 {
			return nil, nil
		}
		entries := make([]streamEntry, 0, len(msgs))
		for i := len(msgs) - 1; i >= 0; i-- {
			msg := msgs[i]
			vals := make(map[string]string, len(msg.Values))
			for k, v := range msg.Values {
				if s, ok := v.(string); ok {
					vals[k] = s
				} else {
					vals[k] = fmt.Sprint(v)
				}
			}
			entries = append(entries, streamEntry{ID: msg.ID, Values: vals})
		}
		return &snapshotEntry{Key: key, Type: "stream", TTLMs: ttlMs, Stream: entries}, nil
	default:
		// Truly unsupported (geo, hyperloglog, etc.) — fleet-db does not
		// use these today. Skip silently rather than fail the whole dump.
		return nil, nil
	}
}

// load reads the snapshot file (or its .bak fallback) and replays it
// into miniredis. Missing snapshot is not an error.
func (m *Manager) load() error {
	// #nosec G304 — path controlled by NewManager caller
	data, err := os.ReadFile(m.snapshotPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Try the backup as a second chance.
			// #nosec G304 — same controlled path, .bak suffix
			bakData, bakErr := os.ReadFile(m.snapshotPath + ".bak")
			if bakErr != nil {
				return nil // no snapshot and no backup — fresh start
			}
			m.logger.Info("loading redis snapshot from backup", "path", m.snapshotPath+".bak")
			data = bakData
		} else {
			return fmt.Errorf("read snapshot: %w", err)
		}
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		// Try backup once.
		// #nosec G304 — same controlled path, .bak suffix
		bakData, bakErr := os.ReadFile(m.snapshotPath + ".bak")
		if bakErr != nil {
			return fmt.Errorf("unmarshal snapshot: %w", err)
		}
		if bakErr := json.Unmarshal(bakData, &snap); bakErr != nil {
			return fmt.Errorf("unmarshal snapshot and backup: %w", err)
		}
		m.logger.Warn("primary snapshot corrupt, recovered from backup", "err", err)
	}
	if snap.SchemaVersion > currentSchemaVersion {
		return fmt.Errorf("snapshot schema_version %d newer than supported %d", snap.SchemaVersion, currentSchemaVersion)
	}
	if err := m.replay(&snap); err != nil {
		return err
	}
	// Seed the dirty-flag with the on-disk content's hash so the first
	// Dump after load short-circuits when the keyspace hasn't been
	// mutated. Without this seed, every CLI invocation rewrites the
	// snapshot file even on read-only commands (each invocation creates
	// a fresh Manager with lastDumpSet=false).
	if entriesJSON, err := json.Marshal(snap.Entries); err == nil {
		hash := sha256.Sum256(entriesJSON)
		m.mu.Lock()
		m.lastDumpHash = hash
		m.lastDumpSet = true
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) replay(snap *snapshot) error {
	// Correct TTLs for elapsed time between dump and load so a key that
	// had 60s left at dump time won't be reloaded as if it still has 60s
	// after a 5-minute restart. Older snapshots without DumpedAt get
	// zero elapsed (IsZero), which keeps backward compat.
	var elapsed time.Duration
	if !snap.DumpedAt.IsZero() {
		elapsed = time.Since(snap.DumpedAt)
		if elapsed < 0 {
			elapsed = 0
		}
	}
	loaded, expired := 0, 0
	for _, e := range snap.Entries {
		remainingTTL := time.Duration(0)
		if e.TTLMs > 0 {
			remainingTTL = time.Duration(e.TTLMs)*time.Millisecond - elapsed
			if remainingTTL <= 0 {
				expired++
				continue
			}
		}
		switch e.Type {
		case "hash":
			if len(e.Hash) == 0 {
				continue
			}
			m.mr.HSet(e.Key, flattenHash(e.Hash)...)
		case "string":
			if err := m.mr.Set(e.Key, e.String); err != nil {
				m.logger.Warn("failed to set string on load", "key", e.Key, "err", err)
				continue
			}
		case "set":
			if len(e.Set) == 0 {
				continue
			}
			m.mr.SetAdd(e.Key, e.Set...)
		case "list":
			if len(e.List) == 0 {
				continue
			}
			// Push from the right so order matches LRANGE 0 -1.
			if _, err := m.mr.Push(e.Key, e.List...); err != nil {
				m.logger.Warn("failed to push list on load", "key", e.Key, "err", err)
				continue
			}
		case "zset":
			if len(e.ZSet) == 0 {
				continue
			}
			for _, z := range e.ZSet {
				if _, err := m.mr.ZAdd(e.Key, z.Score, z.Member); err != nil {
					m.logger.Warn("failed to zadd on load", "key", e.Key, "member", z.Member, "err", err)
				}
			}
		case "stream":
			if len(e.Stream) == 0 {
				continue
			}
			for _, msg := range e.Stream {
				flat := flattenHash(msg.Values)
				if _, err := m.mr.XAdd(e.Key, msg.ID, flat); err != nil {
					m.logger.Warn("failed to xadd on load", "key", e.Key, "id", msg.ID, "err", err)
				}
			}
		default:
			continue
		}
		if remainingTTL > 0 {
			m.mr.SetTTL(e.Key, remainingTTL)
		}
		loaded++
	}
	m.logger.Info("loaded redis snapshot", "entries", loaded, "expired", expired, "schema_version", snap.SchemaVersion)
	return nil
}

func flattenHash(h map[string]string) []string {
	out := make([]string, 0, 2*len(h))
	// Deterministic ordering for tests.
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k, h[k])
	}
	return out
}
