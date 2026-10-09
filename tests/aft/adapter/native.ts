import { probeOccurrences, type SyntheticProbe } from './synthetic-probe.js';
import { z } from 'zod';
import { RedactionFacts, redactionFacts } from './redaction.js';
import { AgentRef, AgentRow, Id, Json, NativeRef, ObservationError, ServiceRegistration, requireFact, sha256, type NativeAccess } from './protocol.js';

export const NativeInput = z.object({ agent: AgentRef, view: z.enum([
  'session', 'inputs', 'completed-models', 'tools', 'usage', 'presence',
]), nativeSessionId: Id, nativeRoot: z.string(), expectedGeneration: Id,
  probeHandle: Id.nullable().optional(),
  maxMessages: z.number().int().min(1).max(200),
}).strict();
const Base = { agentId: Id, nativeSessionId: Id, nativeRoot: z.string(), servicePid: z.number().int().positive(),
  serviceGeneration: Id, registeredEndpointId: Id };
const RecordIdentity = { id: Id, sessionId: Id };
export const NativeOutput = z.discriminatedUnion('view', [
  z.object({ ...Base, view: z.literal('session'), directory: Id, metadataAgentId: Id,
    selectedModel: z.object({ provider: Id, model: Id }).strict().nullable() }).strict(),
  z.object({ ...Base, view: z.literal('presence'), present: z.boolean(), nativeStatus: z.number().int(),
    nativeErrorName: Id.nullable(), nativeErrorSessionId: Id.nullable() }).strict(),
  z.object({ ...Base, view: z.literal('inputs'), complete: z.boolean(), records: z.array(z.object({
    ...RecordIdentity, text: z.string(), createdAt: z.union([z.string(), z.number()]).nullable(),
  }).strict()) }).strict(),
  z.object({ ...Base, view: z.literal('completed-models'), complete: z.boolean(), records: z.array(z.object({
    ...RecordIdentity, completedAt: z.union([z.string(), z.number()]), provider: Id, model: Id,
  }).strict()) }).strict(),
  z.object({ ...Base, view: z.literal('tools'), complete: z.boolean(), records: z.array(z.object({
    ...RecordIdentity, itemId: Id, messageType: Id, name: Id, state: Id, input: Json, output: Json, outputPresent: z.boolean(), inputRedaction: RedactionFacts, outputRedaction: RedactionFacts,
    probe: z.object({ handle: Id, inputOccurrences: z.number().int().nonnegative(), outputOccurrences: z.number().int().nonnegative() }).strict().nullable(),
  }).strict()) }).strict(),
  z.object({ ...Base, view: z.literal('usage'), complete: z.boolean(), records: z.array(z.object({
    ...RecordIdentity, inputTokens: z.number().nonnegative(), outputTokens: z.number().nonnegative(),
    cacheReadTokens: z.number().nonnegative(), cacheWriteTokens: z.number().nonnegative(), costUsd: z.number().finite().nonnegative(),
  }).strict()) }).strict(),
]);
const Message = z.object({ id: Id, sessionID: Id, type: Id, time: z.object({
  created: z.union([z.string(), z.number()]).optional(), completed: z.union([z.string(), z.number()]).nullable().optional(),
}).passthrough(), model: z.object({ providerID: Id, id: Id }).passthrough().optional(),
  finish: Id.optional(), error: Json.optional(), content: z.array(Json).optional(),
  tokens: z.object({ input: z.number().nonnegative().optional(), output: z.number().nonnegative().optional(),
    reasoning: z.number().nonnegative().optional(), cache: z.object({ read: z.number().nonnegative().optional(), write: z.number().nonnegative().optional() }).passthrough().optional(),
  }).passthrough().optional(), cost: z.number().nonnegative().optional(),
}).passthrough();

