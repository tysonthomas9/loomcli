package localredis

import (
	"strings"

	"github.com/alicebob/miniredis/v2/server"
)

// Debounced write-through: persistence used to have exactly two triggers,
// the 30s tick and the final dump in Close. Close is reached only via a
// context.AfterFunc on the serve context, so SIGKILL / OOM / power loss
// skips it entirely and everything written since the last tick is lost —
// up to 30 seconds of tab creates, tab switches and issue-tab edits.
//
// The fix arms a ~1s debounce whenever a terminal-state key is mutated,
// keeping the tick as a floor. The trigger is installed as a miniredis
// SERVER pre-hook rather than as pokes at the call sites, because the
// stores do not share a client: Manager.Client() is used only for the
// Manager's own read sweep, while tabmeta/issuetabs/sessionhistory each
// build an independent client via fleet.NewRedisClient, and
// terminal:ui-state is written with a raw HSet that bypasses every store.
// A hook on the server's command path sees all of them — including the
// embedded fleet-db subprocess, which is not in this process at all.
//
// NOTE: Miniredis.SetError also installs a pre-hook and would silently
// clobber this one. loom never calls SetError; keep it that way.

// mutatingCommands is the set of commands treated as writes. Explicit
// rather than "not in a read set" so an unknown command is inert instead
// of arming a sweep on every read.
//
// EVAL/EVALSHA are included conservatively: the fleet Store ships a Lua
// claim script and we cannot parse Lua to tell reads from writes.
var mutatingCommands = map[string]struct{}{
	"SET": {}, "SETEX": {}, "SETNX": {}, "PSETEX": {}, "GETSET": {}, "GETDEL": {},
	"APPEND": {}, "INCR": {}, "INCRBY": {}, "INCRBYFLOAT": {}, "DECR": {}, "DECRBY": {},
	"DEL": {}, "UNLINK": {}, "EXPIRE": {}, "PEXPIRE": {}, "EXPIREAT": {}, "PEXPIREAT": {},
	"PERSIST": {}, "RENAME": {}, "RENAMENX": {}, "COPY": {}, "RESTORE": {},
	"MSET": {}, "MSETNX": {},
	"HSET": {}, "HSETNX": {}, "HMSET": {}, "HDEL": {}, "HINCRBY": {}, "HINCRBYFLOAT": {},
	"LPUSH": {}, "LPUSHX": {}, "RPUSH": {}, "RPUSHX": {}, "LPOP": {}, "RPOP": {},
	"LSET": {}, "LREM": {}, "LTRIM": {}, "LINSERT": {},
	"SADD": {}, "SREM": {}, "SPOP": {}, "SMOVE": {},
	"ZADD": {}, "ZREM": {}, "ZINCRBY": {},
	"ZREMRANGEBYSCORE": {}, "ZREMRANGEBYRANK": {}, "ZREMRANGEBYLEX": {},
	"XADD": {}, "XDEL": {}, "XTRIM": {},
	"SETRANGE": {}, "SETBIT": {}, "FLUSHDB": {}, "FLUSHALL": {},
	"EVAL": {}, "EVALSHA": {},
}

// preHook runs on the miniredis server's command path, before the command
// executes and while the server mutex is held, for every command from
// every client. It must therefore stay non-blocking and allocation-light.
//
// It MUST always return false. A miniredis pre-hook returning true means
// "this command is already handled" and the command is CONSUMED — the
// write would never reach the store. That is the one way this change
// could destroy data, so the return is unconditional and has no branch.
func (m *Manager) preHook(_ *server.Peer, cmd string, args ...string) bool {
	if triggersWriteThrough(cmd, args) {
		m.markDirty()
	}
	return false
}

// triggersWriteThrough reports whether a command should arm the debounce:
// it must both mutate and touch a terminal-state key.
//
// Deliberately scoped to includedPrefixes ONLY, never fleetPrefixes, even
// when fleetKeys is on. In fleet mode the snapshot covers fleet-db's whole
// keyspace and a dump is a full-keyspace SCAN sweep budgeted up to
// sweepCap (120s); letting fleet-db's write churn arm the debounce would
// put the process into a near-continuous sweep. This ticket is about
// terminal-state loss — fleet keys keep the 30s tick and the Close flush
// they have today. That exclusion is load-bearing, not an oversight.
//
// Every arg is scanned rather than just args[0], so MSET, RENAME, SMOVE
// and EVAL's KEYS[] block are covered without a per-command arity table.
// A false positive (a terminal-looking VALUE) costs one debounced sweep,
// which Dump's hash short-circuit then turns into a no-op disk write.
func triggersWriteThrough(cmd string, args []string) bool {
	if _, ok := mutatingCommands[strings.ToUpper(cmd)]; !ok {
		return false
	}
	for _, arg := range args {
		for _, prefix := range includedPrefixes {
			if strings.HasPrefix(arg, prefix) {
				return true
			}
		}
	}
	return false
}

// markDirty pokes the run loop. dirtyCh is buffered at 1 and the send is
// non-blocking: this runs on the miniredis server goroutine under the
// server mutex, so it must never wait. A dropped poke is harmless — a
// wakeup is already pending and the debounce it arms covers this write
// too.
func (m *Manager) markDirty() {
	select {
	case m.dirtyCh <- struct{}{}:
	default:
	}
}
