#!/usr/bin/env node
// Regex-based linter that prevents hardcoded API paths and localhost URLs
// in frontend source code outside their proper locations (src/api/, tests).

import { readFileSync, readdirSync, statSync } from "fs";
import { join, relative, extname, sep } from "path";
import { fileURLToPath } from "url";

// Allowlist of "relPath:line" entries that are permitted.
// TerminalPanel uses raw /api/ paths for terminal token + WebSocket endpoints.
export const ALLOWLIST = new Set([
  "src/components/TerminalPanel/TerminalPanel.tsx:37",
  "src/components/TerminalPanel/TerminalPanel.tsx:50",
  "src/components/TerminalPanel/TerminalPanel.tsx:372",
  "src/components/TerminalPanel/TerminalPanel.tsx:376",
]);

const RULES = [
  {
    pattern: /\/api\//,
    message: "Hardcoded /api/ path — use src/api/ client instead",
  },
  {
    pattern: /https?:\/\/(localhost|127\.0\.0\.1)/,
    message: "Hardcoded localhost URL",
  },
];

/**
 * Returns true if the file should be excluded from scanning.
 */
export function isExcluded(relPath) {
  const parts = relPath.split("/");
  const base = parts[parts.length - 1];

  // src/api/ directory — expected to contain /api/ paths
  if (parts[0] === "src" && parts[1] === "api") return true;

  // Test files
  if (base.endsWith(".test.ts") || base.endsWith(".test.tsx")) return true;
  if (base.endsWith(".spec.ts") || base.endsWith(".spec.tsx")) return true;

  // Test directories, utilities, and fixtures
  if (parts.includes("__tests__")) return true;
  if (parts.includes("test-utils")) return true;
  if (base === "TestFixtures.tsx") return true;

  return false;
}

/**
 * Returns true if the line is a module import/export or a comment,
 * and should be skipped for rule checking.
 */
function shouldSkipLine(line) {
  const trimmed = line.trimStart();

  // Single-line comments
  if (trimmed.startsWith("//")) return true;

  // JSDoc / block comment lines (lines starting with *)
  if (trimmed.startsWith("*")) return true;

  // Import declarations (import { x } from "..." or import "...")
  if (/^\s*import\b/.test(line)) return true;

  // Re-export declarations (export { x } from "..." or export * from "...")
  if (/^\s*export\s+(type\s+)?\{/.test(line)) return true;
  if (/^\s*export\s+\*/.test(line)) return true;

  // Multi-line import/re-export continuation (e.g., `} from "../api/sse";`)
  if (/^\s*\}?\s*from\s+['"]/.test(line)) return true;

  return false;
}

/**
 * Recursively collect .ts/.tsx files under dir.
 */
export function walkDir(dir) {
  const results = [];
  let entries;
  try {
    entries = readdirSync(dir, { withFileTypes: true });
  } catch {
    return results;
  }
  for (const entry of entries) {
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

/**
 * Scan a single file for hardcoded URL violations.
 * Returns an array of { relPath, line, message, source } violations.
 */
export function scanFile(filePath, frontendDir, contents) {
  if (contents === undefined) {
    try {
      contents = readFileSync(filePath, "utf-8");
    } catch {
      return [];
    }
  }

  const relPath = relative(frontendDir, filePath).replaceAll(sep, "/");
  const lines = contents.split("\n");
  const violations = [];
  let inBlockComment = false;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const trimmed = line.trimStart();

    // Track block comment state
    if (!inBlockComment && trimmed.startsWith("/*")) {
      inBlockComment = true;
      if (trimmed.includes("*/")) {
        inBlockComment = false;
      }
      continue;
    }
    if (inBlockComment) {
      if (trimmed.includes("*/")) {
        inBlockComment = false;
      }
      continue;
    }

    if (shouldSkipLine(line)) continue;

    for (const rule of RULES) {
      if (rule.pattern.test(line)) {
        violations.push({
          relPath,
          line: i + 1,
          message: rule.message,
          source: line.trimEnd(),
        });
      }
    }
  }

  return violations;
}

/**
 * Scan all .ts/.tsx files under src/ for hardcoded URL violations.
 * Returns { violations, allowlistedCount, scannedCount }.
 */
export function scanAll(frontendDir, allowlist = ALLOWLIST) {
  const srcDir = join(frontendDir, "src");
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
  let scannedCount = 0;
  let allowlistedCount = 0;

  for (const filePath of allFiles) {
    const relPath = relative(frontendDir, filePath).replaceAll(sep, "/");

    if (isExcluded(relPath)) continue;

    scannedCount++;
    const fileViolations = scanFile(filePath, frontendDir);

    for (const v of fileViolations) {
      const key = `${v.relPath}:${v.line}`;
      if (allowlist.has(key)) {
        allowlistedCount++;
      } else {
        violations.push(v);
      }
    }
  }

  return { violations, allowlistedCount, scannedCount };
}

function main() {
  const scriptDir = fileURLToPath(new URL(".", import.meta.url));
  const frontendDir = join(scriptDir, "..");

  const result = scanAll(frontendDir);

  if (result.error) {
    console.error(result.error);
    process.exit(result.exitCode);
  }

  const { violations, allowlistedCount, scannedCount } = result;

  if (violations.length === 0) {
    console.log(
      `\u2713 No hardcoded URLs found (scanned ${scannedCount} files${allowlistedCount ? `, ${allowlistedCount} allowlisted` : ""})`,
    );
    process.exit(0);
  }

  for (const v of violations) {
    console.error(`${v.relPath}:${v.line}: ${v.message}`);
    console.error(`  > ${v.source}`);
  }
  console.error(
    `\n\u2717 ${violations.length} hardcoded URL(s) in ${new Set(violations.map((v) => v.relPath)).size} file(s)`,
  );
  process.exit(1);
}

// Only run main() when executed directly (not when imported).
const isMainModule =
  process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1];

if (isMainModule) {
  main();
}
