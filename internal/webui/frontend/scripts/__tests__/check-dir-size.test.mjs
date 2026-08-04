/**
 * Unit tests for the check-dir-size.mjs directory-size checker script.
 *
 * These tests exercise the exported helper functions directly and also run
 * the script as a subprocess to verify exit codes and output formatting.
 */

import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "fs";
import { join } from "path";
import { tmpdir } from "os";
import { execFileSync } from "child_process";
import { fileURLToPath } from "url";
import { describe, it, expect, beforeEach, afterEach } from "vitest";

import {
  shouldSkipDir,
  isCountableFile,
  walkDirs,
  checkDirSize,
  DIR_SIZE_THRESHOLD,
  EXCLUDED_DIR_NAMES,
} from "../check-dir-size.mjs";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const SCRIPT_PATH = fileURLToPath(
  new URL("../check-dir-size.mjs", import.meta.url),
);

/** Create a temp directory that acts as a fake frontend project root. */
function makeTmpProject() {
  const root = mkdtempSync(join(tmpdir(), "check-dir-size-test-"));
  const srcDir = join(root, "src");
  mkdirSync(srcDir, { recursive: true });
  return { root, srcDir };
}

/** Write an empty .ts/.tsx file at the given path under baseDir. */
function writeFile(baseDir, relPath) {
  const full = join(baseDir, relPath);
  mkdirSync(join(full, ".."), { recursive: true });
  writeFileSync(full, "");
}

/** Write N files of the given extension into a directory. */
function writeNFiles(dir, n, ext = ".ts", prefix = "f") {
  mkdirSync(dir, { recursive: true });
  for (let i = 0; i < n; i++) {
    writeFileSync(join(dir, `${prefix}${i}${ext}`), "");
  }
}

/** Run the check-dir-size.mjs logic as a subprocess against a given project dir. */
function runScript(projectRoot) {
  const inline = `
    import { checkDirSize, DIR_SIZE_THRESHOLD } from ${JSON.stringify(SCRIPT_PATH)};
    import { join } from "path";

    const frontendDir = ${JSON.stringify(projectRoot)};
    const srcDir = join(frontendDir, "src");
    const result = checkDirSize(frontendDir, srcDir);

    if (result.error) {
      process.stderr.write(result.error + "\\n");
      process.exit(result.exitCode);
    }

    const { violations, scannedCount } = result;

    if (violations.length === 0) {
      process.stdout.write("\\u2713 All directories within " + DIR_SIZE_THRESHOLD + "-file limit (" + scannedCount + " directories scanned)\\n");
      process.exit(0);
    }

    for (const v of violations) {
      process.stderr.write("  " + v.fileCount + "\\t" + v.relPath + "\\n");
    }
    process.stderr.write("\\n\\u2717 " + violations.length + " directory(ies) exceed " + DIR_SIZE_THRESHOLD + "-file limit\\n");
    process.exit(1);
  `;

  try {
    const stdout = execFileSync("node", ["--input-type=module", "-e", inline], {
      encoding: "utf-8",
      timeout: 10_000,
    });
    return { exitCode: 0, stdout, stderr: "" };
  } catch (err) {
    return {
      exitCode: err.status,
      stdout: err.stdout ?? "",
      stderr: err.stderr ?? "",
    };
  }
}

// ---------------------------------------------------------------------------
// shouldSkipDir
// ---------------------------------------------------------------------------

