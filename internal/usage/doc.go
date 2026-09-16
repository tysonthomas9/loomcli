// Package usage records what each agent session cost.
//
// Collector accumulates a SessionUsage record for one backend and agent over
// the life of a session. Store persists those records as append-only JSONL at
// {loomDir}/usage.jsonl, serializing concurrent appends with flock so several
// loom processes can write the same file safely. Filter selects records when
// reporting.
//
// Normal writes only ever append — a usage record is an observation that
// already happened. The one exception is PurgeOlderThan, which reads the whole
// file, drops records that have aged out, and rewrites it atomically.
package usage
