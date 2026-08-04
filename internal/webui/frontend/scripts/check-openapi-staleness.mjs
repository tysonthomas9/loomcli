#!/usr/bin/env node
// Verifies generated TypeScript types match the OpenAPI spec.
// Exits 0 if types are up-to-date or spec doesn't exist yet.
// Exits 1 if types are stale (spec changed but types not regenerated).

import { existsSync, mkdtempSync, readFileSync, rmSync } from "fs";
import { join } from "path";
import { spawnSync } from "child_process";
import { tmpdir } from "os";
import { fileURLToPath } from "url";

/**
 * Check whether generated TypeScript types are up-to-date with the OpenAPI spec.
 * @param {string} frontendDir - Path to the frontend directory
 * @returns {{ status: "skip"|"ok"|"stale"|"missing"|"error", message: string, diff?: string }}
 */
export function checkStaleness(frontendDir) {
  const specPath = join(frontendDir, "..", "..", "..", "api", "openapi.yaml");
  const generatedPath = join(
    frontendDir,
    "src",
    "types",
    "generated",
    "openapi.ts",
  );

  if (!existsSync(specPath)) {
    return {
      status: "skip",
      message: "api/openapi.yaml not found — skipping staleness check",
    };
  }

  if (!existsSync(generatedPath)) {
    return {
      status: "missing",
      message:
        "api/openapi.yaml exists but src/types/generated/openapi.ts does not\n  Run: npm run generate:types",
    };
  }

  const binPath = join(frontendDir, "node_modules", ".bin", "openapi-typescript");
  if (!existsSync(binPath)) {
    return {
      status: "error",
      message:
        "openapi-typescript is not installed. Task .16 must be implemented first.",
    };
  }

  let tmpDir;
  try {
    tmpDir = mkdtempSync(join(tmpdir(), "openapi-staleness-"));
    const tmpFile = join(tmpDir, "openapi.ts");

    const genResult = spawnSync(binPath, [specPath, "-o", tmpFile], {
      stdio: ["pipe", "pipe", "pipe"],
    });
    if (genResult.status !== 0) {
      const stderrStr = genResult.stderr ? genResult.stderr.toString().trim() : "";
      const stderr = stderrStr
        || (genResult.error ? genResult.error.message : "unknown error");
      return {
        status: "error",
        message: `Failed to generate types from api/openapi.yaml:\n  ${stderr}`,
      };
    }

    // Format the temp file with prettier to match the committed file
    // (generate:types includes a prettier step)
    const prettierBin = join(frontendDir, "node_modules", ".bin", "prettier");
    if (existsSync(prettierBin)) {
      spawnSync(prettierBin, ["--write", tmpFile], {
        stdio: ["pipe", "pipe", "pipe"],
      });
    }

    const committed = readFileSync(generatedPath, "utf-8");
    const regenerated = readFileSync(tmpFile, "utf-8");

    if (committed === regenerated) {
      return { status: "ok", message: "Generated TypeScript types are up to date" };
    }

    const diffResult = spawnSync(
      "diff",
      ["--unified", generatedPath, tmpFile],
      { stdio: ["pipe", "pipe", "pipe"] },
    );
    let diff = "";
    if (diffResult.stdout) {
      const rawDiff = diffResult.stdout.toString();
      const lines = rawDiff.split("\n");
      if (lines.length > 30) {
        diff =
          lines.slice(0, 30).join("\n") +
          "\n... (truncated, run 'npm run generate:types' to see full changes)";
      } else {
        diff = rawDiff;
      }
    }

    return {
      status: "stale",
      message:
        "Generated TypeScript types are stale\n  src/types/generated/openapi.ts does not match api/openapi.yaml\n  Run: npm run generate:types",
      diff,
    };
  } finally {
    if (tmpDir) {
      rmSync(tmpDir, { recursive: true, force: true });
    }
  }
}

function main() {
  const scriptDir = fileURLToPath(new URL(".", import.meta.url));
  const frontendDir = join(scriptDir, "..");

  const result = checkStaleness(frontendDir);

  switch (result.status) {
    case "skip":
      console.log(`\u23ed  ${result.message}`);
      process.exit(0);
    case "ok":
      console.log(`\u2713 ${result.message}`);
      process.exit(0);
    case "missing":
      console.error(`\u2717 ${result.message}`);
      process.exit(1);
    case "stale":
      console.error(`\u2717 ${result.message}`);
      if (result.diff) {
        console.error(`\n${result.diff}`);
      }
      process.exit(1);
    case "error":
      console.error(`\u2717 ${result.message}`);
      process.exit(1);
  }
}

const isMainModule =
  process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1];

if (isMainModule) {
  main();
}
