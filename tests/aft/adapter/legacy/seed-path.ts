import { lstat } from 'node:fs/promises';
import { containedPath } from '../filesystem.js';
import type { OwnedRoot } from '../ownership.js';
import { LegacyError, SeedInput } from './operations.js';

// The launcher obtains this root through Loom's product-resolved worktree
// registry, then holds its exclusive fixture lease through validation and CLI
// completion. A YAML path never chooses the root or the filesystem transport.
export async function validateLocalSeedPath(root: OwnedRoot, relativePath: string, signal: AbortSignal): Promise<void> {
  signal.throwIfAborted();
  if (!SeedInput.shape.relativePath.safeParse(relativePath).success) throw new LegacyError('invalid-input', 'Unsafe seed path');
  try {
    const stat = await lstat(root.path);
    if (stat.dev !== root.device || stat.ino !== root.inode) throw new LegacyError('ownership-mismatch', 'Fixture worktree root changed');
    const dest = await containedPath(root.path, relativePath);
    let existing;
    try { existing = await lstat(dest); }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error; }
    if (existing && (!existing.isFile() || existing.nlink !== 1)) throw new LegacyError('ownership-mismatch', 'Seed target is not an exclusively owned regular file');
    signal.throwIfAborted();
  } catch (error) {
    if (signal.aborted || error instanceof LegacyError) throw error;
    throw new LegacyError('ownership-mismatch', 'Fixture seed path is unavailable or escapes its owned root');
  }
}
