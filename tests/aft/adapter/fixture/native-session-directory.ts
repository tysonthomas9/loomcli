import * as fs from 'node:fs/promises';
import { constants } from 'node:fs';
import path from 'node:path';
import type { OwnedRoot } from '../ownership.js';
import { FixtureError } from './lifecycle.js';

const check = (value: unknown) => { if (!value) throw new FixtureError('identity-mismatch'); };
/** Private descriptor continuity inside the caller's actual namespace. This
 * captures an already-attested directory, not creation, repository membership,
 * an agent, a process or a public root handle. Never pass host coordinates for
 * an inside-container directory. The session owner enrolls it before dispatch.
 * Holding the descriptor prevents inode reuse; it does not prevent rename or
 * prove there were no intermediate namespace changes between observations. */
export async function captureNativeSessionDirectory(root: Readonly<OwnedRoot>,
  enrollCleanup: (cleanup: () => Promise<void>) => void, signal: AbortSignal,
  files: typeof fs = fs) {
  signal.throwIfAborted();
  const expected = Object.freeze({ path: root.path, device: root.device, inode: root.inode });
  check(path.isAbsolute(expected.path) && path.normalize(expected.path) === expected.path &&
    Number.isSafeInteger(expected.device) && expected.device >= 0 &&
    Number.isSafeInteger(expected.inode) && expected.inode >= 0);
  let opening: Promise<fs.FileHandle> | undefined, handle: fs.FileHandle | undefined;
  let disposalRequested = false, closed = false, closing: Promise<void> | undefined;
  const pending = new Set<Promise<void>>();
  const close = async () => {
    disposalRequested = true;
    if (closed) return;
    if (closing) return closing;
    const work = (async () => {
      if (opening) {
        try { handle ??= await opening; } catch { /* No descriptor was returned. */ }
      }
      // Terminal disposal prevents new reads. Existing raw descriptor work
      // must settle before close, even when its caller has already aborted.
      await Promise.allSettled([...pending]);
      if (handle) await handle.close();
      closed = true;
    })();
    closing = work.finally(() => { closing = undefined; });
    return closing;
  };
  // Terminal disposal request is retained even before open returns a handle.
  enrollCleanup(close);
  const named = async () => {
    const stat = await files.lstat(expected.path);
    check(stat.isDirectory() && !stat.isSymbolicLink() && stat.dev === expected.device &&
      stat.ino === expected.inode && await files.realpath(expected.path) === expected.path);
  };
  const verify = (readSignal: AbortSignal): Promise<void> => {
    // Register before the first filesystem await so cleanup can join raw work.
    const work = Promise.resolve().then(async () => {
      readSignal.throwIfAborted(); check(!disposalRequested && !closed && handle);
      await named(); readSignal.throwIfAborted(); check(!disposalRequested && !closed);
      const retained = await handle!.stat();
      check(retained.isDirectory() && retained.dev === expected.device && retained.ino === expected.inode);
      await named(); readSignal.throwIfAborted(); check(!disposalRequested && !closed);
    });
    pending.add(work);
    return work.finally(() => { pending.delete(work); });
  };
  try {
    await named(); signal.throwIfAborted(); check(!disposalRequested);
    opening = files.open(expected.path, constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW);
    handle = await opening;
    await verify(signal);
    signal.throwIfAborted(); check(!disposalRequested && !closed);
    return Object.freeze({ root: expected, verify, close });
  } catch (error) {
    // Failure retains the exact descriptor in its enrolled cleanup if close
    // rejects. No replacement directory is adopted, unlinked or removed.
    await close(); throw error;
  }
}
