// Packaging invariants for @loom/sdk (SP2):
//  - vendorability: driver.js stays single-file (zero local imports) so the
//    driver bundle scripts can embed it verbatim; runner.js's local imports
//    stay exactly the documented set (./internal.js).
//  - npm pack golden: the published tarball contains exactly the pinned file
//    list — no tests, tsconfigs, examples, lockfiles, or node_modules.
//  - exports map: every entry resolves to an existing file and every code
//    entry point publishes types.
//  - examples parse as ES modules.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const sdkDir = path.dirname(fileURLToPath(import.meta.url));
const pkg = JSON.parse(readFileSync(path.join(sdkDir, "package.json"), "utf8"));

function localImports(file) {
  const src = readFileSync(path.join(sdkDir, file), "utf8");
  const specs = [];
  const patterns = [
    /(?:^|\n)\s*(?:import|export)[^"'\n]*from\s*["']([^"']+)["']/g,
    /(?:^|\n)\s*import\s*["']([^"']+)["']/g,
    /import\(\s*["']([^"']+)["']\s*\)/g,
  ];
  for (const re of patterns) {
    for (const m of src.matchAll(re)) specs.push(m[1]);
  }
  return [...new Set(specs.filter((s) => s.startsWith(".") || s.startsWith("/")))].sort();
}

test("vendorability: driver.js is single-file with zero local imports", () => {
  assert.deepEqual(localImports("driver.js"), []);
});

test("vendorability: runner.js local imports are exactly ./internal.js", () => {
  assert.deepEqual(localImports("runner.js"), ["./internal.js"]);
  assert.deepEqual(localImports("internal.js"), []);
});

test("exports map: every entry resolves and code entries publish types", () => {
  for (const [subpath, target] of Object.entries(pkg.exports)) {
    if (typeof target === "string") {
      readFileSync(path.join(sdkDir, target)); // throws if missing
      continue;
    }
    assert.ok(target.types, `${subpath}: missing "types" condition`);
    assert.ok(target.import, `${subpath}: missing "import" condition`);
    readFileSync(path.join(sdkDir, target.types));
    readFileSync(path.join(sdkDir, target.import));
  }
  for (const f of pkg.files) readFileSync(path.join(sdkDir, f));
});

test("exports map: no legacy flue driver compatibility entrypoint", () => {
  assert.equal(Object.hasOwn(pkg.exports, "./flue"), false);
  assert.equal(pkg.files.some((f) => f === "flue.js" || f === "flue.d.ts"), false);
});

test("npm pack golden: tarball contains exactly the pinned files", () => {
  const out = execFileSync("npm", ["pack", "--dry-run", "--json"], {
    cwd: sdkDir,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  const [report] = JSON.parse(out);
  const got = report.files.map((f) => f.path).sort();
  const want = [
    "CHANGELOG.md",
    "README.md",
    "api-surface.v1.json",
    "driver.d.ts",
    "driver.js",
    "index.d.ts",
    "index.js",
    "internal.js",
    "package.json",
    "runner.d.ts",
    "runner.js",
    "runtime-adapters.d.ts",
    "runtime-adapters.js",
    "task-repository-context.gen.d.ts",
  ];
  assert.deepEqual(got, want);
  assert.equal(report.name, pkg.name);
  assert.equal(report.version, pkg.version);
});

test("examples parse as ES modules", () => {
  const dir = path.join(sdkDir, "examples");
  const examples = readdirSync(dir).filter((f) => f.endsWith(".mjs")).sort();
  assert.deepEqual(examples, ["epic-runner-watch.mjs", "task-fan-out.mjs"]);
  for (const f of examples) {
    execFileSync(process.execPath, ["--check", path.join(dir, f)], { stdio: "pipe" });
  }
});
