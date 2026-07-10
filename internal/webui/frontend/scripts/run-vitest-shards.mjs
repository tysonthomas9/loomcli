#!/usr/bin/env node

import { rmSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const frontendRoot = fileURLToPath(new URL("../", import.meta.url));
const vitestBin = resolve(frontendRoot, "node_modules/vitest/vitest.mjs");
const reportsDir = resolve(frontendRoot, ".vitest-reports");
// Twelve process-level shards keep even the jsdom-heavy quarters of this
// 8k-test suite below V8's heap ceiling. VITEST_SHARDS remains available for
// larger runners that can safely trade memory for fewer process startups.
const requestedShards = Number.parseInt(process.env.VITEST_SHARDS || "12", 10);
const shardCount =
  Number.isInteger(requestedShards) && requestedShards > 0
    ? requestedShards
    : 12;
const inputArgs = process.argv.slice(2);
const coverage = inputArgs.includes("--coverage");
const passthroughArgs = inputArgs.filter((arg) => arg !== "--coverage");

function run(args, env = process.env) {
  const result = spawnSync(process.execPath, [vitestBin, ...args], {
    cwd: frontendRoot,
    env,
    stdio: "inherit",
  });
  if (result.error) throw result.error;
  return result.status ?? 1;
}

rmSync(reportsDir, { recursive: true, force: true });

try {
  for (let index = 1; index <= shardCount; index += 1) {
    const args = [
      "run",
      `--shard=${index}/${shardCount}`,
      "--reporter=blob",
      ...passthroughArgs,
    ];
    if (coverage) args.push("--coverage", "--coverage.reporter=json");
    const status = run(
      args,
      coverage ? { ...process.env, VITEST_COVERAGE_SHARD: "1" } : process.env,
    );
    if (status !== 0) process.exit(status);
  }

  const mergeArgs = ["--merge-reports", reportsDir, "--reporter=dot"];
  if (coverage) mergeArgs.push("--coverage");
  process.exitCode = run(mergeArgs);
} finally {
  rmSync(reportsDir, { recursive: true, force: true });
}
