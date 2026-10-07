#!/usr/bin/env node
// Install the UI7 formatter stimulus on one owned Chat target before its next document loads.
import { createInterface } from 'node:readline';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';

const TIME = 'অপৰাহ্ন ১২.৫৯';
const fail = code => { throw new Error(code); };
const browserUrl = (endpoint, profile) => {
  const url = new URL(endpoint);
  if (url.protocol !== 'ws:' || url.hostname !== '127.0.0.1' ||
      !/^\/devtools\/browser\/[A-Za-z0-9-]+$/.test(url.pathname) ||
      url.username || url.password || url.search || url.hash) fail('foreign-locale-endpoint');
  const lines = readFileSync(`${profile}/DevToolsActivePort`, 'utf8').trimEnd().split('\n');
  if (lines.length !== 2 || lines[0] !== url.port || lines[1] !== url.pathname)
    fail('foreign-locale-profile-endpoint');
};
const assertTargets = (targetInfos, browserContextIds, exactPage) => {
  if (!Array.isArray(targetInfos) || !Array.isArray(browserContextIds) || browserContextIds.length)
    fail('ambiguous-locale-context');
  const pages = targetInfos.filter(target => target.type === 'page');
  const owned = pages.filter(target => target.url === exactPage);
  if (owned.length !== 1 || pages.some(target =>
    ![exactPage, 'about:blank', 'chrome://newtab/'].includes(target.url)))
    fail('foreign-locale-page');
  const context = owned[0].browserContextId;
  if (typeof context !== 'string' || !context ||
      pages.some(target => target.browserContextId !== context)) fail('ambiguous-locale-context');
  if (typeof owned[0].targetId !== 'string' || !owned[0].targetId) fail('missing-locale-target');
  return owned[0].targetId;
};
const bounded = (promise, ms, code) => new Promise((resolve, reject) => {
  const timer = setTimeout(() => reject(new Error(code)), ms);
  Promise.resolve(promise).then(
    value => { clearTimeout(timer); resolve(value); },
    error => { clearTimeout(timer); reject(error); },
  );
});

function exactPage(config) {
  const { origin, route } = config;
  if (!/^http:\/\/127\.0\.0\.1:[0-9]+$/.test(origin) ||
      !/^\/ws\/LOCALMODE\/chat\/agt_[A-Za-z0-9_-]+$/.test(route))
    fail('foreign-locale-route');
  return origin + route;
}

export function initSource(page) {
  if (!/^http:\/\/127\.0\.0\.1:[0-9]+\/ws\/LOCALMODE\/chat\/agt_[A-Za-z0-9_-]+$/.test(page))
    fail('foreign-locale-route');
  return `(() => {
    if (location.origin + location.pathname !== ${JSON.stringify(page)}) return;
    if (Object.prototype.hasOwnProperty.call(window, '__aftVisualLocaleOriginal')) return;
    Object.defineProperty(window, '__aftVisualLocaleOriginal', {
      value: Date.prototype.toLocaleTimeString, configurable: true
    });
    Date.prototype.toLocaleTimeString = () => ${JSON.stringify(TIME)};
  })()`;
}

const restoreSource = page => `(() => {
  if (location.origin + location.pathname !== ${JSON.stringify(page)}) return 'foreign';
  if (Object.prototype.hasOwnProperty.call(window, '__aftVisualLocaleOriginal')) {
    Date.prototype.toLocaleTimeString = window.__aftVisualLocaleOriginal;
    delete window.__aftVisualLocaleOriginal;
  }
  return 'restored';
})()`;

