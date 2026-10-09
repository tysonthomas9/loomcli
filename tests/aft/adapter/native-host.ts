import { lstat, open, readFile, realpath } from 'node:fs/promises';
import { constants } from 'node:fs';
import { containedPath } from './filesystem.js';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { AgentRow, AgentHistory, NativeRef, ServiceRegistration, Json, ObservationError, requireFact,
  type NativeAccess, type ProcessIdentity } from './protocol.js';

export interface NativeHostOptions {
  configRoot: string;
  workspaceId: string;
  repo: string;
  pinnedExecutable: string;
  // This is an internal host/platform port; suite data cannot supply code.
  processIdentity?: (pid: number) => Promise<ProcessIdentity>;
  fetch?: typeof fetch;
}
async function linuxProcessIdentity(pid: number): Promise<ProcessIdentity> {
  requireFact(process.platform === 'linux', 'unsupported-capability', 'Native process generation requires the owned Linux runtime');
  const executable = await realpath(`/proc/${pid}/exe`);
  const argv = (await readFile(`/proc/${pid}/cmdline`, 'utf8')).split('\0').filter(Boolean);
  const stat = await readFile(`/proc/${pid}/stat`, 'utf8');
  const end = stat.lastIndexOf(')');
  requireFact(end > 0, 'identity-mismatch', 'Native process generation is unreadable');
  const start = stat.slice(end + 2).split(' ')[19]; // /proc starttime is field 22, after (comm).
  requireFact(start && /^\d+$/.test(start), 'identity-mismatch', 'Native start time is missing');
  const generation = createHash('sha256').update(JSON.stringify({ pid, start, executable })).digest('hex');
  return { pid, generation, executable, argv };
}
/** Direct, read-only native access inside the owned runtime. No ambient HOME,
 * provider directory scan, scenario helper, or browser action is involved. */
