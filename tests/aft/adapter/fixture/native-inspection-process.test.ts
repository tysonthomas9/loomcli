import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import type { spawn, ChildProcess } from 'node:child_process';
import { createRetainedNativeInspectionRunner } from './native-inspection-process.js';
import { NativeSessionLimits, type NativeSessionGuard } from './native-session-boundaries.js';
import type { ProcessRequest } from './production.js';

class Child extends EventEmitter {
  stdout: PassThrough | null = new PassThrough();
  stderr: PassThrough | null = new PassThrough();
  exitCode: number | null = null;
  signalCode: string | null = null;
  killed = false;
  pid = 77;
  kills = 0;
  killResult = true;
  killThrows = false;
  kill() {
    this.kills++;
    if (this.killThrows) throw new Error('private termination error');
    this.killed = this.killResult; return this.killResult;
  }
  exit(code = 0) { this.exitCode = code; this.emit('exit', code, null); }
  close(code = 0) { this.exit(code); this.emit('close', code, null); }
}
function harness(options: { throwSpawn?: boolean; authorize?: () => void; onSpawn?: () => void } = {}) {
  const children: Child[] = [], calls: Parameters<typeof spawn>[] = [], timers: AbortController[] = [];
  let cleanup!: () => Promise<void>;
  const guard: NativeSessionGuard = { begin(signal) {
    const timer = new AbortController(); timers.push(timer);
    return { signal: AbortSignal.any([signal, timer.signal]), release() {} };
  } };
  const spawnChild = ((...args: Parameters<typeof spawn>) => {
    assert.equal(typeof cleanup, 'function'); calls.push(args);
    options.onSpawn?.();
    if (options.throwSpawn) throw new Error('secret spawn error');
    const child = new Child(); children.push(child); return child as unknown as ChildProcess;
  }) as typeof spawn;
  const binding = { connection: 'owned-connection', project: 'owned-project', sourceRoots: ['/owned/source'],
    environment: { PATH: '/owned/bin', HOME: '/owned/home' }, authorize: options.authorize ?? (() => {}) };
  const owner = createRetainedNativeInspectionRunner(binding, callback => { cleanup = callback; }, spawnChild, guard);
  const request = (overrides: Partial<ProcessRequest> = {}): ProcessRequest => ({ binary: 'git',
    args: ['rev-parse', 'HEAD'], cwd: '/owned/source', env: { PASSWORD: 'must-not-inherit' },
    signal: new AbortController().signal, ...overrides });
  return { owner, children, calls, timers, request, cleanup: () => cleanup(), binding };
}

test('fixed commands enroll cleanup before spawn and return exact bytes with private fixed environment', async () => {
  let checks = 0; const h = harness({ authorize: () => { checks++; } });
  for (const args of [['rev-parse', 'HEAD'], ['rev-parse', 'HEAD^{tree}'],
    ['status', '--porcelain', '--untracked-files=no'], ['ls-files', '-z']]) {
    const result = h.owner.run(h.request({ args }));
    const child = h.children.at(-1)!; child.stdout!.write('\uFEFFactual😀\uFFFD\0');
    child.stderr!.write('private ghp_token'); child.close();
    assert.equal(await result, '\uFEFFactual😀\uFFFD\0');
  }
  assert.equal(checks, 12);
  assert.deepEqual(h.calls[0]?.[2], { cwd: '/owned/source',
    env: { PATH: '/owned/bin', HOME: '/owned/home', CONTAINER_CONNECTION: 'owned-connection' },
    signal: h.calls[0]?.[2]?.signal, shell: false, stdio: ['ignore', 'pipe', 'pipe'] });
  await h.cleanup(); assert.equal(h.children.reduce((n, child) => n + child.kills, 0), 0);
});

