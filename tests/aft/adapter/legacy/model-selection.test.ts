import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkConfiguredModel } from './model-selection.js';

test('Cursor backend default has no fabricated exact-model requirement', () => {
  assert.doesNotThrow(() => checkConfiguredModel({ kind: 'backend-default' }, 'cursor', {}, undefined));
  const hints: Record<string, string>[] = [{ LOOM_AGENT_MODEL: 'hint' }, { LOOM_OPENCODE_MODEL: 'hint' }];
  for (const env of hints)
    assert.throws(() => checkConfiguredModel({ kind: 'backend-default' }, 'cursor', env, undefined));
  assert.throws(() => checkConfiguredModel({ kind: 'backend-default' }, 'codex', {}, undefined));
});

test('Codex and Claude reject role overrides before the configured actor can launch', () => {
  for (const backend of ['codex', 'claude']) {
    const selection = { kind: 'exact-model', model: 'configured', selector: 'agent-env' } as const;
    assert.doesNotThrow(() => checkConfiguredModel(selection, backend, { LOOM_AGENT_MODEL: 'configured' }, 'configured'));
    assert.doesNotThrow(() => checkConfiguredModel(selection, backend, { LOOM_AGENT_MODEL: 'configured' }, undefined));
    assert.throws(() => checkConfiguredModel(selection, backend, { LOOM_AGENT_MODEL: 'configured' }, 'role-override'));
    assert.throws(() => checkConfiguredModel(selection, backend, { LOOM_AGENT_MODEL: 'different' }, 'configured'));
  }
});

test('OpenCode environment precedence differs from agent env and owned config selection', () => {
  const envSelection = { kind: 'exact-model', model: 'configured', selector: 'opencode-env' } as const;
  assert.doesNotThrow(() => checkConfiguredModel(envSelection, 'opencode',
    { LOOM_AGENT_MODEL: 'role-override', LOOM_OPENCODE_MODEL: ' configured ' }, 'role-override'));
  assert.throws(() => checkConfiguredModel(envSelection, 'opencode', { LOOM_AGENT_MODEL: 'configured' }, undefined));
  const configSelection = { ...envSelection, selector: 'opencode-config' } as const;
  assert.doesNotThrow(() => checkConfiguredModel(configSelection, 'opencode', {}, undefined, 'configured'));
  assert.throws(() => checkConfiguredModel(configSelection, 'opencode', {}, undefined, 'foreign'));
  assert.throws(() => checkConfiguredModel(configSelection, 'opencode', { LOOM_OPENCODE_MODEL: 'override' }, undefined, 'configured'));
  assert.throws(() => checkConfiguredModel({ ...envSelection, selector: 'native-model' }, 'opencode', {}, undefined));
});
