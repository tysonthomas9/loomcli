import { spawn, type ChildProcess } from 'node:child_process';
import path from 'node:path';
import type { ProcessRequest, ProcessRunner } from './production.js';
import { FixtureError } from './lifecycle.js';
import { NativeSessionLimits, nativeSessionGuard, type NativeSessionGuard } from './native-session-boundaries.js';

export interface NativeInspectionBinding {
  readonly connection: string;
  readonly project: string;
  readonly sourceRoots: readonly string[];
  readonly environment: Readonly<{ PATH: string; HOME: string }>;
  /** Code-owned pure canonical authority check, never a suite callback. */
  authorize(): void;
}
const failed = () => new FixtureError('observation-failed');
function supported(request: ProcessRequest, binding: NativeInspectionBinding) {
  if (!binding.sourceRoots.includes(request.cwd)) return false;
  const same = (values: readonly string[]) => JSON.stringify(request.args) === JSON.stringify(values);
  if (request.binary === 'git') return [
    ['rev-parse', 'HEAD'], ['rev-parse', 'HEAD^{tree}'], ['status', '--porcelain', '--untracked-files=no'], ['ls-files', '-z'],
  ].some(same);
  if (request.binary !== 'podman') return false;
  if (same(['system', 'connection', 'list', '--format', 'json'])) return true;
  if (request.args[0] !== '--connection' || request.args[1] !== binding.connection) return false;
  const args = request.args.slice(2), label = `label=com.docker.compose.project=${binding.project}`;
  if ([['info', '--format', 'json'], ['ps', '-a', '--filter', label, '--format', '{{.ID}}'],
    ['volume', 'ls', '--filter', label, '--format', '{{.Name}}'], ['network', 'ls', '--filter', label, '--format', '{{.ID}}'],
  ].some(values => JSON.stringify(args) === JSON.stringify(values))) return true;
  const inspect = args[0] === 'inspect' ? args : ['image', 'volume', 'network'].includes(args[0] ?? '') ? args.slice(1) : [];
  return inspect.length === 2 && inspect[0] === 'inspect' && /^[A-Za-z0-9._:-]{1,256}$/.test(inspect[1]!);
}

interface Intent {
  child?: ChildProcess;
  exited: boolean;
  exitObserved: boolean;
  close: Promise<void>;
  markClosed(): void;
  chunks: Buffer[];
  bytes: number;
  stderrBytes: number;
}
/** Scoped ONLY to fixed native verification inspection commands. No shell,
 * compose mutation, native query, service kill or generic exec request exists.
 * A child 'error' is not an observed exit: retain the exact handle until close.
 * This proves direct spawned-child lifetime, not detached descendant death. */
