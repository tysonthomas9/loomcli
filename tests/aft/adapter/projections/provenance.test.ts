import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { expectedProjectionIdentity } from './identity.js';
import { receiptSourceVersions } from './receipt-replay.js';

const frontend = new URL('../../../../internal/webui/frontend/', import.meta.url);
const hash = (url: URL) => createHash('sha256').update(readFileSync(url)).digest('hex');
test('static source receipts match all six pinned files; no frontend code executes', () => {
  const paths = {
    chatMarkdownSha256: 'src/components/AgentChat/ChatMarkdown.tsx',
    longTextSha256: 'src/components/AgentChat/LongText.tsx',
    codeHighlightSha256: 'src/components/AgentChat/codeHighlight.ts',
    messageCopySha256: 'src/components/AgentChat/MessageCopyButton.tsx',
    cssSha256: 'src/components/AgentChat/ChatMarkdown.module.css', lockSha256: 'package-lock.json',
  } as const;
  for (const key of Object.keys(paths) as (keyof typeof paths)[]) assert.equal(hash(new URL(paths[key], frontend)), expectedProjectionIdentity[key]);
  const lock = JSON.parse(readFileSync(new URL('package-lock.json', frontend), 'utf8'));
  for (const [name, version] of Object.entries(expectedProjectionIdentity.dependencies)) {
    assert.equal(lock.packages['node_modules/' + name].version, version);
  }
});

test('projection parser dependency closure preserves the exact frontend lock entries', () => {
  const frontendLock = JSON.parse(readFileSync(new URL('package-lock.json', frontend), 'utf8'));
  const ownLock = JSON.parse(readFileSync(new URL('./package-lock.json', import.meta.url), 'utf8'));
  const manifest = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf8'));
  const seen = new Set<string>();
  const visit = (key: string): void => {
    if (seen.has(key)) return;
    seen.add(key);
    assert.deepEqual(ownLock.packages[key], frontendLock.packages[key]);
    for (const name of Object.keys(frontendLock.packages[key].dependencies ?? {})) {
      let parent = key, candidate: string;
      for (;;) {
        candidate = parent + '/node_modules/' + name;
        if (frontendLock.packages[candidate]) break;
        if (!parent.includes('/node_modules/')) { candidate = 'node_modules/' + name; break; }
        parent = parent.slice(0, parent.lastIndexOf('/node_modules/'));
      }
      visit(candidate);
    }
  };
  for (const name of Object.keys(manifest.dependencies)) visit('node_modules/' + name);
});

test('supplemental replay contracts pin each original source independently', () => {
  assert.equal(hash(new URL('../../scripts/coverage-receipts-stream-evidence.py', import.meta.url)), receiptSourceVersions['receipts-stream']);
  assert.equal(hash(new URL('../../scripts/coverage-children-queue.py', import.meta.url)), receiptSourceVersions['children-u3']);
});