test('missing signal, authority, foreign roots and executable mutations deny before spawn', async () => {
  const h = harness();
  for (const change of [ { signal: undefined }, { cwd: '/foreign/source' }, { binary: 'bash' as const },
    { args: ['checkout', 'main'] }, { binary: 'podman' as const, args: ['--connection', 'foreign', 'info', '--format', 'json'] },
    { binary: 'podman' as const, args: ['--connection', 'owned-connection', 'stop', 'container'] },
    { binary: 'podman' as const, args: ['--connection', 'owned-connection', 'inspect', '../foreign'] },
    { binary: 'podman' as const, args: ['--connection', 'owned-connection', 'ps', '-a', '--filter',
      'label=com.docker.compose.project=foreign', '--format', '{{.ID}}'] } ]) {
    await assert.rejects(h.owner.run(h.request(change)));
  }
  const denied = harness({ authorize: () => { throw new Error('secret authorization detail'); } });
  await assert.rejects(denied.owner.run(denied.request()), error => !String(error).includes('secret'));
  assert.equal(h.calls.length + denied.calls.length, 0); await h.cleanup(); await denied.cleanup();
});

test('supported Podman identity inventory uses exact connection/project and no shell', async () => {
  const h = harness(), prefix = ['--connection', 'owned-connection'];
  for (const args of [['system', 'connection', 'list', '--format', 'json'], [...prefix, 'info', '--format', 'json'],
    [...prefix, 'ps', '-a', '--filter', 'label=com.docker.compose.project=owned-project', '--format', '{{.ID}}'],
    [...prefix, 'volume', 'ls', '--filter', 'label=com.docker.compose.project=owned-project', '--format', '{{.Name}}'],
    [...prefix, 'network', 'ls', '--filter', 'label=com.docker.compose.project=owned-project', '--format', '{{.ID}}'],
    [...prefix, 'inspect', 'actual-id'], [...prefix, 'image', 'inspect', 'sha256:abcd'],
    [...prefix, 'volume', 'inspect', 'actual-volume'], [...prefix, 'network', 'inspect', 'actual-network']]) {
    const result = h.owner.run(h.request({ binary: 'podman', args })); h.children.at(-1)!.close();
    assert.equal(await result, '');
  }
  assert.equal(h.calls.length, 9); await h.cleanup();
});

test('synchronous spawn throw retires proven-not-spawned intent without exposing output', async () => {
  const h = harness({ throwSpawn: true });
  await assert.rejects(h.owner.run(h.request()), error => !String(error).includes('secret'));
  await h.cleanup(); await h.cleanup(); assert.equal(h.children.length, 0); assert.equal(h.calls.length, 1);
});

test('synchronous spawn abort observes raw rejection before later cleanup', async () => {
  const signal = new AbortController();
  const h = harness({ throwSpawn: true, onSpawn: () => signal.abort() });
  await assert.rejects(h.owner.run(h.request({ signal: signal.signal })));
  await new Promise<void>(resolve => setImmediate(resolve)); await h.cleanup();
  assert.equal(h.calls.length, 1); assert.equal(h.children.length, 0);
});

test('async child error retains exact handle, uncertain cleanup timeout and retry require actual close', async () => {
  const h = harness(); const denied = assert.rejects(h.owner.run(h.request()));
  const child = h.children[0]!; child.killResult = false;
  child.emit('error', new Error('secret asynchronous launch error')); await denied;
  const cleanup = assert.rejects(h.cleanup()); h.timers.at(-1)!.abort(); await cleanup;
  assert.equal(child.kills, 2);
  const retry = h.cleanup(); child.close(1); await retry; await h.cleanup();
  assert.equal(child.kills, 2); await assert.rejects(h.owner.run(h.request()));
});

test('caller abort and fresh disposal signal retain exact child despite failed termination', async () => {
  const h = harness(), signal = new AbortController();
  const denied = assert.rejects(h.owner.run(h.request({ signal: signal.signal })));
  const child = h.children[0]!; child.killThrows = true; signal.abort(); await denied;
  const closing = h.cleanup(); assert.equal(child.kills, 2); child.close(1); await closing;
  await h.cleanup(); assert.equal(child.kills, 2);
});

