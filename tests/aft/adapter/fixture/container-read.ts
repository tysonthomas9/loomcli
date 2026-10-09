import { execFile } from 'node:child_process';
import { lstat, readFile, writeFile, unlink, realpath } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { z } from 'zod';
import { createNativeHostAccess } from '../native-host.js';
import { ContainerObservationRequest, readContainerObservation } from '../container-observations.js';
import { Id, Json, requireFact } from '../protocol.js';
import { readHttp } from './host.js';

// This is a fixed internal wire protocol. Its CLI is installed by the attested
// adapter build; neither executable paths nor SQL/JS/commands are accepted.
export const ContainerReadRequest = z.discriminatedUnion('operation', [
  ...ContainerObservationRequest.options,
  z.object({ operation: z.literal('fixture-http'), method: z.enum(['GET', 'POST', 'PATCH', 'DELETE']),
    relativePath: z.string().regex(/^\/__(script|reset|requests)(\?|$)/), body: Json }).strict(),
  z.object({ operation: z.literal('configuration-read'), target: z.enum(['opencode', 'emu-scenarios']) }).strict(),
  z.object({ operation: z.literal('configuration-write'), target: z.enum(['opencode', 'emu-scenarios']), bytes: z.string().max(65536).nullable() }).strict(),
  z.object({ operation: z.literal('agent'), agentId: Id }).strict(),
  z.object({ operation: z.literal('sessions'), agentId: Id }).strict(),
  z.object({ operation: z.literal('registration') }).strict(),
  z.object({ operation: z.literal('process') }).strict(),
  z.object({ operation: z.literal('read'), route: Id }).strict(),
  z.object({ operation: z.literal('git-common-dir'), agentId: Id }).strict(),
]);
export type ContainerRead = z.infer<typeof ContainerReadRequest>;
export async function readContainer(input: unknown) {
  const request = ContainerReadRequest.parse(input);
  const native = createNativeHostAccess({ configRoot: '/root/.loom', workspaceId: 'LOCALMODE',
    repo: '/root/.loom/workspaces/LOCALMODE/source-repo', pinnedExecutable: await realpath(process.env.LOOM_OPENCODE_BIN ?? '/usr/local/bin/opencode') });
  switch (request.operation) {
    case 'fixture-http': return readHttp('http://127.0.0.1:4010', request.method, request.relativePath, request.body, AbortSignal.timeout(15000));
    case 'configuration-read': case 'configuration-write': {
      const filename = request.target === 'opencode' ? '/root/.loom/agents-opencode/config/opencode/opencode.json' : '/root/.loom/agents-opencode/emu-scenarios.json';
      const parent = path.dirname(filename);
      requireFact(await realpath(parent) === parent, 'ownership-mismatch', 'Configuration parent is not owned');
      let exists = false;
      try { const stat = await lstat(filename); requireFact(stat.isFile() && !stat.isSymbolicLink() && stat.size <= 65536,
        'ownership-mismatch', 'Configuration file is unsafe'); exists = true; }
      catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error; }
      if (request.operation === 'configuration-read') return { bytes: exists ? await readFile(filename, 'utf8') : null, complete: true };
      if (request.bytes === null) { if (exists) await unlink(filename); }
      else await writeFile(filename, request.bytes, { mode: 0o600, flag: exists ? 'w' : 'wx' });
      return { complete: true };
    }
    case 'git-observe': case 'filesystem-root': case 'filesystem-observe':
      return readContainerObservation(request, native, { workspaceId: 'LOCALMODE', repo: '/root/.loom/workspaces/LOCALMODE/source-repo',
        worktreeParent: '/root/.loom/worktrees/source-repo', commonDir: '/root/.loom/workspaces/LOCALMODE/source-repo/.git' });
    case 'agent': return native.agent(request.agentId);
    case 'sessions': return native.sessions(request.agentId);
    case 'registration': return native.registration();
    case 'process': return native.process();
    case 'read': return native.read(request.route, AbortSignal.timeout(15000));
    case 'git-common-dir': {
      const row = await native.agent(request.agentId);
      const worktree = await realpath(row.worktree_path);
      requireFact(worktree === `/root/.loom/worktrees/source-repo/${row.agent_id}`, 'ownership-mismatch', 'Worktree is outside fixture');
      const output = await new Promise<string>((resolve, reject) => execFile('git', ['rev-parse', '--git-common-dir'], {
        cwd: worktree, env: { PATH: '/usr/local/bin:/usr/bin:/bin' }, encoding: 'utf8', maxBuffer: 1024 * 1024,
      }, (error, stdout) => error ? reject(new Error('Owned Git read failed')) : resolve(stdout.trim())));
      const commonDir = await realpath(path.resolve(worktree, output));
      requireFact(commonDir === '/root/.loom/workspaces/LOCALMODE/source-repo/.git', 'ownership-mismatch', 'Git common directory is outside fixture');
      return { commonDir };
    }
  }
}
// Importing this module never dispatches a request or launches a process.
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { process.stdout.write(JSON.stringify(await readContainer(JSON.parse(process.argv[2] ?? 'null')))); }
  catch { process.stderr.write('Owned container read failed\n'); process.exitCode = 1; }
}
