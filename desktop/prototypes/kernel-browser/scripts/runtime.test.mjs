import test from "node:test";
import assert from "node:assert/strict";
import { chmod, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";

const runtimeScript = new URL("./runtime.sh", import.meta.url).pathname;

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
