// Loom on-host durability store for agent task-runs (FLUE-DURABILITY Phase 0).
//
// Wraps flue's proven SQL agent-execution store (@flue/runtime/node `sqlite()`) at a
// host-persisted path KEYED BY THE TASK-RUN ID. The task-run id is stable across
// reclaim (internal/driver/task_scheduling.go keeps the id; only the lease token
// rotates), so a relaunched local runner re-opens the SAME store and flue's
// reconciler resumes the interrupted submission mid-turn instead of restarting from
// zero. This is the storage half of the FLUE-DURABILITY Phase-0 spike.
//
// Scope: on-host runners (local-task-runner). Cross-sandbox / cross-node durability
// (a control-plane-backed store over @loom/sdk/runner's lease-token transport) is the
// next increment — see FLUE-DURABILITY-PROPOSAL.md.
//
// NOTE (verified against flue's store architecture, 2026-06): flue's stores are
// SQL-driver parameterized — `createSqlAgentExecutionStoreFromSql(sql)` builds the
// full SessionStore + AgentSubmissionStore (including the sessionKey/canonicalization
// derivations in session-identity.ts) from a low-level `SqlStorage`. A backend
// supplies that driver, NOT the ~25 store methods. So the cross-node backing is best
// done by implementing `SqlStorage` over fleet-db's Postgres (reusing ALL of flue's
// store logic), rather than reimplementing the store over a KV as first sketched.

import os from "node:os";
import path from "node:path";
import { sqlite } from "@flue/runtime/node";

const UNSAFE_PATH_CHARS = /[^A-Za-z0-9._-]/g;

// taskRunDurabilityPath resolves the durable SQLite file path for a task-run id.
// Throws on an empty id (a durable path must be deterministic and id-scoped).
export function taskRunDurabilityPath(taskRunId, baseDir) {
  const id = String(taskRunId ?? "").trim();
  if (!id) {
    throw new Error("[loom flue-durability-store] a task-run id is required for a durable store path");
  }
  const root = baseDir
    || process.env.LOOM_TASK_RUN_DURABILITY_DIR
    || path.join(os.homedir(), ".loom", "task-run-durability");
  return path.join(root, id.replace(UNSAFE_PATH_CHARS, "_") + ".sqlite");
}

// loomTaskRunAdapter returns a flue PersistenceAdapter. With a task-run id it is
// backed by a host-persisted SQLite file (durable across process restart, keyed by
// the reclaim-stable id); without one it falls back to in-memory so the module stays
// loadable in dev/unit contexts that have no task-run. The id is read from opts, then
// LOOM_TASK_RUN_ID, then LOOM_ASSIGNED_TASK_ID.
export function loomTaskRunAdapter(opts = {}) {
  const taskRunId = String(
    opts.taskRunId
      ?? process.env.LOOM_TASK_RUN_ID
      ?? process.env.LOOM_ASSIGNED_TASK_ID
      ?? "",
  ).trim();
  const dbPath = taskRunId ? taskRunDurabilityPath(taskRunId, opts.baseDir) : ":memory:";
  return sqlite(dbPath);
}

// Default export follows flue's db.ts convention: a source root's db.ts
// default-exports the PersistenceAdapter the runtime loads at boot.
export default loomTaskRunAdapter();
