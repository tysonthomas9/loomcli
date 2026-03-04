/**
 * Unit tests for the check-no-hardcoded-urls.mjs regex-based linter.
 *
 * Tests the exported scanFile, scanAll, isExcluded functions directly,
 * plus filesystem integration tests.
 */

import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "fs";
import { join } from "path";
import { tmpdir } from "os";
import { describe, it, expect, beforeEach, afterEach } from "vitest";

import {
  scanFile,
  scanAll,
  isExcluded,
  walkDir,
  ALLOWLIST,
} from "../check-no-hardcoded-urls.mjs";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeTmpProject() {
  return mkdtempSync(join(tmpdir(), "check-no-hardcoded-urls-test-"));
}

function writeSource(root, relPath, content) {
  const full = join(root, relPath);
  mkdirSync(join(full, ".."), { recursive: true });
  writeFileSync(full, content);
}

// ---------------------------------------------------------------------------
// isExcluded
// ---------------------------------------------------------------------------

describe("isExcluded", () => {
  it("excludes src/api/ directory", () => {
    expect(isExcluded("src/api/client.ts")).toBe(true);
    expect(isExcluded("src/api/__tests__/client.test.ts")).toBe(true);
  });

  it("excludes test files", () => {
    expect(isExcluded("src/hooks/useSSE.test.ts")).toBe(true);
    expect(isExcluded("src/components/Foo.test.tsx")).toBe(true);
    expect(isExcluded("src/hooks/useSSE.spec.ts")).toBe(true);
  });

  it("excludes __tests__ directories", () => {
    expect(isExcluded("src/components/__tests__/Foo.tsx")).toBe(true);
  });

  it("excludes test-utils", () => {
    expect(isExcluded("src/test-utils/helpers.ts")).toBe(true);
  });

  it("excludes TestFixtures.tsx", () => {
    expect(isExcluded("src/TestFixtures.tsx")).toBe(true);
  });

  it("does not exclude normal source files", () => {
    expect(isExcluded("src/components/Foo.tsx")).toBe(false);
    expect(isExcluded("src/hooks/useSSE.ts")).toBe(false);
    expect(isExcluded("src/utils/format.ts")).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// scanFile — /api/ path detection
// ---------------------------------------------------------------------------

describe("scanFile — /api/ paths", () => {
  it("detects hardcoded /api/ path in string literal", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", `const url = "/api/issues";`);
    expect(v).toHaveLength(1);
    expect(v[0].line).toBe(1);
    expect(v[0].message).toContain("/api/");
  });

  it("detects /api/ in template literal", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", "const url = `/api/issues/${id}`;");
    expect(v).toHaveLength(1);
  });

  it("detects multiple violations in one file", () => {
    const source = [
      "const a = 1;",
      'const x = "/api/issues";',
      "const b = 2;",
      'const y = "/api/config";',
    ].join("\n");
    const v = scanFile("/fake/src/Foo.ts", "/fake", source);
    expect(v).toHaveLength(2);
    expect(v[0].line).toBe(2);
    expect(v[1].line).toBe(4);
  });

  it("reports correct source line preview", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", '  const url = "/api/issues";');
    expect(v[0].source).toBe('  const url = "/api/issues";');
  });
});

// ---------------------------------------------------------------------------
// scanFile — localhost detection
// ---------------------------------------------------------------------------

describe("scanFile — localhost URLs", () => {
  it("detects http://localhost", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", 'const x = "http://localhost:3000";');
    expect(v).toHaveLength(1);
    expect(v[0].message).toContain("localhost");
  });

  it("detects https://localhost", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", 'const x = "https://localhost";');
    expect(v).toHaveLength(1);
  });

  it("detects http://127.0.0.1", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", 'const x = "http://127.0.0.1:8080";');
    expect(v).toHaveLength(1);
  });

  it("detects https://127.0.0.1", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", 'const x = "https://127.0.0.1";');
    expect(v).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// scanFile — skip patterns
// ---------------------------------------------------------------------------

describe("scanFile — skip patterns", () => {
  it("skips single-line comments", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", '// fetch from /api/issues');
    expect(v).toHaveLength(0);
  });

  it("skips JSDoc comment lines", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", " * Maps to /api/issues/graph.");
    expect(v).toHaveLength(0);
  });

  it("skips block comments", () => {
    const source = ["/* This calls /api/issues", " * and more /api/stuff", " */"].join(
      "\n",
    );
    const v = scanFile("/fake/src/Foo.ts", "/fake", source);
    expect(v).toHaveLength(0);
  });

  it("skips single-line block comments", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", "/* /api/test */");
    expect(v).toHaveLength(0);
  });

  it("skips import statements", () => {
    const v = scanFile(
      "/fake/src/Foo.ts",
      "/fake",
      'import { get } from "@/api/client";',
    );
    expect(v).toHaveLength(0);
  });

  it("skips multi-line import continuations (} from ...)", () => {
    const source = [
      "import {",
      "  BeadsSSEClient,",
      "  type ConnectionState,",
      '} from "../api/sse";',
    ].join("\n");
    const v = scanFile("/fake/src/Foo.ts", "/fake", source);
    expect(v).toHaveLength(0);
  });

  it("skips re-export type declarations", () => {
    const v = scanFile(
      "/fake/src/Foo.ts",
      "/fake",
      'export type { MutationPayload } from "../api/sse";',
    );
    expect(v).toHaveLength(0);
  });

  it("skips re-export star declarations", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", 'export * from "../api/sse";');
    expect(v).toHaveLength(0);
  });

  it("skips re-export value declarations", () => {
    const v = scanFile(
      "/fake/src/Foo.ts",
      "/fake",
      'export { MutationType } from "../api/sse";',
    );
    expect(v).toHaveLength(0);
  });

  it("catches export const with /api/ (not a re-export)", () => {
    const v = scanFile(
      "/fake/src/Foo.ts",
      "/fake",
      'export const BASE = "/api/v2/items";',
    );
    expect(v).toHaveLength(1);
  });

  it("catches export function with localhost", () => {
    const v = scanFile(
      "/fake/src/Foo.ts",
      "/fake",
      'export function getUrl() { return "http://localhost:3000"; }',
    );
    expect(v).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// scanFile — edge cases
// ---------------------------------------------------------------------------

describe("scanFile — edge cases", () => {
  it("returns empty array for empty file", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", "");
    expect(v).toHaveLength(0);
  });

  it("returns empty array for clean file", () => {
    const v = scanFile("/fake/src/Foo.ts", "/fake", "const x = 1;\nconst y = 2;\n");
    expect(v).toHaveLength(0);
  });

  it("computes correct relPath", () => {
    const v = scanFile(
      "/project/src/components/Foo.ts",
      "/project",
      'const url = "/api/test";',
    );
    expect(v[0].relPath).toBe("src/components/Foo.ts");
  });

  it("resumes scanning after block comment ends", () => {
    const source = [
      "/* block comment with /api/foo */",
      'const url = "/api/issues";',
    ].join("\n");
    const v = scanFile("/fake/src/Foo.ts", "/fake", source);
    expect(v).toHaveLength(1);
    expect(v[0].line).toBe(2);
  });
});

// ---------------------------------------------------------------------------
// scanAll — filesystem integration
// ---------------------------------------------------------------------------

describe("scanAll", () => {
  let root;

  beforeEach(() => {
    root = makeTmpProject();
  });

  afterEach(() => {
    rmSync(root, { recursive: true, force: true });
  });

  it("scans src/ files and finds violations", () => {
    writeSource(root, "src/components/Foo.ts", 'const url = "/api/issues";');
    writeSource(root, "src/hooks/useBar.ts", 'const x = "http://localhost:3000";');

    const result = scanAll(root, new Set());
    expect(result.violations).toHaveLength(2);
    expect(result.scannedCount).toBe(2);
  });

  it("excludes src/api/ directory", () => {
    writeSource(root, "src/api/client.ts", 'const url = "/api/issues";');
    writeSource(root, "src/components/Foo.ts", "const x = 1;");

    const result = scanAll(root, new Set());
    expect(result.violations).toHaveLength(0);
  });

  it("excludes test files", () => {
    writeSource(root, "src/components/Foo.test.ts", 'const url = "/api/issues";');
    writeSource(root, "src/components/__tests__/Bar.ts", 'const url = "/api/issues";');
    writeSource(root, "src/components/Clean.ts", "const x = 1;");

    const result = scanAll(root, new Set());
    expect(result.violations).toHaveLength(0);
    expect(result.scannedCount).toBe(1);
  });

  it("respects allowlist", () => {
    writeSource(root, "src/components/Foo.ts", 'const url = "/api/issues";');
    const allowlist = new Set(["src/components/Foo.ts:1"]);

    const result = scanAll(root, allowlist);
    expect(result.violations).toHaveLength(0);
    expect(result.allowlistedCount).toBe(1);
  });

  it("returns error if src/ not found", () => {
    // Root exists but no src/ directory
    const result = scanAll(root, new Set());
    expect(result.error).toContain("src/ directory not found");
    expect(result.exitCode).toBe(2);
  });

  it("returns 0 violations for clean project", () => {
    writeSource(root, "src/components/Foo.ts", "const x = 1;");
    writeSource(root, "src/hooks/useBar.ts", "const y = 2;");

    const result = scanAll(root, new Set());
    expect(result.violations).toHaveLength(0);
    expect(result.scannedCount).toBe(2);
  });
});

// ---------------------------------------------------------------------------
// walkDir
// ---------------------------------------------------------------------------

describe("walkDir", () => {
  let root;

  beforeEach(() => {
    root = makeTmpProject();
  });

  afterEach(() => {
    rmSync(root, { recursive: true, force: true });
  });

  it("collects .ts and .tsx files recursively", () => {
    writeSource(root, "a.ts", "");
    writeSource(root, "b.tsx", "");
    writeSource(root, "sub/c.ts", "");

    const files = walkDir(root);
    expect(files).toHaveLength(3);
  });

  it("skips node_modules", () => {
    writeSource(root, "a.ts", "");
    writeSource(root, "node_modules/pkg/index.ts", "");

    const files = walkDir(root);
    expect(files).toHaveLength(1);
  });

  it("ignores non-ts files", () => {
    writeSource(root, "a.ts", "");
    writeSource(root, "b.js", "");
    writeSource(root, "c.css", "");

    const files = walkDir(root);
    expect(files).toHaveLength(1);
  });

  it("returns empty for nonexistent directory", () => {
    const files = walkDir(join(root, "nonexistent"));
    expect(files).toHaveLength(0);
  });
});

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

describe("ALLOWLIST", () => {
  it("is a Set with TerminalPanel entries", () => {
    expect(ALLOWLIST).toBeInstanceOf(Set);
    expect(ALLOWLIST.size).toBe(4);
    for (const entry of ALLOWLIST) {
      expect(entry).toContain("TerminalPanel");
    }
  });
});
