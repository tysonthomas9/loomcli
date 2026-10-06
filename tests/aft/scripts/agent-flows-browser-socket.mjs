#!/usr/bin/env node
// Keep agent-browser's per-session Unix socket under macOS's 103-byte limit.
import { chmodSync, lstatSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';

const [action, receiptPath, selectionText] = process.argv.slice(2);
const fail = message => { throw new Error(`owned browser socket: ${message}`); };
const rootPattern = /^\/private\/tmp\/aftsock\.[A-Za-z0-9]{6}$/;
const maxSocketBytes = 103;
const maxPid = '2147483647'; // pid_t upper bound; actual AFT process.pid is shorter.
const socketName = suite => `aft-${suite.replace(/[^a-zA-Z0-9-]/g, '-')}-${maxPid}.sock`;
const owned = receipt => {
  if (!rootPattern.test(receipt.path) || !Number.isSafeInteger(receipt.dev) ||
      !Number.isSafeInteger(receipt.ino) || !Number.isInteger(receipt.max_socket_bytes) ||
      receipt.max_socket_bytes > maxSocketBytes) fail('invalid socket receipt');
  const stat = lstatSync(receipt.path);
  if (!stat.isDirectory() || stat.isSymbolicLink() || stat.dev !== receipt.dev ||
      stat.ino !== receipt.ino || (stat.mode & 0o777) !== 0o700)
    fail('socket directory ownership or mode changed');
  return receipt;
};
const read = () => {
  const stat = lstatSync(receiptPath);
  if (!stat.isFile() || stat.isSymbolicLink() || (stat.mode & 0o077)) fail('unsafe socket receipt');
  return owned(JSON.parse(readFileSync(receiptPath, 'utf8')));
};

if (action === 'create') {
  const selection = JSON.parse(selectionText);
  if (!Array.isArray(selection.suites) || !selection.suites.length ||
      !selection.suites.every(suite => typeof suite.name === 'string' && /^[a-z][a-z0-9-]*$/.test(suite.name)))
    fail('invalid selected suite names');
  const path = mkdtempSync('/private/tmp/aftsock.');
  try {
    chmodSync(path, 0o700);
    const longest = Math.max(...selection.suites.map(suite => Buffer.byteLength(`${path}/${socketName(suite.name)}`)));
    if (longest > maxSocketBytes) fail(`selected browser socket would be ${longest} bytes (max ${maxSocketBytes})`);
    const stat = lstatSync(path);
    const receipt = { path, dev: stat.dev, ino: stat.ino, max_socket_bytes: longest,
      session_pattern: 'aft-<selected-suite>-<pid>', socket_suffix: '.sock' };
    writeFileSync(receiptPath, `${JSON.stringify(receipt)}\n`, { flag: 'wx', mode: 0o600 });
    process.stdout.write(`${JSON.stringify(receipt)}\n`);
  } catch (error) {
    rmSync(path, { recursive: true, force: false });
    throw error;
  }
} else if (action === 'verify') {
  const receipt = read();
  if (selectionText !== receipt.path) fail('browser socket environment mismatch');
  process.stdout.write(`${receipt.path}\n`);
} else if (action === 'cleanup') {
  rmSync(read().path, { recursive: true, force: false });
} else fail('invalid action');