export function createRetainedNativeInspectionRunner(binding: NativeInspectionBinding,
  enrollCleanup: (cleanup: () => Promise<void>) => void,
  spawnChild: typeof spawn = spawn, guard: NativeSessionGuard = nativeSessionGuard):
  { run: ProcessRunner; dispose(): Promise<void> } {
  if (!binding.connection || !binding.project || !binding.environment.PATH || !binding.environment.HOME ||
    !binding.sourceRoots.length || binding.sourceRoots.length > 32 ||
    new Set(binding.sourceRoots).size !== binding.sourceRoots.length ||
    binding.sourceRoots.some(root => !path.isAbsolute(root) || path.normalize(root) !== root)) throw failed();
  const config = Object.freeze({ connection: binding.connection, project: binding.project,
    sourceRoots: Object.freeze([...binding.sourceRoots]), environment: Object.freeze({ ...binding.environment }),
    authorize: binding.authorize });
  const intents = new Set<Intent>();
  const lifetime = new AbortController();
  let disposalRequested = false, poisoned = false, active = false;
  let disposing: Promise<void> | undefined;
  const terminate = (intent: Intent) => {
    const child = intent.child;
    // Exit can precede stream-close. Never signal a reaped predecessor while
    // waiting for pipes, nor interpret an already-sent signal as actual exit.
    if (!child || intent.exitObserved || child.exitCode !== null || child.signalCode !== null || child.killed) return;
    try { child.kill(); } catch { /* Keep the handle; only close retires it. */ }
  };
  async function awaitBound<T>(work: Promise<T>, signal: AbortSignal): Promise<T> {
    // A synchronous spawn/cleanup callback can abort before this guard begins.
    // Observe rejection without replacing the retained original promise.
    void work.catch(() => undefined);
    signal.throwIfAborted(); let abort!: () => void;
    const cancelled = new Promise<never>((_yes, reject) => {
      abort = () => reject(failed()); signal.addEventListener('abort', abort, { once: true });
      if (signal.aborted) abort();
    });
    try { const value = await Promise.race([work, cancelled]); signal.throwIfAborted(); return value; }
    finally { signal.removeEventListener('abort', abort); }
  }
  async function dispose() {
    disposalRequested = true; lifetime.abort();
    const bounded = guard.begin(new AbortController().signal);
    try {
      while (disposing) {
        try { await awaitBound(disposing, bounded.signal); }
        catch { bounded.signal.throwIfAborted(); }
      }
      bounded.signal.throwIfAborted();
      const work = (async () => {
        for (const intent of intents) {
          bounded.signal.throwIfAborted();
          if (!intent.exited) {
            // Only this captured ChildProcess is addressed, never a PID lookup.
            // False/throw cannot establish exit; still require the close event.
            terminate(intent);
            await intent.close;
          }
          intent.chunks = []; intents.delete(intent);
        }
      })();
      disposing = work.finally(() => { disposing = undefined; });
      await awaitBound(disposing, bounded.signal);
    } finally { bounded.release(); }
  }
  enrollCleanup(dispose);
  const run: ProcessRunner = async request => {
    if (disposalRequested || poisoned || active || !request.signal || !supported(request, config)) throw failed();
    try { request.signal.throwIfAborted(); config.authorize(); } catch { throw failed(); }
    const bounded = guard.begin(AbortSignal.any([request.signal, lifetime.signal]));
    let close!: () => void;
    const intent: Intent = { exited: false, exitObserved: false, close: new Promise<void>(resolve => { close = resolve; }),
      markClosed: () => { intent.exited = true; close(); }, chunks: [], bytes: 0, stderrBytes: 0 };
    // The intent and cleanup exist before even a synchronous spawn attempt.
    intents.add(intent); active = true;
    let rejectResult!: (error: Error) => void;
    try {
      bounded.signal.throwIfAborted(); config.authorize();
      const output = new Promise<string>((resolve, reject) => {
        rejectResult = reject;
        try {
          intent.child = spawnChild(request.binary, [...request.args], { cwd: request.cwd,
            env: { ...config.environment, CONTAINER_CONNECTION: config.connection },
            signal: bounded.signal, shell: false, stdio: ['ignore', 'pipe', 'pipe'] });
        } catch { intent.markClosed(); reject(failed()); return; }
        const child = intent.child;
        child.once('exit', () => { intent.exitObserved = true; });
        child.once('close', code => {
          intent.markClosed();
          if (poisoned || code !== 0) { reject(failed()); return; }
          try {
            const value = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(Buffer.concat(intent.chunks));
            resolve(value);
          } catch { reject(failed()); }
        });
        const unhealthy = () => { poisoned = true; reject(failed()); };
        child.on('error', unhealthy);
        if (!child.stdout || !child.stderr) { unhealthy(); return; }
        child.stdout!.on('data', (chunk: Buffer) => {
          intent.bytes += chunk.length;
          if (intent.bytes > NativeSessionLimits.reply) { poisoned = true; reject(failed()); terminate(intent); }
          else if (!poisoned) intent.chunks.push(Buffer.from(chunk));
        });
        child.stderr!.on('data', (chunk: Buffer) => {
          // Count private bytes, never retain raw credential-bearing diagnostics.
          intent.stderrBytes += chunk.length;
          if (intent.stderrBytes > NativeSessionLimits.stderr) { poisoned = true; reject(failed()); terminate(intent); }
        });
        child.stdout.on('error', unhealthy); child.stderr.on('error', unhealthy);
      });
      const value = await awaitBound(output, bounded.signal);
      config.authorize(); bounded.signal.throwIfAborted();
      return value;
    } catch {
      poisoned = true; rejectResult?.(failed());
      // Node's signal branch may already have sent termination. A separate
      // attempt addresses the same handle and remains uncertain until close.
      if (!intent.exited) terminate(intent);
      throw failed();
    } finally {
      intent.chunks = [];
      if (intent.exited) intents.delete(intent);
      active = false; bounded.release();
    }
  };
  return { run, dispose };
}
