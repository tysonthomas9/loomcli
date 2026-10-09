import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { createServer } from 'node:net';
import { FixtureError } from './lifecycle.js';

export interface HostCommand {
  executable: string; argv: string[]; cwd: string; env: Record<string, string>;
}
export interface CliCompletion { exitCode: number | null; stdout: string; stderr: string; complete: boolean }
export interface OwnedCliProcess extends OwnedProcess { completion(signal: AbortSignal): Promise<CliCompletion> }
export interface OwnedProcess {
  pid: number; generation: string; executable: string; argv: readonly string[];
  state(): 'running' | 'exited';
  ready(signal: AbortSignal): Promise<void>;
  stop(): Promise<void>;
}
export interface HostProcesses {
  run(command: HostCommand, signal?: AbortSignal): Promise<string>;
  start(command: HostCommand, readinessText: string, generation?: string): OwnedProcess;
  launch?(command: HostCommand, stdin: string, generation: string): OwnedCliProcess;
}
export interface PortReservation { port: number; release(): Promise<void> }
export async function reservePort(): Promise<PortReservation> {
  const server = createServer();
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const address = server.address();
  if (!address || typeof address === 'string') { server.close(); throw new FixtureError('observation-failed'); }
  return { port: address.port, release: () => new Promise<void>((resolve, reject) => server.close(error => error ? reject(new FixtureError('observation-failed')) : resolve())) };
}
// A process group's identity is established only by this ChildProcess handle.
// Never look up or kill a process by port, basename, argv text or a saved PID.
export const nodeProcesses: HostProcesses = {
  run(command, signal) {
    return new Promise((resolve, reject) => {
      const child = spawn(command.executable, command.argv, { cwd: command.cwd, env: command.env,
        signal, shell: false, stdio: ['ignore', 'pipe', 'ignore'] });
      const chunks: Buffer[] = []; let bytes = 0;
      child.stdout.on('data', (chunk: Buffer) => { bytes += chunk.length; if (bytes > 4 * 1024 * 1024) child.kill(); else chunks.push(chunk); });
      child.on('error', () => reject(new FixtureError('observation-failed')));
      child.on('close', code => code === 0 && bytes <= 4 * 1024 * 1024 ? resolve(Buffer.concat(chunks).toString('utf8')) : reject(new FixtureError('observation-failed')));
    });
  },
  launch(command, stdin, generation) {
    const child = spawn(command.executable, command.argv, { cwd: command.cwd, env: command.env,
      shell: false, detached: true, stdio: ['pipe', 'pipe', 'ignore'] });
    let running = true; let bytes = 0; const chunks: Buffer[] = [];
    const spawned = new Promise<void>((resolve, reject) => { child.once('spawn', resolve); child.once('error', () => reject(new FixtureError('observation-failed'))); });
    void spawned.catch(() => undefined);
    const completed = new Promise<CliCompletion>((resolve, reject) => {
      child.stdout.on('data', (chunk: Buffer) => { bytes += chunk.length; if (bytes > 4 * 1024 * 1024) child.kill(); else chunks.push(chunk); });
      child.once('error', () => { running = false; reject(new FixtureError('observation-failed')); });
      child.once('close', code => { running = false;
        if (bytes > 4 * 1024 * 1024 || code === null) reject(new FixtureError('observation-failed'));
        else resolve({ exitCode: code, stdout: Buffer.concat(chunks).toString('utf8'), stderr: '', complete: true }); });
    });
    void completed.catch(() => undefined); child.stdin.on('error', () => undefined); child.stdin.end(stdin);
    return { pid: child.pid ?? 0, generation, executable: command.executable, argv: Object.freeze([...command.argv]), state: () => running ? 'running' : 'exited',
      async ready(signal) { signal.throwIfAborted(); await spawned; signal.throwIfAborted(); },
      async completion(signal) {
        signal.throwIfAborted(); let abort!: () => void;
        const cancelled = new Promise<never>((_resolve, reject) => { abort = () => reject(new FixtureError('observation-failed')); signal.addEventListener('abort', abort, { once: true }); });
        try { return await Promise.race([completed, cancelled]); } finally { signal.removeEventListener('abort', abort); }
      },
      async stop() {
        if (running && child.pid) {
          try { process.kill(-child.pid, 'SIGKILL'); } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw new FixtureError('observation-failed'); }
        }
        await completed.catch(() => undefined);
        if (child.pid) { try { process.kill(-child.pid, 0); throw new FixtureError('ownership-mismatch'); } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw new FixtureError('ownership-mismatch'); } }
      },
    };
  },
  start(command, readinessText, registeredGeneration) {
    const child = spawn(command.executable, command.argv, { cwd: command.cwd, env: command.env,
      shell: false, detached: true, stdio: ['ignore', 'pipe', 'pipe'] });
    const generation = registeredGeneration ?? randomUUID(); let running = true;
    let readyResolve!: () => void; let readyReject!: (error: Error) => void;
    const ready = new Promise<void>((resolve, reject) => { readyResolve = resolve; readyReject = reject; });
    // Suppress unhandled rejection if a partial acquisition fails before ready().
    void ready.catch(() => undefined);
    let closedResolve!: () => void;
    const closed = new Promise<void>(resolve => { closedResolve = resolve; });
    let tail = '';
    const data = (chunk: Buffer) => {
      // Only the readiness boolean survives; raw logs may contain credentials.
      tail = (tail + chunk.toString('utf8')).slice(-8192);
      if (tail.includes(readinessText)) { tail = ''; readyResolve(); }
    };
    child.stdout.on('data', data); child.stderr.on('data', data);
    child.on('error', () => { running = false; readyReject(new FixtureError('observation-failed')); closedResolve(); });
    child.on('close', () => { running = false; readyReject(new FixtureError('observation-failed')); closedResolve(); });
    return {
      pid: child.pid ?? 0, generation, executable: command.executable, argv: Object.freeze([...command.argv]),
      state: () => running ? 'running' : 'exited',
      async ready(signal) {
        signal.throwIfAborted();
        let abort!: () => void;
        const aborted = new Promise<void>((_resolve, reject) => {
          abort = () => reject(new FixtureError('observation-failed'));
          signal.addEventListener('abort', abort, { once: true });
        });
        try { await Promise.race([ready, aborted]); signal.throwIfAborted(); if (!running) throw new FixtureError('observation-failed'); }
        finally { signal.removeEventListener('abort', abort); }
      },
      async stop() {
        if (running && child.pid) {
          // The group exists only while the exact spawned leader is alive.
          // This covers group members only. Detached services are separately
          // enrolled from product registration and kernel generation identity.
          try { process.kill(-child.pid, 'SIGKILL'); } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw new FixtureError('observation-failed'); }
        }
        await closed;
        if (child.pid) {
          try { process.kill(-child.pid, 0); throw new FixtureError('ownership-mismatch'); }
          catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw new FixtureError('ownership-mismatch'); }
        }
      },
    };
  },
};
