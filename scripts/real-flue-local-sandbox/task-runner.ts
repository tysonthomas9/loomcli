import fs from 'node:fs';
import path from 'node:path';
import { local } from '@flue/runtime/node';

type TaskRunRequest = {
  lease_token?: string;
  provider_profile?: string;
  task_id?: string;
  task_run_id?: string;
  sandbox_placement?: {
    provider?: string;
  };
  [key: string]: unknown;
};

type ExecResult = {
  exitCode?: number;
  stdout: string;
};

const request = JSON.parse(process.env.LOOM_TASK_RUN_REQUEST_JSON || '{}') as TaskRunRequest;
const logPath = process.argv[2];

if (!logPath) {
  console.error('usage: task-runner.ts <task-runner-log-path>');
  process.exit(2);
}

if (process.env.LOOM_TASK_RUN_LEASE_TOKEN !== request.lease_token) {
  console.error('task-run lease token did not reach task runner process');
  process.exit(3);
}

if (request.provider_profile !== 'flue-local') {
  console.error('unexpected provider profile ' + request.provider_profile);
  process.exit(4);
}

const worktreePath = process.env.LOOM_WORKTREE_PATH || process.cwd();
const safeTaskId = String(request.task_id || 'task').replace(/[^A-Za-z0-9_.-]/g, '_');
const sandboxCwd = path.join(worktreePath, '.loom', 'task-runner-sandboxes', safeTaskId);
fs.mkdirSync(sandboxCwd, { recursive: true });

const sandbox = local({ cwd: sandboxCwd });
const env = await sandbox.createSessionEnv({ id: request.task_run_id || safeTaskId });

const sandboxRequest = {
  ...request,
  lease_token: request.lease_token ? '[redacted]' : '',
  lease_token_received_by_host_runner: String(Boolean(request.lease_token)),
};
await env.writeFile('task-request.json', JSON.stringify(sandboxRequest, null, 2) + '\n');

const pwd = (await env.exec('pwd')) as ExecResult;
await env.exec('printf "%s\n" LOCAL_SANDBOX_TASK_RUNNER_OK > agent-output.txt');

const marker = (await env.readFile('agent-output.txt')).trim();
const leaseProbe = (await env.exec('printf "%s" "$LOOM_TASK_RUN_LEASE_TOKEN"')) as ExecResult;

if (pwd.exitCode !== 0 || marker !== 'LOCAL_SANDBOX_TASK_RUNNER_OK') {
  console.error('Flue local sandbox filesystem/shell verification failed');
  process.exit(5);
}

if (leaseProbe.stdout.trim() !== '') {
  console.error('task-run lease token leaked into the Flue local sandbox env');
  process.exit(6);
}

fs.appendFileSync(logPath, String(request.task_id) + '\n');
console.log(
  JSON.stringify({
    status: 'completed',
    exitCode: 0,
    logsRef: 'logs://' + request.task_run_id,
    runtime_metadata: {
      task_runner: 'flue-local-sandbox',
      sandbox_provider: request.sandbox_placement?.provider || '',
      sandbox_cwd: sandboxCwd,
      sandbox_pwd: pwd.stdout.trim(),
      lease_token_visible_in_sandbox: 'false',
    },
  }),
);
