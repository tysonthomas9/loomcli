/**
 * Unit tests for the check-loc.mjs LOC checker script.
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
  shouldSkip,
  walkDir,
  countLines,
  checkLoc,
  THRESHOLD_TS,
  THRESHOLD_TSX,
  ALLOWLIST,
} from "../check-loc.mjs";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const SCRIPT_PATH = fileURLToPath(
  new URL("../check-loc.mjs", import.meta.url),
);

/** Create a temp directory that acts as a fake frontend project root. */
function makeTmpProject() {
  const root = mkdtempSync(join(tmpdir(), "check-loc-test-"));
  const srcDir = join(root, "src");
  mkdirSync(srcDir, { recursive: true });
  return { root, srcDir };
}

/** Write a .ts file with exactly `lines` newline characters under srcDir. */
function writeFile(srcDir, relPath, lines) {
  const full = join(srcDir, relPath);
  mkdirSync(join(full, ".."), { recursive: true });
  // Each line is "// line N\n" — `lines` newlines total.
  const content = Array.from({ length: lines }, (_, i) => `// line ${i + 1}`)
    .join("\n")
    .concat("\n");
  writeFileSync(full, content);
}

/** Run the check-loc.mjs script as a subprocess against a given project dir.
 *  We invoke it via `node --eval` to set the working directory context. */
