import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { DatabaseSync } from 'node:sqlite';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const shell = readFileSync(join(dirname(fileURLToPath(import.meta.url)), 'agent-flows-native-session.sh'), 'utf8');
const match = shell.match(/node -e '\n([\s\S]*?)\n' "\$agent_id"/);
assert.ok(match, 'native probe JavaScript must be extractable for offline ownership checks');
const root = mkdtempSync('/private/tmp/aft-native-');
const database = join(root, 'agents.db');
const code = match[1].replace('"/root/.loom/agents.db"', JSON.stringify(database));
const db = new DatabaseSync(database);
db.exec(`CREATE TABLE agents (agent_id TEXT, workspace_id TEXT, name TEXT, harness TEXT,
  repo TEXT, preset TEXT, created_by_kind TEXT, parent_agent_id TEXT, root_agent_id TEXT,
  harness_session_id TEXT, harness_session_root TEXT);
  CREATE TABLE agent_native_sessions (agent_id TEXT, harness TEXT, native_root TEXT, native_id TEXT);`);
const repo = '/root/.loom/workspaces/LOCALMODE/source-repo';
const run = 'af12345678';
const lead = `aft-coverage-${run}`;
const child = `aft-child-${run}`;
const declared = { leads: [{ name: lead, suite: 'coverage-one' }],
  children: [{ name: child, parent: lead, suite: 'coverage-one' }] };
const insert = db.prepare('INSERT INTO agents VALUES (?,?,?,?,?,?,?,?,?,?,?)');
insert.run('agt_LEAD', 'LOCALMODE', lead, 'opencode', repo, 'lead', 'user', null, null, 'ses_lead', '');
insert.run('agt_CHILD', 'LOCALMODE', child, 'opencode', repo, 'task', 'agent', 'agt_LEAD', 'agt_LEAD', 'ses_child', '');
db.prepare('INSERT INTO agent_native_sessions VALUES (?,?,?,?)').run('agt_LEAD', 'opencode', '', 'ses_lead');
db.prepare('INSERT INTO agent_native_sessions VALUES (?,?,?,?)').run('agt_CHILD', 'opencode', '', 'ses_child');
const probe = (id, session = 'aft-coverage-one-123', names = declared) => spawnSync(process.execPath,
  ['-e', code, id, run, JSON.stringify(names), repo, session], { encoding: 'utf8' });
const update = sql => db.exec(sql);
try {
  assert.equal(probe('agt_LEAD').status, 0);
  assert.equal(probe('agt_CHILD').status, 0);
  assert.match(probe('agt_CHILD', 'aft-foreign-123').stderr, /agent-session-mismatch/);
  update("UPDATE agents SET repo='/foreign' WHERE agent_id='agt_CHILD'");
  assert.match(probe('agt_CHILD').stderr, /repo-mismatch/);
  update(`UPDATE agents SET repo='${repo}', parent_agent_id='agt_OTHER' WHERE agent_id='agt_CHILD'`);
  assert.match(probe('agt_CHILD').stderr, /child-parent-mismatch/);
  update("UPDATE agents SET parent_agent_id='agt_LEAD', root_agent_id='agt_OTHER' WHERE agent_id='agt_CHILD'");
  assert.match(probe('agt_CHILD').stderr, /child-parent-mismatch/);
  update("UPDATE agents SET root_agent_id='agt_LEAD', harness='claude' WHERE agent_id='agt_CHILD'");
  assert.match(probe('agt_CHILD').stderr, /harness-mismatch/);
  update("UPDATE agents SET harness='opencode' WHERE agent_id='agt_CHILD'");
  update("DELETE FROM agent_native_sessions WHERE agent_id='agt_CHILD'");
  assert.match(probe('agt_CHILD').stderr, /native-owner-mismatch/);
  update(`UPDATE agents SET name='live-recovery-restart-${run}' WHERE agent_id='agt_LEAD'`);
  assert.equal(probe('agt_LEAD', '', null).status, 0, 'legacy run-owned recovery name stays accepted');
  update("UPDATE agents SET name='foreign' WHERE agent_id='agt_LEAD'");
  assert.match(probe('agt_LEAD', '', null).stderr, /agent-name-not-run-owned/);
  console.log('agent-flow native ownership: declared Lead/child accepted; foreign session/repo/parent/root/backend/NativeRef refused');
} finally {
  db.close();
  rmSync(root, { recursive: true, force: true });
}
