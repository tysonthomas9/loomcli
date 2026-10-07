// Read-only, allowlisted OpenCode failure evidence. This file is sent to node -e
// inside the run-owned loom-local container; it never emits raw native responses.
const crypto = require('node:crypto')
const fs = require('node:fs')
const path = require('node:path')

const types = new Set([
  'provider.rate-limit', 'provider.auth', 'provider.quota', 'provider.content-filter',
  'provider.transport', 'provider.internal', 'provider.invalid-output',
  'provider.invalid-request', 'provider.unsupported-operation', 'provider.no-route',
  'provider.unknown', 'provider.timeout', 'permission.rejected', 'tool.execution', 'unknown',
])
const sha = (value) => crypto.createHash('sha256').update(value).digest('hex')
const unavailable = (reason, extra = {}) => ({status: 'unavailable', reason, ...extra})

function owned(row, parent, recorded, expected) {
  if (!row || row.agent_id !== expected.agent || row.name !== expected.name ||
      row.repo !== expected.repo || row.harness !== 'opencode' || row.preset !== 'task' ||
      row.created_by_kind !== 'agent' || row.parent_agent_id !== expected.parent ||
      row.root_agent_id !== expected.parent || !row.worktree_path ||
      row.native_id !== expected.native || row.native_root !== expected.root ||
      !recorded) return false
  return !!parent && parent.agent_id === expected.parent && parent.name === expected.parentName &&
    parent.preset === 'lead' && parent.harness === 'opencode' && parent.repo === expected.repo &&
    parent.created_by_kind === 'user' && parent.parent_agent_id === null && parent.root_agent_id === null
}

function soleServicePid(proc = fs) {
  const pids = proc.readdirSync('/proc').filter((name) => /^\d+$/.test(name)).map((name) => {
    try {
      const args = proc.readFileSync(`/proc/${name}/cmdline`).toString().split('\0').filter(Boolean)
      const at = args.indexOf('serve')
      return at > 0 && /(?:^|\/)opencode$/.test(args[at - 1]) &&
        args[at + 1] === '--service' && at + 2 === args.length ? Number(name) : null
    } catch { return null }
  }).filter((pid) => pid !== null)
  return pids.length === 1 && pids[0] > 1 ? pids[0] : null
}

function serviceRegistration(reg, pid) {
  let base
  try { base = new URL(reg?.url) } catch { return null }
  if (base.protocol !== 'http:' || !['127.0.0.1', 'localhost'].includes(base.hostname) ||
      !base.port || base.pathname !== '/' || base.search || base.hash || base.username || base.password ||
      !Number.isInteger(pid) || pid < 2 || reg.pid !== pid ||
      typeof reg.password !== 'string' || !reg.password) return null
  return base
}

const serviceInfoOwned = (info, pid) => Number.isInteger(pid) && info?.pid === pid

function sessionOwned(session, expected) {
  return session?.data?.id === expected.native &&
    session.data.metadata?.agent_id === expected.agent &&
    session.data.location?.directory === expected.worktree
}

function model(value) {
  if (value == null) return null
  if (typeof value.providerID !== 'string' || !value.providerID || value.providerID === 'aft' ||
      typeof value.id !== 'string' || !value.id) throw Error('invalid-model')
  return value.providerID + '/' + value.id
}

