#!/usr/bin/env node
// smoke-load-server.mjs — prove a packaged built-in `server.mjs` loads under
// THIS node (process.execPath) by forking it exactly the way the production
// launcher does (internal/driver/sandbox/launcher.go flueLocalLauncher: same
// env, same stdio shape) and waiting for the Flue one-shot IPC `ready`
// handshake. Because `fork` spawns process.execPath, running this under the
// embedded `node` proves that binary loads the entire module graph, including
// the nested dist/node_modules/@loom/sdk external.
//
// The `{type:'ready'}` handshake exists ONLY at the pinned Flue commit
// (internal/workflows/FLUE_COMMIT); a server.mjs built against a drifted Flue
// (ALLOW_FLUE_PIN_DRIFT=1) never sends it and this smoke times out.
//
//   usage: <node> smoke-load-server.mjs <server.mjs> <workflowName> [timeoutMs=20000]
//   exit:  0 ready · 1 child exited/errored before ready · 2 timeout · 64 usage
//
// Dependencies: node:child_process, node:path only (runs under a bare
// embedded Node with no node_modules).
import { fork } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, resolve } from 'node:path';

const [, , serverArg, workflowName, timeoutArg] = process.argv;
const usage = 'usage: node smoke-load-server.mjs <server.mjs> <workflowName> [timeoutMs=20000]';

if (!serverArg || !workflowName) {
  console.error(usage);
  process.exit(64);
}
const timeoutMs = timeoutArg === undefined ? 20000 : Number(timeoutArg);
if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
  console.error(`${usage}\ninvalid timeoutMs: ${timeoutArg}`);
  process.exit(64);
}
const serverPath = resolve(serverArg);
if (!existsSync(serverPath)) {
  console.error(`${usage}\nserver.mjs not found: ${serverPath}`);
  process.exit(64);
}
const bundleRoot = dirname(serverPath);

const stdout = [];
const stderr = [];
let settled = false;

const child = fork(serverPath, [], {
  cwd: bundleRoot,
  env: {
    ...process.env,
    FLUE_MODE: 'local',
    FLUE_CLI_TARGET: 'workflow',
    FLUE_CLI_NAME: workflowName,
    FLUE_INTERNAL_CLI_IPC: '1',
  },
  stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
});

child.stdout?.on('data', (data) => stdout.push(data));
child.stderr?.on('data', (data) => stderr.push(data));

function dump(label) {
  const out = Buffer.concat(stdout).toString('utf8').trim();
  const err = Buffer.concat(stderr).toString('utf8').trim();
  console.error(`${label} execPath=${process.execPath} node=${process.version} server=${serverPath} workflow=${workflowName}`);
  if (out) console.error(`--- child stdout ---\n${out}`);
  if (err) console.error(`--- child stderr ---\n${err}`);
}

function finish(code, label) {
  if (settled) return;
  settled = true;
  clearTimeout(timer);
  try {
    child.kill('SIGTERM');
  } catch {
    // already gone
  }
  if (code === 0) {
    console.log(`ready execPath=${process.execPath} node=${process.version} server=${serverPath} workflow=${workflowName}`);
  } else {
    dump(label);
  }
  // Exit once the child is gone so no orphan survives the smoke; the child
  // may already have exited (the "exited before ready" path), and a child
  // that ignores SIGTERM is force-killed after a short grace period.
  if (child.exitCode !== null || child.signalCode !== null) {
    process.exit(code);
  }
  child.once('exit', () => process.exit(code));
  setTimeout(() => {
    try {
      child.kill('SIGKILL');
    } catch {
      // already gone
    }
    process.exit(code);
  }, 2000);
}

const timer = setTimeout(() => finish(2, `timeout after ${timeoutMs}ms waiting for {type:'ready'}`), timeoutMs);

child.on('message', (message) => {
  if (message && typeof message === 'object' && message.type === 'ready') {
    finish(0, 'ready');
  }
});
child.on('error', (error) => finish(1, `child error: ${error && error.message ? error.message : String(error)}`));
child.on('exit', (code, signal) => {
  if (!settled) finish(1, `child exited before ready (code=${code} signal=${signal})`);
});