function runScript(projectRoot) {
  // The script resolves paths relative to its own location (scripts/../src).
  // To test with a custom project dir we need a thin wrapper that overrides the
  // import.meta.url-based resolution. Instead, we call checkLoc directly from
  // a small inline ESM script.
  const inline = `
    import { checkLoc, THRESHOLD_TS, THRESHOLD_TSX } from ${JSON.stringify(SCRIPT_PATH)};
    import { join } from "path";

    const frontendDir = ${JSON.stringify(projectRoot)};
    const srcDir = join(frontendDir, "src");
    const allowlist = new Map();
    const result = checkLoc(frontendDir, srcDir, allowlist, { ts: THRESHOLD_TS, tsx: THRESHOLD_TSX });

    if (result.error) {
      process.stderr.write(result.error + "\\n");
      process.exit(result.exitCode);
    }

    const { violations, allowlistedCount } = result;

    if (violations.length === 0) {
      process.stdout.write("✓ All files within limits (" + allowlistedCount + " allowlisted)\\n");
      process.exit(0);
    }

    violations.sort((a, b) => b.loc - a.loc);
    for (const v of violations) {
      const suffix = v.ceiling !== null ? " (ceiling: " + v.ceiling + ")" : "";
      process.stderr.write("  " + v.loc + "\\t" + v.relPath + suffix + "\\n");
    }
    process.stderr.write("\\n✗ " + violations.length + " file(s) exceed LOC limits (" + THRESHOLD_TSX + " .tsx / " + THRESHOLD_TS + " .ts)\\n");
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

/** Run the script with a custom allowlist. */
function runScriptWithAllowlist(projectRoot, allowlistEntries) {
  const allowlistStr = JSON.stringify(allowlistEntries);
  const inline = `
    import { checkLoc, THRESHOLD_TS, THRESHOLD_TSX } from ${JSON.stringify(SCRIPT_PATH)};
    import { join } from "path";

    const frontendDir = ${JSON.stringify(projectRoot)};
    const srcDir = join(frontendDir, "src");
    const allowlist = new Map(${allowlistStr});
    const result = checkLoc(frontendDir, srcDir, allowlist, { ts: THRESHOLD_TS, tsx: THRESHOLD_TSX });

    if (result.error) {
      process.stderr.write(result.error + "\\n");
      process.exit(result.exitCode);
    }

    const { violations, allowlistedCount } = result;

    if (violations.length === 0) {
      process.stdout.write("✓ All files within limits (" + allowlistedCount + " allowlisted)\\n");
      process.exit(0);
    }

    violations.sort((a, b) => b.loc - a.loc);
    for (const v of violations) {
      const suffix = v.ceiling !== null ? " (ceiling: " + v.ceiling + ")" : "";
      process.stderr.write("  " + v.loc + "\\t" + v.relPath + suffix + "\\n");
    }
    process.stderr.write("\\n✗ " + violations.length + " file(s) exceed LOC limits (" + THRESHOLD_TSX + " .tsx / " + THRESHOLD_TS + " .ts)\\n");
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
// shouldSkip
// ---------------------------------------------------------------------------

describe("shouldSkip", () => {
  it("skips .test.ts files", () => {
    expect(shouldSkip("src/utils/helpers.test.ts")).toBe(true);
  });

  it("skips .test.tsx files", () => {
    expect(shouldSkip("src/components/Button.test.tsx")).toBe(true);
  });

  it("skips .spec.ts files", () => {
    expect(shouldSkip("src/utils/helpers.spec.ts")).toBe(true);
  });

  it("skips .spec.tsx files", () => {
    expect(shouldSkip("src/components/Button.spec.tsx")).toBe(true);
  });

  it("skips .d.ts declaration files", () => {
    expect(shouldSkip("src/types/global.d.ts")).toBe(true);
  });

  it("skips vite-env files", () => {
    expect(shouldSkip("src/vite-env.d.ts")).toBe(true);
    expect(shouldSkip("src/vite-env-custom.ts")).toBe(true);
  });

  it("skips TestFixtures.tsx", () => {
    expect(shouldSkip("src/components/TestFixtures.tsx")).toBe(true);
  });

  it("skips files inside __tests__ directories", () => {
    expect(shouldSkip("src/components/__tests__/Button.tsx")).toBe(true);
  });

  it("skips files inside test-utils directories", () => {
    expect(shouldSkip("src/test-utils/render.ts")).toBe(true);
  });

  it("does not skip normal source files", () => {
    expect(shouldSkip("src/App.tsx")).toBe(false);
    expect(shouldSkip("src/utils/helpers.ts")).toBe(false);
    expect(shouldSkip("src/components/Button/Button.tsx")).toBe(false);
  });

  it("does not skip files that merely contain 'test' in the name", () => {
    expect(shouldSkip("src/components/TestRunner.tsx")).toBe(false);
    expect(shouldSkip("src/utils/testHelpers.ts")).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// walkDir
// ---------------------------------------------------------------------------

describe("walkDir", () => {
  let tmpRoot;

  beforeEach(() => {
    tmpRoot = mkdtempSync(join(tmpdir(), "walkDir-test-"));
  });

  afterEach(() => {
    rmSync(tmpRoot, { recursive: true, force: true });
  });

  it("collects .ts and .tsx files recursively", () => {
    mkdirSync(join(tmpRoot, "a", "b"), { recursive: true });
    writeFileSync(join(tmpRoot, "one.ts"), "");
    writeFileSync(join(tmpRoot, "a", "two.tsx"), "");
    writeFileSync(join(tmpRoot, "a", "b", "three.ts"), "");

    const files = walkDir(tmpRoot);
    expect(files).toHaveLength(3);
    expect(files).toContain(join(tmpRoot, "one.ts"));
    expect(files).toContain(join(tmpRoot, "a", "two.tsx"));
    expect(files).toContain(join(tmpRoot, "a", "b", "three.ts"));
  });

  it("ignores non-TS files", () => {
    writeFileSync(join(tmpRoot, "readme.md"), "");
    writeFileSync(join(tmpRoot, "style.css"), "");
    writeFileSync(join(tmpRoot, "index.js"), "");
    writeFileSync(join(tmpRoot, "real.ts"), "");

    const files = walkDir(tmpRoot);
    expect(files).toEqual([join(tmpRoot, "real.ts")]);
  });

  it("skips node_modules directories", () => {
    mkdirSync(join(tmpRoot, "node_modules", "pkg"), { recursive: true });
    writeFileSync(join(tmpRoot, "node_modules", "pkg", "index.ts"), "");
    writeFileSync(join(tmpRoot, "app.ts"), "");

    const files = walkDir(tmpRoot);
    expect(files).toEqual([join(tmpRoot, "app.ts")]);
  });

  it("returns empty array for a directory with no TS files", () => {
    writeFileSync(join(tmpRoot, "readme.md"), "");
    expect(walkDir(tmpRoot)).toEqual([]);
  });
});

// ---------------------------------------------------------------------------
// countLines
// ---------------------------------------------------------------------------

describe("countLines", () => {
  let tmpRoot;

  beforeEach(() => {
    tmpRoot = mkdtempSync(join(tmpdir(), "countLines-test-"));
  });

  afterEach(() => {
    rmSync(tmpRoot, { recursive: true, force: true });
  });

  it("counts newline characters (wc -l style)", () => {
    const filePath = join(tmpRoot, "a.ts");
    // 3 lines of content ending with newline = 3 newline chars
    writeFileSync(filePath, "line1\nline2\nline3\n");
    expect(countLines(filePath)).toBe(3);
  });

  it("returns 0 for an empty file", () => {
    const filePath = join(tmpRoot, "empty.ts");
    writeFileSync(filePath, "");
    expect(countLines(filePath)).toBe(0);
  });

  it("does not count a trailing non-newline line", () => {
    const filePath = join(tmpRoot, "no-trailing.ts");
    writeFileSync(filePath, "line1\nline2");
    // Only 1 newline character
    expect(countLines(filePath)).toBe(1);
  });

  it("counts exact number of newlines for large content", () => {
    const filePath = join(tmpRoot, "big.ts");
    const lines = 600;
    const content = Array.from({ length: lines }, (_, i) => `// ${i}`).join("\n") + "\n";
    writeFileSync(filePath, content);
    expect(countLines(filePath)).toBe(lines);
  });
});

// ---------------------------------------------------------------------------
// checkLoc (direct function calls)
// ---------------------------------------------------------------------------

describe("checkLoc", () => {
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

  it("passes for files under 2000 LOC", () => {
    writeFile(srcDir, "small.ts", 100);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toEqual([]);
    expect(result.allowlistedCount).toBe(0);
  });

  it("passes for a file at exactly 2000 LOC", () => {
    writeFile(srcDir, "exact.ts", 2000);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toEqual([]);
  });

  it("fails for files over 2000 LOC without allowlist", () => {
    writeFile(srcDir, "big.ts", 2001);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0]).toMatchObject({
      relPath: "src/big.ts",
      loc: 2001,
      ceiling: null,
    });
  });

  it("passes for allowlisted file at its ceiling", () => {
    writeFile(srcDir, "large.tsx", 1500);
    const allowlist = new Map([["src/large.tsx", 1500]]);
    const result = checkLoc(root, srcDir, allowlist);
    expect(result.violations).toEqual([]);
    expect(result.allowlistedCount).toBe(1);
  });

  it("passes for allowlisted file under its ceiling", () => {
    writeFile(srcDir, "large.tsx", 1500);
    const allowlist = new Map([["src/large.tsx", 1800]]);
    const result = checkLoc(root, srcDir, allowlist);
    expect(result.violations).toEqual([]);
    expect(result.allowlistedCount).toBe(1);
  });

  it("fails for allowlisted file over its ceiling", () => {
    writeFile(srcDir, "large.tsx", 1801);
    const allowlist = new Map([["src/large.tsx", 1800]]);
    const result = checkLoc(root, srcDir, allowlist);
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0]).toMatchObject({
      relPath: "src/large.tsx",
      loc: 1801,
      ceiling: 1800,
    });
  });

  it("skips test files", () => {
    writeFile(srcDir, "util.test.ts", 2500);
    writeFile(srcDir, "Component.test.tsx", 2500);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toEqual([]);
  });

  it("skips spec files", () => {
    writeFile(srcDir, "util.spec.ts", 2500);
    writeFile(srcDir, "Component.spec.tsx", 2500);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toEqual([]);
  });

  it("skips .d.ts files", () => {
    writeFile(srcDir, "global.d.ts", 2500);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toEqual([]);
  });

  it("skips __tests__ directory", () => {
    writeFile(srcDir, "components/__tests__/Big.tsx", 2500);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toEqual([]);
  });

  it("skips test-utils directory", () => {
    writeFile(srcDir, "test-utils/render.ts", 2500);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toEqual([]);
  });

  it("skips TestFixtures.tsx", () => {
    writeFile(srcDir, "components/TestFixtures.tsx", 2500);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toEqual([]);
  });

  it("skips vite-env files", () => {
    writeFile(srcDir, "vite-env.d.ts", 2500);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toEqual([]);
  });

  it("sorts violations by LOC descending", () => {
    writeFile(srcDir, "a.ts", 2100);
    writeFile(srcDir, "b.ts", 2500);
    writeFile(srcDir, "c.ts", 2300);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toHaveLength(3);
    expect(result.violations[0].loc).toBe(2500);
    expect(result.violations[1].loc).toBe(2300);
    expect(result.violations[2].loc).toBe(2100);
  });

  it("returns error when src directory does not exist", () => {
    const missing = join(root, "nonexistent");
    const result = checkLoc(root, missing);
    expect(result.error).toContain("not found");
    expect(result.exitCode).toBe(2);
  });

  it("respects custom threshold as a single number", () => {
    writeFile(srcDir, "medium.ts", 2500);
    const result = checkLoc(root, srcDir, new Map(), 200);
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0].loc).toBe(2500);
  });

  it("applies 2000-line threshold to both .tsx and .ts files", () => {
    writeFile(srcDir, "large-component.tsx", 2001);
    writeFile(srcDir, "large-util.ts", 1500);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0].relPath).toContain(".tsx");
  });

  it("passes .tsx file at exactly 300 lines", () => {
    writeFile(srcDir, "exact.tsx", 2000);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toEqual([]);
  });

  it("fails .tsx file at 2001 lines", () => {
    writeFile(srcDir, "over.tsx", 2001);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toHaveLength(1);
    expect(result.violations[0].relPath).toBe("src/over.tsx");
  });

  it("passes .ts file at 1500 lines (under 2000 threshold)", () => {
    writeFile(srcDir, "util.ts", 1500);
    const result = checkLoc(root, srcDir, new Map());
    expect(result.violations).toEqual([]);
  });

  it("respects custom thresholds object", () => {
    writeFile(srcDir, "a.tsx", 150);
    writeFile(srcDir, "b.ts", 250);
    const result = checkLoc(root, srcDir, new Map(), { ts: 200, tsx: 100 });
    expect(result.violations).toHaveLength(2);
  });

  it("accepts a single number threshold for backward compatibility", () => {
    writeFile(srcDir, "big.tsx", 250);
    const result = checkLoc(root, srcDir, new Map(), 200);
    expect(result.violations).toHaveLength(1);
  });
});

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