function parseSse(raw, native) {
  if (typeof raw !== 'string' || Buffer.byteLength(raw) > 262144 || !/\r?\n\r?\n$/.test(raw))
    return unavailable('log-truncated')
  const frames = raw.split(/\r?\n\r?\n/).filter(Boolean)
  if (!frames.length || frames.length > 501) return unavailable('log-size-or-event-limit')
  const events = []
  for (const frame of frames) {
    const data = frame.split(/\r?\n/).filter((line) => line.startsWith('data:'))
      .map((line) => line.slice(5).trimStart()).join('\n')
    if (!data) continue
    let event
    try { event = JSON.parse(data) } catch { return unavailable('log-invalid-json') }
    events.push(event)
  }
  if (!events.length || events.length > 501) return unavailable('log-size-or-event-limit')
  const synced = events.at(-1)
  if (synced?.type !== 'log.synced' || synced.aggregateID !== native ||
      events.slice(0, -1).some((event) => event.type === 'log.synced'))
    return unavailable('log-not-synced')
  const durable = events.slice(0, -1)
  if (!durable.length && synced.seq === undefined) return unavailable('log-no-durable-events')
  if (!durable.length || durable.length > 500 || !Number.isInteger(synced.seq))
    return unavailable('log-empty-or-truncated')
  if (durable[0].type !== 'session.created' || durable[0].durable?.seq !== 0)
    return unavailable('log-prefix-missing')
  for (let index = 0; index < durable.length; index++) {
    const event = durable[index]
    if (typeof event.id !== 'string' || !/^evt_[A-Za-z0-9_-]+$/.test(event.id) ||
        event.durable?.aggregateID !== native || !Number.isInteger(event.durable.seq) ||
        event.durable.seq < 0 ||
        (index > 0 && event.durable.seq <= durable[index - 1].durable.seq) ||
        (event.data?.sessionID !== undefined && event.data.sessionID !== native))
      return unavailable('log-foreign-or-order')
  }
  if (synced.seq < durable.at(-1).durable.seq) return unavailable('log-watermark-mismatch')
  return {status: 'parsed', events: durable, watermark: synced.seq}
}

function projectLog(raw, native, anchor, root) {
  const parsed = parseSse(raw, native)
  if (parsed.status !== 'parsed') return parsed
  const failures = parsed.events.filter((event) => event.type === 'session.execution.failed')
  if (failures.length !== 1) return unavailable('native-failure-count', {failure_count: failures.length})
  const event = failures[0]
  const error = event.data?.error
  if (!error || !types.has(error.type) || typeof error.message !== 'string' ||
      (error.status !== undefined && (!Number.isInteger(error.status) || error.status < 100 || error.status > 599)))
    return unavailable('native-error-invalid-shape')
  const canonical = error.message || error.type // Loom's toolError.text contract.
  const nativeFailure = {event_id: event.id, seq: event.durable.seq, session_id: native,
    type: error.type, status: error.status ?? null, message_byte_length: Buffer.byteLength(canonical),
    message_sha256: sha(canonical), watermark: parsed.watermark}
  if (!anchor) return {status: 'unlinked', reason: 'loom-turn-not-finished', native_failure: nativeFailure}
  if (typeof root !== 'string' ||
      anchor.event_id !== `agent.turn_completed:${root}:${native}:${anchor.turn_id}` ||
      !Number.isInteger(anchor.seq) ||
      typeof anchor.turn_id !== 'string' || !anchor.turn_id ||
      !/^[a-f0-9]{64}$/.test(anchor.error_sha256)) return unavailable('loom-anchor-invalid')
  if (nativeFailure.message_sha256 !== anchor.error_sha256)
    return unavailable('native-loom-error-hash-mismatch', {native_failure: nativeFailure})
  return {status: 'linked', native_failure: nativeFailure,
    loom_turn: {event_id: anchor.event_id, seq: anchor.seq, turn_id: anchor.turn_id,
      error_sha256: anchor.error_sha256}}
}

async function read(response, maxBytes) {
  if (!response.ok || !response.body) throw Error('http-unavailable')
  const chunks = []
  let size = 0
  for await (const chunk of response.body) {
    size += chunk.length
    if (size > maxBytes) throw Error('response-too-large')
    chunks.push(chunk)
  }
  return Buffer.concat(chunks).toString('utf8')
}