export async function localeLifecycle(call, config, nextCommand, emit) {
  const page = exactPage(config);
  let sessionId;
  let identifier;
  let cleanupFailure;
  try {
    const { targetInfos } = await call('Target.getTargets');
    const { browserContextIds } = await call('Target.getBrowserContexts');
    const targetId = assertTargets(targetInfos, browserContextIds, page);
    ({ sessionId } = await call('Target.attachToTarget', { targetId, flatten: true }));
    if (typeof sessionId !== 'string' || !sessionId) fail('locale-attach-failed');
    ({ identifier } = await call('Page.addScriptToEvaluateOnNewDocument',
      { source: initSource(page) }, sessionId));
    if (typeof identifier !== 'string' || !identifier) fail('locale-setup-failed');
    emit({ status: 'installed', targetId, origin: config.origin, route: config.route });
    if (await nextCommand() !== 'reload') fail('locale-reload-required');
    const fresh = await call('Target.getTargets');
    if (assertTargets(fresh.targetInfos, browserContextIds, page) !== targetId)
      fail('locale-target-changed');
    const { frameTree } = await call('Page.getFrameTree', {}, sessionId);
    if (frameTree?.frame?.url !== page || !frameTree.frame.loaderId)
      fail('locale-frame-changed');
    await call('Page.reload', { loaderId: frameTree.frame.loaderId }, sessionId);
    emit({ status: 'reloaded', targetId, origin: config.origin, route: config.route });
    if (await nextCommand() !== 'close') fail('locale-close-required');
  } finally {
    if (sessionId) {
      if (identifier) {
        try {
          await call('Page.removeScriptToEvaluateOnNewDocument', { identifier }, sessionId);
        } catch { cleanupFailure = 'locale-removal-failed'; }
      }
      try {
        const result = await call('Runtime.evaluate',
          { expression: restoreSource(page), returnByValue: true }, sessionId);
        if (result.result?.value !== 'restored') cleanupFailure ??= 'locale-restore-failed';
      } catch { cleanupFailure ??= 'locale-restore-failed'; }
      try {
        await call('Target.detachFromTarget', { sessionId });
      } catch { cleanupFailure ??= 'locale-detach-failed'; }
    }
    if (cleanupFailure) fail(cleanupFailure);
  }
}

async function hold(config, input) {
  browserUrl(config.endpoint, config.profile);
  exactPage(config);
  const socket = new WebSocket(config.endpoint);
  let nextId = 0;
  const pending = new Map();
  socket.onmessage = message => {
    let response;
    try { response = JSON.parse(message.data); } catch { return; }
    const entry = pending.get(response.id);
    if (!entry) return;
    pending.delete(response.id);
    if (response.error) entry.reject(new Error('locale-cdp-command-failed'));
    else entry.resolve(response.result);
  };
  socket.onclose = () => {
    for (const entry of pending.values()) entry.reject(new Error('locale-cdp-disconnected'));
    pending.clear();
  };
  const call = (method, params = {}, sessionId) => bounded(new Promise((resolve, reject) => {
    const id = ++nextId;
    pending.set(id, { resolve, reject });
    socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
  }), 5000, 'locale-cdp-timeout');
  try {
    await bounded(new Promise((resolve, reject) => {
      socket.onopen = resolve;
      socket.onerror = () => reject(new Error('locale-cdp-connect-failed'));
    }), 5000, 'locale-cdp-connect-timeout');
    const nextCommand = () => bounded(new Promise(resolve => {
      input.once('line', resolve);
      input.once('close', () => resolve(null));
    }), 90000, 'locale-holder-timeout');
    await localeLifecycle(call, config, nextCommand,
      receipt => process.stdout.write(`${JSON.stringify(receipt)}\n`));
  } finally {
    if (socket.readyState === WebSocket.CONNECTING) {
      try { socket.close(); } catch { /* Python bounds and owns this child. */ }
    } else if (socket.readyState !== WebSocket.CLOSED) {
      const closed = new Promise(resolve => socket.addEventListener('close', resolve, { once: true }));
      socket.close();
      await bounded(closed, 2000, 'locale-socket-close-failed');
    }
  }
}

