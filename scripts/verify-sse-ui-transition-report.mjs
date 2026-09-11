#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";

function fail(message) {
  console.error(`[sse-ui-manifest] FATAL: ${message}`);
  process.exit(1);
}

function parseArgs(argv) {
  const args = {};
  for (let i = 0; i < argv.length; i += 2) {
    const key = argv[i];
    const value = argv[i + 1];
    if (!key?.startsWith("--") || value === undefined)
      fail(`invalid argument ${key ?? ""}`);
    args[key.slice(2)] = value;
  }
  for (const key of ["mode", "manifest", "tier", "backend", "report"]) {
    if (!args[key]) fail(`--${key} is required`);
  }
  if (!new Set(["list", "result"]).has(args.mode))
    fail("--mode must be list or result");
  return args;
}

function loadManifest(file, tier, backend) {
  const lines = fs.readFileSync(file, "utf8").split(/\r?\n/);
  if (lines.shift() !== "version\t2")
    fail("manifest must start with version<TAB>2");
  const entries = [];
  for (const [offset, line] of lines.entries()) {
    if (!line || line.startsWith("#")) continue;
    const fields = line.split("\t");
    if (fields.length !== 5)
      fail(`manifest line ${offset + 2} must have five tab-separated fields`);
    const [entryTier, entryBackend, caseId, spec, marker] = fields;
    if (!/^(mocked|paired)$/.test(entryTier)) fail(`invalid tier ${entryTier}`);
    if (!/^(mocked|redis|postgres)$/.test(entryBackend))
      fail(`invalid backend ${entryBackend}`);
    if (!/^T\d{2}(?:-[A-Z0-9]+)*$/.test(caseId))
      fail(`invalid case id ${caseId}`);
    if (
      !spec.startsWith("tests/e2e/") ||
      path.isAbsolute(spec) ||
      spec.includes("..")
    )
      fail(`unsafe spec path ${spec}`);
    if (marker !== `@sse-ui-transition @${caseId}`)
      fail(`marker for ${caseId} must be exact`);
    entries.push({
      tier: entryTier,
      backend: entryBackend,
      caseId,
      spec,
      marker,
    });
  }
  const duplicateKeys = entries
    .map((entry) => `${entry.tier}/${entry.backend}/${entry.caseId}`)
    .filter((key, index, all) => all.indexOf(key) !== index);
  if (duplicateKeys.length)
    fail(
      `duplicate manifest entries: ${[...new Set(duplicateKeys)].join(", ")}`,
    );
  const selected = entries.filter(
    (entry) => entry.tier === tier && entry.backend === backend,
  );
  if (!selected.length) fail(`manifest has no ${tier}/${backend} cases`);
  const requiredSets = {
    "mocked/mocked": [
      "T09-RESTORE",
      "T09-SWITCH",
      "T09-REFRESH",
      "T09-AUTH",
      "T08-DRAFT",
      "T08-TAB",
      "T08-SWITCH",
      "T10-REFRESH",
      "T10-SWITCH",

      "T01",
      "T02",
      "T03",
      "T04",
      "T05",
      "T07",
      "T11-READY",
      "T11-LIST",
      "T11-GRAPH",
      "T11-BLOCKED",
      "T11-DORMANT",
      "T04-WORKSPACE",
      "T04-REPO",
    ],
    "paired/redis": [
      "T01",
      "T02",
      "T03",
      "T04",
      "T06",
      "T08-DRAFT",
      "T11-GRAPH",
      "T12",
    ],
    "paired/postgres": [
      "T01",
      "T02",
      "T03",
      "T04",
      "T06",
      "T08-DRAFT",
      "T11-GRAPH",
      "T12",
    ],
  };
  const key = `${tier}/${backend}`;
  const required = requiredSets[key];
  if (!required) fail(`unsupported manifest selection ${key}`);
  const actual = selected.map((entry) => entry.caseId).sort();
  if (JSON.stringify(actual) !== JSON.stringify([...required].sort())) {
    fail(
      `${key} cases are ${actual.join(",")}; required ${required.join(",")}`,
    );
  }
  return selected;
}