describe("constants", () => {
  it("THRESHOLD_TS is 2000", () => {
    expect(THRESHOLD_TS).toBe(2000);
  });

  it("THRESHOLD_TSX is 2000", () => {
    expect(THRESHOLD_TSX).toBe(2000);
  });

  it("ALLOWLIST is a Map (empty under global 2000-line ceiling)", () => {
    expect(ALLOWLIST).toBeInstanceOf(Map);
    expect(ALLOWLIST.size).toBe(0);
  });
});

// ---------------------------------------------------------------------------
// Subprocess integration tests (exit code + output format)
// ---------------------------------------------------------------------------

describe("check-loc subprocess", () => {
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

  it("exits 0 and prints success message for compliant files", () => {
    writeFile(srcDir, "small.ts", 100);
    const result = runScript(root);
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain("All files within limits");
    expect(result.stdout).toContain("0 allowlisted");
  });

  it("exits 1 and prints violation details for oversized files", () => {
    writeFile(srcDir, "big.ts", 2001);
    const result = runScript(root);
    expect(result.exitCode).toBe(1);
    expect(result.stderr).toContain("2001");
    expect(result.stderr).toContain("src/big.ts");
    expect(result.stderr).toContain("1 file(s) exceed LOC limits (2000 .tsx / 2000 .ts)");
  });

  it("prints ceiling in output for allowlisted violations", () => {
    writeFile(srcDir, "big.tsx", 2100);
    const result = runScriptWithAllowlist(root, [["src/big.tsx", 1800]]);
    expect(result.exitCode).toBe(1);
    expect(result.stderr).toContain("(ceiling: 1800)");
  });

  it("does not print ceiling for non-allowlisted violations", () => {
    writeFile(srcDir, "big.ts", 2100);
    const result = runScript(root);
    expect(result.exitCode).toBe(1);
    expect(result.stderr).not.toContain("(ceiling:");
  });

  it("sorts violations by LOC descending in output", () => {
    writeFile(srcDir, "a.ts", 2100);
    writeFile(srcDir, "b.ts", 2500);
    writeFile(srcDir, "c.ts", 2300);
    const result = runScript(root);
    expect(result.exitCode).toBe(1);

    const lines = result.stderr.split("\n").filter((l) => l.match(/^\s+\d+\t/));
    expect(lines).toHaveLength(3);
    // Verify descending order by extracting LOC values
    const locs = lines.map((l) => parseInt(l.trim().split("\t")[0], 10));
    expect(locs).toEqual([2500, 2300, 2100]);
  });

  it("reports correct file count in summary", () => {
    writeFile(srcDir, "a.ts", 2200);
    writeFile(srcDir, "b.ts", 2500);
    const result = runScript(root);
    expect(result.exitCode).toBe(1);
    expect(result.stderr).toContain("2 file(s) exceed LOC limits (2000 .tsx / 2000 .ts)");
  });

  it("counts allowlisted files in success message", () => {
    writeFile(srcDir, "ok.ts", 100);
    writeFile(srcDir, "large.tsx", 1500);
    const result = runScriptWithAllowlist(root, [["src/large.tsx", 1800]]);
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain("1 allowlisted");
  });
});