export async function verifyNativeService(access: NativeAccess, expectedGeneration: string, signal: AbortSignal) {
  const registration = ServiceRegistration.parse(await access.registration());
  let url: URL;
  try { url = new URL(registration.url); } catch { throw new ObservationError('identity-mismatch', 'Invalid native registration'); }
  requireFact(url.protocol === 'http:' && ['127.0.0.1', 'localhost'].includes(url.hostname) &&
    url.port && Number(url.port) <= 65535 && !url.username && !url.password && url.pathname === '/' && !url.search && !url.hash,
  'ownership-mismatch', 'Native endpoint is outside the owned service');
  const process = await access.process();
  requireFact(process.pid === registration.pid && process.generation === registration.generation &&
    registration.generation === expectedGeneration && process.executable === access.pinnedExecutable &&
    process.argv[0] === access.pinnedExecutable && process.argv[1] === 'serve' && process.argv.includes('--service'),
  'identity-mismatch', 'Native service process changed');
  const info = await access.read('/api/info', signal);
  requireFact(info.status === 200 && typeof info.body === 'object' && info.body !== null &&
    !Array.isArray(info.body) && info.body.pid === registration.pid,
  'identity-mismatch', 'Native info does not match the registered process');
  return registration;
}
export async function observeNative(input: z.infer<typeof NativeInput>, access: NativeAccess,
  owned: AgentRow, signal: AbortSignal, probe?: SyntheticProbe, secrets: readonly string[] = []): Promise<z.infer<typeof NativeOutput>> {
  requireFact(!input.probeHandle || (input.view === 'tools' && probe?.handle === input.probeHandle), 'ownership-mismatch', 'Native synthetic probe is not bound');
  const before = await verifyNativeService(access, input.expectedGeneration, signal);
  const row = AgentRow.parse(await access.agent(input.agent.agentId));
  requireFact(row.agent_id === owned.agent_id && row.workspace_id === owned.workspace_id && row.repo === owned.repo &&
    row.worktree_path === owned.worktree_path && row.branch === owned.branch && row.parent_agent_id === owned.parent_agent_id &&
    row.root_agent_id === owned.root_agent_id && row.harness_session_id === input.nativeSessionId && row.harness_session_root === input.nativeRoot,
  'identity-mismatch', 'Native registry identity changed');
  const refs = z.array(NativeRef).parse(await access.sessions(row.agent_id));
  const matching = refs.filter(ref => ref.agent_id === row.agent_id && ref.native_id === input.nativeSessionId && ref.native_root === input.nativeRoot);
  requireFact(matching.length === 1 && refs.every(ref => ref.agent_id === row.agent_id), 'ownership-mismatch', 'Native session registration is missing, duplicated or foreign');
  const base = { agentId: row.agent_id, nativeSessionId: input.nativeSessionId, nativeRoot: input.nativeRoot,
    servicePid: before.pid, serviceGeneration: before.generation, registeredEndpointId: before.endpointId };
  const response = await access.read(`/api/session/${encodeURIComponent(input.nativeSessionId)}`, signal);
  let output: z.infer<typeof NativeOutput>;
  if (response.status === 404 && input.view === 'presence') {
    const missing = z.object({ _tag: z.literal('SessionNotFoundError'), sessionID: z.literal(input.nativeSessionId),
      message: z.literal(`Session not found: ${input.nativeSessionId}`) }).strict().safeParse(response.body);
    requireFact(missing.success, 'identity-mismatch', 'Native absence lacks the exact SessionNotFoundError receipt');
    output = { ...base, view: 'presence', present: false, nativeStatus: 404,
      nativeErrorName: 'SessionNotFoundError', nativeErrorSessionId: input.nativeSessionId };
  } else {
    requireFact(response.status === 200, 'observation-failed', 'Native session is unreadable');
    const session = z.object({ data: z.object({ id: Id, metadata: z.object({ agent_id: Id }).passthrough(),
      location: z.object({ directory: Id }).passthrough(), model: z.object({ providerID: Id, id: Id }).passthrough().nullable().optional(),
    }).passthrough() }).passthrough().safeParse(response.body);
    requireFact(session.success && session.data.data.id === input.nativeSessionId && session.data.data.metadata.agent_id === row.agent_id &&
      session.data.data.location.directory === row.worktree_path, 'identity-mismatch', 'Foreign native session or location');
    if (input.view === 'presence') output = { ...base, view: 'presence', present: true, nativeStatus: 200, nativeErrorName: null, nativeErrorSessionId: null };
    else if (input.view === 'session') output = { ...base, view: 'session', directory: session.data.data.location.directory,
      metadataAgentId: session.data.data.metadata.agent_id,
      selectedModel: session.data.data.model ? { provider: session.data.data.model.providerID, model: session.data.data.model.id } : null };
    else {
      const params = new URLSearchParams({ order: 'asc', limit: String(input.maxMessages) });
      const messages = await access.read(`/api/session/${encodeURIComponent(input.nativeSessionId)}/message?${params}`, signal);
      requireFact(messages.status === 200, 'observation-failed', 'Native messages are unreadable');
      const parsed = z.object({ data: z.array(Message) }).passthrough().safeParse(messages.body);
      requireFact(parsed.success && parsed.data.data.length <= input.maxMessages, 'observation-failed', 'Native messages are malformed or over bound');
      const records = parsed.data.data;
      requireFact(new Set(records.map(message => message.id)).size === records.length &&
        records.every(message => message.sessionID === input.nativeSessionId), 'identity-mismatch', 'Native messages are duplicated or foreign');
      // The pinned native endpoint does not expose a continuation receipt. A
      // full page is explicitly incomplete; it cannot prove exactness/absence.
      const complete = records.length < input.maxMessages;
      if (probe) requireFact(records.every(message => message.type !== 'assistant' || Array.isArray(message.content)),
        'observation-failed', 'Native tool content is missing');
      if (input.view === 'inputs') output = { ...base, view: 'inputs', complete, records: records.filter(message => message.type === 'user').map(message => ({
        id: message.id, sessionId: message.sessionID,
        text: (message.content ?? []).flatMap(part => part && typeof part === 'object' && !Array.isArray(part) && part.type === 'text' && typeof part.text === 'string' ? [part.text] : []).join(''),
        createdAt: message.time.created ?? null,
      })) };
      else if (input.view === 'completed-models') output = { ...base, view: 'completed-models', complete,
        records: records.filter(message => message.type === 'assistant' && message.time.completed != null && message.finish && !message.error).map(message => {
          requireFact(message.model, 'observation-failed', 'Completed native model is missing');
          return { id: message.id, sessionId: message.sessionID, completedAt: message.time.completed!, provider: message.model.providerID, model: message.model.id };
        }) };
      else if (input.view === 'usage') output = { ...base, view: 'usage', complete,
        records: records.filter(message => message.type === 'assistant' && message.time.completed != null && message.tokens).map(message => ({
          id: message.id, sessionId: message.sessionID, inputTokens: message.tokens!.input ?? 0,
          outputTokens: (message.tokens!.output ?? 0) + (message.tokens!.reasoning ?? 0),
          cacheReadTokens: message.tokens!.cache?.read ?? 0, cacheWriteTokens: message.tokens!.cache?.write ?? 0, costUsd: message.cost ?? 0,
        })) };
      else output = { ...base, view: 'tools', complete, records: records.flatMap(message => (message.content ?? []).flatMap(part => {
        if (!part || typeof part !== 'object' || Array.isArray(part) || part.type !== 'tool') return [];
        const tool = z.object({ id: Id, name: Id, state: z.object({ status: Id, input: Json, content: Json.optional() }).passthrough() }).passthrough().parse(part);
        if (probe && tool.state.status === 'completed') requireFact(tool.state.content !== undefined,
          'observation-failed', 'Completed native tool output is missing');
        return [{ id: `${message.id}/tool/${tool.id}`, sessionId: message.sessionID, itemId: message.id, name: tool.name,
          messageType: message.type, state: tool.state.status, input: tool.state.input, output: tool.state.content ?? null, outputPresent: tool.state.content !== undefined,
          inputRedaction: redactionFacts(tool.state.input, secrets), outputRedaction: redactionFacts(tool.state.content ?? null, secrets),
          probe: probe ? { handle: probe.handle, inputOccurrences: probeOccurrences(JSON.stringify(tool.state.input || {}), probe),
            outputOccurrences: probeOccurrences(JSON.stringify(tool.state.content || {}), probe) } : null }];
      })) };
    }
  }
  if (output.view === 'tools') requireFact(new Set(output.records.map(record => record.id)).size === output.records.length,
    'identity-mismatch', 'Native tool IDs are duplicated');
  const after = await verifyNativeService(access, input.expectedGeneration, signal);
  requireFact(await sha256(JSON.stringify(before)) === await sha256(JSON.stringify(after)), 'identity-mismatch', 'Native registration changed during observation');
  const afterRow = AgentRow.parse(await access.agent(input.agent.agentId));
  requireFact(afterRow.agent_id === row.agent_id && afterRow.workspace_id === row.workspace_id && afterRow.repo === row.repo &&
    afterRow.worktree_path === row.worktree_path && afterRow.branch === row.branch && afterRow.parent_agent_id === row.parent_agent_id &&
    afterRow.root_agent_id === row.root_agent_id && afterRow.created_by_kind === row.created_by_kind && afterRow.created_by_id === row.created_by_id &&
    afterRow.revision === row.revision && afterRow.harness_session_id === row.harness_session_id && afterRow.harness_session_root === row.harness_session_root, 'identity-mismatch', 'Native agent changed during observation');
  return NativeOutput.parse(output);
}
