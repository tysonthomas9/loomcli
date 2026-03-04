/**
 * Unit tests for the check-no-raw-fetch.mjs AST-based linter.
 *
 * Tests the exported scanFile and scanAll functions directly, plus
 * subprocess integration tests for exit codes and output formatting.
 */

import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "fs";
import { join } from "path";
import { tmpdir } from "os";
import { describe, it, expect, beforeEach, afterEach } from "vitest";

import { scanFile, scanAll, ALLOWLIST } from "../check-no-raw-fetch.mjs";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Create a temp directory that acts as a fake frontend project root. */
function makeTmpProject() {
  const root = mkdtempSync(join(tmpdir(), "check-no-raw-fetch-test-"));
  return root;
}

/** Write a source file under the project root. */
function writeSource(root, relPath, content) {
  const full = join(root, relPath);
  mkdirSync(join(full, ".."), { recursive: true });
  writeFileSync(full, content);
}

// ---------------------------------------------------------------------------
// scanFile — direct function tests
// ---------------------------------------------------------------------------

describe("scanFile", () => {
  it("detects bare fetch()", () => {
    const violations = scanFile("test.ts", null, `const res = fetch("/api/foo");`);
    expect(violations).toHaveLength(1);
    expect(violations[0].line).toBe(1);
  });

  it("detects globalThis.fetch()", () => {
    const violations = scanFile(
      "test.ts",
      null,
      `const res = globalThis.fetch("/api/foo");`,
    );
    expect(violations).toHaveLength(1);
    expect(violations[0].line).toBe(1);
  });

  it("detects parenthesized fetch: (fetch)(url)", () => {
    const violations = scanFile("test.ts", null, `const res = (fetch)("/api/foo");`);
    expect(violations).toHaveLength(1);
  });

  it("detects type-asserted fetch: (fetch as any)(url)", () => {
    const violations = scanFile(
      "test.ts",
      null,
      `const res = (fetch as any)("/api/foo");`,
    );
    expect(violations).toHaveLength(1);
  });

  it("detects window.fetch()", () => {
    const violations = scanFile(
      "test.ts",
      null,
      `const res = window.fetch("/api/foo");`,
    );
    expect(violations).toHaveLength(1);
    expect(violations[0].line).toBe(1);
  });

  it("ignores fetch in comments", () => {
    const violations = scanFile(
      "test.ts",
      null,
      `// fetch("/api/foo");\nconst x = 1;`,
    );
    expect(violations).toHaveLength(0);
  });

  it("ignores fetch in string literals", () => {
    const violations = scanFile(
      "test.ts",
      null,
      `const s = "fetch('/api/foo')";`,
    );
    expect(violations).toHaveLength(0);
  });

  it("ignores non-fetch function calls", () => {
    const violations = scanFile(
      "test.ts",
      null,
      `const res = fetchData("/api/foo");`,
    );
    expect(violations).toHaveLength(0);
  });

  it("ignores template literals containing fetch", () => {
    const violations = scanFile(
      "test.ts",
      null,
      "const s = `fetch('/api/foo')`;",
    );
    expect(violations).toHaveLength(0);
  });

  it("reports correct line numbers", () => {
    const source = [
      "const a = 1;",
      "const b = 2;",
      "const c = 3;",
      "const d = 4;",
      "const res = fetch('/api');",
    ].join("\n");
    const violations = scanFile("test.ts", null, source);
    expect(violations).toHaveLength(1);
    expect(violations[0].line).toBe(5);
  });

  it("detects multiple fetch calls in one file", () => {
    const source = [
      "const a = fetch('/one');",
      "const b = 'not a call';",
      "const c = globalThis.fetch('/two');",
    ].join("\n");
    const violations = scanFile("test.ts", null, source);
    expect(violations).toHaveLength(2);
    expect(violations[0].line).toBe(1);
    expect(violations[1].line).toBe(3);
  });

  it("handles TSX files with JSX", () => {
    const source = [
      "import React from 'react';",
      "export function Comp() {",
      "  const data = fetch('/api');",
      "  return <div>{JSON.stringify(data)}</div>;",
      "}",
    ].join("\n");
    const violations = scanFile("test.tsx", null, source);
    expect(violations).toHaveLength(1);
    expect(violations[0].line).toBe(3);
  });

  it("computes relative paths when sourceRoot is provided", () => {
    const violations = scanFile(
      "/project/src/components/Foo.ts",
      "/project",
      `fetch("/api");`,
    );
    expect(violations).toHaveLength(1);
    expect(violations[0].relPath).toBe("src/components/Foo.ts");
  });

  it("returns empty array for files with no fetch calls", () => {
    const violations = scanFile("test.ts", null, "const x = 1;\nconst y = 2;\n");
    expect(violations).toHaveLength(0);
  });

  it("returns empty array for empty files", () => {
    const violations = scanFile("test.ts", null, "");
    expect(violations).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------
// scanAll — integration with file system
// ---------------------------------------------------------------------------

describe("scanAll", () => {
  let root;

  beforeEach(() => {
    root = makeTmpProject();
  });

  afterEach(() => {
    rmSync(root, { recursive: true, force: true });
  });

  it("scans components, hooks, and utils directories", () => {
    writeSource(root, "src/components/Foo.ts", `fetch("/api");`);
    writeSource(root, "src/hooks/useBar.ts", `globalThis.fetch("/api");`);
    writeSource(root, "src/utils/helper.ts", `fetch("/api");`);

    const result = scanAll(root);
    expect(result.violations).toHaveLength(3);
    expect(result.scannedCount).toBe(3);
  });

  it("does not scan src/api/ directory", () => {
    writeSource(root, "src/api/client.ts", `fetch("/api");`);
    writeSource(root, "src/components/Foo.ts", "const x = 1;");

    const result = scanAll(root);
    expect(result.violations).toHaveLength(0);
    expect(result.scannedCount).toBe(1);
  });

  it("returns 0 violations for clean codebase", () => {
    writeSource(root, "src/components/Foo.ts", "const x = 1;");
    writeSource(root, "src/hooks/useBar.ts", "const y = 2;");
    writeSource(root, "src/utils/helper.ts", "const z = 3;");

    const result = scanAll(root);
    expect(result.violations).toHaveLength(0);
    expect(result.scannedCount).toBe(3);
  });

  it("skips missing scan directories gracefully", () => {
    // Only create components, not hooks or utils
    writeSource(root, "src/components/Foo.ts", "const x = 1;");

    const result = scanAll(root);
    expect(result.violations).toHaveLength(0);
    expect(result.scannedCount).toBe(1);
  });

  it("scans nested directories", () => {
    writeSource(
      root,
      "src/components/deep/nested/Foo.tsx",
      `const res = fetch("/api");`,
    );

    const result = scanAll(root);
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0].relPath).toContain("deep/nested/Foo.tsx");
  });

  it("reports correct relPath format", () => {
    writeSource(root, "src/components/Foo.ts", `fetch("/api");`);

    const result = scanAll(root);
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0].relPath).toBe("src/components/Foo.ts");
  });
});

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

describe("constants", () => {
  it("ALLOWLIST is an empty Set", () => {
    expect(ALLOWLIST).toBeInstanceOf(Set);
    expect(ALLOWLIST.size).toBe(0);
  });
});
