import { spawn, type ChildProcess } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { createServer } from 'node:net';
import { FixtureError } from './lifecycle.js';

export interface HostCommand {
  executable: string; argv: string[]; cwd: string; env: Record<string, string>;
}
export interface CliCompletion { exitCode: number | null; stdout: string; stderr: string; complete: boolean }
export interface ProcessOutputSnapshot {
  stdout:string;stderr:string;stdoutComplete:boolean;stderrComplete:boolean;closed:boolean;
}
export interface OwnedCliProcess extends OwnedProcess { completion(signal: AbortSignal): Promise<CliCompletion> }
export interface OwnedProcess {
  pid: number; generation: string; executable: string; argv: readonly string[];
  state(): 'running' | 'exited';
  ready(signal: AbortSignal): Promise<void>;
  stop(): Promise<void>;
  // Private transport bytes; sanitize at the canonical evidence boundary.
  // A running prefix cannot establish absence of a later product log event.
  output?():ProcessOutputSnapshot;
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
export class LaunchNotStarted extends FixtureError { constructor() { super('observation-failed'); } }
export function createNodeProcesses(spawnChild: typeof spawn = spawn, signalGroup: typeof process.kill = process.kill): HostProcesses {
  const launch = (...args: Parameters<typeof spawn>): ChildProcess => { try { return spawnChild(...args); } catch { throw new LaunchNotStarted(); } };
  return {
  run(command, signal) {
    return new Promise((resolve, reject) => {
      const child = launch(command.executable, command.argv, { cwd: command.cwd, env: command.env,
        signal, shell: false, stdio: ['ignore', 'pipe', 'ignore'] });
      const chunks: Buffer[] = []; let bytes = 0;
      child.stdout!.on('data', (chunk: Buffer) => { bytes += chunk.length; if (bytes > 4 * 1024 * 1024) child.kill(); else chunks.push(chunk); });
      child.on('error', () => reject(new FixtureError('observation-failed')));
      child.on('close', code => code === 0 && bytes <= 4 * 1024 * 1024 ? resolve(Buffer.concat(chunks).toString('utf8')) : reject(new FixtureError('observation-failed')));
    });
  },
  launch(command, stdin, generation) {
    const child = launch(command.executable, command.argv, { cwd: command.cwd, env: command.env,
      shell: false, detached: true, stdio: ['pipe', 'pipe', 'pipe'] });
    let running = true; let bytes = 0; let errorBytes = 0; const chunks: Buffer[] = []; const errors: Buffer[] = [];
    const spawned = new Promise<void>((resolve, reject) => { child.once('spawn', resolve); child.once('error', () => reject(new FixtureError('observation-failed'))); });
    void spawned.catch(() => undefined);
    const completed = new Promise<CliCompletion>((resolve, reject) => {
      child.stdout!.on('data', (chunk: Buffer) => { bytes += chunk.length; if (bytes > 4 * 1024 * 1024) child.kill(); else chunks.push(chunk); });
      child.stderr!.on('data', (chunk: Buffer) => { errorBytes += chunk.length; if (errorBytes > 4 * 1024 * 1024) child.kill(); else errors.push(chunk); });
      child.once('error', () => { running = false; reject(new FixtureError('observation-failed')); });
      child.once('close', code => { running = false;
        if (bytes > 4 * 1024 * 1024 || errorBytes > 4 * 1024 * 1024 || code === null) reject(new FixtureError('observation-failed'));
        else resolve({ exitCode: code, stdout: Buffer.concat(chunks).toString('utf8'), stderr: Buffer.concat(errors).toString('utf8'), complete: true }); });
    });
    void completed.catch(() => undefined); child.stdin!.on('error', () => undefined); child.stdin!.end(stdin);
    return { pid: child.pid ?? 0, generation, executable: command.executable, argv: Object.freeze([...command.argv]), state: () => running ? 'running' : 'exited',
      async ready(signal) { signal.throwIfAborted(); await spawned; signal.throwIfAborted(); },
      async completion(signal) {
        signal.throwIfAborted(); let abort!: () => void;
        const cancelled = new Promise<never>((_resolve, reject) => { abort = () => reject(new FixtureError('observation-failed')); signal.addEventListener('abort', abort, { once: true }); });
        try { return await Promise.race([completed, cancelled]); } finally { signal.removeEventListener('abort', abort); }
      },
      async stop() {
        if (running && child.pid) {
          try { signalGroup(-child.pid, 'SIGKILL'); } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw new FixtureError('observation-failed'); }
        }
        await completed.catch(() => undefined);
        if (child.pid) { try { signalGroup(-child.pid, 0); throw new FixtureError('ownership-mismatch'); } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw new FixtureError('ownership-mismatch'); } }
      },
    };
  },
  start(command, readinessText, registeredGeneration) {
    const child = launch(command.executable, command.argv, { cwd: command.cwd, env: command.env,
      shell: false, detached: true, stdio: ['ignore', 'pipe', 'pipe'] });
    const generation = registeredGeneration ?? randomUUID(); let running = true, outputHealthy=true, outputClosed=false;
    let readyResolve!: () => void; let readyReject!: (error: Error) => void;
    const ready = new Promise<void>((resolve, reject) => { readyResolve = resolve; readyReject = reject; });
    // Suppress unhandled rejection if a partial acquisition fails before ready().
    void ready.catch(() => undefined);
    let closedResolve!: () => void;
    const closed = new Promise<void>(resolve => { closedResolve = resolve; });
    const output={stdout:[] as Buffer[],stderr:[] as Buffer[]},sizes={stdout:0,stderr:0};
    const capture=(stream:'stdout'|'stderr',chunk:Buffer)=>{
      sizes[stream]+=chunk.length;
      if(sizes[stream]<=4*1024*1024)output[stream].push(Buffer.from(chunk));
      data(chunk);
    };
    let tail = '';
    const data = (chunk: Buffer) => {
      // Readiness and bounded private evidence are independent; raw bytes are
      // never placed in public fixture receipts by this transport.
      tail = (tail + chunk.toString('utf8')).slice(-8192);
      if (tail.includes(readinessText)) { tail = ''; readyResolve(); }
    };
    child.stdout!.on('data', (chunk:Buffer)=>capture('stdout',chunk)); child.stderr!.on('data', (chunk:Buffer)=>capture('stderr',chunk));
    child.on('error', () => { running = false;outputHealthy=false; readyReject(new FixtureError('observation-failed')); closedResolve(); });
    child.on('close', () => { running = false;outputClosed=true; readyReject(new FixtureError('observation-failed')); closedResolve(); });
    return {
      pid: child.pid ?? 0, generation, executable: command.executable, argv: Object.freeze([...command.argv]),
      state: () => running ? 'running' : 'exited',
      output:()=>({stdout:Buffer.concat(output.stdout).toString('utf8'),stderr:Buffer.concat(output.stderr).toString('utf8'),
        stdoutComplete:outputHealthy&&sizes.stdout<=4*1024*1024,stderrComplete:outputHealthy&&sizes.stderr<=4*1024*1024,closed:outputClosed}),
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
          try { signalGroup(-child.pid, 'SIGKILL'); } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw new FixtureError('observation-failed'); }
        }
        await closed;
        if (child.pid) {
          try { signalGroup(-child.pid, 0); throw new FixtureError('ownership-mismatch'); }
          catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ESRCH') throw new FixtureError('ownership-mismatch'); }
        }
      },
    };
  },
  };
}
export const nodeProcesses = createNodeProcesses();
