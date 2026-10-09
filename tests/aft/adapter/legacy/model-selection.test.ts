import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkConfiguredModel } from './model-selection.js';

test('Cursor backend default has no fabricated exact-model requirement', () => {
  assert.doesNotThrow(() => checkConfiguredModel({ kind: 'backend-default' }, 'cursor', {}));
  const hints: Record<string, string>[] = [{ LOOM_AGENT_MODEL: 'hint' }, { LOOM_OPENCODE_MODEL: 'hint' }];
  for (const env of hints)
    assert.throws(() => checkConfiguredModel({ kind: 'backend-default' }, 'cursor', env));
  assert.throws(() => checkConfiguredModel({ kind: 'backend-default' }, 'codex', {}));
});

test('Codex and Claude reject mismatched effective environments', () => {
  for (const backend of ['codex', 'claude']) {
    const selection = { kind: 'exact-model', model: 'configured', selector: 'agent-env' } as const;
    assert.doesNotThrow(() => checkConfiguredModel(selection, backend, { LOOM_AGENT_MODEL: 'configured' }));
    assert.doesNotThrow(() => checkConfiguredModel(selection, backend, { LOOM_AGENT_MODEL: ' configured ' }));
    assert.throws(() => checkConfiguredModel(selection, backend, { LOOM_AGENT_MODEL: 'different' }));
  }
});

test('OpenCode environment precedence differs from agent env and owned config selection', () => {
  const envSelection = { kind: 'exact-model', model: 'configured', selector: 'opencode-env' } as const;
  assert.doesNotThrow(() => checkConfiguredModel(envSelection, 'opencode',
    { LOOM_AGENT_MODEL: 'role-override', LOOM_OPENCODE_MODEL: ' configured ' }));
  assert.throws(() => checkConfiguredModel(envSelection, 'opencode', { LOOM_AGENT_MODEL: ' configured ' }));
  const configSelection = { ...envSelection, selector: 'opencode-config' } as const;
  assert.doesNotThrow(() => checkConfiguredModel(configSelection, 'opencode', {}, 'configured'));
  assert.throws(() => checkConfiguredModel(configSelection, 'opencode', {}, 'foreign'));
  assert.throws(() => checkConfiguredModel(configSelection, 'opencode', { LOOM_OPENCODE_MODEL: 'override' }, 'configured'));
  assert.throws(() => checkConfiguredModel({ ...envSelection, selector: 'native-model' }, 'opencode', {}));
});
