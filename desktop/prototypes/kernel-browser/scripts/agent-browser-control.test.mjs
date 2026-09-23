import test from "node:test";
import assert from "node:assert/strict";
import { chmod, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";

const controlScript = new URL("./agent-browser-control.sh", import.meta.url).pathname;

test("connects agent-browser to the selected browser CDP endpoint", async () => {
  const root = await mkdtemp(join(tmpdir(), "loom-agent-browser-control-"));
  const log = join(root, "agent-browser.log");
  const curl = join(root, "curl");
  const agentBrowser = join(root, "agent-browser");
  await writeFile(curl, `#!/usr/bin/env bash
printf '%s\\n' "$*" >> "$FAKE_CURL_LOG"
printf '%s\\n' '{"appId":"app-c","cdpPort":63222,"targetId":"TARGETC"}'
`);
  await writeFile(agentBrowser, `#!/usr/bin/env bash
printf '%s\\n' "$*" >> "$FAKE_AGENT_BROWSER_LOG"
if [[ "$*" == *"tab list --json"* ]]; then
  printf '%s\\n' '{"success":true,"data":{"tabs":[{"active":true,"targetId":"TARGETC"}]}}'
fi
`);
  await Promise.all([chmod(curl, 0o755), chmod(agentBrowser, 0o755)]);

  const result = spawnSync("bash", [controlScript, "app-c", "snapshot", "-i"], {
    encoding: "utf8",
    env: {
      ...process.env,
      LOOM_KERNEL_RUNTIME_ID: "kernel-test-runtime",
      LOOM_KERNEL_CURL_BIN: curl,
      LOOM_KERNEL_AGENT_BROWSER_BIN: agentBrowser,
      FAKE_CURL_LOG: join(root, "curl.log"),
      FAKE_AGENT_BROWSER_LOG: log,
    },
  });

  assert.equal(result.status, 0, result.stderr);
  assert.equal(
    (await readFile(log, "utf8")).trim(),
    "--session loom-kernel-test-runtime-app-c --cdp 63222 tab list --json\n--session loom-kernel-test-runtime-app-c --cdp 63222 --pin-tab snapshot -i",
  );
  assert.match(await readFile(join(root, "curl.log"), "utf8"), /\/api\/status\/app-c/);
});

test("rejects invalid browser ids before calling agent-browser", () => {
  const result = spawnSync("bash", [controlScript, "../../other", "snapshot", "-i"], {
    encoding: "utf8",
    env: { ...process.env, LOOM_KERNEL_RUNTIME_ID: "kernel-test-runtime" },
  });

  assert.equal(result.status, 2);
  assert.match(result.stderr, /browser app id must match app-\[a-z\]/);
});
