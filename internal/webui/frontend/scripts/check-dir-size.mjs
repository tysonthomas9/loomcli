#!/usr/bin/env node
// Check that no directory under frontend src/ contains more than 15 .ts/.tsx
// files at depth 1. This is a directory-breadth gate that forces large
// directories to be split into sub-directories — it does NOT roll child
// directories' counts up into the parent.
//
// Excluded (whole-subtree, never walked into, never measured):
//   __tests__, test-utils, generated, node_modules, dist
//
// Generated is excluded because generated code is machine-produced and its
// count is driven by the OpenAPI spec, not developer discipline.
//
// Important divergence from check-loc.mjs: this gate DOES count .test.ts /
// .test.tsx / .spec.ts / .spec.tsx files that live next to source files. The
// Phase 6 split tasks sized their work against the raw .ts/.tsx count (e.g.
// "src/hooks (82 files)" includes co-located test files), so filtering those
// out would silently allow a directory with 7 source + 15 tests = 22 files to
// pass while still contradicting the split tasks' own arithmetic. This also
// means .d.ts declaration files (e.g. src/vite-env.d.ts) are counted — the
// gate is purely syntactic on filename extensions and deliberately avoids any
// file-level skip logic.
//
// No allowlist: if a directory exceeds 15 files, split it into sub-dirs. There
// is no env var, CLI flag, or config file that can exempt a directory.

import { readdirSync, statSync } from "fs";
import { join, relative, sep } from "path";
import { fileURLToPath } from "url";

export const DIR_SIZE_THRESHOLD = 15;

export const EXCLUDED_DIR_NAMES = new Set([
  "__tests__",
  "test-utils",
  "generated",
  "node_modules",
  "dist",
]);

/** Returns true if a directory with this basename should be pruned from the walk. */
export function shouldSkipDir(basename) {
  return EXCLUDED_DIR_NAMES.has(basename);
}

/** Returns true if this filename should count toward its directory's total. */
export function isCountableFile(name) {
  return name.endsWith(".ts") || name.endsWith(".tsx");
}

/**
 * Recursively walk `rootDir`, returning one entry per non-excluded directory
 * (including `rootDir` itself) with the count of direct-child .ts/.tsx files.
 * Child directories' files do NOT roll up into the parent — each directory's
 * fileCount reflects only its own depth-1 contents.
 */
export function walkDirs(rootDir) {
  const results = [];

  function visit(dirPath) {
    const entries = readdirSync(dirPath, { withFileTypes: true });
    let fileCount = 0;
    const subDirs = [];
    for (const entry of entries) {
      if (entry.isDirectory()) {
        if (shouldSkipDir(entry.name)) continue;
        subDirs.push(join(dirPath, entry.name));
      } else if (entry.isFile() && isCountableFile(entry.name)) {
        fileCount++;
      }
    }
    results.push({ dirPath, fileCount });
    for (const sub of subDirs) {
      visit(sub);
    }
  }

  visit(rootDir);
  return results;
}

/**
 * Check directory size limits for all directories under srcDir.
 * Returns either { error, exitCode } on setup failure, or
 * { violations, scannedCount } on success. Violations are sorted descending by
 * fileCount. `relPath` is frontend-relative using forward slashes.
 */
export function checkDirSize(frontendDir, srcDir, { threshold = DIR_SIZE_THRESHOLD } = {}) {
  let srcStat;
  try {
    srcStat = statSync(srcDir);
  } catch {
    return { error: `Error: src/ directory not found at ${srcDir}`, exitCode: 2 };
  }
  if (!srcStat.isDirectory()) {
    return { error: `Error: ${srcDir} is not a directory`, exitCode: 2 };
  }

  const walked = walkDirs(srcDir);
  const violations = [];

  for (const { dirPath, fileCount } of walked) {
    if (fileCount > threshold) {
      const relPath = relative(frontendDir, dirPath).replaceAll(sep, "/");
      violations.push({ relPath, fileCount, threshold });
    }
  }

  violations.sort((a, b) => b.fileCount - a.fileCount);

  return { violations, scannedCount: walked.length };
}

function main() {
  const scriptDir = fileURLToPath(new URL(".", import.meta.url));
  const frontendDir = join(scriptDir, "..");
  const srcDir = join(frontendDir, "src");

  const result = checkDirSize(frontendDir, srcDir);

  if (result.error) {
    console.error(result.error);
    process.exit(result.exitCode);
  }

  const { violations, scannedCount } = result;

  if (violations.length === 0) {
    console.log(
      `\u2713 All directories within ${DIR_SIZE_THRESHOLD}-file limit (${scannedCount} directories scanned)`,
    );
    process.exit(0);
  }

  for (const v of violations) {
    console.error(`  ${v.fileCount}\t${v.relPath}`);
  }
  console.error(
    `\n\u2717 ${violations.length} directory(ies) exceed ${DIR_SIZE_THRESHOLD}-file limit`,
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
