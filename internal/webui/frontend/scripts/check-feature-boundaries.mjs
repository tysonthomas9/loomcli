#!/usr/bin/env node

import { existsSync, readFileSync, readdirSync } from "fs";
import { dirname, extname, join, normalize, relative, sep } from "path";
import { fileURLToPath } from "url";
import ts from "typescript";

function slash(path) {
  return path.replaceAll(sep, "/");
}

function featureOf(path) {
  return /^src\/features\/([^/]+)(?:\/|$)/.exec(slash(path))?.[1] ?? null;
}

function isTest(path) {
  const normalized = slash(path);
  return (
    normalized.includes("/__tests__/") ||
    /\.(?:test|spec)\.[cm]?[jt]sx?$/.test(normalized)
  );
}

function moduleSpecifier(node) {
  if (
    (ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) &&
    node.moduleSpecifier &&
    ts.isStringLiteral(node.moduleSpecifier)
  ) {
    return node.moduleSpecifier.text;
  }
  if (
    ts.isCallExpression(node) &&
    node.expression.kind === ts.SyntaxKind.ImportKeyword &&
    node.arguments.length === 1 &&
    ts.isStringLiteral(node.arguments[0])
  ) {
    return node.arguments[0].text;
  }
  return null;
}

function walk(node, visit) {
  visit(node);
  ts.forEachChild(node, (child) => walk(child, visit));
}

export function scanFile(filePath, contents) {
  const sourceFeature = featureOf(filePath);
  const source = ts.createSourceFile(
    filePath,
    contents,
    ts.ScriptTarget.Latest,
    true,
    filePath.endsWith("x") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
  const violations = [];

  walk(source, (node) => {
    const specifier = moduleSpecifier(node);
    if (!specifier) return;

    const alias = /^@\/features\/([^/]+)(?:\/(.+))?$/.exec(specifier);
    if (alias) {
      const [, targetFeature, internalPath] = alias;
      if (sourceFeature && sourceFeature !== targetFeature) {
        violations.push({
          filePath,
          specifier,
          reason: `feature ${sourceFeature} cannot import feature ${targetFeature}`,
        });
      } else if (!sourceFeature && internalPath) {
        violations.push({
          filePath,
          specifier,
          reason: `import ${targetFeature} through @/features/${targetFeature}`,
        });
      }
      return;
    }

    if (!sourceFeature || !specifier.startsWith(".")) return;
    const resolved = slash(normalize(join(dirname(filePath), specifier)));
    const targetFeature = featureOf(resolved);
    if (targetFeature && targetFeature !== sourceFeature) {
      violations.push({
        filePath,
        specifier,
        reason: `feature ${sourceFeature} cannot import feature ${targetFeature}`,
      });
    }
  });

  return violations;
}

function collectFiles(directory, root) {
  const files = [];
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const absolute = join(directory, entry.name);
    if (entry.isDirectory()) {
      if (entry.name !== "node_modules") {
        files.push(...collectFiles(absolute, root));
      }
      continue;
    }
    if (![".ts", ".tsx", ".mts", ".cts"].includes(extname(entry.name))) {
      continue;
    }
    const path = slash(relative(root, absolute));
    if (!isTest(path)) files.push({ absolute, path });
  }
  return files;
}

export function scanAll(root) {
  const src = join(root, "src");
  const features = join(src, "features");
  const violations = [];

  if (existsSync(features)) {
    for (const entry of readdirSync(features, { withFileTypes: true })) {
      if (!entry.isDirectory()) continue;
      if (!existsSync(join(features, entry.name, "index.ts"))) {
        violations.push({
          filePath: slash(relative(root, join(features, entry.name))),
          specifier: "",
          reason: "feature is missing its public index.ts entry",
        });
      }
    }
  }

  const files = collectFiles(src, root);
  for (const file of files) {
    violations.push(
      ...scanFile(file.path, readFileSync(file.absolute, "utf8")),
    );
  }
  return { violations, scannedCount: files.length };
}

function main() {
  const scriptDir = fileURLToPath(new URL(".", import.meta.url));
  const root = join(scriptDir, "..");
  const result = scanAll(root);
  if (result.violations.length === 0) {
    console.log(
      `✓ No frontend feature boundary violations (scanned ${result.scannedCount} files)`,
    );
    return;
  }
  for (const violation of result.violations) {
    console.error(
      `${violation.filePath}: ${violation.specifier || "<feature>"} — ${violation.reason}`,
    );
  }
  process.exitCode = 1;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main();
