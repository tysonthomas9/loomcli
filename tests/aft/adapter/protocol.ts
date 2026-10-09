import { z } from 'zod';
import { JsonValueSchema, type JsonValue } from '@tysonthomas9/aft/types';

export const Id = z.string().min(1).max(512);
export const Digest = z.string().regex(/^[a-f0-9]{64}$/);
export const RelativePath = z.string().min(1).max(4096).refine(
  value => !value.startsWith('/') && !value.includes('\\') && !value.includes('\0') &&
    value.split('/').every(part => part !== '' && part !== '.' && part !== '..'),
  'Expected a bounded relative path',
);
export const Json = JsonValueSchema;
export type Json = JsonValue;
export const AgentRef = z.object({ fixtureLeaseId: Id, workspaceId: Id, agentId: Id }).strict();
export type AgentRef = z.infer<typeof AgentRef>;

// These are Loom wire protocols, not AFT's provenance, bindings or result schemas.
export const AgentRow = z.object({
  agent_id: Id, workspace_id: Id, repo: Id, worktree_path: Id, branch: Id,
  harness: z.literal('opencode'), harness_session_id: Id, harness_session_root: z.string(),
  parent_agent_id: Id.nullable(), root_agent_id: Id.nullable(), created_by_kind: Id,
  created_by_id: Id.nullable(), preset: Id, revision: z.number().int().nonnegative(),
  state: Id, running_turn_id: Id.nullable(), deleted_at: Id.nullable(), history_purged_at: Id.nullable(),
}).passthrough();
export type AgentRow = z.infer<typeof AgentRow>;
export const NativeRef = z.object({ agent_id: Id, harness: z.literal('opencode'), native_root: z.string(), native_id: Id }).strict();
export type NativeRef = z.infer<typeof NativeRef>;
export const Event = z.object({
  agent_id: Id, seq: z.number().int().positive(), event_id: Id, kind: Id,
  turn_id: z.string().nullable(), payload: Json, created_at: Id,
}).strict();
export type Event = z.infer<typeof Event>;
export const EventPage = z.object({
  events: z.array(Event), snapshot_seq: z.number().int().nonnegative(),
  next: z.number().int().nonnegative(), more: z.boolean(),
}).strict();
export const HttpResponse = z.object({ status: z.number().int().min(100).max(599), body: Json }).strict();
export type HttpResponse = z.infer<typeof HttpResponse>;
export type ReadTransport = (path: string, signal: AbortSignal) => Promise<HttpResponse>;

export const ServiceRegistration = z.object({
  url: Id, password: Id, pid: z.number().int().positive(), generation: Id,
  endpointId: Id,
}).strict();
export type ServiceRegistration = z.infer<typeof ServiceRegistration>;
export interface ProcessIdentity {
  pid: number; generation: string; executable: string; argv: string[];
}
export interface NativeAccess {
  registration(): Promise<ServiceRegistration>;
  process(): Promise<ProcessIdentity>;
  pinnedExecutable: string;
  sessions(agentId: string): Promise<NativeRef[]>;
  agent(agentId: string): Promise<AgentRow>;
  read: ReadTransport;
  log?(nativeSessionId: string, signal: AbortSignal): Promise<string>;
}
export class ObservationError extends Error {
  constructor(readonly code: 'ownership-mismatch' | 'identity-mismatch' | 'source-mismatch' |
    'deadline-exceeded' | 'incomplete-pages' | 'observation-failed' | 'unsupported-capability',
    message: string) { super(message); }
}
export function requireFact(condition: unknown, code: ObservationError['code'], message: string): asserts condition {
  if (!condition) throw new ObservationError(code, message);
}
export const sha256 = async (value: string | Uint8Array): Promise<string> => {
  const { createHash } = await import('node:crypto');
  return createHash('sha256').update(value).digest('hex');
};

// Public evidence is projected first, then redacted recursively. Native credentials
// and endpoint URLs are never included in any public projection or exception.
export function redact(value: Json, secrets: readonly string[] = []): Json {
  if (typeof value === 'string') {
    let safe = value;
    for (const secret of secrets.filter(Boolean).sort((a, b) => b.length - a.length)) safe = safe.split(secret).join('[REDACTED]');
    return safe.replace(/\b(?:gh[pousr]_[A-Za-z0-9_]+|github_pat_[A-Za-z0-9_]+|sk-[A-Za-z0-9_-]{8,})\b/g, '[REDACTED]')
      .replace(/\b(?:Bearer|Basic)\s+[A-Za-z0-9+/=_-]+/gi, '[REDACTED]')
      .replace(/\b(token|password|secret|api[_-]?key)\s*[=:]\s*[^\s,;]+/gi, '$1=[REDACTED]');
  }
  if (Array.isArray(value)) return value.map(entry => redact(entry, secrets));
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([key, entry]) => [key,
    /^(?:authorization|password|token|secret|api[_-]?key|ownerToken)$/i.test(key) ? '[REDACTED]' : redact(entry, secrets),
  ]));
  return value;
}
