import { constants } from 'node:fs';
import { lstat, open, readdir, realpath } from 'node:fs/promises';
import path from 'node:path';
import { z } from 'zod';
import { Digest, Id, RelativePath, requireFact, sha256 } from './protocol.js';

export const FilesystemInput = z.object({ leaseId: Id, rootId: Id,
  relativePaths: z.array(RelativePath).min(1).max(1000), view: z.enum(['presence', 'bytes', 'tree-digest']),
  maxBytes: z.number().int().min(1).max(16 * 1024 * 1024), maxEntries: z.number().int().min(1).max(10000),
}).strict();
export const FilesystemOutput = z.object({ entries: z.array(z.object({
  relativePath: RelativePath, exists: z.boolean(), kind: z.enum(['file', 'directory', 'missing']),
  bytes: z.number().int().nonnegative().nullable(), sha256: Digest.nullable(),
  contentBase64: z.string().nullable(),
}).strict()) }).strict();

// Every ancestor is checked, including when the final path is absent. ENOENT
// on an unreadable/dangling symlink cannot be converted into negative evidence.
export async function containedPath(root: string, relative: string): Promise<string> {
  RelativePath.parse(relative);
  requireFact(await realpath(root) === root && (await lstat(root)).isDirectory(), 'ownership-mismatch', 'Owned root changed');
  let current = root;
  for (const component of relative.split('/')) {
    current = path.join(current, component);
    try { requireFact(!(await lstat(current)).isSymbolicLink(), 'ownership-mismatch', 'Symlink in owned observation path'); }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error; }
  }
  requireFact(current.startsWith(root + path.sep), 'ownership-mismatch', 'Path is outside owned root');
  return current;
}
export async function observeFilesystem(input: z.infer<typeof FilesystemInput>, root: string): Promise<z.infer<typeof FilesystemOutput>> {
  requireFact(new Set(input.relativePaths).size === input.relativePaths.length, 'identity-mismatch', 'Duplicate filesystem paths');
  const entries: z.infer<typeof FilesystemOutput>['entries'] = [];
  let consumed = 0;
  const visit = async (relative: string): Promise<void> => {
    requireFact(entries.length < input.maxEntries, 'incomplete-pages', 'Filesystem entry bound reached');
    const absolute = await containedPath(root, relative);
    let stat;
    try { stat = await lstat(absolute); } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error;
      entries.push({ relativePath: relative, exists: false, kind: 'missing', bytes: null, sha256: null, contentBase64: null });
      return;
    }
    requireFact(stat.isFile() || stat.isDirectory(), 'observation-failed', 'Unsupported filesystem entry');
    if (stat.isDirectory()) {
      requireFact(input.view !== 'bytes', 'observation-failed', 'Exact bytes require a regular file');
      entries.push({ relativePath: relative, exists: true, kind: 'directory', bytes: null, sha256: null, contentBase64: null });
      if (input.view === 'tree-digest') {
        const children = (await readdir(absolute)).sort();
        for (const child of children) await visit(`${relative}/${child}`);
      }
      return;
    }
    if (input.view === 'presence') {
      entries.push({ relativePath: relative, exists: true, kind: 'file', bytes: stat.size, sha256: null, contentBase64: null });
      return;
    }
    requireFact(consumed + stat.size <= input.maxBytes, 'incomplete-pages', 'Filesystem byte bound reached');
    const file = await open(absolute, constants.O_RDONLY | constants.O_NOFOLLOW);
    try {
      const before = await file.stat();
      requireFact(before.ino === stat.ino && before.dev === stat.dev && before.isFile(), 'identity-mismatch', 'File changed before read');
      const bytes = Buffer.alloc(before.size + 1);
      let count = 0;
      while (count < bytes.length) {
        const read = await file.read(bytes, count, bytes.length - count, count);
        if (!read.bytesRead) break;
        count += read.bytesRead;
      }
      const after = await file.stat();
      const named = await lstat(await containedPath(root, relative));
      requireFact(count === before.size && after.size === before.size && after.mtimeMs === before.mtimeMs && after.ctimeMs === before.ctimeMs &&
        named.ino === after.ino && named.dev === after.dev, 'identity-mismatch', 'File changed during read');
      consumed += count;
      const content = bytes.subarray(0, count);
      entries.push({ relativePath: relative, exists: true, kind: 'file', bytes: count, sha256: await sha256(content),
        contentBase64: input.view === 'bytes' ? content.toString('base64') : null });
    } finally { await file.close(); }
  };
  for (const relative of input.relativePaths) await visit(relative);
  requireFact(new Set(entries.map(entry => entry.relativePath)).size === entries.length, 'identity-mismatch', 'Overlapping filesystem paths');
  return FilesystemOutput.parse({ entries });
}
