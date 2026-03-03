#!/usr/bin/env node
// AST-based linter enforcing component module isolation.
// Components must import from other components only through barrel exports (index.ts),
// never from internal modules directly.

import { readFileSync, readdirSync, statSync } from "fs";
import { join, relative, extname, sep, dirname, normalize } from "path";
import { fileURLToPath } from "url";
import ts from "typescript";

// Known legacy violations allowlisted by { source, target } pairs.
// source = relative path from frontend root (e.g. "src/components/SwimLaneBoard/SwimLaneBoard.tsx")
// target = the internal import path (e.g. "@/components/KanbanBoard/columnConfigs")
export const ALLOWLIST = [
  {
    source: "src/components/SwimLaneBoard/SwimLaneBoard.tsx",
    target: "@/components/KanbanBoard/columnConfigs",
  },
  {
    source: "src/components/SwimLaneBoard/SwimLaneBoard.tsx",
    target: "@/components/KanbanBoard/types",
  },
  {
    source: "src/components/SwimLaneBoard/SwimLaneBoard.tsx",
    target: "@/components/StatusColumn/utils",
  },
  {
    source: "src/components/SwimLane/SwimLane.tsx",
    target: "@/components/KanbanBoard/types",
  },
  {
    source: "src/components/StatusDropdown/StatusDropdown.tsx",
    target: "@/components/StatusColumn/utils",
  },
  {
    source: "src/components/GraphControls/GraphControls.tsx",
    target: "@/components/StatusColumn/utils",
  },
  {
    source: "src/components/KanbanBoard/KanbanBoard.tsx",
    target: "@/components/StatusColumn/utils",
  },
  {
    source: "src/components/IssueDetailPanel/CommentsSection.tsx",
    target: "@/components/table/columns",
  },
  {
    source: "src/components/IssueDetailPanel/IssueHeader.tsx",
    target: "../StatusColumn/utils",
  },
];

/**
 * Extract the top-level component directory name from a file path
 * relative to the components directory.
 * e.g. "SwimLaneBoard/SwimLaneBoard.tsx" → "SwimLaneBoard"
 * e.g. "table/columns.ts" → "table"
 */
function getComponentName(relToComponents) {
  const first = relToComponents.split("/")[0];
  return first || null;
}

/**
 * Returns true if a file is a test file that should be excluded.
 */
function isTestFile(filePath) {
  const parts = filePath.split("/");
  const base = parts[parts.length - 1];
  if (parts.includes("__tests__")) return true;
  if (base.endsWith(".test.ts") || base.endsWith(".test.tsx")) return true;
  if (base.endsWith(".spec.ts") || base.endsWith(".spec.tsx")) return true;
  return false;
}

/**
 * Check resolved component-relative segments for a cross-component violation.
 * segments = path split by "/" relative to src/components/.
 * Returns { targetComponent, targetModule, importPath } if violation, or null.
 */
function checkSegments(segments, sourceComponent, importPath) {
  if (!segments[0]) return null;

  const targetComponent = segments[0];

  // Same component — not a violation
  if (targetComponent === sourceComponent) return null;

  // Bare import (e.g. @/components/KanbanBoard or ../KanbanBoard) → barrel import, OK
  if (segments.length === 1) return null;

  // Explicit /index import — equivalent to barrel, OK
  if (segments.length === 2 && segments[1] === "index") return null;

  // Cross-component internal import — violation
  return {
    targetComponent,
    targetModule: segments.slice(1).join("/"),
    importPath,
  };
}

/**
 * Parse an import path and determine if it's a cross-component internal import.
 * relToComponents is the source file path relative to src/components/ (e.g. "IssueDetailPanel/IssueHeader.tsx").
 * Returns { targetComponent, targetModule, importPath } if violation, or null.
 */
function parseImportTarget(importPath, sourceComponent, relToComponents) {
  // Handle @/ alias imports
  const aliasPrefix = "@/components/";
  if (importPath.startsWith(aliasPrefix)) {
    const rest = importPath.slice(aliasPrefix.length);
    const segments = rest.split("/");
    return checkSegments(segments, sourceComponent, importPath);
  }

  // Handle relative imports that may cross component boundaries
  if (importPath.startsWith("../")) {
    const sourceDir = dirname(relToComponents);
    const resolved = normalize(join(sourceDir, importPath)).replaceAll(sep, "/");

    // If resolved goes above components dir (starts with ..), it's outside scope
    if (resolved.startsWith("..")) return null;

    const segments = resolved.split("/");
    return checkSegments(segments, sourceComponent, importPath);
  }

  // Non-component @/ imports or same-directory relative imports — not relevant
  return null;
}

