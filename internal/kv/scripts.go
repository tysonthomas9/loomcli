// Package kv provides atomic Redis operations for distributed task management.
// It implements Lua scripts for claiming tasks, maintaining heartbeats, and
// completing work without race conditions in a fleet of workers.
package kv

import (
	"github.com/redis/go-redis/v9"
)

const (
	// KeyPrefix is the namespace prefix for all Redis keys.
	KeyPrefix = "loom:"

	// DefaultTTLSeconds is the default TTL for task ownership and worker state keys.
	DefaultTTLSeconds = 300 // 5 minutes
)

// Redis key builders.
func taskOwnerKey(taskID string) string {
	return KeyPrefix + "task:" + taskID + ":owner"
}

func workerStateKey(workerID string) string {
	return KeyPrefix + "worker:" + workerID + ":state"
}

func activeWorkersKey() string {
	return KeyPrefix + "workers:active"
}

// claimScript atomically claims a task for a worker.
//
// KEYS[1] = task owner key
// KEYS[2] = worker state key
// KEYS[3] = active workers ZSET key
// ARGV[1] = worker_id
// ARGV[2] = task_id
// ARGV[3] = task_title
// ARGV[4] = agent_type
// ARGV[5] = ttl_seconds
// ARGV[6] = current_time_ms
//
// Returns: {status, message}
//   - {1, ""} on success
//   - {0, existing_owner} if already claimed by another worker
var claimScript = redis.NewScript(`
local existing = redis.call('GET', KEYS[1])
if existing and existing ~= ARGV[1] then
    return {0, existing}
end

redis.call('SET', KEYS[1], ARGV[1], 'EX', tonumber(ARGV[5]))
redis.call('HSET', KEYS[2],
    'task_id', ARGV[2],
    'task_title', ARGV[3],
    'state', 'working',
    'claimed_at', ARGV[6],
    'agent_type', ARGV[4])
redis.call('EXPIRE', KEYS[2], tonumber(ARGV[5]))
redis.call('ZADD', KEYS[3], tonumber(ARGV[6]), ARGV[1])

return {1, ''}
`)

// heartbeatScript atomically extends TTLs for a worker's keys.
//
// KEYS[1] = worker state key
// KEYS[2] = active workers ZSET key
// ARGV[1] = worker_id
// ARGV[2] = ttl_seconds
// ARGV[3] = current_time_ms
//
// Returns: {status, message}
//   - {1, ttl_seconds} on success
//   - {0, "no_active_session"} if worker not registered
//   - {0, "ownership_lost"} if task was reclaimed by another worker
var heartbeatScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
    return {0, 'no_active_session'}
end

local task_id = redis.call('HGET', KEYS[1], 'task_id')
if task_id and task_id ~= false then
    local task_owner_key = '` + KeyPrefix + `task:' .. task_id .. ':owner'
    local owner = redis.call('GET', task_owner_key)
    if owner and owner ~= ARGV[1] then
        return {0, 'ownership_lost'}
    end
    if owner == ARGV[1] then
        redis.call('EXPIRE', task_owner_key, tonumber(ARGV[2]))
    end
end

redis.call('EXPIRE', KEYS[1], tonumber(ARGV[2]))
redis.call('ZADD', KEYS[2], tonumber(ARGV[3]), ARGV[1])

return {1, ARGV[2]}
`)

// completeScript atomically verifies ownership and cleans up after task completion.
//
// KEYS[1] = task owner key
// KEYS[2] = worker state key
// KEYS[3] = active workers ZSET key
// ARGV[1] = worker_id
// ARGV[2] = task_id
// ARGV[3] = current_time_ms
// ARGV[4] = ttl_seconds
//
// Returns: {status, message}
//   - {1, ""} on success
//   - {0, "task_not_found"} if task key doesn't exist
//   - {0, "not_owner"} if worker doesn't own the task
var completeScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[1])
if not owner then
    return {0, 'task_not_found'}
end
if owner ~= ARGV[1] then
    return {0, 'not_owner'}
end

redis.call('DEL', KEYS[1])
redis.call('HDEL', KEYS[2], 'task_id', 'task_title', 'claimed_at')
redis.call('HSET', KEYS[2], 'state', 'idle')
redis.call('EXPIRE', KEYS[2], tonumber(ARGV[4]))
redis.call('ZADD', KEYS[3], tonumber(ARGV[3]), ARGV[1])

return {1, ''}
`)

// cleanupOwnerScript atomically checks task ownership and deletes only if it matches.
// This prevents a TOCTOU race in the stale detector's cleanupWorker where a new worker
// could claim the task between the GET and DEL operations.
//
// KEYS[1] = task owner key
// ARGV[1] = expected_owner (worker ID)
//
// Returns:
//
//	1 if key deleted or didn't exist (safe to proceed)
//	0 if key exists with a different owner (skip deletion)
var cleanupOwnerScript = redis.NewScript(`
local owner = redis.call('GET', KEYS[1])
if not owner then
    return 1
end
if owner == ARGV[1] then
    redis.call('DEL', KEYS[1])
    return 1
end
return 0
`)
