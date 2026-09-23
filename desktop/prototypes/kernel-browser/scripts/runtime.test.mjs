import test from "node:test";
import assert from "node:assert/strict";
import { chmod, mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";

const runtimeScript = new URL("./runtime.sh", import.meta.url).pathname;
const healthScript = new URL("./verify-control-health.sh", import.meta.url).pathname;

async function fakeRuntime({ failSecondRun = false } = {}) {
  const root = await mkdtemp(join(tmpdir(), "loom-kernel-runtime-test-"));
  const log = join(root, "calls.log");
  const docker = join(root, "docker");
  const curl = join(root, "curl");
  const sleep = join(root, "sleep");
  await writeFile(docker, `#!/usr/bin/env bash
printf '%s\\n' "$*" >> "$FAKE_RUNTIME_LOG"
if [[ "$*" == *"container inspect"* ]]; then exit 1; fi
if [[ "$*" == *"run -d"* && "$*" == *"app-b"* && "${failSecondRun ? "yes" : "no"}" == yes ]]; then exit 1; fi
if [[ "$*" == *"ps --all --quiet"* ]]; then printf 'owned-container\\n'; fi
exit 0
`);
  await writeFile(curl, "#!/usr/bin/env bash\nexit 0\n");
  await writeFile(sleep, "#!/usr/bin/env bash\nexit 0\n");
  await Promise.all([chmod(docker, 0o755), chmod(curl, 0o755), chmod(sleep, 0o755)]);
  const result = spawnSync("bash", [runtimeScript, "start"], {
    encoding: "utf8",
    env: {
      ...process.env,
      PATH: `${root}:${process.env.PATH}`,
      FAKE_RUNTIME_LOG: log,
      LOOM_KERNEL_STATE_DIR: join(root, "state"),
      LOOM_KERNEL_DOCKER_CONTEXT: "fake-context",
    },
  });
  return { root, log: await readFile(log, "utf8"), result };
}

test("failed startup removes only containers created for that runtime", async () => {
  const run = await fakeRuntime({ failSecondRun: true });
  assert.notEqual(run.result.status, 0);
  assert.match(run.log, /rm --force owned-container/);
});

test("start generates one runtime id and uses it in names and labels", async () => {
  const run = await fakeRuntime();
  assert.equal(run.result.status, 0, run.result.stderr);
  const labels = [...run.log.matchAll(/io\.loom\.runtime-id=([^ ]+)/g)].map((match) => match[1]);
  assert.ok(labels.length >= 2);
  assert.equal(new Set(labels).size, 1);
  assert.doesNotMatch(run.log, /--name loom-kernel-browser-app-a(?: |$)/);
});

test("health probe follows the persisted runtime id and configured CDP ports", async () => {
  const root = await mkdtemp(join(tmpdir(), "loom-kernel-health-test-"));
  const state = join(root, "state");
  const log = join(root, "calls.log");
  await writeFile(join(root, "docker"), `#!/usr/bin/env bash
printf 'docker %s\\n' "$*" >> "$FAKE_RUNTIME_LOG"
exit 0
`);
  await writeFile(join(root, "curl"), `#!/usr/bin/env bash
printf 'curl %s\\n' "$*" >> "$FAKE_RUNTIME_LOG"
exit 0
`);
  await writeFile(join(root, "sleep"), "#!/usr/bin/env bash\nexit 0\n");
  await Promise.all([
    chmod(join(root, "docker"), 0o755),
    chmod(join(root, "curl"), 0o755),
    chmod(join(root, "sleep"), 0o755),
  ]);
  await mkdir(state);
  await writeFile(join(state, "runtime-id"), "test runtime\n");

  const result = spawnSync("bash", [healthScript, "1"], {
    encoding: "utf8",
    env: {
      ...process.env,
      PATH: `${root}:${process.env.PATH}`,
      FAKE_RUNTIME_LOG: log,
      LOOM_KERNEL_STATE_DIR: state,
      LOOM_KERNEL_APP_A_CDP_PORT: "41222",
      LOOM_KERNEL_APP_B_CDP_PORT: "42222",
      LOOM_KERNEL_HEALTH_INTERVAL_SECONDS: "1",
    },
  });

  assert.equal(result.status, 0, result.stderr);
  const calls = await readFile(log, "utf8");
  assert.match(calls, /127\.0\.0\.1:41222\/json\/version/);
  assert.match(calls, /127\.0\.0\.1:42222\/json\/version/);
  assert.match(calls, /exec loom-kernel-browser-test-runtime-app-a/);
  assert.match(calls, /exec loom-kernel-browser-test-runtime-app-b/);
});

test("add starts only the requested task-owned browser", async () => {
  const root = await mkdtemp(join(tmpdir(), "loom-kernel-add-test-"));
  const state = join(root, "state");
  const log = join(root, "calls.log");
  await writeFile(join(root, "docker"), `#!/usr/bin/env bash
printf '%s\\n' "$*" >> "$FAKE_RUNTIME_LOG"
if [[ "$*" == *"container inspect"* ]]; then exit 1; fi
exit 0
`);
  await writeFile(join(root, "curl"), "#!/usr/bin/env bash\nexit 0\n");
  await writeFile(join(root, "sleep"), "#!/usr/bin/env bash\nexit 0\n");
  await Promise.all([
    chmod(join(root, "docker"), 0o755),
    chmod(join(root, "curl"), 0o755),
    chmod(join(root, "sleep"), 0o755),
  ]);
  await mkdir(state);
  await writeFile(join(state, "runtime-id"), "test-runtime\n");

  const result = spawnSync("bash", [runtimeScript, "add", "app-c", "63080", "63222", "63224", "63101", "56200"], {
    encoding: "utf8",
    env: {
      ...process.env,
      PATH: `${root}:${process.env.PATH}`,
      FAKE_RUNTIME_LOG: log,
      LOOM_KERNEL_STATE_DIR: state,
      LOOM_KERNEL_DOCKER_CONTEXT: "fake-context",
    },
  });

  assert.equal(result.status, 0, result.stderr);
  const calls = await readFile(log, "utf8");
  assert.match(calls, /--name loom-kernel-browser-test-runtime-app-c/);
  assert.match(calls, /io\.loom\.browser-app=app-c/);
  assert.match(calls, /127\.0\.0\.1:63222:9222/);
  assert.doesNotMatch(calls, /browser-app=app-a/);
  assert.doesNotMatch(calls, /browser-app=app-b/);
});
