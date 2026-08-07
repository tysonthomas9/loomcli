package localredis

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// isVanishedKeyErr reports whether a per-key read error only means the key
// vanished between enumeration and the read. TTL'd FleetDB keys expire under
// normal operation and should be skipped rather than aborting a snapshot.
func isVanishedKeyErr(err error) bool {
	return errors.Is(err, redis.Nil) || errors.Is(err, miniredis.ErrKeyNotFound)
}

// readDirectBatch uses miniredis' in-process read API for the embedded FleetDB
// manager. Going back through RESP, even with pipelines, still made a 700k-key
// production sweep contend with FleetDB and UI traffic long enough to exhaust
// the whole-sweep budget. The terminal-only manager keeps the RESP path in
// manager.go, which preserves its transport fault-injection coverage.
func (m *Manager) readDirectBatch(parent context.Context, keys []string, out *[]snapshotEntry, st *sweepStats) {
	ctx, cancel := context.WithTimeout(parent, m.batchTimeout)
	defer cancel()

	for i, key := range keys {
		if err := ctx.Err(); err != nil {
			m.abortBatch(keys[i:], err, nil, st)
			return
		}
		entry, err := m.readDirectEntry(ctx, key)
		switch {
		case err != nil && isVanishedKeyErr(err):
			st.skipped++
		case err != nil:
			m.recordSnapshotReadError(key, err, st)
		case entry == nil:
			st.skipped++
		default:
			st.read++
			*out = append(*out, *entry)
		}
	}
}

//nolint:gocognit,cyclop,funlen // Snapshot types stay together for symmetry with replay.
func (m *Manager) readDirectEntry(ctx context.Context, key string) (*snapshotEntry, error) {
	typ := m.mr.Type(key)
	if typ == "" {
		return nil, miniredis.ErrKeyNotFound
	}
	ttlMs := int64(-1)
	if ttl := m.mr.TTL(key); ttl > 0 {
		ttlMs = ttl.Milliseconds()
	}

	switch typ {
	case "hash":
		fields, err := m.mr.HKeys(key)
		if err != nil {
			return nil, err
		}
		if len(fields) == 0 {
			return nil, nil
		}
		values := make(map[string]string, len(fields))
		for _, field := range fields {
			values[field] = m.mr.HGet(key, field)
		}
		return &snapshotEntry{Key: key, Type: "hash", TTLMs: ttlMs, Hash: values}, nil
	case "string":
		value, err := m.mr.Get(key)
		if err != nil {
			return nil, err
		}
		return &snapshotEntry{Key: key, Type: "string", TTLMs: ttlMs, String: value}, nil
	case "set":
		members, err := m.mr.Members(key)
		if err != nil {
			return nil, err
		}
		if len(members) == 0 {
			return nil, nil
		}
		return &snapshotEntry{Key: key, Type: "set", TTLMs: ttlMs, Set: members}, nil
	case "list":
		// Miniredis' direct List method exposes the backing slice after it
		// drops the store lock. Use RESP for this comparatively rare type so
		// concurrent pushes cannot race snapshot serialization.
		values, err := m.client.LRange(ctx, key, 0, -1).Result()
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			return nil, nil
		}
		return &snapshotEntry{Key: key, Type: "list", TTLMs: ttlMs, List: values}, nil
	case "zset":
		members, err := m.mr.ZMembers(key)
		if err != nil {
			return nil, err
		}
		if len(members) == 0 {
			return nil, nil
		}
		scores, err := m.mr.ZMScore(key, members...)
		if err != nil {
			return nil, err
		}
		entries := make([]zEntry, 0, len(members))
		for i, member := range members {
			entries = append(entries, zEntry{Member: member, Score: scores[i]})
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Score == entries[j].Score {
				return entries[i].Member < entries[j].Member
			}
			return entries[i].Score < entries[j].Score
		})
		return &snapshotEntry{Key: key, Type: "zset", TTLMs: ttlMs, ZSet: entries}, nil
	case "stream":
		// Like List, the direct Stream method exposes internal slices. The
		// bounded RESP read returns an isolated copy and also applies the cap
		// before values cross the connection.
		messages, err := m.client.XRevRangeN(ctx, key, "+", "-", maxStreamEntriesPerKey).Result()
		if err != nil {
			return nil, err
		}
		if len(messages) == 0 {
			return nil, nil
		}
		slices.Reverse(messages)
		entries := make([]streamEntry, 0, len(messages))
		for _, message := range messages {
			values := make(map[string]string, len(message.Values))
			for field, value := range message.Values {
				values[field] = fmt.Sprint(value)
			}
			entries = append(entries, streamEntry{ID: message.ID, Values: values})
		}
		return &snapshotEntry{Key: key, Type: "stream", TTLMs: ttlMs, Stream: entries}, nil
	default:
		return nil, nil
	}
}
