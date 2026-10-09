import { z } from 'zod';
import { AgentRef, Digest, Id, RelativePath, requireFact, sha256, type ReadTransport } from './protocol.js';

export const FilesInput = z.object({ agent: AgentRef, path: RelativePath,
  view: z.enum(['content', 'stat']), maxBytes: z.number().int().min(1).max(4 * 1024 * 1024),
}).strict();
export const FilesOutput = z.object({ path: RelativePath, content: z.string().nullable(), size: z.number().int().nonnegative(),
  binary: z.boolean(), truncated: z.boolean(), version: Id, sha256: Digest.nullable(),
}).strict();
export async function observeFiles(input: z.infer<typeof FilesInput>, repo: string, read: ReadTransport, signal: AbortSignal): Promise<z.infer<typeof FilesOutput>> {
  const query = new URLSearchParams({ scope: 'agent', target: input.agent.agentId, repo: repo.split('/').at(-1)!, path: input.path });
  const suffix = input.view === 'stat' ? '/stat' : '';
  const result = await read(`/api/workspaces/${encodeURIComponent(input.agent.workspaceId)}/files${suffix}?${query}`, signal);
  requireFact(result.status === 200, 'observation-failed', 'Owned Files read is unavailable');
  const base = z.object({ path: Id, size: z.number().int().nonnegative(), version: Id }).passthrough();
  const parsed = base.safeParse(result.body);
  requireFact(parsed.success && parsed.data.path === input.path, 'identity-mismatch', 'Files returned a foreign or malformed path');
  const full = input.view === 'content' ? base.extend({ content: z.string(), binary: z.boolean(), truncated: z.boolean() }).parse(result.body) : null;
  const content = full?.content ?? null;
  const binary = full?.binary ?? false;
  const truncated = full?.truncated ?? false;
  requireFact(!truncated && (content === null || Buffer.byteLength(content) <= input.maxBytes), 'incomplete-pages', 'Files response is incomplete or over bound');
  const hash = content !== null && !binary ? await sha256(content) : null;
  if (hash !== null) requireFact(parsed.data.size === Buffer.byteLength(content!) && parsed.data.version === `sha256:${hash}`,
    'identity-mismatch', 'Files bytes, size and strong version disagree');
  return { path: input.path, content, size: parsed.data.size, version: parsed.data.version, binary, truncated, sha256: hash };
}