describe("shouldSkipDir", () => {
  it("skips __tests__ directories", () => {
    expect(shouldSkipDir("__tests__")).toBe(true);
  });

  it("skips test-utils directories", () => {
    expect(shouldSkipDir("test-utils")).toBe(true);
  });

  it("skips generated directories", () => {
    expect(shouldSkipDir("generated")).toBe(true);
  });

  it("skips node_modules directories", () => {
    expect(shouldSkipDir("node_modules")).toBe(true);
  });

  it("skips dist directories", () => {
    expect(shouldSkipDir("dist")).toBe(true);
  });

  it("does not skip ordinary directory names", () => {
    expect(shouldSkipDir("hooks")).toBe(false);
    expect(shouldSkipDir("components")).toBe(false);
    expect(shouldSkipDir("api")).toBe(false);
  });

  it("is case-sensitive: Generated is not excluded", () => {
    expect(shouldSkipDir("Generated")).toBe(false);
    expect(shouldSkipDir("__Tests__")).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// isCountableFile
// ---------------------------------------------------------------------------

describe("isCountableFile", () => {
  it("counts .ts files", () => {
    expect(isCountableFile("foo.ts")).toBe(true);
  });

  it("counts .tsx files", () => {
    expect(isCountableFile("foo.tsx")).toBe(true);
  });

  it("counts .d.ts files (ends in .ts)", () => {
    expect(isCountableFile("global.d.ts")).toBe(true);
  });

  it("counts .test.ts files (no file-level filter)", () => {
    expect(isCountableFile("foo.test.ts")).toBe(true);
  });

  it("counts .spec.tsx files (no file-level filter)", () => {
    expect(isCountableFile("foo.spec.tsx")).toBe(true);
  });

  it("does not count .js files", () => {
    expect(isCountableFile("foo.js")).toBe(false);
  });

  it("does not count .css files", () => {
    expect(isCountableFile("foo.css")).toBe(false);
  });

  it("does not count .md files", () => {
    expect(isCountableFile("README.md")).toBe(false);
  });

  it("does not count files with no extension", () => {
    expect(isCountableFile("Makefile")).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// walkDirs
// ---------------------------------------------------------------------------

describe("walkDirs", () => {
  let tmpRoot;

  beforeEach(() => {
    tmpRoot = mkdtempSync(join(tmpdir(), "walkDirs-test-"));
  });

  afterEach(() => {
    rmSync(tmpRoot, { recursive: true, force: true });
  });

  it("returns a single zero-count entry for an empty directory", () => {
    const result = walkDirs(tmpRoot);
    expect(result).toEqual([{ dirPath: tmpRoot, fileCount: 0 }]);
  });

  it("counts .ts/.tsx files at depth 1", () => {
    writeNFiles(tmpRoot, 5, ".ts");
    const result = walkDirs(tmpRoot);
    expect(result).toHaveLength(1);
    expect(result[0]).toEqual({ dirPath: tmpRoot, fileCount: 5 });
  });

  it("walks into sub-directories and lists each separately", () => {
    writeNFiles(tmpRoot, 3, ".ts");
    writeNFiles(join(tmpRoot, "sub"), 4, ".tsx");
    const result = walkDirs(tmpRoot);
    expect(result).toHaveLength(2);
    const rootEntry = result.find((r) => r.dirPath === tmpRoot);
    const subEntry = result.find((r) => r.dirPath === join(tmpRoot, "sub"));
    expect(rootEntry.fileCount).toBe(3);
    expect(subEntry.fileCount).toBe(4);
  });

  it("does NOT roll child counts up into the parent", () => {
    writeNFiles(join(tmpRoot, "child"), 20, ".ts");
    const result = walkDirs(tmpRoot);
    const rootEntry = result.find((r) => r.dirPath === tmpRoot);
    // Parent has zero direct files despite a child with 20.
    expect(rootEntry.fileCount).toBe(0);
  });

  it("prunes __tests__ subdirectories entirely", () => {
    writeNFiles(tmpRoot, 5, ".ts");
    writeNFiles(join(tmpRoot, "__tests__"), 30, ".ts");
    const result = walkDirs(tmpRoot);
    expect(result).toHaveLength(1);
    expect(result[0]).toEqual({ dirPath: tmpRoot, fileCount: 5 });
  });

  it("prunes generated subdirectories entirely", () => {
    writeNFiles(join(tmpRoot, "generated"), 50, ".ts");
    writeNFiles(tmpRoot, 2, ".ts");
    const result = walkDirs(tmpRoot);
    expect(result).toHaveLength(1);
    expect(result[0].fileCount).toBe(2);
  });

  it("prunes test-utils, node_modules, dist", () => {
    writeNFiles(join(tmpRoot, "test-utils"), 30, ".ts");
    writeNFiles(join(tmpRoot, "node_modules"), 30, ".ts");
    writeNFiles(join(tmpRoot, "dist"), 30, ".ts");
    writeNFiles(tmpRoot, 1, ".ts");
    const result = walkDirs(tmpRoot);
    expect(result).toHaveLength(1);
    expect(result[0].fileCount).toBe(1);
  });

  it("prunes __tests__ at any depth", () => {
    writeNFiles(join(tmpRoot, "a", "b", "__tests__"), 20, ".ts");
    writeNFiles(join(tmpRoot, "a", "b"), 3, ".ts");
    const result = walkDirs(tmpRoot);
    const paths = result.map((r) => r.dirPath);
    expect(paths).not.toContain(join(tmpRoot, "a", "b", "__tests__"));
    expect(paths).toContain(join(tmpRoot, "a", "b"));
  });

  it("counts mixed .ts/.tsx/.js/.md — only .ts and .tsx", () => {
    writeFileSync(join(tmpRoot, "a.ts"), "");
    writeFileSync(join(tmpRoot, "b.tsx"), "");
    writeFileSync(join(tmpRoot, "c.js"), "");
    writeFileSync(join(tmpRoot, "d.md"), "");
    writeFileSync(join(tmpRoot, "e.css"), "");
    const result = walkDirs(tmpRoot);
    expect(result[0].fileCount).toBe(2);
  });

  it("counts .test.ts and .spec.tsx files at depth 1 (no file-level filter)", () => {
    writeFileSync(join(tmpRoot, "a.ts"), "");
    writeFileSync(join(tmpRoot, "a.test.ts"), "");
    writeFileSync(join(tmpRoot, "b.spec.tsx"), "");
    const result = walkDirs(tmpRoot);
    expect(result[0].fileCount).toBe(3);
  });
});

// ---------------------------------------------------------------------------
// checkDirSize (direct function calls)
// ---------------------------------------------------------------------------

describe("checkDirSize", () => {
  let root;
  let srcDir;

  beforeEach(() => {
    const tmp = makeTmpProject();
    root = tmp.root;
    srcDir = tmp.srcDir;
  });

  afterEach(() => {
    rmSync(root, { recursive: true, force: true });
  });

  it("passes for an empty src/", () => {
    const result = checkDirSize(root, srcDir);
    expect(result.violations).toEqual([]);
    expect(result.scannedCount).toBe(1);
  });

  it("passes for a src/ with 10 files", () => {
    writeNFiles(srcDir, 10, ".ts");
    const result = checkDirSize(root, srcDir);
    expect(result.violations).toEqual([]);
  });

  it("passes for a directory with exactly 15 files (boundary)", () => {
    writeNFiles(srcDir, 15, ".ts");
    const result = checkDirSize(root, srcDir);
    expect(result.violations).toEqual([]);
  });

  it("fails for a directory with 16 files (boundary)", () => {
    writeNFiles(srcDir, 16, ".ts");
    const result = checkDirSize(root, srcDir);
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0]).toMatchObject({
      relPath: "src",
      fileCount: 16,
      threshold: 15,
    });
  });

  it("reports nested violation with frontend-relative path", () => {
    writeNFiles(join(srcDir, "hooks"), 20, ".ts");
    const result = checkDirSize(root, srcDir);
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0]).toMatchObject({
      relPath: "src/hooks",
      fileCount: 20,
    });
  });

  it("sorts multiple violations descending by fileCount", () => {
    writeNFiles(join(srcDir, "a"), 18, ".ts");
    writeNFiles(join(srcDir, "b"), 22, ".ts");
    writeNFiles(join(srcDir, "c"), 16, ".ts");
    const result = checkDirSize(root, srcDir);
    expect(result.violations).toHaveLength(3);
    expect(result.violations.map((v) => v.fileCount)).toEqual([22, 18, 16]);
  });

  it("does NOT flag a __tests__ subdir with 30 files inside a 5-file dir", () => {
    writeNFiles(join(srcDir, "feature"), 5, ".ts");
    writeNFiles(join(srcDir, "feature", "__tests__"), 30, ".ts");
    const result = checkDirSize(root, srcDir);
    expect(result.violations).toEqual([]);
  });

  it("does NOT flag a generated/ subdir with 50 files", () => {
    writeNFiles(join(srcDir, "types", "generated"), 50, ".ts");
    writeNFiles(join(srcDir, "types"), 3, ".ts");
    const result = checkDirSize(root, srcDir);
    expect(result.violations).toEqual([]);
  });

  it("regression: 8 source + 8 .test.ts files totals 16 — IS a violation", () => {
    const dir = join(srcDir, "mixed");
    mkdirSync(dir, { recursive: true });
    for (let i = 0; i < 8; i++) writeFileSync(join(dir, `a${i}.ts`), "");
    for (let i = 0; i < 8; i++) writeFileSync(join(dir, `a${i}.test.ts`), "");
    const result = checkDirSize(root, srcDir);
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0]).toMatchObject({
      relPath: "src/mixed",
      fileCount: 16,
    });
  });

  it("accepts a custom threshold via opts", () => {
    writeNFiles(join(srcDir, "small"), 12, ".ts");
    const result = checkDirSize(root, srcDir, { threshold: 10 });
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0].fileCount).toBe(12);
    expect(result.violations[0].threshold).toBe(10);
  });

  it("returns exit code 2 error when srcDir does not exist", () => {
    const missing = join(root, "nonexistent");
    const result = checkDirSize(root, missing);
    expect(result.error).toContain("not found");
    expect(result.exitCode).toBe(2);
  });

  it("returns exit code 2 error when srcDir is a file, not a directory", () => {
    const filePath = join(root, "some-file.ts");
    writeFileSync(filePath, "");
    const result = checkDirSize(root, filePath);
    expect(result.error).toContain("not a directory");
    expect(result.exitCode).toBe(2);
  });

  it("relPath uses forward slashes even on path separators", () => {
    writeNFiles(join(srcDir, "a", "b", "c"), 20, ".ts");
    const result = checkDirSize(root, srcDir);
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0].relPath).toBe("src/a/b/c");
  });

  it("scannedCount reflects all walked directories", () => {
    writeNFiles(srcDir, 2, ".ts");
    writeNFiles(join(srcDir, "a"), 2, ".ts");
    writeNFiles(join(srcDir, "b"), 2, ".ts");
    const result = checkDirSize(root, srcDir);
    expect(result.scannedCount).toBe(3);
  });

  it("a .tsx-only directory is counted correctly", () => {
    writeNFiles(srcDir, 16, ".tsx");
    const result = checkDirSize(root, srcDir);
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0].fileCount).toBe(16);
  });
});

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

