import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { createServer } from 'node:net';
import { FixtureError } from './lifecycle.js';

export interface HostCommand {
  executable: string; argv: string[]; cwd: string; env: Record<string, string>;
}
export interface OwnedProcess {
  pid: number; generation: string; executable: string; argv: readonly string[];
  state(): 'running' | 'exited';
  ready(signal: AbortSignal): Promise<void>;
  stop(): Promise<void>;
}
export interface HostProcesses {
  run(command: HostCommand, signal?: AbortSignal): Promise<string>;
  start(command: HostCommand, readinessText: string): OwnedProcess;
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
  start(command, readinessText) {
    const child = spawn(command.executable, command.argv, { cwd: command.cwd, env: command.env,
      shell: false, detached: true, stdio: ['ignore', 'pipe', 'pipe'] });
    const generation = randomUUID(); let running = true;
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
          // Immediate group termination also covers owned backend descendants.
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
