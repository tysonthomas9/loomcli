#!/usr/bin/env node

import { readFileSync } from "fs";
import { pathToFileURL } from "url";
import ts from "typescript";

export function parseRanges(input) {
  const ranges = new Map();
  for (const line of input.split("\n")) {
    if (line.trim() === "") continue;
    const fields = line.split("\t");
    const start = Number(fields[1]);
    const end = Number(fields[2]);
    if (
      fields.length !== 3 ||
      !Number.isInteger(start) ||
      !Number.isInteger(end) ||
      start < 1 ||
      end < start
    ) {
      throw new Error(`malformed range line ${JSON.stringify(line)}`);
    }
    if (!ranges.has(fields[0])) ranges.set(fields[0], []);
    ranges.get(fields[0]).push({ start, end });
  }
  return ranges;
}

function scriptKindFor(path) {
  return path.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
}

function isJSDocNode(node) {
  return (
    node.kind >= ts.SyntaxKind.FirstJSDocNode &&
    node.kind <= ts.SyntaxKind.LastJSDocNode
  );
}

function isJsxText(node) {
  return node.kind === ts.SyntaxKind.JsxText;
}

function commentRangesAt(text, pos) {
  return [
    ...(ts.getLeadingCommentRanges(text, pos) ?? []),
    ...(ts.getTrailingCommentRanges(text, pos) ?? []),
  ];
}

export function findCommentRanges(path, text) {
  const sourceFile = ts.createSourceFile(
    path,
    text,
    ts.ScriptTarget.Latest,
    false,
    scriptKindFor(path),
  );
  const found = new Map();
  const visit = (node) => {
    if (isJSDocNode(node) || isJsxText(node)) return;
    const children = node.getChildren(sourceFile);
    if (children.length === 0 && node.kind !== ts.SyntaxKind.SyntaxList) {
      for (const range of commentRangesAt(text, node.pos)) {
        found.set(range.pos, range);
      }
    }
    for (const child of children) visit(child);
  };
  visit(sourceFile);
  const shebang = ts.getShebang(text);
  return [...found.values()]
    .filter((range) => !(shebang && range.pos === 0))
    .sort((a, b) => a.pos - b.pos)
    .map((range) => ({
      startLine: sourceFile.getLineAndCharacterOfPosition(range.pos).line + 1,
      endLine: sourceFile.getLineAndCharacterOfPosition(range.end).line + 1,
    }));
}

function isAdded(line, added) {
  return added.some((r) => line >= r.start && line <= r.end);
}

export function scanSource(path, text, added) {
  const flagged = new Set();
  for (const comment of findCommentRanges(path, text)) {
    for (let line = comment.startLine; line <= comment.endLine; line++) {
      if (isAdded(line, added)) flagged.add(line);
    }
  }
  const lines = text.split("\n");
  return [...flagged]
    .sort((a, b) => a - b)
    .map((line) => ({ path, line, text: (lines[line - 1] ?? "").trim() }));
}

export function run(input, readFile, write) {
  let ranges;
  try {
    ranges = parseRanges(input);
  } catch (err) {
    write(`check-no-new-comments: ${err.message}\n`);
    return 2;
  }
  const violations = [];
  for (const [path, added] of ranges) {
    let text;
    try {
      text = readFile(path);
    } catch (err) {
      write(`check-no-new-comments: read ${path}: ${err.message}\n`);
      return 2;
    }
    violations.push(...scanSource(path, text, added));
  }
  for (const v of violations) write(`${v.path}:${v.line}: ${v.text}\n`);
  return violations.length > 0 ? 1 : 0;
}

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) {
  process.exitCode = run(
    readFileSync(0, "utf8"),
    (path) => readFileSync(path, "utf8"),
    (s) => process.stdout.write(s),
  );
}
