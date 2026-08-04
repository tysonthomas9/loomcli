#!/usr/bin/env node
// Check that frontend source files do not exceed the global LOC ceiling (2000).

import { readFileSync, readdirSync, statSync } from "fs";
import { join, relative, extname, sep } from "path";
import { fileURLToPath } from "url";

export const THRESHOLD_TS = 2000;
export const THRESHOLD_TSX = 2000;

// Retained for API compatibility with existing tests/importers; no per-file
// exceptions are currently needed now that the global ceiling is 2000.
export const ALLOWLIST = new Map();

// Patterns to skip (test files, generated files, fixtures).
export function shouldSkip(relPath) {
  const base = relPath.split("/").pop();

  if (base.endsWith(".test.ts") || base.endsWith(".test.tsx")) return true;
  if (base.endsWith(".spec.ts") || base.endsWith(".spec.tsx")) return true;
  if (base.endsWith(".d.ts")) return true;
  if (base.startsWith("vite-env")) return true;
  if (base === "TestFixtures.tsx") return true;

  const parts = relPath.split("/");
  if (parts.includes("__tests__")) return true;
  if (parts.includes("test-utils")) return true;
  if (parts.includes("generated")) return true;

  return false;
}

// Recursively collect .ts/.tsx files under dir.
export function walkDir(dir) {
  const results = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "node_modules") continue;
      results.push(...walkDir(full));
    } else {
      const ext = extname(entry.name);
      if (ext === ".ts" || ext === ".tsx") {
        results.push(full);
      }
    }
  }
  return results;
}

export function countLines(filePath) {
  const content = readFileSync(filePath, "utf-8");
  // Match wc -l: count newline characters.
  let count = 0;
  for (let i = 0; i < content.length; i++) {
    if (content[i] === "\n") count++;
  }
  return count;
}

/**
 * Check LOC limits for all .ts/.tsx files under srcDir.
 * Returns { violations, allowlistedCount } without calling process.exit,
 * so callers (including tests) can inspect the result.
 */
export function checkLoc(frontendDir, srcDir, allowlist = ALLOWLIST, thresholds = { ts: THRESHOLD_TS, tsx: THRESHOLD_TSX }) {
  let srcStat;
  try {
    srcStat = statSync(srcDir);
  } catch {
    return { error: `Error: src/ directory not found at ${srcDir}`, exitCode: 2 };
  }
  if (!srcStat.isDirectory()) {
    return { error: `Error: ${srcDir} is not a directory`, exitCode: 2 };
  }

  const allFiles = walkDir(srcDir);
  const violations = [];
  let allowlistedCount = 0;

  for (const filePath of allFiles) {
    const relPath = relative(frontendDir, filePath).replaceAll(sep, "/");

    if (shouldSkip(relPath)) continue;

    const loc = countLines(filePath);
    const ceiling = allowlist.get(relPath);

    const ext = relPath.endsWith(".tsx") ? "tsx" : "ts";
    const threshold = typeof thresholds === "number" ? thresholds : (thresholds[ext] ?? THRESHOLD_TS);

    if (ceiling !== undefined) {
      allowlistedCount++;
      if (loc > ceiling) {
        violations.push({ relPath, loc, ceiling });
      }
    } else if (loc > threshold) {
      violations.push({ relPath, loc, ceiling: null });
    }
  }

  // Sort by LOC descending.
  violations.sort((a, b) => b.loc - a.loc);

  return { violations, allowlistedCount };
}

function main() {
  const scriptDir = fileURLToPath(new URL(".", import.meta.url));
  const frontendDir = join(scriptDir, "..");
  const srcDir = join(frontendDir, "src");

  const result = checkLoc(frontendDir, srcDir);

  if (result.error) {
    console.error(result.error);
    process.exit(result.exitCode);
  }

  const { violations, allowlistedCount } = result;

  if (violations.length === 0) {
    console.log(`\u2713 All files within limits (${allowlistedCount} allowlisted)`);
    process.exit(0);
  }

  for (const v of violations) {
    const suffix = v.ceiling !== null ? ` (ceiling: ${v.ceiling})` : "";
    console.error(`  ${v.loc}\t${v.relPath}${suffix}`);
  }
  console.error(
    `\n\u2717 ${violations.length} file(s) exceed LOC limits (${THRESHOLD_TSX} .tsx / ${THRESHOLD_TS} .ts)`,
  );
  process.exit(1);
}

// Only run main() when executed directly (not when imported).
const isMainModule =
  process.argv[1] &&
  fileURLToPath(import.meta.url) === process.argv[1];

if (isMainModule) {
  main();
}