/**
 * Recursively collect .ts/.tsx files under dir, excluding test files.
 */
function collectFiles(dir, rootDir) {
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
      if (entry.name === "node_modules" || entry.name === "__tests__") continue;
      results.push(...collectFiles(full, rootDir));
    } else {
      const ext = extname(entry.name);
      if (ext === ".ts" || ext === ".tsx") {
        const rel = relative(rootDir, full).replaceAll(sep, "/");
        if (!isTestFile(rel)) {
          results.push(full);
        }
      }
    }
  }
  return results;
}

/**
 * Walk every node in the AST, calling callback for each.
 */
function walkNode(node, callback) {
  callback(node);
  ts.forEachChild(node, (child) => walkNode(child, callback));
}

/**
 * Extract the module specifier string from an AST node.
 * Handles import/export declarations and dynamic import() expressions.
 */
function getModuleSpecifier(node) {
  // Static: import ... from "..." / export ... from "..."
  if (
    (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) &&
    node.moduleSpecifier &&
    ts.isStringLiteral(node.moduleSpecifier)
  ) {
    return node.moduleSpecifier.text;
  }

  // Dynamic: import("...")
  if (
    ts.isCallExpression(node) &&
    node.expression.kind === ts.SyntaxKind.ImportKeyword &&
    node.arguments.length >= 1 &&
    ts.isStringLiteral(node.arguments[0])
  ) {
    return node.arguments[0].text;
  }

  return null;
}

/**
 * Scan a single file for cross-component boundary violations.
 * Returns an array of { relPath, line, importPath, sourceComponent, targetComponent, targetModule }.
 */
export function scanFile(filePath, sourceRoot, contents) {
  if (contents === undefined) {
    try {
      contents = readFileSync(filePath, "utf-8");
    } catch {
      return [];
    }
  }

  const relPath = sourceRoot
    ? relative(sourceRoot, filePath).replaceAll(sep, "/")
    : filePath;

  // Determine source component from the file path
  const componentsPrefix = "src/components/";
  if (!relPath.startsWith(componentsPrefix)) return [];

  const relToComponents = relPath.slice(componentsPrefix.length);
  const sourceComponent = getComponentName(relToComponents);
  if (!sourceComponent) return [];

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
    const specifier = getModuleSpecifier(node);
    if (!specifier) return;

    const result = parseImportTarget(specifier, sourceComponent, relToComponents);
    if (result) {
      const { line } = sourceFile.getLineAndCharacterOfPosition(
        node.getStart(sourceFile),
      );
      violations.push({
        relPath,
        line: line + 1,
        importPath: result.importPath,
        sourceComponent,
        targetComponent: result.targetComponent,
        targetModule: result.targetModule,
      });
    }
  });

  return violations;
}

/**
 * Check if a violation is in the allowlist.
 */
function isAllowlisted(violation) {
  return ALLOWLIST.some(
    (entry) =>
      entry.source === violation.relPath && entry.target === violation.importPath,
  );
}

/**
 * Scan all component files for boundary violations.
 * Returns { violations, allowlistedCount, scannedCount }.
 */
export function scanAll(rootDir) {
  const componentsDir = join(rootDir, "src", "components");
  let stat;
  try {
    stat = statSync(componentsDir);
  } catch {
    return {
      error: `Error: src/components/ directory not found at ${componentsDir}`,
      exitCode: 2,
    };
  }
  if (!stat.isDirectory()) {
    return { error: `Error: ${componentsDir} is not a directory`, exitCode: 2 };
  }

  const files = collectFiles(componentsDir, rootDir);
  const violations = [];
  let allowlistedCount = 0;

  for (const filePath of files) {
    const fileViolations = scanFile(filePath, rootDir);
    for (const v of fileViolations) {
      if (isAllowlisted(v)) {
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

  const result = scanAll(rootDir);

  if (result.error) {
    console.error(result.error);
    process.exit(result.exitCode);
  }

  const { violations, allowlistedCount, scannedCount } = result;

  if (violations.length === 0) {
    console.log(
      `\u2713 No component boundary violations (scanned ${scannedCount} files${allowlistedCount ? `, ${allowlistedCount} allowlisted` : ""})`,
    );
    process.exit(0);
  }

  for (const v of violations) {
    console.error(
      `${v.relPath}:${v.line}  imports ${v.importPath} (use @/components/${v.targetComponent} instead)`,
    );
  }
  console.error(
    `\n\u2717 ${violations.length} component boundary violation(s) found`,
  );
  console.error(
    "  Components must import from barrel exports (index.ts), not internal modules.",
  );
  process.exit(1);
}

// Only run main() when executed directly (not when imported).
const isMainModule =
  process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1];

if (isMainModule) {
  main();
}
