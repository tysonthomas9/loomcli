#!/usr/bin/env node
// Hold one origin-scoped clipboard-read override in the run-owned Chrome process.
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createInterface } from 'node:readline';

const fail = code => { throw new Error(code); };
const browserUrl = (endpoint, profile) => {
  const url = new URL(endpoint);
  if (url.protocol !== 'ws:' || url.hostname !== '127.0.0.1' ||
      !/^\/devtools\/browser\/[A-Za-z0-9-]+$/.test(url.pathname) ||
      url.username || url.password || url.search || url.hash) fail('foreign-endpoint');
  const lines = readFileSync(`${profile}/DevToolsActivePort`, 'utf8').trimEnd().split('\n');
  if (lines.length !== 2 || lines[0] !== url.port || lines[1] !== url.pathname)
    fail('foreign-profile-endpoint');
  return url;
};

export function assertTargets(targetInfos, browserContextIds, exactPage) {
  if (!Array.isArray(targetInfos) || !Array.isArray(browserContextIds) || browserContextIds.length)
    fail('ambiguous-browser-context');
  const pages = targetInfos.filter(target => target.type === 'page');
  const owned = pages.filter(target => target.url === exactPage);
  if (owned.length !== 1 || pages.some(target =>
    ![exactPage, 'about:blank', 'chrome://newtab/'].includes(target.url))) fail('foreign-page');
  const context = owned[0].browserContextId;
  if (typeof context !== 'string' || !context ||
      pages.some(target => target.browserContextId !== context)) fail('ambiguous-browser-context');
  if (typeof owned[0].targetId !== 'string' || !owned[0].targetId) fail('missing-target');
  return owned[0].targetId;
}

export function permissionParams(origin) {
  const url = new URL(origin);
  if (url.protocol !== 'http:' || url.hostname !== '127.0.0.1' ||
      !url.port || url.href !== `${origin}/`) fail('foreign-origin');
  return { permission: { name: 'clipboard-read' }, setting: 'granted',
           origin, embeddedOrigin: origin };
}

const bounded = (promise, ms, code) => new Promise((resolve, reject) => {
  const timer = setTimeout(() => reject(new Error(code)), ms);
  Promise.resolve(promise).then(
    value => { clearTimeout(timer); resolve(value); },
    error => { clearTimeout(timer); reject(error); },
  );
});

async function hold(config, input) {
  const { endpoint, profile, origin, route } = config;
  browserUrl(endpoint, profile);
  permissionParams(origin);
  if (!/^\/ws\/LOCALMODE\/chat\/agt_[A-Za-z0-9_-]+$/.test(route)) fail('foreign-route');
  const exactPage = origin + route;
  const socket = new WebSocket(endpoint);
  let nextId = 0;
  const pending = new Map();
  socket.onmessage = message => {
    let response;
    try { response = JSON.parse(message.data); } catch { return; }
    const entry = pending.get(response.id);
    if (!entry) return;
    pending.delete(response.id);
    if (response.error) entry.reject(new Error('cdp-command-failed'));
    else entry.resolve(response.result);
  };
  socket.onclose = () => {
    for (const entry of pending.values()) entry.reject(new Error('cdp-disconnected'));
    pending.clear();
  };
  const call = (method, params = {}) => bounded(new Promise((resolve, reject) => {
    const id = ++nextId;
    pending.set(id, { resolve, reject });
    socket.send(JSON.stringify({ id, method, params }));
  }), 5000, 'cdp-timeout');

  try {
    await bounded(new Promise((resolve, reject) => {
      socket.onopen = resolve;
      socket.onerror = () => reject(new Error('cdp-connect-failed'));
    }), 5000, 'cdp-connect-timeout');
    const { targetInfos } = await call('Target.getTargets');
    const { browserContextIds } = await call('Target.getBrowserContexts');
    const targetId = assertTargets(targetInfos, browserContextIds, exactPage);
    await call('Browser.setPermission', permissionParams(origin));
    process.stdout.write(`${JSON.stringify({ status: 'granted', targetId,
      context: 'owned-default', origin, route })}\n`);
    const command = await bounded(new Promise(resolve => {
      input.once('line', resolve);
      input.once('close', () => resolve(null));
    }), 20000, 'holder-timeout');
    input.close();
    if (command !== 'close') fail('holder-close-required');
  } finally {
    if (socket.readyState === WebSocket.CONNECTING) {
      try { socket.close(); } catch { /* Python bounds and owns this child. */ }
    } else if (socket.readyState !== WebSocket.CLOSED) {
      const closed = new Promise(resolve => socket.addEventListener('close', resolve, { once: true }));
      socket.close();
      await bounded(closed, 2000, 'holder-close-timeout');
    }
  }
}

function selfTest() {
  const origin = 'http://127.0.0.1:1234';
  const page = `${origin}/ws/LOCALMODE/chat/agt_owned`;
  const owned = { type: 'page', url: page, targetId: 'target_1', browserContextId: 'default_1' };
  const tab = { type: 'page', url: 'chrome://newtab/', targetId: 'target_2', browserContextId: 'default_1' };
  if (assertTargets([owned, tab], [], page) !== 'target_1') fail('owned-target-rejected');
  const expected = permissionParams(origin);
  if (expected.origin !== origin || expected.embeddedOrigin !== origin ||
      expected.permission.name !== 'clipboard-read' || 'browserContextId' in expected)
    fail('permission-scope-changed');
  const rejects = (fn, code) => {
    try { fn(); } catch (error) { if (error.message === code) return; }
    fail(`negative-accepted-${code}`);
  };
  rejects(() => permissionParams('http://foreign.example:1234'), 'foreign-origin');
  rejects(() => assertTargets([{ ...owned, url: `${origin}/other` }], [], page), 'foreign-page');
  rejects(() => assertTargets([owned, { ...tab, url: 'http://foreign.example/' }], [], page), 'foreign-page');
  rejects(() => assertTargets([owned, { ...tab, browserContextId: 'other' }], [], page), 'ambiguous-browser-context');
  rejects(() => assertTargets([owned], ['default_1'], page), 'ambiguous-browser-context');
  rejects(() => assertTargets([owned, { ...owned, targetId: 'target_3' }], [], page), 'foreign-page');
  const profile = mkdtempSync('/private/tmp/aft-visual-clipboard-static.');
  try {
    writeFileSync(`${profile}/DevToolsActivePort`, '1235\n/devtools/browser/owned\n');
    browserUrl('ws://127.0.0.1:1235/devtools/browser/owned', profile);
    rejects(() => browserUrl('ws://foreign.example:1235/devtools/browser/owned', profile), 'foreign-endpoint');
    rejects(() => browserUrl('ws://127.0.0.1:9999/devtools/browser/owned', profile), 'foreign-profile-endpoint');
  } finally { rmSync(profile, { recursive: true }); }
  process.stdout.write('PASS owned CDP target/context/origin negatives\n');
}

if (process.argv[2] === 'self-test') selfTest();
else if (process.argv[2] === 'hold') {
  const input = createInterface({ input: process.stdin, crlfDelay: Infinity });
  input.once('line', async line => {
    try { await hold(JSON.parse(line), input); }
    catch (error) {
      const code = /^[a-z-]+$/.test(error.message) ? error.message : 'clipboard-holder-failed';
      process.stderr.write(`${JSON.stringify({ status: 'blocked', reason: code })}\n`);
      process.exitCode = 1;
    }
    finally { input.close(); process.stdin.pause(); }
  });
} else fail('invalid-action');
