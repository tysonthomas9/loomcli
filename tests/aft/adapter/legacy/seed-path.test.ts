import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, lstat, symlink, link, rm, realpath } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { validateLocalSeedPath } from './seed-path.js';
import { LegacyError } from './operations.js';

test('task-owned local seed path validation rejects symlinks, hard links and replaced roots', async () => {
  const ownedTest = await mkdtemp(fileURLToPath(new URL('.seed-test-', import.meta.url)));
  try {
    const worktree = path.join(ownedTest, 'worktree'); await mkdir(worktree);
    const rootPath = await realpath(worktree); const stat = await lstat(rootPath);
    const root = { path: rootPath, device: stat.dev, inode: stat.ino }; const signal = new AbortController().signal;
    await validateLocalSeedPath(root, 'nested/new-file.txt', signal);
    await writeFile(path.join(worktree, 'safe.txt'), 'fixture bytes'); await validateLocalSeedPath(root, 'safe.txt', signal);
    const outside = path.join(ownedTest, 'outside.txt'); await writeFile(outside, 'outside fixture bytes');
    await symlink(outside, path.join(worktree, 'escape'));
    await symlink(ownedTest, path.join(worktree, 'escape-dir'));
    await link(outside, path.join(worktree, 'shared-hardlink'));
    for (const relative of ['escape', 'escape-dir/new.txt', 'shared-hardlink']) {
      await assert.rejects(validateLocalSeedPath(root, relative, signal), error => error instanceof LegacyError && error.code === 'ownership-mismatch');
    }
    await assert.rejects(validateLocalSeedPath({ ...root, inode: root.inode + 1 }, 'safe.txt', signal),
      error => error instanceof LegacyError && error.code === 'ownership-mismatch');
    assert.equal((await lstat(outside)).size, Buffer.byteLength('outside fixture bytes'));
  } finally { await rm(ownedTest, { recursive: true, force: true }); }
});
