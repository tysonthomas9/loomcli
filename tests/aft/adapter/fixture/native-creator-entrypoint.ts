import { createHash } from 'node:crypto';
import { FixtureError } from './lifecycle.js';

// Frozen56bb source only. This is a preparation artifact, not a runnable build
// receipt or creator result. Default startup remains disabled until the fixed
// wrapper's sealed build and continuous descriptor/process handoff are proven.
export const NativeCreatorEntrypointSource = Object.freeze({
  commit: '56bb2fcd1d7c1eae8a192cdeed012ddcce7351d8',
  relativePath: 'test/local-mode/local-mode-entrypoint',
  sha256: '1e8e5eb27bf7aee63d27d84ca56747a6153dccf8c840c5e04f649780c3afdcbb',
});
const original = '    loom workspace create "$WORKSPACE" --repos "$SOURCE_REPO" --path "$WORKSPACE_ROOT" --branch localmode';
// Fixed fixture-owned module on the existing /opt/aft build mount. No caller
// command, executable, module path, shell fragment or scenario selects it.
const replacement = '    node /opt/aft/fixture/native-creator-wrapper.js "$WORKSPACE" "$SOURCE_REPO" "$WORKSPACE_ROOT"';
const hash = (bytes: Uint8Array) => createHash('sha256').update(bytes).digest('hex');

/** Prepare ONLY a byte-auditable one-call runtime copy. It changes no source
 * file, writes/launches nothing, and grants no path/workspace/process authority.
 * The fixed wrapper still must pass the original exact CLI argv/env/cwd and
 * retain original source/parent/managed FDs through serve/store initialization.
 * An exiting wrapper plus a receipt file alone cannot complete that proof. */
export function prepareNativeCreatorEntrypoint(source: Uint8Array) {
  if (source.byteLength > 65536 || hash(source) !== NativeCreatorEntrypointSource.sha256)
    throw new FixtureError('source-mismatch');
  const bytes = Buffer.from(source);
  const needle = Buffer.from(original + '\n'), injected = Buffer.from(replacement + '\n');
  const offset = bytes.indexOf(needle);
  if (offset < 0 || bytes.indexOf(needle, offset + 1) !== -1 || (offset !== 0 && bytes[offset - 1] !== 10))
    throw new FixtureError('source-mismatch');
  const copy = Buffer.concat([bytes.subarray(0, offset), injected, bytes.subarray(offset + needle.length)]);
  return Object.freeze({ bytes: copy, source: NativeCreatorEntrypointSource,
    adaptedSha256: hash(copy), activation: 'unsupported-until-retained-creator-handoff' as const,
    change: Object.freeze({ line: bytes.subarray(0, offset).filter(value => value === 10).length + 1,
      offset, original, replacement }),
    // This is the required wrapper argv TEMPLATE, not observed process facts.
    creatorArgvTemplate: Object.freeze(['workspace', 'create', '$WORKSPACE', '--repos', '$SOURCE_REPO',
      '--path', '$WORKSPACE_ROOT', '--branch', 'localmode']),
  });
}
