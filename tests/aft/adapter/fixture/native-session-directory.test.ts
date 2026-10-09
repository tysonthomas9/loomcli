import test from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { constants } from 'node:fs';
import { captureNativeSessionDirectory } from './native-session-directory.js';

const signal = () => new AbortController().signal;
function deferred() { let release!: () => void; const promise = new Promise<void>(resolve => { release = resolve; }); return { promise, release }; }
async function setup() {
  const parent = await fs.mkdtemp(path.resolve('fixture/test-artifacts-native-session-directory-'));
  const directory = path.join(parent, 'owned'); await fs.mkdir(directory);
  const stat = await fs.lstat(directory), callbacks: (() => Promise<void>)[] = [];
  return { parent, directory, root: { path: directory, device: stat.dev, inode: stat.ino },
    enroll: (cleanup: () => Promise<void>) => { callbacks.push(cleanup); }, callbacks,
    remove: () => fs.rm(parent, { recursive: true, force: true }) };
}
function wrap(actual: fs.FileHandle, close: () => Promise<void>) {
  return new Proxy(actual, { get(target, key) {
    if (key === 'close') return close;
    const value = Reflect.get(target, key); return typeof value === 'function' ? value.bind(target) : value;
  } });
}

test('one retained nofollow directory survives child content changes; cleanup only closes its descriptor', async () => {
  const s = await setup(); let opens = 0, closes = 0;
  try {
    const files = { ...fs, open: async (...args: Parameters<typeof fs.open>) => {
      assert.equal(s.callbacks.length, 1); assert.equal(args[0], s.directory);
      assert.equal(args[1], constants.O_RDONLY | constants.O_DIRECTORY | constants.O_NOFOLLOW);
      opens++; const actual = await fs.open(...args); return wrap(actual, async () => { closes++; await actual.close(); });
    } };
    const binding = await captureNativeSessionDirectory(s.root, s.enroll, signal(), files);
    await fs.writeFile(path.join(s.directory, 'child'), 'actual private test bytes');
    await binding.verify(signal()); await binding.verify(signal()); assert.equal(opens, 1);
    assert.deepEqual(binding.root, s.root); assert.ok(Object.isFrozen(binding.root));
    await binding.close(); await s.callbacks[0]!(); assert.equal(closes, 1);
    await assert.rejects(binding.verify(signal())); assert.equal(await fs.readFile(path.join(s.directory, 'child'), 'utf8'), 'actual private test bytes');
  } finally { await s.callbacks[0]?.(); await s.remove(); }
});

for (const replacement of ['directory', 'symlink'] as const) test(`${replacement} replacement denies retained directory without adopting/deleting it`, async () => {
  const s = await setup(); try {
    const binding = await captureNativeSessionDirectory(s.root, s.enroll, signal());
    const original = s.directory + '-original'; await fs.rename(s.directory, original);
    if (replacement === 'directory') await fs.mkdir(s.directory); else await fs.symlink(original, s.directory);
    await assert.rejects(binding.verify(signal())); await binding.close();
    assert.ok((await fs.lstat(original)).isDirectory()); assert.equal((await fs.lstat(s.directory)).isSymbolicLink(), replacement === 'symlink');
  } finally { await s.callbacks[0]?.(); await s.remove(); }
});

test('foreign stamp and preabort reject before open', async () => {
  const s = await setup(); let opens = 0; try {
    const files = { ...fs, open: async (...args: Parameters<typeof fs.open>) => { opens++; return fs.open(...args); } };
    await assert.rejects(captureNativeSessionDirectory({ ...s.root, inode: s.root.inode + 1 }, s.enroll, signal(), files));
    const caller = new AbortController(); caller.abort();
    await assert.rejects(captureNativeSessionDirectory(s.root, s.enroll, caller.signal, files));
    assert.equal(opens, 0); await s.callbacks[0]!();
  } finally { await s.remove(); }
});

test('swap between validation and open rejects; acquisition closes the returned exact foreign descriptor', async () => {
  const s = await setup(); let closes = 0; try {
    const files = { ...fs, open: async (...args: Parameters<typeof fs.open>) => {
      await fs.rename(s.directory, s.directory + '-original'); await fs.mkdir(s.directory);
      const actual = await fs.open(...args); return wrap(actual, async () => { closes++; await actual.close(); });
    } };
    await assert.rejects(captureNativeSessionDirectory(s.root, s.enroll, signal(), files));
    await s.callbacks[0]!(); assert.equal(closes, 1); assert.ok((await fs.lstat(s.directory)).isDirectory());
  } finally { await s.callbacks[0]?.(); await s.remove(); }
});

