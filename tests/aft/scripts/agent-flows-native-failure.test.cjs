const test = require('node:test')
const assert = require('node:assert/strict')
const crypto = require('node:crypto')
const probe = require('./agent-flows-native-failure.cjs')

const id = 'ses_owned'
const message = 'private native provider detail'
const digest = crypto.createHash('sha256').update(message).digest('hex')
const anchor = {event_id: 'evt_loom', seq: 6, turn_id: 'turn_child', error_sha256: digest}
const event = (seq, type, data) => ({id: `evt_${seq}`, type, data: {sessionID: id, ...data},
  durable: {aggregateID: id, seq, version: 1}})
const log = (rows, synced = true) => [...rows, ...(synced ? [{type: 'log.synced', aggregateID: id,
  seq: rows.at(-1)?.durable.seq}] : [])].map((row) => `data: ${JSON.stringify(row)}\n\n`).join('')
const rows = [event(0, 'session.created', {}),
  event(1, 'session.execution.failed', {error: {type: 'provider.auth', message, status: 401}})]

test('exact synced native failure links by owned session and saved Loom hash without message', () => {
  const linked = probe.projectLog(log(rows), id, anchor)
  assert.equal(linked.status, 'linked')
  assert.deepEqual(linked.loom_turn, anchor)
  assert.equal(linked.native_failure.type, 'provider.auth')
  assert.equal(linked.native_failure.status, 401)
  assert.equal(linked.native_failure.message_byte_length, Buffer.byteLength(message))
  assert.equal(linked.native_failure.message_sha256, digest)
  assert.equal(linked.native_failure.event_id, 'evt_1')
  assert.equal(linked.native_failure.seq, 1)
  assert.equal(linked.native_failure.session_id, id)
  assert.doesNotMatch(JSON.stringify(linked), /private|detail/)
  const waiting = probe.projectLog(log(rows), id, null)
  assert.equal(waiting.status, 'unlinked')
  assert.equal(waiting.reason, 'loom-turn-not-finished')
})

test('missing marker, truncation, foreign session, duplicate sequence/failure, and wrong hash fail closed', () => {
  assert.equal(probe.projectLog(log(rows, false), id, anchor).reason, 'log-not-synced')
  assert.equal(probe.projectLog(log(rows).slice(0, -2), id, anchor).reason, 'log-truncated')
  assert.equal(probe.projectLog(log(rows), 'ses_foreign', anchor).reason, 'log-not-synced')
  assert.equal(probe.projectLog(log(rows.slice(1)), id, anchor).reason, 'log-prefix-missing')
  assert.equal(probe.projectLog(log([rows[0], {...rows[1], durable: {...rows[1].durable, seq: 0}}]), id, anchor).reason,
    'log-foreign-or-order')
  assert.equal(probe.projectLog(log([...rows, event(2, 'session.execution.failed', rows[1].data)]), id, anchor).reason,
    'native-failure-count')
  assert.equal(probe.projectLog(log(rows), id, {...anchor, error_sha256: '0'.repeat(64)}).reason,
    'native-loom-error-hash-mismatch')
  const skippedInternalSeq = [rows[0], {...rows[1], durable: {...rows[1].durable, seq: 3}}]
  assert.equal(probe.projectLog(log(skippedInternalSeq), id, anchor).status, 'linked')
  const hiddenAfterLastVisible = log(rows).replace('"seq":1}\n\n', '"seq":4}\n\n')
  assert.equal(probe.projectLog(hiddenAfterLastVisible, id, anchor).status, 'linked')
})

test('structured type, HTTP status, and message are allowlisted and redacted', () => {
  for (const error of [
    {type: 'private.foreign', message},
    {type: 'provider.auth', message, status: 99},
    {type: 'provider.auth', message, status: '401'},
    {type: 'provider.auth', message: {secret: 'private'}},
  ]) {
    const result = probe.projectLog(log([rows[0], event(1, 'session.execution.failed', {error})]), id, anchor)
    assert.equal(result.reason, 'native-error-invalid-shape')
    assert.doesNotMatch(JSON.stringify(result), /private|secret/)
  }
})

test('ownership, service PID, and selected/default models stay distinct', () => {
  const expected = {agent: 'agt_child', parent: 'agt_parent', native: id, root: '', repo: '/repo',
    name: 'aft-child-a-af12345678', parentName: 'aft-child-lead-af12345678', worktree: '/repo/worktree'}
  const row = {agent_id: expected.agent, name: expected.name, repo: expected.repo, harness: 'opencode',
    preset: 'task', created_by_kind: 'agent', parent_agent_id: expected.parent, root_agent_id: expected.parent,
    native_id: id, native_root: '', worktree_path: expected.worktree}
  const parent = {agent_id: expected.parent, name: expected.parentName, repo: expected.repo,
    harness: 'opencode', preset: 'lead', created_by_kind: 'user', parent_agent_id: null, root_agent_id: null}
  assert.equal(probe.owned(row, parent, {owned: true}, expected), true)
  assert.equal(probe.owned({...row, native_id: 'ses_foreign'}, parent, {owned: true}, expected), false)
  assert.equal(probe.owned(row, {...parent, name: 'foreign'}, {owned: true}, expected), false)
  assert.equal(probe.owned(row, parent, null, expected), false)
  const proc = (entries) => ({readdirSync: () => Object.keys(entries),
    readFileSync: (name) => Buffer.from(entries[name.split('/')[2]] ?? '')})
  const serve = '/usr/local/bin/opencode\0serve\0--service\0'
  assert.equal(probe.soleServicePid(proc({'42': serve, self: 'ignored'})), 42)
  assert.equal(probe.soleServicePid(proc({})), null)
  assert.equal(probe.soleServicePid(proc({'1': serve})), null)
  assert.equal(probe.soleServicePid(proc({'42': serve, '43': serve})), null)
  assert.equal(probe.soleServicePid(proc({'42': '/usr/local/bin/other\0serve\0--service\0'})), null)
  assert.equal(probe.soleServicePid(proc({'42': serve + '--foreign\0'})), null)
  const reg = {url: 'http://127.0.0.1:1234/', pid: 42, password: 'private'}
  assert.ok(probe.serviceRegistration(reg, 42))
  assert.equal(probe.serviceRegistration(reg, 43), null)
  assert.equal(probe.serviceRegistration({...reg, url: 'http://foreign:1234/'}, 42), null)
  assert.equal(probe.serviceRegistration({...reg, url: 'http://127.0.0.1/'}, 42), null)
  assert.equal(probe.serviceRegistration({...reg, url: 'http://127.0.0.1:1234/?foreign=1'}, 42), null)
  assert.equal(probe.serviceRegistration({...reg, url: 'http://127.0.0.1:1234/#foreign'}, 42), null)
  assert.equal(probe.serviceInfoOwned({pid: 42}, 42), true)
  assert.equal(probe.serviceInfoOwned({pid: 43}, 42), false)
  const session = {data: {id, metadata: {agent_id: expected.agent}, location: {directory: expected.worktree}}}
  assert.equal(probe.sessionOwned(session, expected), true)
  assert.equal(probe.sessionOwned({...session, data: {...session.data, metadata: {agent_id: 'agt_foreign'}}}, expected), false)
  assert.equal(probe.sessionOwned({...session, data: {...session.data, location: {directory: '/foreign'}}}, expected), false)
  assert.equal(probe.model(null), null)
  assert.equal(probe.model({providerID: 'openai', id: 'gpt-5.5'}), 'openai/gpt-5.5')
  assert.equal(probe.model({providerID: 'opencode', id: 'exo-free'}), 'opencode/exo-free')
})
