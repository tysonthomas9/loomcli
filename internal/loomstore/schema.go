package loomstore

// migrations are applied in order; PRAGMA user_version records how many ran.
// Never edit a shipped entry: append a new one.
var migrations = []string{`
CREATE TABLE agents (
  agent_id           TEXT PRIMARY KEY,
  workspace_id       TEXT NOT NULL,
  name               TEXT NOT NULL,
  profile_key        TEXT NOT NULL,          -- per-agent profile; set at Create, never changed by rename
  preset             TEXT NOT NULL,
  preset_version     TEXT NOT NULL,
  mode               TEXT NOT NULL CHECK (mode IN ('persistent','single_task')),
  interaction_mode   TEXT NOT NULL CHECK (interaction_mode IN ('interactive','background')),
  role_kind          TEXT NOT NULL,
  spec_json          TEXT NOT NULL,
  spec_version       INTEGER NOT NULL,
  owner_kind         TEXT NOT NULL,
  owner_id           TEXT NOT NULL,
  created_by_kind    TEXT NOT NULL,
  created_by_id      TEXT NOT NULL,
  parent_agent_id    TEXT REFERENCES agents(agent_id),
  root_agent_id      TEXT,
  subject_type       TEXT, subject_id TEXT, subject_version TEXT,
  external_key       TEXT,
  create_request_id  TEXT NOT NULL,
  last_request_id    TEXT,
  repo               TEXT NOT NULL,
  base_ref           TEXT,
  worktree_path      TEXT, branch TEXT,
  harness            TEXT NOT NULL,
  harness_session_id TEXT,
  host               TEXT NOT NULL DEFAULT 'local',
  model              TEXT,
  state              TEXT NOT NULL,
  state_reason       TEXT,
  waiting_on         TEXT,
  attempt            INTEGER NOT NULL DEFAULT 0,
  outcome            TEXT,
  archive_reason     TEXT,
  attention_reason   TEXT,
  running_turn_id    TEXT,
  create_step        INTEGER NOT NULL DEFAULT 0,
  delete_requested   INTEGER NOT NULL DEFAULT 0,
  delete_result_json TEXT,
  last_active_at     TEXT,
  created_at         TEXT NOT NULL,
  updated_at         TEXT NOT NULL,
  archived_at        TEXT,
  finished_at        TEXT,
  history_purged_at  TEXT,
  deleted_at         TEXT
);
CREATE UNIQUE INDEX agents_name    ON agents(workspace_id, name)         WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX agents_xkey    ON agents(workspace_id, external_key) WHERE external_key IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX agents_create  ON agents(workspace_id, create_request_id);
CREATE UNIQUE INDEX agents_session ON agents(harness, harness_session_id);
CREATE INDEX        agents_parent  ON agents(parent_agent_id);
CREATE INDEX        agents_state   ON agents(workspace_id, state);
CREATE INDEX        agents_archived ON agents(archived_at) WHERE history_purged_at IS NULL;
CREATE INDEX        agents_finished ON agents(finished_at) WHERE history_purged_at IS NULL;

CREATE TRIGGER agents_interaction_mode_immutable
BEFORE UPDATE OF interaction_mode ON agents
WHEN NEW.interaction_mode IS NOT OLD.interaction_mode
BEGIN SELECT RAISE(ABORT, 'interaction_mode is immutable'); END;

CREATE TRIGGER agents_profile_key_immutable
BEFORE UPDATE OF profile_key ON agents
WHEN NEW.profile_key IS NOT OLD.profile_key
BEGIN SELECT RAISE(ABORT, 'profile_key is immutable'); END;

CREATE TABLE agent_native_sessions (        -- every native session an agent ever owned (R29 purge scope)
  agent_id    TEXT NOT NULL REFERENCES agents(agent_id),
  harness     TEXT NOT NULL,
  native_root TEXT NOT NULL,                -- the root the harness returned at launch; never re-resolved
  native_id   TEXT NOT NULL,
  recorded_at TEXT NOT NULL,
  PRIMARY KEY (harness, native_root, native_id)
);
CREATE INDEX agent_native_sessions_agent ON agent_native_sessions(agent_id);
CREATE TRIGGER agent_native_sessions_no_update BEFORE UPDATE ON agent_native_sessions
BEGIN SELECT RAISE(ABORT, 'agent_native_sessions is append-only'); END;
CREATE TRIGGER agent_native_sessions_no_delete BEFORE DELETE ON agent_native_sessions
BEGIN SELECT RAISE(ABORT, 'agent_native_sessions is append-only'); END;

CREATE TABLE agent_slots (
  agent_id    TEXT NOT NULL REFERENCES agents(agent_id),
  sender      TEXT NOT NULL,
  request_id  TEXT NOT NULL,
  body        TEXT NOT NULL,
  source      TEXT NOT NULL,
  state       TEXT NOT NULL,
  native_key  TEXT,
  queued_at   TEXT,
  first       INTEGER NOT NULL DEFAULT 0,
  updated_at  TEXT NOT NULL,
  PRIMARY KEY (agent_id, sender)
);
CREATE INDEX agent_slots_waiting ON agent_slots(agent_id, state, queued_at);

CREATE TABLE agent_send_receipts (
  agent_id    TEXT NOT NULL REFERENCES agents(agent_id),
  request_id  TEXT NOT NULL,
  sender      TEXT NOT NULL,
  result_json TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  PRIMARY KEY (agent_id, request_id)
);

CREATE TABLE agent_events (
  agent_id         TEXT NOT NULL REFERENCES agents(agent_id),
  seq              INTEGER NOT NULL,
  event_id         TEXT NOT NULL,
  kind             TEXT NOT NULL,
  turn_id          TEXT,
  redacted_payload TEXT NOT NULL,
  created_at       TEXT NOT NULL,
  PRIMARY KEY (agent_id, seq),
  UNIQUE (agent_id, event_id)
);
CREATE TRIGGER agent_events_no_update BEFORE UPDATE ON agent_events
BEGIN SELECT RAISE(ABORT, 'agent_events is append-only'); END;
`, `
ALTER TABLE agents ADD COLUMN harness_session_root TEXT; -- the current session's NativeRef root; NULL (a legacy row) resolves only when exactly one recorded root has harness_session_id
`, `
CREATE TABLE native_purge_pending (         -- an owned native session a failed Open left behind; removed once purged
  harness     TEXT NOT NULL,
  native_root TEXT NOT NULL,
  native_id   TEXT NOT NULL,
  PRIMARY KEY (harness, native_root, native_id),
  FOREIGN KEY (harness, native_root, native_id) REFERENCES agent_native_sessions(harness, native_root, native_id)
);
`, `
ALTER TABLE agent_send_receipts ADD COLUMN body TEXT;       -- the Send's message text; NULL on a legacy row or a Send with no message
ALTER TABLE agent_send_receipts ADD COLUMN native_key TEXT; -- the input key the message was handed over with
CREATE INDEX agent_send_receipts_native_key ON agent_send_receipts(agent_id, native_key);
`, `
CREATE INDEX agent_events_kind ON agent_events(agent_id, kind); -- task_completed notices and the last message, without a history scan
`, `
ALTER TABLE agents ADD COLUMN attempt_after_seq INTEGER NOT NULL DEFAULT 0; -- the last event seq before the current attempt began; set with the reopen
-- An existing agent past its first attempt (0) starts its attempt at its last
-- saved reopen (agent.state_changed finished -> active), but only when every
-- reopen saved one (as many as its attempt); otherwise, as after a crash
-- before Send published a reopen, at its last event, so an earlier attempt's
-- reply never counts as this one's.
UPDATE agents SET attempt_after_seq = CASE
  WHEN (SELECT COUNT(*) FROM agent_events e WHERE e.agent_id = agents.agent_id AND e.kind = 'agent.state_changed'
          AND json_extract(e.redacted_payload, '$.from') = 'finished'
          AND json_extract(e.redacted_payload, '$.to') = 'active') = agents.attempt
  THEN (SELECT MAX(seq) FROM agent_events e WHERE e.agent_id = agents.agent_id AND e.kind = 'agent.state_changed'
          AND json_extract(e.redacted_payload, '$.from') = 'finished'
          AND json_extract(e.redacted_payload, '$.to') = 'active')
  ELSE (SELECT COALESCE(MAX(seq), 0) FROM agent_events e WHERE e.agent_id = agents.agent_id) END
WHERE attempt > 0;
`, `
ALTER TABLE agents ADD COLUMN history_purge_failed_at TEXT; -- set while a due R29 purge has failed (incomplete expiry); cleared when the purge succeeds or the deadline ends
`, `
CREATE TABLE custom_models (                -- model ids a workspace adds to a harness's catalog (MCS3)
  workspace_id TEXT NOT NULL,
  harness      TEXT NOT NULL,
  model        TEXT NOT NULL,
  pos          INTEGER NOT NULL,            -- the order they were set in
  PRIMARY KEY (workspace_id, harness, model)
);
`, `
-- DF1: the Notify records at the end of a slot's body, as JSON {"keys": [...],
-- "at": <byte offset of the first>}, so a reader names them without parsing
-- text; the handed receipt keeps the slot's at hand-over. NULL when none.
ALTER TABLE agent_slots ADD COLUMN notices TEXT;
ALTER TABLE agent_send_receipts ADD COLUMN notices TEXT;
`, `
ALTER TABLE agents ADD COLUMN revision INTEGER NOT NULL DEFAULT 0; -- OR2: bumped by one with every state change; its events are named <agent>:<revision>:<kind>
`, `
-- OR3c: a child attempt's task_completed owed to its parent, saved with the
-- state change that ends the attempt and deleted with the record's append.
CREATE TABLE agent_completion_markers (
  child_agent_id  TEXT NOT NULL,
  attempt         INTEGER NOT NULL,
  parent_agent_id TEXT NOT NULL,
  outcome         TEXT NOT NULL,
  branch          TEXT NOT NULL,
  summary         TEXT,            -- NULL on a marker the upgrade saved: read it from the child
  result          TEXT,
  created_at      TEXT NOT NULL,
  PRIMARY KEY (child_agent_id, attempt)
);
CREATE INDEX agent_completion_markers_parent ON agent_completion_markers(parent_agent_id);
-- An attempt that ended before the upgrade with its record still unsaved.
INSERT INTO agent_completion_markers (child_agent_id, attempt, parent_agent_id, outcome, branch, created_at)
SELECT a.agent_id, a.attempt, a.parent_agent_id, a.outcome, COALESCE(a.branch, ''), a.updated_at FROM agents a
WHERE a.parent_agent_id IS NOT NULL AND a.mode = 'single_task' AND a.outcome IS NOT NULL AND a.deleted_at IS NULL
  AND a.state IN ('finished', 'archived') AND NOT EXISTS (SELECT 1 FROM agent_events e
    WHERE e.agent_id = a.parent_agent_id AND e.event_id = 'task_completed:' || a.agent_id || ':' || a.attempt);
`, `
-- OR4a: create_incomplete now means a Create no retry can finish; before,
-- any failed Create showed it. A row below done (5) that shows it retries.
UPDATE agents SET attention_reason = 'create_retrying'
WHERE attention_reason = 'create_incomplete' AND create_step < 5 AND deleted_at IS NULL;
`, `
-- OR5a: one claim per ask, saved before its Reply. The first Respond to save
-- it binds its request and payload; state is claimed until the outcome is
-- known: replied, or unknown (no evidence either way; never replied again).
-- An ask is its ID on its turn: codex reuses an ID on a later connection.
-- A released claim (its Reply never sent) stays, so its request stays bound
-- to its turn's ask; only one claim on an ask is not released.
CREATE TABLE IF NOT EXISTS agent_ask_claims (
  agent_id     TEXT NOT NULL REFERENCES agents(agent_id),
  ask_id       TEXT NOT NULL,
  turn_id      TEXT NOT NULL,
  request_id   TEXT NOT NULL,
  payload_hash TEXT NOT NULL,
  state        TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  PRIMARY KEY (agent_id, ask_id, turn_id, request_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS agent_ask_claims_one ON agent_ask_claims(agent_id, ask_id, turn_id) WHERE state != 'released';
`, `
-- OR5d: each Update and harness switch request an agent applied, so a retry
-- of any of them returns its saved result. A switch saves its row as
-- switching, with its request and Open key, before it stops the turn; its
-- commit saves it done. An agent has at most one switch pending.
CREATE TABLE IF NOT EXISTS agent_update_requests (
  agent_id            TEXT NOT NULL REFERENCES agents(agent_id),
  request_id          TEXT NOT NULL,
  kind                TEXT NOT NULL,
  payload_hash        TEXT NOT NULL,
  status              TEXT NOT NULL,
  payload             TEXT NOT NULL,
  from_harness        TEXT NOT NULL,
  to_harness          TEXT NOT NULL,
  open_key            TEXT NOT NULL,
  target_spec_version INTEGER NOT NULL,
  result              TEXT NOT NULL,
  created_at          TEXT NOT NULL,
  PRIMARY KEY (agent_id, request_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS agent_update_requests_switching ON agent_update_requests(agent_id) WHERE status = 'switching';
`, `
-- OR10: an agent's watch on one PR of its repo. viewer is the host GitHub
-- login the watch last read as; the cursors are what the agent was last
-- told about (head SHA, check runs, comments), last_told its text, and
-- wake_count the wakes since check or conflict news.
CREATE TABLE IF NOT EXISTS pr_watches (
  agent_id        TEXT NOT NULL,
  workspace_id    TEXT NOT NULL,
  owner           TEXT NOT NULL,
  repo            TEXT NOT NULL,
  number          INTEGER NOT NULL,
  viewer          TEXT NOT NULL,
  head_sha        TEXT NOT NULL,
  checks_cursor   TEXT NOT NULL,
  comments_cursor TEXT NOT NULL,
  wake_count      INTEGER NOT NULL DEFAULT 0,
  last_told       TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  PRIMARY KEY (agent_id, owner, repo, number)
);
CREATE INDEX IF NOT EXISTS pr_watches_workspace ON pr_watches(workspace_id);
`, historyRepair, `
-- OR7: usage-limit auto-resume. A workspace's opt-in (off by default), and
-- each agent's one resume owed: its latest turn ended on a usage limit, the
-- attempt-th in a row, so a resume is due at due_at. A Send consumes it in
-- its own transaction, and any other turn end drops it.
CREATE TABLE IF NOT EXISTS agent_workspace_settings (
  workspace_id TEXT PRIMARY KEY,
  limit_resume INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS agent_limit_resumes (
  agent_id TEXT PRIMARY KEY REFERENCES agents(agent_id),
  turn_id  TEXT NOT NULL,
  attempt  INTEGER NOT NULL,
  session  TEXT NOT NULL,          -- the native session whose turn hit the limit
  due_at   TEXT NOT NULL
);
`}

