import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { createHandler, createStore } from './app.js';

test('store posts and lists messages per channel', () => {
  const store = createStore();
  assert.deepEqual(store.channels(), ['general', 'random']);
  store.post('general', 'ann', ' hi ');
  assert.equal(store.messages('general')[0].text, 'hi');
  assert.equal(store.messages('random').length, 0);
});

test('store rejects unknown channels and empty text', () => {
  const store = createStore();
  assert.throws(() => store.post('nope', 'ann', 'hi'), /unknown channel/);
  assert.throws(() => store.post('general', 'ann', '  '), /empty message/);
});

test('HTTP API round trip', async (t) => {
  const server = createServer(createHandler(createStore())).listen(0);
  t.after(() => server.close());
  const base = `http://localhost:${server.address().port}`;
  const posted = await fetch(`${base}/api/channels/general/messages`, { method: 'POST', body: JSON.stringify({ user: 'bob', text: 'yo' }) });
  assert.equal(posted.status, 201);
  const list = await (await fetch(`${base}/api/channels/general/messages`)).json();
  assert.deepEqual(list.map((m) => m.text), ['yo']);
  const bad = await fetch(`${base}/api/channels/nope/messages`, { method: 'POST', body: '{"text":"x"}' });
  assert.equal(bad.status, 400);
});
