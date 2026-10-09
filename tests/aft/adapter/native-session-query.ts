import { z } from 'zod';
import { ContainerRootIdentity } from './container-observations.js';
import { WorkspaceRepositoryFact } from './workspaces.js';
import type { NativeHostAccess } from './native-host.js';
import { AgentRow, HttpResponse, Id, Json, NativeRef, NativeRegistrationIdentity, ProcessIdentitySchema, ServiceRegistration,
  nativeRegistrationIdentity, redact, requireFact } from './protocol.js';

const Binding = { workspaceBindingId: Id };
const AgentId = Id.regex(/^agt_[A-Za-z0-9_-]+$/);
const SessionId = Id.regex(/^[A-Za-z0-9_-]+$/);
const Limit = z.number().int().min(1).max(200);
/** Private query bodies only. Framing, capture/close and descriptor lifetime
 * belong to the fixture session owner; no request chooses SQL, paths or code. */
export const NativeSessionQuery = z.discriminatedUnion('operation', [
  z.object({ ...Binding, operation: z.literal('raw-agent'), agentId: AgentId }).strict(),
  z.object({ ...Binding, operation: z.literal('sessions'), agentId: AgentId,
    maxRegistrations: z.number().int().min(1).max(1000) }).strict(),
  z.object({ ...Binding, operation: z.literal('registration-identity') }).strict(),
  z.object({ ...Binding, operation: z.literal('native-process') }).strict(),
  z.object({ ...Binding, operation: z.literal('native-info') }).strict(),
  z.object({ ...Binding, operation: z.literal('native-session'), agentId: AgentId,
    nativeSessionId: SessionId, nativeRoot: NativeRef.shape.native_root }).strict(),
  z.object({ ...Binding, operation: z.literal('native-messages'), agentId: AgentId,
    nativeSessionId: SessionId, nativeRoot: NativeRef.shape.native_root, limit: Limit }).strict(),
  z.object({ ...Binding, operation: z.literal('source-physical'), sourceKey: Id }).strict(),
  z.object({ ...Binding, operation: z.literal('agent-physical'), agentId: AgentId }).strict(),
]);
export type NativeSessionQuery = z.infer<typeof NativeSessionQuery>;
/** The root is the exact repository in retained workspace topology, not a
 * claim about the creator's original pre-workspace source directory. */
export const NativeSessionPhysicalSource = z.object({ repository: WorkspaceRepositoryFact,
  root: ContainerRootIdentity }).strict();
export const NativeSessionPhysicalAgent = z.object({ agentId: AgentId, root: ContainerRootIdentity, commonDir: Id }).strict();
export const NativeSessionQueryReply = z.discriminatedUnion('operation', [
  z.object({ ...Binding, operation: z.literal('raw-agent'), data: AgentRow }).strict(),
  z.object({ ...Binding, operation: z.literal('sessions'), data: z.array(NativeRef).max(1000) }).strict(),
  z.object({ ...Binding, operation: z.literal('registration-identity'), data: NativeRegistrationIdentity }).strict(),
  z.object({ ...Binding, operation: z.literal('native-process'), data: ProcessIdentitySchema }).strict(),
  z.object({ ...Binding, operation: z.literal('native-info'), data: HttpResponse }).strict(),
  z.object({ ...Binding, operation: z.literal('native-session'), data: HttpResponse }).strict(),
  z.object({ ...Binding, operation: z.literal('native-messages'), data: HttpResponse }).strict(),
  z.object({ ...Binding, operation: z.literal('source-physical'), data: NativeSessionPhysicalSource }).strict(),
  z.object({ ...Binding, operation: z.literal('agent-physical'), data: NativeSessionPhysicalAgent }).strict(),
]);
export type NativeSessionQueryReply = z.infer<typeof NativeSessionQueryReply>;
export interface NativeSessionPhysicalPorts {
  source(sourceKey: string, signal: AbortSignal): Promise<z.infer<typeof NativeSessionPhysicalSource>>;
  agent(agentId: string, signal: AbortSignal): Promise<z.infer<typeof NativeSessionPhysicalAgent>>;
}
export interface NativeSessionQueryBinding {
  readonly workspaceBindingId: string;
  readonly workspaceId: string;
  readonly sources: readonly { readonly sourceKey: string; readonly repository: z.infer<typeof WorkspaceRepositoryFact>;
    readonly root: z.infer<typeof ContainerRootIdentity> }[];
  readonly access: NativeHostAccess;
  readonly physical: NativeSessionPhysicalPorts;
  readonly secrets: readonly string[];
  /** Exact owner/container/helper/store/physical continuity; never a new capture. */
  verify(signal: AbortSignal): Promise<void>;
}
/** Delegates the existing canonical access. This creates no ownership grant,
 * session, process, SQL connection, descriptor or parallel identity registry. */
