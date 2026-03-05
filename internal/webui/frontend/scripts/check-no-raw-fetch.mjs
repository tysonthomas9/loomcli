#!/usr/bin/env node
// AST-based linter that bans direct fetch() calls outside src/api/.
// Components, hooks, and utils must use the API client (src/api/client.ts).

import { readFileSync, readdirSync } from "fs";
import { join, relative, extname, sep } from "path";
import { fileURLToPath } from "url";
import ts from "typescript";

// Directories to exclude when scanning src/ (relative names, not paths).
const EXCLUDE_DIRS = new Set(["api", "__tests__", "node_modules"]);

// Allowlist of "relPath:line" entries permitted to use raw fetch().
// Currently empty — the codebase is clean.
export const ALLOWLIST = new Set([]);

/**
 * Recursively strip parentheses, type assertions, and non-null assertions
 * to expose the underlying expression node.
 */
function unwrapExpression(node) {
  while (
    ts.isParenthesizedExpression(node) ||
    ts.isAsExpression(node) ||
    ts.isNonNullExpression(node) ||
    ts.isTypeAssertionExpression(node)
  ) {
    node = node.expression;
  }
  return node;
}

/**
 * Returns true if node is a CallExpression calling fetch(),
 * globalThis.fetch(), or window.fetch().
 */
function isFetchCall(node) {
  if (!ts.isCallExpression(node)) return false;

  const callee = unwrapExpression(node.expression);

  // bare fetch()
  if (ts.isIdentifier(callee) && callee.text === "fetch") {
    return true;
  }

  // globalThis.fetch() or window.fetch()
  if (
    ts.isPropertyAccessExpression(callee) &&
    ts.isIdentifier(callee.expression) &&
    (callee.expression.text === "globalThis" ||
      callee.expression.text === "window") &&
    callee.name.text === "fetch"
  ) {
    return true;
  }

  return false;
}

/**
 * Walk every node in the AST, calling callback for each.
 */
function walkNode(node, callback) {
  callback(node);
  ts.forEachChild(node, (child) => walkNode(child, callback));
}

/**
 * Recursively collect .ts/.tsx files under dir, skipping EXCLUDE_DIRS.
 */
function collectFiles(dir) {
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
      if (EXCLUDE_DIRS.has(entry.name)) continue;
      results.push(...collectFiles(full));
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
 * Scan a single file for raw fetch() calls.
 * Returns an array of { relPath, line } violations.
 */
export function scanFile(filePath, sourceRoot, contents) {
  if (contents === undefined) {
    try {
      contents = readFileSync(filePath, "utf-8");
    } catch {
      return [];
    }
  }

  const scriptKind = filePath.endsWith(".tsx")
    ? ts.ScriptKind.TSX
    : ts.ScriptKind.TS;

  const sourceFile = ts.createSourceFile(
    filePath,
    contents,
    ts.ScriptTarget.Latest,
    true,
    scriptKind,
  );

  const violations = [];

  walkNode(sourceFile, (node) => {
    if (isFetchCall(node)) {
      const { line } = sourceFile.getLineAndCharacterOfPosition(
        node.getStart(sourceFile),
      );
      const relPath = sourceRoot
        ? relative(sourceRoot, filePath).replaceAll(sep, "/")
        : filePath;
      violations.push({ relPath, line: line + 1 });
    }
  });

  return violations;
}

/**
 * Scan all files under src/ (excluding EXCLUDE_DIRS) for raw fetch() calls.
 * Returns { violations, allowlistedCount, scannedCount }.
 */
export function scanAll(rootDir) {
  const violations = [];
  let allowlistedCount = 0;

  const srcDir = join(rootDir, "src");
  const files = collectFiles(srcDir);

  for (const filePath of files) {
    const fileViolations = scanFile(filePath, rootDir);
    for (const v of fileViolations) {
      const key = `${v.relPath}:${v.line}`;
      if (ALLOWLIST.has(key)) {
        allowlistedCount++;
      } else {
        violations.push(v);
      }
    }
  }

  return { violations, allowlistedCount, scannedCount: files.length };
}

function main() {
  const scriptDir = fileURLToPath(new URL(".", import.meta.url));
  const rootDir = join(scriptDir, "..");

  const { violations, allowlistedCount, scannedCount } = scanAll(rootDir);

  if (violations.length === 0) {
    console.log(
      `\u2713 No raw fetch() calls found outside src/api/ (scanned ${scannedCount} files${allowlistedCount ? `, ${allowlistedCount} allowlisted` : ""})`,
    );
    process.exit(0);
  }

  for (const v of violations) {
    console.error(`${v.relPath}:${v.line}  fetch() call`);
  }
  console.error(
    `\n\u2717 ${violations.length} raw fetch() call(s) found outside src/api/`,
  );
  console.error("  Use the API client (src/api/client.ts) instead.");
  process.exit(1);
}

// Only run main() when executed directly (not when imported).
const isMainModule =
  process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1];

if (isMainModule) {
  main();
}
