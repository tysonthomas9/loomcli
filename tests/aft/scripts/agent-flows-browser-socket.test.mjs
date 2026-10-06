import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { chmodSync, existsSync, lstatSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const scripts = dirname(fileURLToPath(import.meta.url));
const helper = join(scripts, 'agent-flows-browser-socket.mjs');
const wrapper = join(scripts, 'agent-flows-browser');
const root = mkdtempSync('/private/tmp/aft-browser-socket-test-');
const receipt = join(root, 'socket.json');
const otherReceipt = join(root, 'other.json');
const selected = { suites: [{ name: 'live-receipts-stream-waiting' }, { name: 'coverage-lifecycle-delete' }] };
const run = (...args) => execFileSync(process.execPath, [helper, ...args], { encoding: 'utf8' });
const refuse = (...args) => assert.notEqual(spawnSync(process.execPath, [helper, ...args]).status, 0);
let owned, other;
try {
  owned = JSON.parse(run('create', receipt, JSON.stringify(selected)));
  other = JSON.parse(run('create', otherReceipt, JSON.stringify(selected)));
  assert.notEqual(owned.path, other.path, 'concurrent runs have distinct socket roots');
  assert.match(owned.path, /^\/private\/tmp\/aftsock\.[A-Za-z0-9]{6}$/);
  assert.equal(lstatSync(owned.path).mode & 0o777, 0o700);
  assert.equal(owned.max_socket_bytes, Buffer.byteLength(`${owned.path}/aft-live-receipts-stream-waiting-2147483647.sock`));
  assert.ok(owned.max_socket_bytes <= 103);
  assert.equal(run('verify', receipt, owned.path).trim(), owned.path);
  refuse('verify', receipt, other.path);
  const bin = join(root, 'browser-stub.sh');
  writeFileSync(bin, '#!/bin/sh\nprintf "%s\\n%s\\n%s\\n" "$AGENT_BROWSER_SOCKET_DIR" "$HOME" "$*"\n');
  chmodSync(bin, 0o700);
  const home = join(root, 'private-home');
  const profiles = join(root, 'profiles');
  const linkedWrapper = join(root, 'agent-browser');
  symlinkSync(wrapper, linkedWrapper);
  const env = { ...process.env, HOME: home, AFT_BROWSER_BIN: bin, AFT_BROWSER_PROFILES: profiles,
    AFT_BROWSER_SOCKET_RECEIPT: receipt, AGENT_BROWSER_SOCKET_DIR: owned.path,
    AFT_TESTS_DIR: dirname(scripts) };
  const command = spawnSync('bash', [linkedWrapper, '--session', 'aft-live-receipts-stream-waiting-12345', 'status'],
    { env, encoding: 'utf8' });
  assert.equal(command.status, 0, command.stderr);
  assert.deepEqual(command.stdout.trim().split('\n'), [owned.path, home,
    `--profile ${profiles}/aft-live-receipts-stream-waiting-12345 --session aft-live-receipts-stream-waiting-12345 status`]);
  assert.ok(existsSync(join(profiles, 'aft-live-receipts-stream-waiting-12345')));
  assert.notEqual(spawnSync('bash', [linkedWrapper, '--session', 'aft-live-receipts-stream-waiting-12345', 'status'],
    { env: { ...env, AGENT_BROWSER_SOCKET_DIR: other.path } }).status, 0, 'wrapper refuses foreign socket root');
  const forged = { ...owned, ino: owned.ino + 1 };
  writeFileSync(receipt, `${JSON.stringify(forged)}\n`);
  refuse('cleanup', receipt);
  assert.ok(existsSync(owned.path), 'wrong inode cannot delete this run socket root');
  writeFileSync(receipt, `${JSON.stringify(owned)}\n`);
  const longReceipt = join(root, 'long.json');
  refuse('create', longReceipt, JSON.stringify({ suites: [{ name: `long-${'x'.repeat(100)}` }] }));
  assert.ok(!existsSync(longReceipt), 'overlength suite refuses before leaving a receipt');
  run('cleanup', receipt);
  assert.ok(!existsSync(owned.path));
  assert.ok(existsSync(other.path), 'cleanup preserves the other run');
  run('cleanup', otherReceipt);
  assert.ok(!existsSync(other.path));
  console.log('agent-flow browser socket: private short path, wrapper HOME, ownership and cleanup gates passed');
} finally {
  if (owned && existsSync(owned.path)) run('cleanup', receipt);
  if (other && existsSync(other.path)) run('cleanup', otherReceipt);
  rmSync(root, { recursive: true, force: true });
}
