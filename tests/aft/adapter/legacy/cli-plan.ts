import { isDeepStrictEqual } from 'node:util';
import { LegacyError, RoleInput, TaskInput, SeedInput, UsageInput, type LegacyLease, type CliCommand } from './operations.js';

// The concrete fixture hook is trusted code, but its argv/env port is wider
// than the public operations. Revalidate the fixed actor plan at that boundary.
export function checkedCliPlan(lease: LegacyLease, command: CliCommand) {
  const fail: () => never = () => { throw new LegacyError('invalid-input', 'CLI plan differs from a closed legacy operation'); };
  if (command.binary !== lease.binary || command.cwd !== lease.cwd || command.argv.some(arg => /[\0\r\n]/.test(arg))) fail();
  const argv = command.argv;
  let envOverrides: Record<string, string> = {}; let waitForExit = true;
  if (argv[0] === '--workspace' && argv[2] === 'role') {
    const input = RoleInput.safeParse({ leaseId: lease.id, workspaceId: argv[1], operation: argv[3], name: argv[3] === 'list' ? null : argv[4] });
    if (!input.success || !lease.workspaces.includes(input.data.workspaceId)) fail();
    const expected = ['--workspace', input.data.workspaceId, 'role', input.data.operation,
      ...(input.data.operation === 'show' ? [input.data.name] : []), '--json'];
    if (!isDeepStrictEqual(argv, expected) || (input.data.operation === 'show' && !lease.roles.some(role => role.workspaceId === input.data.workspaceId && role.name === input.data.name))) fail();
    envOverrides = {};
  } else if (argv[0] === 'usage') {
    const matches = lease.agents.filter(agent => agent.name === argv[4] && agent.workspaceId === command.env.LOOM_WORKSPACE_ID);
    if (matches.length !== 1) fail();
    const agent = matches[0]!;
    if (!UsageInput.safeParse({ agent: { fixtureLeaseId: lease.id, workspaceId: agent.workspaceId, agentId: agent.id } }).success ||
      !isDeepStrictEqual(argv, ['usage', '--format', 'json', '--agent', agent.name])) fail();
    envOverrides = { LOOM_WORKSPACE_ID: agent.workspaceId };
  } else if (argv[0] === '--workspace' && argv[2] === '--backend' && argv[4] === 'task') {
    const repo = command.env.LOOM_SOURCE_REPOS === '' ? null : lease.repos.find(row => row.workspaceId === argv[1] && row.sourcePath === command.env.LOOM_SOURCE_REPOS);
    if (repo === undefined) fail();
    const input = TaskInput.safeParse({ leaseId: lease.id, workspaceId: argv[1], backend: argv[3], agentName: argv[5],
      mode: argv.length === 6 ? 'once' : argv.length === 7 ? 'auto' : 'daemon',
      issueId: command.env.LOOM_ASSIGNED_TASK_ID === '' ? null : command.env.LOOM_ASSIGNED_TASK_ID, repoName: repo?.name ?? null });
    if (!input.success || !lease.agents.some(agent => agent.name === input.data.agentName && agent.workspaceId === input.data.workspaceId) ||
      (input.data.issueId !== null && !lease.issues.some(issue => issue.workspaceId === input.data.workspaceId && issue.id === input.data.issueId))) fail();
    const expected = ['--workspace', input.data.workspaceId, '--backend', input.data.backend, 'task', input.data.agentName,
      ...(input.data.mode === 'once' ? [] : ['--auto']), ...(input.data.mode === 'daemon' ? ['--daemon-mode'] : [])];
    if (!isDeepStrictEqual(argv, expected)) fail();
    envOverrides = { LOOM_WORKSPACE_ID: input.data.workspaceId, LOOM_ASSIGNED_TASK_ID: input.data.issueId ?? '', LOOM_SOURCE_REPOS: repo?.sourcePath ?? '' };
    waitForExit = false;
  } else if (argv[0] === 'daemon' && argv[1] === 'seed-worktree') {
    const input = SeedInput.safeParse({ leaseId: lease.id, workspaceId: argv[3], agentName: argv[5], relativePath: argv[7], content: command.stdin, commitMessage: argv[11] });
    if (!input.success || lease.evidence !== 'deterministic' || !lease.agents.some(agent => agent.workspaceId === input.data.workspaceId && agent.name === input.data.agentName)) fail();
    const expected = ['daemon', 'seed-worktree', '--workspace', input.data.workspaceId, '--agent', input.data.agentName,
      '--file', input.data.relativePath, '--content', '-', '--message', input.data.commitMessage];
    if (!isDeepStrictEqual(argv, expected)) fail();
    envOverrides = { LOOM_TESTSUPPORT: '1' };
  } else fail();
  if (!isDeepStrictEqual(command.env, { ...lease.env, ...envOverrides }) || (argv[1] !== 'seed-worktree' && command.stdin !== '')) fail();
  return { argv: [...argv], envOverrides, stdin: command.stdin, waitForExit };
}
