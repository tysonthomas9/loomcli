import { execFile } from 'node:child_process';
import { realpath } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { z } from 'zod';
import { createNativeHostAccess } from '../native-host.js';
import { Id, requireFact } from '../protocol.js';

// This is a fixed internal wire protocol. Its CLI is installed by the attested
// adapter build; neither executable paths nor SQL/JS/commands are accepted.
export const ContainerReadRequest = z.discriminatedUnion('operation', [
  z.object({ operation: z.literal('agent'), agentId: Id }).strict(),
  z.object({ operation: z.literal('sessions'), agentId: Id }).strict(),
  z.object({ operation: z.literal('registration') }).strict(),
  z.object({ operation: z.literal('process') }).strict(),
  z.object({ operation: z.literal('read'), route: Id }).strict(),
  z.object({ operation: z.literal('git-common-dir'), agentId: Id }).strict(),
]);
export async function readContainer(input: unknown) {
  const request = ContainerReadRequest.parse(input);
  const native = createNativeHostAccess({ configRoot: '/root/.loom', workspaceId: 'LOCALMODE',
    repo: '/root/.loom/workspaces/LOCALMODE/source-repo', pinnedExecutable: await realpath(process.env.LOOM_OPENCODE_BIN ?? '/usr/local/bin/opencode') });
  switch (request.operation) {
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
