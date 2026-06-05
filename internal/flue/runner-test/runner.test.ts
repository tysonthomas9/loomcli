// @ts-nocheck — vitest transpiles (no tsc); the @daytona/sdk import is aliased
// to ./stubs/daytona-sdk so setSandbox() and Daytona.create() share state.
import { describe, it, expect, beforeEach } from 'vitest';
import { execSync } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { setSandbox } from '@daytona/sdk';
import { run, selectSyncStrategy } from '../template/.flue/workflows/runner.ts';

const git = (cwd, args) => execSync(`git ${args}`, { cwd, encoding: 'utf8' });

// A sandbox whose executeCommand runs the command on the host (real git),
// matching the { result, exitCode } contract sh()/shTry() expect.
function realSandbox(id) {
  return {
    id,
    deleted: false,
    process: {
      async executeCommand(cmd) {
        try {
          const out = execSync(cmd, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] });
          return { result: out, exitCode: 0 };
        } catch (e) {
          return { result: `${e.stdout ?? ''}${e.stderr ?? ''}`, exitCode: e.status ?? 1 };
        }
      },
    },
    async delete() {
      this.deleted = true;
    },
  };
}

describe('selectSyncStrategy', () => {
  it('defaults to patch-back, resolves known names, throws on unknown', () => {
    expect(selectSyncStrategy({ sync_strategy: undefined })).toBeTruthy(); // patch-back
    const epic = selectSyncStrategy({ sync_strategy: 'epic-branch' });
    expect(epic.hydrateRef?.({ epic_branch: 'loom/e' })).toBe('loom/e');
    expect(() => selectSyncStrategy({ sync_strategy: 'nope' })).toThrow(/unknown sync_strategy/);
  });
});

describe('run() daytona-task epic-branch — executes the real flow against local git', () => {
  let bare, cwd;

  beforeEach(() => {
    const root = mkdtempSync(path.join(tmpdir(), 'runner-test-'));
    bare = path.join(root, 'remote.git');
    cwd = path.join(root, 'project'); // sandbox_cwd; the clone creates it
    const seed = path.join(root, 'seed');
    execSync(`git init -q --bare ${bare}`);
    execSync(`git clone -q ${bare} ${seed}`);
    const id = '-c user.name=t -c user.email=t@e';
    git(seed, 'checkout -q -b master');
    writeFileSync(path.join(seed, 'README.md'), 'hi\n');
    git(seed, `${id} add -A`);
    git(seed, `${id} commit -q -m base`);
    git(seed, 'push -q origin master');
    git(seed, 'push -q origin master:refs/heads/loom/epic-test'); // the shared epic branch
  });

  // init returns a harness whose session.prompt creates the agent's file AND
  // returns a THENABLE WITHOUT .finally — exactly what flue returns, and exactly
  // what broke the runner when it chained `.prompt(...).finally(...)` directly.
  const initThatWorks = () => async (_agent) => ({
    session: async () => ({
      prompt: (_p) => {
        writeFileSync(path.join(cwd, 'EPIC_FILE.md'), 'agent work\n');
        const value = { usage: { input: 10, output: 5 } };
        return { then: (onF, onR) => Promise.resolve(value).then(onF, onR) }; // no .finally
      },
    }),
  });

  it('commits the agent work onto the epic branch and tolerates the no-.finally prompt', async () => {
    const sandbox = realSandbox('sb-test');
    setSandbox(sandbox);

    const result = await run({
      init: initThatWorks(),
      payload: {
        sandbox: 'daytona-task',
        prompt: 'do the work',
        repo_remote_url: bare,
        sync_strategy: 'epic-branch',
        epic_branch: 'loom/epic-test',
        sandbox_cwd: cwd,
        task_id: 'T-1',
      },
      env: { DAYTONA_API_KEY: 'test' },
    });

    expect(result.status).toBe('completed');
    expect(result.files_changed).toBe(1);
    expect(sandbox.deleted).toBe(true);

    // the commit landed on the shared epic branch in the remote
    const verify = path.join(path.dirname(bare), 'verify');
    execSync(`git clone -q ${bare} ${verify}`);
    expect(git(verify, 'ls-tree --name-only origin/loom/epic-test')).toContain('EPIC_FILE.md');
    expect(git(verify, 'rev-list --count origin/master..origin/loom/epic-test').trim()).toBe('1');
  });
});