export function createNativeSessionQueryMapper(binding: NativeSessionQueryBinding) {
  const workspaceBindingId = Id.parse(binding.workspaceBindingId), workspaceId = Id.parse(binding.workspaceId);
  const sources = z.array(z.object({ sourceKey: Id, repository: WorkspaceRepositoryFact, root: ContainerRootIdentity }).strict()).min(1).max(32).parse(binding.sources);
  requireFact(new Set(sources.map(value => value.sourceKey)).size === sources.length &&
    new Set(sources.map(value => value.repository.repo)).size === sources.length &&
    new Set(sources.map(value => value.repository.repoName)).size === sources.length &&
    sources.every(value => value.root.path === value.repository.repo),
  'ownership-mismatch', 'Native source binding is missing or ambiguous');
  const access = binding.access, verify = binding.verify.bind(binding);
  const rawAgent = access.rawAgent.bind(access), sessions = access.sessions.bind(access), registration = access.registration.bind(access);
  const process = access.process.bind(access), read = access.read.bind(access);
  const sourcePhysical = binding.physical.source.bind(binding.physical), agentPhysical = binding.physical.agent.bind(binding.physical);
  const secrets = [...binding.secrets];
  const ownedCall = async <T>(signal: AbortSignal, call: () => Promise<T>): Promise<T> => {
    signal.throwIfAborted(); await verify(signal); signal.throwIfAborted();
    const value = await call();
    signal.throwIfAborted(); await verify(signal); signal.throwIfAborted();
    return value;
  };
  const ownedRow = async (agentId: string, signal: AbortSignal) => {
    const row = AgentRow.parse(await ownedCall(signal, () => rawAgent(agentId, signal)));
    requireFact(row.agent_id === agentId && row.workspace_id === workspaceId && sources.some(value => value.repository.repo === row.repo),
      'identity-mismatch', 'Native row differs from the bound workspace/source');
    return row;
  };
  const ownedSession = async (agentId: string, nativeSessionId: string, nativeRoot: string, signal: AbortSignal) => {
    await ownedRow(agentId, signal);
    const refs = z.array(NativeRef).max(1000).parse(await ownedCall(signal, () => sessions(agentId)));
    requireFact(refs.every(value => value.agent_id === agentId) &&
      refs.filter(value => value.native_id === nativeSessionId && value.native_root === nativeRoot).length === 1,
    'ownership-mismatch', 'Native session is not uniquely registered to the bound actor');
  };
  let active = false;
  return async (raw: unknown, signal: AbortSignal): Promise<NativeSessionQueryReply> => {
    const request = NativeSessionQuery.parse(Json.parse(raw));
    requireFact(Buffer.byteLength(JSON.stringify(request)) <= 1024 * 1024, 'incomplete-pages', 'Native query exceeds its byte bound');
    requireFact(request.workspaceBindingId === workspaceBindingId, 'ownership-mismatch', 'Native query binding is foreign');
    if (request.operation === 'source-physical') requireFact(sources.some(value => value.sourceKey === request.sourceKey),
      'ownership-mismatch', 'Native source key is not bound');
    requireFact(!active, 'ownership-mismatch', 'Native query overlaps another operation');
    signal.throwIfAborted(); active = true;
    try {
      await verify(signal); signal.throwIfAborted();
      const replySecrets = [...secrets];
      const nativeRead = async (route: string) => {
        const before = ServiceRegistration.parse(await ownedCall(signal, registration));
        nativeRegistrationIdentity(before); replySecrets.push(before.password);
        signal.throwIfAborted();
        const response = await ownedCall(signal, () => read(route, signal));
        signal.throwIfAborted();
        const after = ServiceRegistration.parse(await ownedCall(signal, registration));
        requireFact(JSON.stringify(before) === JSON.stringify(after), 'identity-mismatch', 'Native endpoint changed during the read');
        return response;
      };
      let data: unknown;
      switch (request.operation) {
        case 'raw-agent': data = await ownedRow(request.agentId, signal); break;
        case 'sessions': {
          await ownedRow(request.agentId, signal);
          const refs = z.array(NativeRef).max(request.maxRegistrations).parse(await ownedCall(signal, () => sessions(request.agentId)));
          requireFact(refs.every(value => value.agent_id === request.agentId) &&
            new Set(refs.map(value => JSON.stringify([value.harness,value.native_root,value.native_id]))).size === refs.length,
          'identity-mismatch', 'Native registrations are foreign or duplicated');
          data = refs; break;
        }
        case 'registration-identity': data = nativeRegistrationIdentity(await ownedCall(signal, registration)); break;
        case 'native-process': data = ProcessIdentitySchema.parse(await ownedCall(signal, process)); break;
        case 'native-info': data = await nativeRead('/api/info'); break;
        case 'native-session':
        case 'native-messages': {
          await ownedSession(request.agentId, request.nativeSessionId, request.nativeRoot, signal);
          const suffix = request.operation === 'native-messages' ? `/message?order=asc&limit=${request.limit}` : '';
          data = await nativeRead(`/api/session/${encodeURIComponent(request.nativeSessionId)}${suffix}`);
          await ownedSession(request.agentId, request.nativeSessionId, request.nativeRoot, signal);
          break;
        }
        case 'source-physical': {
          const expected = sources.find(value => value.sourceKey === request.sourceKey);
          requireFact(expected, 'ownership-mismatch', 'Native source key is not bound');
          const observed = NativeSessionPhysicalSource.parse(await ownedCall(signal, () => sourcePhysical(request.sourceKey, signal)));
          requireFact(JSON.stringify(observed.repository) === JSON.stringify(expected.repository) &&
            JSON.stringify(observed.root) === JSON.stringify(expected.root),
            'identity-mismatch', 'Native source physical association changed');
          data = observed; break;
        }
        case 'agent-physical': {
          const row = await ownedRow(request.agentId, signal);
          const source = sources.find(value => value.repository.repo === row.repo)!;
          const observed = NativeSessionPhysicalAgent.parse(await ownedCall(signal, () => agentPhysical(request.agentId, signal)));
          requireFact(observed.agentId === row.agent_id && observed.root.path === row.worktree_path && observed.commonDir === source.repository.commonDir,
            'identity-mismatch', 'Native agent physical association changed');
          data = observed; break;
        }
      }
      signal.throwIfAborted(); await verify(signal); signal.throwIfAborted();
      const reply = NativeSessionQueryReply.parse(Json.parse({ workspaceBindingId, operation: request.operation, data }));
      const json = Json.parse(reply);
      requireFact(JSON.stringify(redact(json, replySecrets)) === JSON.stringify(json), 'observation-failed', 'Native reply contains private material');
      return reply;
    } finally { active = false; }
  };
}
