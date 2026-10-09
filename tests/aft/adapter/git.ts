import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { realpath } from 'node:fs/promises';
import { z } from 'zod';
import { AgentRef, Id, RelativePath, requireFact } from './protocol.js';

export const GitInput = z.object({ agent: AgentRef, view: z.enum(['status', 'refs', 'diff', 'origin']),
  paths: z.array(RelativePath).max(1000), maxBytes: z.number().int().min(1).max(4 * 1024 * 1024),
}).strict();
export const GitOutput = z.object({ head: z.string().regex(/^[a-f0-9]{40,64}$/), branch: Id,
  worktree: Id, commonDir: Id, status: z.array(z.object({ path: Id, status: Id, originalPath: Id.nullable() }).strict()),
  refs: z.array(z.object({ ref: Id, oid: z.string().regex(/^[a-f0-9]{40,64}$/) }).strict()),
  diff: z.string().nullable(), origin: z.object({ fetch: z.array(Id), push: z.array(Id), mirror: z.boolean(), pushRefspecs: z.array(Id) }).strict().nullable(),
}).strict();
export interface GitReader { read(args: readonly string[], cwd: string, maxBytes: number): Promise<{ code: number; stdout: string }> }
export const localGit: GitReader = {
  async read(args, cwd, maxBytes) {
    try {
      const result = await promisify(execFile)('git', ['-c', 'core.fsmonitor=false', '-c', 'core.hooksPath=/dev/null', ...args], { cwd, maxBuffer: maxBytes, encoding: 'utf8',
        env: { PATH: process.env.PATH, HOME: cwd, GIT_CONFIG_NOSYSTEM: '1', GIT_CONFIG_GLOBAL: '/dev/null', GIT_OPTIONAL_LOCKS: '0', GIT_TERMINAL_PROMPT: '0' } });
      return { code: 0, stdout: result.stdout };
    } catch (error) {
      const result = error as { code?: number; stdout?: string };
      if (typeof result.code !== 'number') throw new Error('Owned Git read failed');
      return { code: result.code, stdout: result.stdout ?? '' };
    }
  },
};
export async function observeGit(input: z.infer<typeof GitInput>, owned: { worktree: string; commonDir: string; branch: string },
  reader: GitReader = localGit): Promise<z.infer<typeof GitOutput>> {
  const read = async (args: string[], absent = false): Promise<string> => {
    const result = await reader.read(args, owned.worktree, input.maxBytes);
    requireFact(result.code === 0 || (absent && result.code === 1), 'observation-failed', 'Owned Git observation failed');
    requireFact(Buffer.byteLength(result.stdout) <= input.maxBytes, 'incomplete-pages', 'Git observation exceeds bound');
    return result.stdout;
  };
  const attest = async () => {
    const worktree = (await read(['rev-parse', '--show-toplevel'])).trim();
    const commonDir = await realpath((await read(['rev-parse', '--path-format=absolute', '--git-common-dir'])).trim());
    const branch = (await read(['branch', '--show-current'])).trim();
    const head = (await read(['rev-parse', 'HEAD'])).trim();
    requireFact(worktree === owned.worktree && await realpath(worktree) === worktree && commonDir === owned.commonDir && branch === owned.branch,
      'ownership-mismatch', 'Foreign Git worktree, branch or common directory');
    return { worktree, commonDir, branch, head };
  };
  const before = await attest();
  const status: z.infer<typeof GitOutput>['status'] = [];
  const fields = (await read(['status', '--porcelain=v1', '-z', '--untracked-files=all', '--', ...input.paths])).split('\0');
  for (let i = 0; i < fields.length; i++) {
    const field = fields[i]; if (!field) continue;
    requireFact(field.length >= 4 && field[2] === ' ', 'observation-failed', 'Malformed Git status');
    const state = field.slice(0, 2);
    const originalPath = /[RC]/.test(state) ? fields[++i] : null;
    requireFact(originalPath !== undefined, 'observation-failed', 'Missing renamed Git path');
    status.push({ path: field.slice(3), status: state, originalPath });
  }
  const refs = (await read(['for-each-ref', '--format=%(refname)%09%(objectname)', 'refs/heads', 'refs/remotes'])).trim().split('\n').filter(Boolean).map(line => {
    const [ref, oid] = line.split('\t'); return { ref: ref!, oid: oid! };
  });
  const diff = input.view === 'diff' ? await read(['diff', '--no-ext-diff', '--no-textconv', '--binary', 'HEAD', '--', ...input.paths]) : null;
  const origin = input.view === 'origin' ? {
    fetch: (await read(['config', '--get-all', 'remote.origin.url'], true)).trim().split('\n').filter(Boolean),
    push: (await read(['config', '--get-all', 'remote.origin.pushurl'], true)).trim().split('\n').filter(Boolean),
    mirror: (await read(['config', '--bool', '--get', 'remote.origin.mirror'], true)).trim() === 'true',
    pushRefspecs: (await read(['config', '--get-all', 'remote.origin.push'], true)).trim().split('\n').filter(Boolean),
  } : null;
  if (origin && !origin.push.length) origin.push = [...origin.fetch];
  requireFact(new Set(status.map(entry => entry.path)).size === status.length && new Set(refs.map(entry => entry.ref)).size === refs.length,
    'identity-mismatch', 'Duplicate Git observation identity');
  const after = await attest();
  requireFact(JSON.stringify(before) === JSON.stringify(after), 'identity-mismatch', 'Git identity changed during read');
  return GitOutput.parse({ ...before, status, refs, diff, origin });
}