describe("constants", () => {
  it("DIR_SIZE_THRESHOLD is 15", () => {
    expect(DIR_SIZE_THRESHOLD).toBe(15);
  });

  it("EXCLUDED_DIR_NAMES is a Set containing exactly the 5 expected names", () => {
    expect(EXCLUDED_DIR_NAMES).toBeInstanceOf(Set);
    expect(EXCLUDED_DIR_NAMES.size).toBe(5);
    expect(EXCLUDED_DIR_NAMES.has("__tests__")).toBe(true);
    expect(EXCLUDED_DIR_NAMES.has("test-utils")).toBe(true);
    expect(EXCLUDED_DIR_NAMES.has("generated")).toBe(true);
    expect(EXCLUDED_DIR_NAMES.has("node_modules")).toBe(true);
    expect(EXCLUDED_DIR_NAMES.has("dist")).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// Subprocess integration tests (exit code + output format)
// ---------------------------------------------------------------------------

describe("check-dir-size subprocess", () => {
  let root;
  let srcDir;

  beforeEach(() => {
    const tmp = makeTmpProject();
    root = tmp.root;
    srcDir = tmp.srcDir;
  });

  afterEach(() => {
    rmSync(root, { recursive: true, force: true });
  });

  it("exits 0 and prints success message for empty src/", () => {
    const result = runScript(root);
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain("within 15-file limit");
    expect(result.stdout).toContain("1 directories scanned");
  });

  it("exits 1 and prints the violation count and path on stderr", () => {
    writeNFiles(join(srcDir, "fat"), 16, ".ts");
    const result = runScript(root);
    expect(result.exitCode).toBe(1);
    expect(result.stderr).toContain("16");
    expect(result.stderr).toContain("src/fat");
    expect(result.stderr).toContain("1 directory(ies) exceed 15-file limit");
  });

  it("prints violations in descending fileCount order", () => {
    writeNFiles(join(srcDir, "a"), 18, ".ts");
    writeNFiles(join(srcDir, "b"), 22, ".ts");
    writeNFiles(join(srcDir, "c"), 16, ".ts");
    const result = runScript(root);
    expect(result.exitCode).toBe(1);
    const lines = result.stderr.split("\n").filter((l) => l.match(/^\s+\d+\t/));
    const counts = lines.map((l) => parseInt(l.trim().split("\t")[0], 10));
    expect(counts).toEqual([22, 18, 16]);
  });

  it("reports correct directory count in failure summary", () => {
    writeNFiles(join(srcDir, "a"), 20, ".ts");
    writeNFiles(join(srcDir, "b"), 25, ".ts");
    const result = runScript(root);
    expect(result.exitCode).toBe(1);
    expect(result.stderr).toContain("2 directory(ies) exceed 15-file limit");
  });

  it("exits 2 when src/ does not exist", () => {
    rmSync(srcDir, { recursive: true, force: true });
    const result = runScript(root);
    expect(result.exitCode).toBe(2);
    expect(result.stderr).toContain("not found");
  });

  it("regression: a directory with exactly 15 files passes", () => {
    writeNFiles(join(srcDir, "exact"), 15, ".ts");
    const result = runScript(root);
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain("within 15-file limit");
  });

  it("regression: __tests__ with 30 files inside a 5-file dir produces 0 violations", () => {
    writeNFiles(join(srcDir, "feature"), 5, ".ts");
    writeNFiles(join(srcDir, "feature", "__tests__"), 30, ".ts");
    const result = runScript(root);
    expect(result.exitCode).toBe(0);
  });
});
