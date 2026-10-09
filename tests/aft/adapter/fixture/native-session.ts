import * as fs from 'node:fs/promises';
import path from 'node:path';
import type { OwnedRoot } from '../ownership.js';
import type { OwnedWorkspaceRecord } from '../workspaces.js';
import { createNativeHostAccess, type NativeHostAccess, type NativeHostOptions } from '../native-host.js';
import { createNativeSessionQueryMapper, type NativeSessionQueryBinding, type NativeSessionQueryReply } from '../native-session-query.js';
import { FixtureError } from './lifecycle.js';
import { captureNativeStore, type NativeStoreBinding } from './native-store.js';
import { captureNativeSessionDirectory } from './native-session-directory.js';
import { RetainedNativeSession, nativeSessionGuard, type NativeSessionGuard } from './native-session-boundaries.js';

const check = (value: unknown) => { if (!value) throw new FixtureError('identity-mismatch'); };
export interface NativeQuerySessionCoordinates {
  readonly configurationRoot: Readonly<OwnedRoot>;
  readonly workspace: Pick<OwnedWorkspaceRecord, 'workspaceId' | 'repo' | 'storeId' | 'storeGeneration'>;
  readonly workspaceBindingId: string;
  readonly sources: NativeSessionQueryBinding['sources'];
  /** Actual attested common directories in this namespace, not host aliases. */
  readonly commonDirectories: readonly Readonly<OwnedRoot>[];
  readonly pinnedExecutable: string;
  readonly physical: NativeSessionQueryBinding['physical'];
  readonly secrets: readonly string[];
  readonly processIdentity?: NativeHostOptions['processIdentity'];
  readonly fetch?: NativeHostOptions['fetch'];
  /** Trusted owner checks the already-retained lease/container/helper/parent
   * identities. A replacement must fail; this callback must never recapture. */
  verifyNamespace(signal: AbortSignal): Promise<void>;
  enrollCleanup(cleanup: () => Promise<void>): void;
}

async function waitForQuery<T>(work: Promise<T>, signal: AbortSignal): Promise<T> {
  // Raw work is retained for cleanup even if a synchronous callback aborted.
  void work.catch(() => undefined);
  signal.throwIfAborted();
  let abort!: () => void;
  const cancelled = new Promise<never>((_resolve, reject) => {
    abort = () => reject(new FixtureError('observation-failed'));
    signal.addEventListener('abort', abort, { once: true });
    if (signal.aborted) abort();
  });
  try { const value = await Promise.race([work, cancelled]); signal.throwIfAborted(); return value; }
  finally { signal.removeEventListener('abort', abort); }
}

/** Inside-namespace query/descriptor owner. Trusted creation receipts and
 * namespace/process/build authority precede this private composition. It does
 * not launch a helper, create a workspace, issue a grant or attest a host path
 * as a container path. Framing/lifetime byte limits belong to the fixed pipe.
 * One retained store descriptor survives every canonical query; SQLite's
 * existing individual connections remain separate and are not atomic with
 * these physical checks. The enclosing owner must retain actual helper exit. */
