import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createNativeArchiveHttp } from './native-archive-http.js';

const agent = { fixtureLeaseId: 'owned-lease', workspaceId: 'LOCALMODE', agentId: 'agt_editor-1' };
const origin = 'http://127.0.0.1:4100';
const key = 'cov-files-actualRUN-agt_editor-1-cleanup';

for (const status of [200, 202, 204, 409, 500]) test(`archive wire preserves HTTP ${status} without a JSON oracle`, async () => {
  let calls = 0, closes = 0;
  const deadlines: number[] = [];
  const transport = createNativeArchiveHttp(async (url, request) => {
    calls++;
    assert.equal(String(url), `${origin}/api/workspaces/LOCALMODE/v1/agents/agt_editor-1/archive`);
    assert.equal(request?.method, 'POST'); assert.equal(request?.body, '{"cancel":true}');
    assert.equal(request?.redirect, 'error');
    const headers = new Headers(request?.headers);
    assert.equal(headers.get('content-type'), 'application/json');
    assert.equal(headers.get('idempotency-key'), key);
    const response = new Response(status === 204 ? null : new ReadableStream({ cancel() { closes++; } }), { status });
    response.json = async () => { throw new Error('JSON parsing must not occur'); };
    response.text = async () => { throw new Error('Body decoding must not occur'); };
    return response;
  }, milliseconds => { deadlines.push(milliseconds); return new AbortController().signal; });
  assert.equal(await transport(origin, agent, key, new AbortController().signal), status);
  assert.equal(calls, 1); assert.equal(closes, status === 204 ? 0 : 1);
  assert.deepEqual(deadlines, [15000]);
});

test('archive wire rejects abort, origin escape, malformed target and header changes before HTTP', async () => {
  let calls = 0;
  const transport = createNativeArchiveHttp(async () => { calls++; return new Response(); });
  const aborted = new AbortController(); aborted.abort();
  await assert.rejects(transport(origin, agent, key, aborted.signal));
  for (const value of ['https://example.test', 'http://127.0.0.1:4100/foreign', 'http://user@127.0.0.1:4100'])
    await assert.rejects(transport(value, agent, key, new AbortController().signal));
  for (const value of [{ ...agent, workspaceId: '../foreign' }, { ...agent, agentId: 'foreign' }])
    await assert.rejects(transport(origin, value, key, new AbortController().signal));
  for (const value of ['', ' leading', 'trailing ', 'key\r\nInjected: value', 'non-ascii-\u00e9'])
    await assert.rejects(transport(origin, agent, value, new AbortController().signal));
  assert.equal(calls, 0);
});

test('archive wire never retries a rejected request or failed response close', async () => {
  for (const phase of ['request', 'close']) {
    let calls = 0;
    const transport = createNativeArchiveHttp(async () => {
      calls++;
      if (phase === 'request') throw new Error('uncertain request');
      return new Response(new ReadableStream({ cancel() { throw new Error('uncertain close'); } }));
    });
    await assert.rejects(transport(origin, agent, key, new AbortController().signal));
    assert.equal(calls, 1);
  }
});

for (const cause of ['caller', 'deadline']) test(`archive wire cannot report success after ${cause} abort during response close`, async () => {
  const caller = new AbortController(), deadline = new AbortController();
  let enter!: () => void, finish!: () => void;
  const entered = new Promise<void>(resolve => { enter = resolve; });
  const pending = new Promise<void>(resolve => { finish = resolve; });
  let calls = 0;
  const transport = createNativeArchiveHttp(async () => {
    calls++;
    return new Response(new ReadableStream({ async cancel() { enter(); await pending; } }));
  }, () => deadline.signal);
  const request = transport(origin, agent, key, caller.signal);
  await entered;
  (cause === 'caller' ? caller : deadline).abort(); finish();
  await assert.rejects(request); assert.equal(calls, 1);
});