test('cleanup during pending open waits for and closes late descriptor once; no successful binding', async () => {
  const s = await setup(), entered = deferred(), release = deferred(); let closes = 0; try {
    const files = { ...fs, open: async (...args: Parameters<typeof fs.open>) => {
      const actual = await fs.open(...args); entered.release(); await release.promise;
      return wrap(actual, async () => { closes++; await actual.close(); });
    } };
    const acquisition = assert.rejects(captureNativeSessionDirectory(s.root, s.enroll, signal(), files)); await entered.promise;
    const a = s.callbacks[0]!(), b = s.callbacks[0]!(); release.release(); await Promise.all([acquisition, a, b]);
    assert.equal(closes, 1); await s.callbacks[0]!(); assert.equal(closes, 1);
  } finally { release.release(); await s.callbacks[0]?.(); await s.remove(); }
});

test('failed close retains same descriptor for fresh retry and denies all later reads', async () => {
  const s = await setup(); let closes = 0; try {
    const files = { ...fs, open: async (...args: Parameters<typeof fs.open>) => {
      const actual = await fs.open(...args); return wrap(actual, async () => {
        if (++closes === 1) throw new Error('injected close failure'); await actual.close();
      });
    } };
    const binding = await captureNativeSessionDirectory(s.root, s.enroll, signal(), files);
    await assert.rejects(binding.close(), /injected close failure/); await assert.rejects(binding.verify(signal()));
    await s.callbacks[0]!(); await binding.close(); assert.equal(closes, 2);
  } finally { await s.callbacks[0]?.(); await s.remove(); }
});

test('late open with failed disposal retains exact descriptor until a successful cleanup retry', async () => {
  const s = await setup(), entered = deferred(), release = deferred(); let closes = 0, opens = 0; try {
    const files = { ...fs, open: async (...args: Parameters<typeof fs.open>) => {
      opens++; const actual = await fs.open(...args); entered.release(); await release.promise;
      return wrap(actual, async () => { if (++closes <= 2) throw new Error('injected late close failure'); await actual.close(); });
    } };
    const acquisition = assert.rejects(captureNativeSessionDirectory(s.root, s.enroll, signal(), files)); await entered.promise;
    const closing = assert.rejects(s.callbacks[0]!(), /injected late close failure/);
    release.release(); await Promise.all([acquisition, closing]);
    assert.equal(closes, 1, 'acquisition and concurrent disposal join the same failed close');
    await assert.rejects(s.callbacks[0]!(), /injected late close failure/);
    await s.callbacks[0]!(); await s.callbacks[0]!();
    assert.equal(closes, 3); assert.equal(opens, 1);
  } finally { release.release(); await s.callbacks[0]?.(); await s.remove(); }
});

test('abort or cleanup during deferred stat exposes no successful verification', async () => {
  for (const transition of ['abort', 'cleanup'] as const) {
    const s = await setup(), entered = deferred(), release = deferred(); let blocked = false, closes = 0;
    try {
      const files = { ...fs, open: async (...args: Parameters<typeof fs.open>) => {
        const actual = await fs.open(...args); return new Proxy(actual, { get(target, key) {
          if (key === 'close') return async () => { closes++; await target.close(); };
          if (key === 'stat') return async () => { if (blocked) { entered.release(); await release.promise; } return target.stat(); };
          const value = Reflect.get(target, key); return typeof value === 'function' ? value.bind(target) : value;
        } });
      } };
      const binding = await captureNativeSessionDirectory(s.root, s.enroll, signal(), files), caller = new AbortController();
      blocked = true; const denied = assert.rejects(binding.verify(caller.signal)); await entered.promise;
      const closing = transition === 'cleanup' ? binding.close() : undefined;
      if (transition === 'abort') caller.abort();
      await Promise.resolve(); assert.equal(closes, 0, 'pending raw descriptor read is retained before cleanup');
      release.release(); await denied; await closing;
    } finally { release.release(); await s.callbacks[0]?.(); await s.remove(); }
  }
});