// historyRepair (OR11) is the one-time scan of histories saved before a state
// change and its events were one write: it is run again with no effect. An
// agent's current state with no saved transition to it gets one
// history.repaired event: its current state and the state its history last
// shows (creating when none), never a transition. What can't be rebuilt is
// recorded in agent_history_unrepaired instead: a saved transition whose from
// is not the state saved before it (a lost transition between them), and a
// child attempt that ended with no task_completed on its parent and none owed
// (its outcome, head and summary are gone). Deleted agents and purged
// histories are empty on purpose: they are never repaired or reported, and a
// purged or deleted child's attempts are not owed a record.
const historyRepair = `
CREATE TABLE IF NOT EXISTS agent_history_unrepaired (
  agent_id   TEXT NOT NULL,
  gap        TEXT NOT NULL,   -- state: detail is the seq after the lost transition; task_completed: <child>:<attempt>
  detail     TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (agent_id, gap, detail)
);
INSERT OR IGNORE INTO agent_history_unrepaired (agent_id, gap, detail, created_at)
SELECT agent_id, 'state', seq, strftime('%Y-%m-%dT%H:%M:%f', 'now') || '000000Z'
FROM (SELECT e.agent_id, e.seq, e.kind, json_extract(e.redacted_payload, '$.from') AS from_state,
    LAG(CASE e.kind WHEN 'history.repaired' THEN json_extract(e.redacted_payload, '$.state')
      ELSE json_extract(e.redacted_payload, '$.to') END, 1, 'creating') OVER (PARTITION BY e.agent_id ORDER BY e.seq) AS prev
  FROM agent_events e JOIN agents a ON a.agent_id = e.agent_id
  WHERE a.deleted_at IS NULL AND a.history_purged_at IS NULL AND e.kind IN ('agent.state_changed', 'history.repaired'))
WHERE kind = 'agent.state_changed' AND from_state IS NOT prev;
INSERT OR IGNORE INTO agent_events (agent_id, seq, event_id, kind, redacted_payload, created_at)
SELECT agent_id, (SELECT COALESCE(MAX(seq), 0) + 1 FROM agent_events e WHERE e.agent_id = a.agent_id),
  'history.repaired:' || agent_id || ':' || revision, 'history.repaired',
  json_object('agentId', agent_id, 'type', 'history.repaired', 'state', state, 'reason', state_reason,
    'outcome', outcome, 'attempt', attempt, 'after', seen),
  strftime('%Y-%m-%dT%H:%M:%f', 'now') || '000000Z'
FROM (SELECT a.*, COALESCE((SELECT CASE e.kind WHEN 'history.repaired' THEN json_extract(e.redacted_payload, '$.state')
    ELSE json_extract(e.redacted_payload, '$.to') END FROM agent_events e
    WHERE e.agent_id = a.agent_id AND e.kind IN ('agent.state_changed', 'history.repaired')
    ORDER BY e.seq DESC LIMIT 1), 'creating') AS seen
  FROM agents a WHERE a.deleted_at IS NULL AND a.history_purged_at IS NULL) a
WHERE state != seen;
INSERT OR IGNORE INTO agent_history_unrepaired (agent_id, gap, detail, created_at)
WITH RECURSIVE c AS (SELECT c.* FROM agents c JOIN agents p ON p.agent_id = c.parent_agent_id
    WHERE c.mode = 'single_task' AND c.deleted_at IS NULL AND c.history_purged_at IS NULL
      AND p.deleted_at IS NULL AND p.history_purged_at IS NULL),
  n(k) AS (SELECT 0 UNION ALL SELECT k + 1 FROM n WHERE k + 1 < (SELECT MAX(attempt) FROM c))
SELECT c.parent_agent_id, 'task_completed', c.agent_id || ':' || n.k, strftime('%Y-%m-%dT%H:%M:%f', 'now') || '000000Z'
FROM c JOIN n ON n.k < c.attempt
WHERE NOT EXISTS (SELECT 1 FROM agent_events e WHERE e.agent_id = c.parent_agent_id
    AND e.event_id = 'task_completed:' || c.agent_id || ':' || n.k)
  AND NOT EXISTS (SELECT 1 FROM agent_completion_markers m WHERE m.child_agent_id = c.agent_id AND m.attempt = n.k);
`
