import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { prepareNativeCreatorEntrypoint, NativeCreatorEntrypointSource } from './native-creator-entrypoint.js';

// Adapter gate cwd, same for source and emitted tests; read the actual frozen
// source file rather than manufacture a matching script fixture.
const sourceFile = path.resolve('../../../test/local-mode/local-mode-entrypoint');
const hash = (bytes: Uint8Array) => createHash('sha256').update(bytes).digest('hex');

test('frozen source runtime copy differs in exactly the authentic creator command line', async () => {
  const source = await readFile(sourceFile), snapshot = Buffer.from(source);
  assert.equal(hash(source), NativeCreatorEntrypointSource.sha256);
  const result = prepareNativeCreatorEntrypoint(source);
  assert.deepEqual(source, snapshot); assert.equal(result.change.line, 127);
  const before = source.toString('utf8').split('\n'), after = result.bytes.toString('utf8').split('\n');
  assert.equal(before.length, after.length);
  assert.deepEqual(before.map((value, index) => value === after[index] ? null : index).filter(value => value !== null), [126]);
  assert.equal(before[126], result.change.original); assert.equal(after[126], result.change.replacement);
  assert.equal(result.change.replacement, '    node /opt/aft/dist/fixture/native-creator-wrapper.js "$WORKSPACE" "$SOURCE_REPO" "$WORKSPACE_ROOT"');
  const reverted = Buffer.from(result.bytes.toString('utf8').replace(result.change.replacement, result.change.original));
  assert.deepEqual(reverted, source); assert.equal(hash(result.bytes), result.adaptedSha256);
  assert.equal(result.activation, 'unsupported-until-retained-creator-handoff');
  assert.deepEqual(result.creatorArgvTemplate, ['workspace', 'create', '$WORKSPACE', '--repos', '$SOURCE_REPO', '--path', '$WORKSPACE_ROOT', '--branch', 'localmode']);
  assert.ok(Object.isFrozen(result.creatorArgvTemplate)); assert.ok(Object.isFrozen(result.change));
});

for (const change of ['missing-call', 'duplicate-call', 'other-line', 'crlf', 'bom'] as const)
  test(`source mismatch ${change} is denied instead of transformed or repaired`, async () => {
    const source = await readFile(sourceFile), text = source.toString('utf8');
    let changed: Buffer;
    if (change === 'missing-call') changed = Buffer.from(text.replace('loom workspace create', 'loom workspace list'));
    else if (change === 'duplicate-call') changed = Buffer.from(text + '\n' + text.split('\n')[126] + '\n');
    else if (change === 'other-line') changed = Buffer.from(text.replace('set -euo pipefail', 'set -eu'));
    else if (change === 'crlf') changed = Buffer.from(text.replaceAll('\n', '\r\n'));
    else changed = Buffer.concat([Buffer.from([239, 187, 191]), source]);
    assert.throws(() => prepareNativeCreatorEntrypoint(changed), /source-mismatch/);
  });

test('oversized, invalid, empty and caller-supplied replacement bytes never select a command', () => {
  for (const bytes of [Buffer.alloc(65537), Buffer.from([255]), Buffer.alloc(0),
    Buffer.from('node /caller/module.js "$WORKSPACE"')]) assert.throws(() => prepareNativeCreatorEntrypoint(bytes), /source-mismatch/);
});
