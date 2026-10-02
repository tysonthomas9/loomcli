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
`}