export async function captureNativeQuerySession(coordinates: NativeQuerySessionCoordinates,
  signal: AbortSignal, files: typeof fs = fs, guard: NativeSessionGuard = nativeSessionGuard) {
  signal.throwIfAborted();
  const configurationRoot = Object.freeze({ ...coordinates.configurationRoot });
  const workspace = Object.freeze({ ...coordinates.workspace });
  const sources = structuredClone(coordinates.sources);
  const commonDirectories = coordinates.commonDirectories.map(root => Object.freeze({ ...root }));
  const pinnedExecutable = coordinates.pinnedExecutable;
  const verifyNamespace = coordinates.verifyNamespace.bind(coordinates);
  const processIdentity = coordinates.processIdentity, fetch = coordinates.fetch;
  const physical = { source: coordinates.physical.source.bind(coordinates.physical),
    agent: coordinates.physical.agent.bind(coordinates.physical) };
  check(path.isAbsolute(pinnedExecutable) && path.normalize(pinnedExecutable) === pinnedExecutable &&
    sources.some(source => source.repository.repo === workspace.repo) &&
    new Set(commonDirectories.map(root => root.path)).size === commonDirectories.length &&
    commonDirectories.length === new Set(sources.map(source => source.repository.commonDir)).size &&
    sources.every(source => commonDirectories.some(root => root.path === source.repository.commonDir)));
  let store: NativeStoreBinding | undefined;
  const directories: Awaited<ReturnType<typeof captureNativeSessionDirectory>>[] = [];
  const cleanups: (() => Promise<void>)[] = [];
  let terminal = false, ready = false, frameSignal: AbortSignal | undefined;
  let pending: Promise<NativeSessionQueryReply> | undefined;
  const verify = async (readSignal: AbortSignal) => {
    readSignal.throwIfAborted(); check(!terminal && store);
    await verifyNamespace(readSignal); readSignal.throwIfAborted(); check(!terminal);
    for (const directory of directories) { await directory.verify(readSignal); check(!terminal); }
    await store!.verify(readSignal);
    check(store!.storeId === workspace.storeId && store!.storeGeneration === workspace.storeGeneration);
    await verifyNamespace(readSignal); readSignal.throwIfAborted(); check(!terminal);
  };
  const access = (): NativeHostAccess => {
    check(frameSignal && !terminal && store && ready); frameSignal!.throwIfAborted();
    // Bind the actual current caller to legacy no-signal sessions/registration
    // ports. Recreating this fixed facade never recreates the captured FD.
    return createNativeHostAccess({ configRoot: configurationRoot.path, workspaceId: workspace.workspaceId,
      repo: workspace.repo, ownedRepositories: sources.map(source => source.repository), capturedStore: store,
      pinnedExecutable, processIdentity, fetch, signal: frameSignal });
  };
  const sameSignal = (readSignal: AbortSignal) => { check(readSignal === frameSignal); return access(); };
  const proxy: NativeHostAccess = {
    pinnedExecutable,
    rawAgent: (id, readSignal) => sameSignal(readSignal).rawAgent(id, readSignal),
    agentIdentity: (id, readSignal) => sameSignal(readSignal).agentIdentity!(id, readSignal),
    agent: id => access().agent(id), sessions: id => access().sessions(id),
    registration: () => access().registration(), process: () => access().process(),
    read: (route, readSignal) => sameSignal(readSignal).read(route, readSignal),
  };
  // Canonical schema/finite topology validation occurs before descriptor open.
  const map = createNativeSessionQueryMapper({ workspaceBindingId: coordinates.workspaceBindingId,
    workspaceId: workspace.workspaceId, sources, access: proxy, physical,
    secrets: [...coordinates.secrets], verify });
  const owner = new RetainedNativeSession(async (_bound, retain) => {
    retain({ verify: () => verify(signal), async dispose(cleanupSignal) {
      terminal = true;
      if (pending) await pending.catch(() => undefined);
      cleanupSignal.throwIfAborted();
      while (cleanups.length) {
        await cleanups[cleanups.length - 1]!();
        cleanups.pop(); cleanupSignal.throwIfAborted();
      }
    } });
    // Preserve the literal acquisition caller at namespace and filesystem ports.
    await verifyNamespace(signal); signal.throwIfAborted(); check(!terminal);
    const enroll = (cleanup: () => Promise<void>) => { cleanups.push(cleanup); };
    for (const root of [configurationRoot, ...sources.map(source => source.root), ...commonDirectories]) {
      directories.push(await captureNativeSessionDirectory(root, enroll, signal, files));
      signal.throwIfAborted(); check(!terminal);
    }
    store = await captureNativeStore(configurationRoot, enroll, signal, files);
    await verify(signal); ready = true;
  }, cleanup => coordinates.enrollCleanup(() => {
    // Make the outer reservation terminal synchronously, including the small
    // interval before RetainedNativeSession joins acquisition verification.
    terminal = true; return cleanup();
  }), guard);
  // Cleanup is enrolled before even the first namespace check/open.
  try { await owner.acquire(signal); }
  catch (error) { terminal = true; throw error; }
  return Object.freeze({
    async query(raw: unknown, readSignal: AbortSignal): Promise<NativeSessionQueryReply> {
      readSignal.throwIfAborted(); check(!terminal && ready && !pending);
      const bound = guard.begin(readSignal);
      frameSignal = readSignal;
      const work = Promise.resolve().then(() => map(raw, readSignal));
      pending = work;
      // Keep the reservation and literal signal until *raw* work settles.
      void work.then(() => { pending = undefined; frameSignal = undefined; },
        () => { terminal = true; pending = undefined; frameSignal = undefined; });
      try {
        const reply = await waitForQuery(work, bound.signal);
        check(!terminal); readSignal.throwIfAborted(); return reply;
      } catch (error) { terminal = true; throw error; }
      finally { bound.release(); }
    },
    async dispose() { terminal = true; return owner.dispose(); },
  });
}