export function createNativeHostAccess(options: NativeHostOptions): NativeAccess {
  const processIdentity = options.processIdentity ?? linuxProcessIdentity;
  const request = options.fetch ?? fetch;
  const registrationPath = path.join(options.configRoot, 'agents-opencode/state/opencode/service.json');
  const dbPath = path.join(options.configRoot, 'agents.db');
  let rootIdentity: { ino: number; dev: number } | undefined;
  const verifyRoot = async () => {
    const stat = await lstat(options.configRoot);
    requireFact(stat.isDirectory() && !stat.isSymbolicLink() && await realpath(options.configRoot) === options.configRoot &&
      (!rootIdentity || (stat.ino === rootIdentity.ino && stat.dev === rootIdentity.dev)),
      'ownership-mismatch', 'Native configuration root changed');
    rootIdentity ??= { ino: stat.ino, dev: stat.dev };
  };
  const verifyFile = async (file: string) => {
    await verifyRoot();
    await containedPath(options.configRoot, path.relative(options.configRoot, file));
    const stat = await lstat(file);
    requireFact(stat.isFile() && !stat.isSymbolicLink(), 'ownership-mismatch', 'Native store file changed');
    return stat;
  };
  const boundedRegistration = async () => {
    const stat = await verifyFile(registrationPath);
    requireFact(stat.size <= 65536, 'observation-failed', 'Native registration exceeds bound');
    const handle = await open(registrationPath, constants.O_RDONLY | constants.O_NOFOLLOW);
    try {
      const opened = await handle.stat();
      requireFact(opened.ino === stat.ino && opened.dev === stat.dev, 'identity-mismatch', 'Native registration changed');
      const bytes = Buffer.alloc(65537);
      const read = await handle.read(bytes, 0, bytes.length, 0);
      requireFact(read.bytesRead === stat.size, 'identity-mismatch', 'Native registration changed during read');
      const named = await verifyFile(registrationPath);
      requireFact(named.ino === stat.ino && named.dev === stat.dev && named.size === stat.size && named.mtimeMs === stat.mtimeMs,
        'identity-mismatch', 'Native registration changed during read');
      return JSON.parse(bytes.subarray(0, read.bytesRead).toString('utf8')) as { pid?: number; url?: string; password?: string };
    } finally { await handle.close(); }
  };
  const privateRegistration = async () => {
    const raw = await boundedRegistration();
    requireFact(Number.isInteger(raw.pid) && raw.pid! > 0, 'identity-mismatch', 'Native service PID is missing');
    const proc = await processIdentity(raw.pid!);
    return ServiceRegistration.parse({ pid: raw.pid, url: raw.url, password: raw.password, generation: proc.generation,
      endpointId: createHash('sha256').update(JSON.stringify({ pid: raw.pid, generation: proc.generation, url: raw.url })).digest('hex') });
  };
  const query = async (sql: string, args: string[]) => {
    const before = await verifyFile(dbPath);
    const { DatabaseSync } = await import('node:sqlite');
    const db = new DatabaseSync(dbPath, { readOnly: true });
    try {
      const rows = db.prepare(sql).all(...args);
      const after = await verifyFile(dbPath);
      requireFact(before.ino === after.ino && before.dev === after.dev, 'identity-mismatch', 'Native database changed');
      return rows;
    } finally { db.close(); }
  };
  return {
    pinnedExecutable: options.pinnedExecutable,
    registration: privateRegistration,
    process: async () => processIdentity((await privateRegistration()).pid),
    async agent(agentId) {
      const rows = await query('SELECT agent_id, workspace_id, repo, worktree_path, branch, harness, harness_session_id, harness_session_root, parent_agent_id, root_agent_id, created_by_kind, created_by_id, preset, revision, state, running_turn_id, deleted_at, history_purged_at, model, outcome FROM agents WHERE agent_id=? AND workspace_id=?', [agentId, options.workspaceId]);
      requireFact(rows.length === 1, 'identity-mismatch', 'Native agent row is missing or duplicated');
      const row = AgentRow.parse(rows[0]);
      requireFact(row.repo === options.repo, 'ownership-mismatch', 'Native agent belongs to another repository');
      return row;
    },
    async history(agentId) {
      const rows = await query('SELECT a.agent_id, a.workspace_id, a.repo, a.revision, a.deleted_at, a.history_purged_at, (SELECT COUNT(*) FROM agent_events e WHERE e.agent_id=a.agent_id) AS saved_event_count FROM agents a WHERE a.agent_id=? AND a.workspace_id=?', [agentId,options.workspaceId]);
      requireFact(rows.length===1,'identity-mismatch','Agent saved-history identity is missing or duplicated');
      const raw = rows[0] as Record<string,unknown>;
      const value = AgentHistory.parse({agentId:raw.agent_id,workspaceId:raw.workspace_id,repo:raw.repo,revision:raw.revision,
        deletedAt:raw.deleted_at,historyPurgedAt:raw.history_purged_at,savedEventCount:raw.saved_event_count});
      requireFact(value.agentId===agentId&&value.workspaceId===options.workspaceId&&value.repo===options.repo,
        'identity-mismatch','Agent saved-history belongs to another owned repository'); return value;
    },
    async sessions(agentId) {
      const rows = await query('SELECT agent_id, harness, native_root, native_id FROM agent_native_sessions WHERE agent_id=? ORDER BY native_id', [agentId]);
      return rows.map(row => NativeRef.parse(row));
    },
    async log(nativeSessionId, signal) {
      requireFact(/^[A-Za-z0-9_-]+$/.test(nativeSessionId), 'identity-mismatch', 'Native session ID is outside the protocol');
      const registration = await privateRegistration();
      const base = new URL(registration.url);
      requireFact(base.protocol === 'http:' && ['127.0.0.1', 'localhost'].includes(base.hostname) && base.port &&
        !base.username && !base.password && base.pathname === '/' && !base.search && !base.hash,
        'ownership-mismatch', 'Native registration is not loopback-owned');
      const url = new URL(`/api/experimental/session/${encodeURIComponent(nativeSessionId)}/log?follow=false`, base);
      const response = await request(url, { redirect: 'error', headers: { Authorization: `Basic ${Buffer.from(`opencode:${registration.password}`).toString('base64')}` },
        signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]) });
      requireFact(response.status === 200 && response.headers.get('content-type')?.startsWith('text/event-stream') && response.body,
        'observation-failed', 'Native log is unreadable');
      let count = 0; const chunks: Uint8Array[] = [];
      for await (const chunk of response.body) {
        count += chunk.byteLength; requireFact(count <= 262144, 'incomplete-pages', 'Native log byte bound reached'); chunks.push(chunk);
      }
      return Buffer.concat(chunks).toString('utf8');
    },
    async read(route, signal) {
      // All paths come from fixed adapter implementations; reject even internal
      // mistakes that could turn this private credential into generic HTTP.
      requireFact(route === '/api/info' || /^\/api\/session\/[A-Za-z0-9_%.-]+(?:\/message\?(?:order=asc&limit=\d+))?$/.test(route),
        'ownership-mismatch', 'Native route is outside the read protocol');
      const registration = await privateRegistration();
      const base = new URL(registration.url);
      requireFact(base.protocol === 'http:' && ['127.0.0.1', 'localhost'].includes(base.hostname) && base.port &&
        !base.username && !base.password && base.pathname === '/' && !base.search && !base.hash,
      'ownership-mismatch', 'Native registration is not loopback-owned');
      const target = new URL(route, base);
      const segment = route.split('/')[3]?.split('?')[0];
      requireFact(target.pathname + target.search === route && (!segment || /^[A-Za-z0-9_-]+$/.test(decodeURIComponent(segment))),
        'ownership-mismatch', 'Native route changed its protocol target');
      const response = await request(target, { method: 'GET', redirect: 'error',
        headers: { Authorization: `Basic ${Buffer.from(`opencode:${registration.password}`).toString('base64')}` },
        signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]) });
      requireFact(response.body, 'observation-failed', 'Native response has no body');
      let bytes = 0; const chunks: Uint8Array[] = [];
      for await (const chunk of response.body) {
        bytes += chunk.byteLength;
        if (bytes > 4 * 1024 * 1024) { await response.body.cancel().catch(() => {}); throw new ObservationError('incomplete-pages', 'Native response exceeds its byte bound'); }
        chunks.push(chunk);
      }
      let body;
      try { body = Json.parse(JSON.parse(Buffer.concat(chunks).toString('utf8'))); }
      catch { throw new ObservationError('observation-failed', 'Native response is not valid JSON'); }
      return { status: response.status, body };
    },
  };
}