async function selfTest() {
  const origin = 'http://127.0.0.1:1234';
  const route = '/ws/LOCALMODE/chat/agt_owned';
  const config = { origin, route };
  const page = origin + route;
  const owned = { type: 'page', url: page, targetId: 'owned', browserContextId: 'default' };
  const targetInfos = [owned, { type: 'page', url: 'chrome://newtab/',
    targetId: 'newtab', browserContextId: 'default' }];
  const run = async (mutate, commands = ['reload', 'close']) => {
    const calls = [];
    const emitted = [];
    const call = async (method, params = {}, sessionId) => {
      calls.push({ method, params, sessionId });
      if (mutate?.method === method) throw new Error(mutate.code);
      if (method === 'Target.getTargets') return { targetInfos:
        calls.filter(x => x.method === 'Target.getTargets').length > 1 && mutate?.targetsAfter
          ? mutate.targetsAfter : (mutate?.targets ?? targetInfos) };
      if (method === 'Target.getBrowserContexts')
        return { browserContextIds: mutate?.contexts ?? [] };
      if (method === 'Target.attachToTarget') return { sessionId: 'session' };
      if (method === 'Page.addScriptToEvaluateOnNewDocument') return { identifier: 'script' };
      if (method === 'Page.getFrameTree') return { frameTree: { frame: {
        url: mutate?.frameUrl ?? page, loaderId: 'owned-loader' } } };
      if (method === 'Runtime.evaluate') return { result: { value: 'restored' } };
      return {};
    };
    const queue = [...commands];
    await localeLifecycle(call, config, async () => queue.shift(), x => emitted.push(x));
    return { calls, emitted };
  };
  const ok = await run();
  if (ok.emitted.map(x => x.status).join(',') !== 'installed,reloaded' ||
      ok.calls.filter(x => x.method === 'Page.removeScriptToEvaluateOnNewDocument').length !== 1 ||
      ok.calls.some(x => x.method === 'Browser.setPermission') ||
      !initSource(page).includes(TIME)) fail('locale-happy-path-failed');
  const original = () => 'short';
  const makeContext = pathname => {
    function DateFake() {}
    DateFake.prototype.toLocaleTimeString = original;
    return { Date: DateFake, window: {}, location: { origin, pathname } };
  };
  const ownedDocument = makeContext(route);
  runInNewContext(initSource(page), ownedDocument);
  if (ownedDocument.Date.prototype.toLocaleTimeString() !== TIME) fail('locale-stimulus-missing');
  runInNewContext(restoreSource(page), ownedDocument);
  if (ownedDocument.Date.prototype.toLocaleTimeString !== original ||
      '__aftVisualLocaleOriginal' in ownedDocument.window) fail('locale-restore-failed');
  const foreignDocument = makeContext('/foreign');
  runInNewContext(initSource(page), foreignDocument);
  if (foreignDocument.Date.prototype.toLocaleTimeString !== original)
    fail('foreign-document-mutated');
  const rejects = async (mutate, code, commands) => {
    try { await run(mutate, commands); } catch (error) {
      if (error.message === code) return;
    }
    fail(`negative-accepted-${code}`);
  };
  await rejects({ targets: [{ ...owned, url: origin + '/foreign' }] }, 'foreign-locale-page');
  await rejects({ targets: [{ ...owned, url: page, targetId: 'other' },
    { ...owned, targetId: 'owned' }] }, 'foreign-locale-page');
  await rejects({ targetsAfter: [{ ...owned, targetId: 'replacement' }] }, 'locale-target-changed');
  await rejects({ contexts: ['foreign-context'] }, 'ambiguous-locale-context');
  await rejects({ frameUrl: origin + '/foreign' }, 'locale-frame-changed');
  await rejects({ method: 'Page.addScriptToEvaluateOnNewDocument',
    code: 'locale-cdp-command-failed' }, 'locale-cdp-command-failed');
  await rejects({ method: 'Page.removeScriptToEvaluateOnNewDocument',
    code: 'locale-cdp-command-failed' }, 'locale-removal-failed');
  await rejects(null, 'locale-reload-required', ['close']);
  const profile = mkdtempSync('/private/tmp/aft-visual-locale-static.');
  try {
    writeFileSync(`${profile}/DevToolsActivePort`, '1235\n/devtools/browser/owned\n');
    browserUrl('ws://127.0.0.1:1235/devtools/browser/owned', profile);
    const rejectUrl = (endpoint, code) => {
      try { browserUrl(endpoint, profile); }
      catch (error) { if (error.message === code) return; }
      fail(`negative-accepted-${code}`);
    };
    rejectUrl('ws://foreign.example:1235/devtools/browser/owned', 'foreign-locale-endpoint');
    rejectUrl('ws://127.0.0.1:9999/devtools/browser/owned', 'foreign-locale-profile-endpoint');
  } finally { rmSync(profile, { recursive: true }); }
  try { initSource(origin + '/ws/OTHER/chat/agt_owned'); }
  catch (error) { if (error.message === 'foreign-locale-route') {
    process.stdout.write('PASS scoped locale CDP lifecycle negatives\n'); return;
  } }
  fail('foreign-locale-route-accepted');
}

if (process.argv[2] === 'self-test') await selfTest();
else if (process.argv[2] === 'hold') {
  const input = createInterface({ input: process.stdin, crlfDelay: Infinity });
  input.once('line', async line => {
    try { await hold(JSON.parse(line), input); }
    catch (error) {
      const code = /^[a-z-]+$/.test(error.message) ? error.message : 'locale-holder-failed';
      process.stderr.write(`${JSON.stringify({ status: 'blocked', reason: code })}\n`);
      process.exitCode = 1;
    } finally { input.close(); process.stdin.pause(); }
  });
} else fail('invalid-action');
