import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { z } from 'zod';

const base = '56bb2fcd1d7c1eae8a192cdeed012ddcce7351d8';
test('source receipts hash the original pinned Git blobs, independently of catalog payloads', () => {
  const receipts = z.object({ loomBase: z.literal(base), evidenceClass: z.literal('static source receipt only'),
    sources: z.array(z.object({ path: z.string().regex(/^[A-Za-z0-9_./-]+$/).refine(path =>
      !path.startsWith('/') && path.split('/').every(part => part && part !== '.' && part !== '..')),
    sha256: z.string().regex(/^[a-f0-9]{64}$/) }).strict()).length(36) }).passthrough()
    .parse(JSON.parse(readFileSync(new URL('./source-receipts.json', import.meta.url), 'utf8')));
  assert.equal(new Set(receipts.sources.map(source => source.path)).size, 36);
  const cwd = fileURLToPath(new URL('../../../../', import.meta.url));
  for (const source of receipts.sources) {
    // Fixed read-only Git plumbing: no legacy source helper/launcher executes.
    const original = execFileSync('git', ['show', `${base}:${source.path}`], { cwd });
    assert.equal(createHash('sha256').update(original).digest('hex'), source.sha256, source.path);
  }
});
