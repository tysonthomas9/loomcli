import { z } from 'zod';
import { AgentRef, Digest, Id, RelativePath, requireFact, sha256, type ReadTransport } from './protocol.js';

export const FilesInput = z.object({ agent: AgentRef, path: RelativePath,
  view: z.enum(['content', 'stat']), maxBytes: z.number().int().min(1).max(4 * 1024 * 1024),
}).strict();
const FileIdentity = { path: RelativePath, size: z.number().int().nonnegative(), version: Id };
export const FilesOutput = z.discriminatedUnion('view', [
  z.object({ ...FileIdentity, view: z.literal('content'), content: z.string().nullable(),
    binary: z.boolean(), truncated: z.literal(false), sha256: Digest.nullable() }).strict(),
  z.object({ ...FileIdentity, view: z.literal('stat'), isDirectory: z.boolean(), modifiedAt: Id }).strict(),
]);
export async function observeFiles(input: z.infer<typeof FilesInput>, repo: string, read: ReadTransport, signal: AbortSignal): Promise<z.infer<typeof FilesOutput>> {
  const query = new URLSearchParams({ scope: 'agent', target: input.agent.agentId, repo: repo.split('/').at(-1)!, path: input.path });
  const suffix = input.view === 'stat' ? '/stat' : '';
  const result = await read(`/api/workspaces/${encodeURIComponent(input.agent.workspaceId)}/files${suffix}?${query}`, signal);
  requireFact(result.status === 200, 'observation-failed', 'Owned Files read is unavailable');
  const base = z.object({ path: Id, size: z.number().int().nonnegative(), version: Id }).passthrough();
  const parsed = base.safeParse(result.body);
  requireFact(parsed.success && parsed.data.path === input.path, 'identity-mismatch', 'Files returned a foreign or malformed path');
  if (input.view === 'stat') {
    const stat = base.extend({ is_dir: z.boolean(), mod_time: Id }).parse(result.body);
    return { view: 'stat', path: input.path, size: stat.size, version: stat.version,
      isDirectory: stat.is_dir, modifiedAt: stat.mod_time };
  }
  const full = base.extend({ content: z.string().nullable().optional(), binary: z.boolean(), truncated: z.boolean() }).parse(result.body);
  requireFact(!full.truncated && (full.content == null || Buffer.byteLength(full.content) <= input.maxBytes),
    'incomplete-pages', 'Files response is incomplete or over bound');
  requireFact(full.binary || typeof full.content === 'string', 'observation-failed', 'Files text content is missing');
  const content = full.content ?? null;
  const hash = content !== null && !full.binary ? await sha256(content) : null;
  if (hash !== null) requireFact(full.size === Buffer.byteLength(content!) && full.version === `sha256:${hash}`,
    'identity-mismatch', 'Files bytes, size and strong version disagree');
  return { view: 'content', path: input.path, content, size: full.size, version: full.version,
    binary: full.binary, truncated: false, sha256: hash };
}