async function main() {
  const [agent, parentID, native, root, repo, run, eventID, seq, turnID, errorHash] = process.argv.slice(1)
  const receipt = {agent_id: agent, native_id: native, native_root: root,
    registry_requested_model: null, native_session_selected_model: null,
    native_service_default_model: null, model_evidence: 'selected-and-default-only'}
  const emit = (value) => process.stdout.write(JSON.stringify({...receipt, ...value}) + '\n')
  const fail = (reason) => emit(unavailable(reason))
  try {
    if (process.env.LOOM_CONFIG_DIR !== '/root/.loom' || !/^agt_[A-Za-z0-9]+$/.test(agent) ||
        !/^agt_[A-Za-z0-9]+$/.test(parentID) || !/^af[a-z0-9]{8}$/.test(run))
      return fail('owned-runtime-invalid')
    const {DatabaseSync} = require('node:sqlite')
    const db = new DatabaseSync('/root/.loom/agents.db', {readOnly: true})
    const row = db.prepare('SELECT agent_id,name,repo,harness,preset,created_by_kind,parent_agent_id,root_agent_id,model,worktree_path,harness_session_id AS native_id,harness_session_root AS native_root FROM agents WHERE agent_id=? AND workspace_id=?').get(agent, 'LOCALMODE')
    const lead = db.prepare('SELECT agent_id,name,repo,harness,preset,created_by_kind,parent_agent_id,root_agent_id FROM agents WHERE agent_id=? AND workspace_id=?').get(parentID, 'LOCALMODE')
    const registered = db.prepare('SELECT 1 FROM agent_native_sessions WHERE agent_id=? AND harness=? AND native_root=? AND native_id=?').get(agent, 'opencode', root, native)
    const letter = row?.name === `aft-child-a-${run}` ? 'a' : 'b'
    const expected = {agent, parent: parentID, native, root, repo,
      name: `aft-child-${letter}-${run}`, parentName: `aft-child-lead-${run}`}
    if (!owned(row, lead, registered, expected)) return fail('foreign-child-or-native-ref')
    receipt.registry_requested_model = row.model
    const pid = soleServicePid()
    if (!pid) return fail('service-process-ambiguous-or-absent')
    let reg
    try { reg = JSON.parse(fs.readFileSync(path.join(process.env.LOOM_CONFIG_DIR,
      'agents-opencode/state/opencode/service.json'), 'utf8')) }
    catch { return fail('service-registration-unavailable') }
    const base = serviceRegistration(reg, pid)
    if (!base) return fail('service-registration-invalid')
    const auth = 'Basic ' + Buffer.from('opencode:' + reg.password).toString('base64')
    const get = async (route, max = 65536) => {
      const response = await fetch(new URL(route, base),
        {headers: {Authorization: auth}, signal: AbortSignal.timeout(2500)})
      return JSON.parse(await read(response, max))
    }
    let info, session
    try {
      info = await get('/api/info')
      if (!serviceInfoOwned(info, pid)) return fail('service-process-identity-mismatch')
      session = await get('/api/session/' + encodeURIComponent(native))
    } catch { return fail('owned-service-or-session-unavailable') }
    if (!sessionOwned(session, {agent, native, worktree: row.worktree_path}))
      return fail('session-owner-or-worktree-mismatch')
    try { receipt.native_session_selected_model = model(session.data.model) }
    catch { return fail('session-selected-model-invalid') }
    try {
      const url = new URL('/api/model/default', base)
      url.searchParams.set('location[directory]', row.worktree_path)
      receipt.native_service_default_model = model((await get(url)).data)
      receipt.default_model_status = 'observed'
    } catch { receipt.default_model_status = 'unavailable' }
    const anchor = errorHash ? {event_id: eventID, seq: Number(seq), turn_id: turnID, error_sha256: errorHash} : null
    try {
      const url = new URL('/api/experimental/session/' + encodeURIComponent(native) + '/log', base)
      url.searchParams.set('follow', 'false')
      const response = await fetch(url, {headers: {Authorization: auth}, signal: AbortSignal.timeout(3000)})
      if (!response.headers.get('content-type')?.startsWith('text/event-stream')) return fail('log-content-type-invalid')
      const result = projectLog(await read(response, 262144), native, anchor, root)
      emit(result)
    } catch { fail('native-log-unavailable') }
  } catch { fail('native-diagnostic-unavailable') }
}

module.exports = {owned, soleServicePid, serviceRegistration, serviceInfoOwned,
  sessionOwned, model, parseSse, projectLog}
if (/^agt_[A-Za-z0-9]+$/.test(process.argv[1] ?? '')) main()