test('exit before stream-close never signals predecessor while disposal still waits for close', async () => {
  const h = harness(), result = h.owner.run(h.request()); const denied = assert.rejects(result);
  const child = h.children[0]!; child.exit(); const closing = h.cleanup();
  await denied; assert.equal(child.kills, 0);
  child.emit('close', 0, null); await closing; assert.equal(child.kills, 0);
});

test('concurrent disposal joins one retained close; in-flight command excludes another launch', async () => {
  const h = harness(), denied = assert.rejects(h.owner.run(h.request()));
  await assert.rejects(h.owner.run(h.request())); assert.equal(h.calls.length, 1);
  const first = h.cleanup(), second = h.cleanup(); const child = h.children[0]!;
  await denied; assert.equal(child.kills, 1); child.close(1); await Promise.all([first, second]);
});

test('success after grant revocation is denied, with no data or additional child', async () => {
  let authorized = true;
  const h = harness({ authorize: () => { if (!authorized) throw new Error('revoked'); } });
  const denied = assert.rejects(h.owner.run(h.request())); authorized = false;
  h.children[0]!.stdout!.write('must-not-expose'); h.children[0]!.close(); await denied;
  await h.cleanup(); assert.equal(h.calls.length, 1);
});

test('stdout and stderr exact bounds succeed; overflow retains child and never reports empty diagnostics', async () => {
  const exact = harness(); const result = exact.owner.run(exact.request());
  exact.children[0]!.stdout!.write(Buffer.alloc(NativeSessionLimits.reply, 'x'));
  exact.children[0]!.stderr!.write(Buffer.alloc(NativeSessionLimits.stderr, 's')); exact.children[0]!.close();
  assert.equal((await result).length, NativeSessionLimits.reply); await exact.cleanup();
  for (const stream of ['stdout', 'stderr'] as const) {
    const h = harness(), denied = assert.rejects(h.owner.run(h.request())); const child = h.children[0]!;
    child[stream]!.write(Buffer.alloc(NativeSessionLimits[stream === 'stdout' ? 'reply' : 'stderr'] + 1));
    await denied; assert.equal(child.kills, 1); const closing = h.cleanup(); child.close(1); await closing;
    await assert.rejects(h.owner.run(h.request()));
  }
});

test('stream errors remain incomplete after zero close; malformed UTF8 and nonzero exit deny', async () => {
  for (const failure of ['stdout', 'stderr', 'nonzero', 'utf8', 'missing-pipe'] as const) {
    const h = harness(); if (failure === 'missing-pipe') {
      // Injectable spawn returns an actual handle even though pipe setup is incomplete.
      const child = new Child(); child.stdout = null;
      const guard: NativeSessionGuard = { begin(signal) { return { signal, release() {} }; } };
      const owner = createRetainedNativeInspectionRunner(h.binding, () => {}, (() => child) as unknown as typeof spawn, guard);
      const denied = assert.rejects(owner.run(h.request())); await denied;
      const closing = owner.dispose(); child.close(); await closing; continue;
    }
    const denied = assert.rejects(h.owner.run(h.request())), child = h.children[0]!;
    if (failure === 'stdout' || failure === 'stderr') child[failure]!.emit('error', new Error('private stream failure'));
    if (failure === 'utf8') child.stdout!.write(Buffer.from([0xf0, 0x90, 0x80]));
    child.close(failure === 'nonzero' ? 9 : 0); await denied; await h.cleanup();
  }
});

test('inspection guard timeout poisons new work; cleanup independently waits for exact child close', async () => {
  const h = harness(), denied = assert.rejects(h.owner.run(h.request())); h.timers[0]!.abort(); await denied;
  await assert.rejects(h.owner.run(h.request())); const closing = h.cleanup();
  assert.equal(h.timers.at(-1)!.signal.aborted, false); h.children[0]!.close(1); await closing;
});