function collectSpecs(suite, inheritedFile = "", found = []) {
  const file = suite.file || inheritedFile;
  for (const spec of suite.specs || [])
    found.push({ ...spec, file: spec.file || file });
  for (const child of suite.suites || [])
    collectSpecs(child, child.file || file, found);
  return found;
}

function caseIdForTitle(title) {
  const match = title.match(
    /@sse-ui-transition @(T\d{2}(?:-[A-Z0-9]+)*)(?:\s|$)/,
  );
  return match?.[1];
}

function normalizeFile(file, rootDir) {
  const normalizedRoot = String(rootDir || "").replaceAll("\\", "/");
  if (!normalizedRoot.endsWith("/tests/e2e")) {
    fail(
      `Playwright rootDir is not the expected tests/e2e directory: ${rootDir || "missing"}`,
    );
  }
  const normalized = path.isAbsolute(file || "")
    ? String(file).replaceAll("\\", "/")
    : path.posix.join(normalizedRoot, String(file || "")).replaceAll("\\", "/");
  const testRoot = normalized.lastIndexOf("/tests/e2e/");
  if (testRoot < 0)
    fail(`reported file is outside Playwright rootDir: ${file || "missing"}`);
  return normalized.slice(testRoot + 1);
}

const args = parseArgs(process.argv.slice(2));
const expected = loadManifest(args.manifest, args.tier, args.backend);
let report;
try {
  report = JSON.parse(fs.readFileSync(args.report, "utf8"));
} catch (error) {
  fail(`cannot read Playwright JSON report ${args.report}: ${error.message}`);
}
if ((report.errors || []).length > 0) {
  fail(
    `Playwright reported ${report.errors.length} global setup, teardown or reporter errors`,
  );
}

const selected = [];
for (const suite of report.suites || []) {
  for (const spec of collectSpecs(suite)) {
    const caseId = caseIdForTitle(spec.title || "");
    if (caseId)
      selected.push({
        ...spec,
        caseId,
        file: normalizeFile(spec.file, report.config?.rootDir),
      });
  }
}

const unexpected = selected.filter(
  (spec) => !expected.some((entry) => entry.caseId === spec.caseId),
);
if (unexpected.length)
  fail(
    `report selected unexpected cases: ${unexpected.map((spec) => spec.caseId).join(", ")}`,
  );

for (const entry of expected) {
  const matches = selected.filter((spec) => spec.caseId === entry.caseId);
  if (matches.length !== 1)
    fail(
      `${entry.caseId} selected ${matches.length} times; expected exactly once`,
    );
  if (matches[0].file !== entry.spec)
    fail(`${entry.caseId} ran from ${matches[0].file}; expected ${entry.spec}`);
  if (!(matches[0].title || "").includes(entry.marker))
    fail(`${entry.caseId} title marker changed`);
}

const counts = {
  expected: expected.length,
  executed: 0,
  skipped: 0,
  failed: 0,
  flaky: 0,
};
if (args.mode === "result") {
  for (const spec of selected) {
    const tests = spec.tests || [];
    if (tests.length !== 1)
      fail(
        `${spec.caseId} has ${tests.length} project results; expected exactly one`,
      );
    const test = tests[0];
    const results = test.results || [];
    const skipped =
      test.expectedStatus === "skipped" ||
      (test.annotations || []).some(
        (annotation) => annotation.type === "skip",
      ) ||
      results.some((result) => result.status === "skipped");
    if (skipped || results.length === 0) counts.skipped += 1;
    else counts.executed += 1;
    if (test.status === "flaky" || results.length > 1) counts.flaky += 1;
    if (
      test.status === "unexpected" ||
      !spec.ok ||
      results.some((result) => !["passed", "skipped"].includes(result.status))
    ) {
      counts.failed += 1;
    }
  }
}

const summary = {
  version: 2,
  tier: args.tier,
  backend: args.backend,
  cases: expected.map(({ caseId, spec, marker }) => ({ caseId, spec, marker })),
  counts,
};
const encoded = JSON.stringify(summary, null, 2) + "\n";
if (args.summary) fs.writeFileSync(args.summary, encoded);
process.stdout.write(encoded);

if (
  args.mode === "result" &&
  (counts.executed !== counts.expected ||
    counts.skipped ||
    counts.failed ||
    counts.flaky)
) {
  fail("required cases did not all pass once with zero skips and zero retries");
}
