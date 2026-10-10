#!/usr/bin/env node
// Checks tests/aft/loomgit-case-map.json against the suites: every loomgit case
// (fake forge, real-GitHub matrix, live lead assignment) must be mapped, and the
// map must not name a case that no longer exists. Trailing "[needs ...]" labels
// are ignored. Usage: node tests/aft/scripts/check-case-map.mjs (needs $AFT_DIR).
import { readFileSync, readdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const aftDir = process.env.AFT_DIR || '/Users/tyson/codebase/code-agents/testing-app';
const { loadSuite } = await import(pathToFileURL(join(aftDir, 'dist/runner.js')).href);
const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const map = JSON.parse(readFileSync(join(root, 'loomgit-case-map.json'), 'utf8'));
const files = [
  ...readdirSync(join(root, 'suites')).filter((f) => /^loomgit-.*\.test\.yaml$/.test(f)).map((f) => join(root, 'suites', f)),
  ...readdirSync(join(root, 'real-github-suites')).filter((f) => f.endsWith('.test.yaml')).map((f) => join(root, 'real-github-suites', f)),
  join(root, 'live-interactive-suites', 'll-lead-assignment.test.yaml'),
];
const bare = (name) => name.replace(/(\s*\[needs [^\]]+\])+$/, '');
const problems = [];
const seen = new Set();
for (const file of files) {
  const suite = file.split('/').pop().replace('.test.yaml', '');
  const mapped = map.suites[suite] || {};
  for (const test of (await loadSuite(file)).tests) {
    const name = bare(test.name);
    seen.add(`${suite}::${name}`);
    const entry = mapped[name];
    if (!entry) problems.push(`unmapped: ${suite} :: ${name}`);
    else if (!entry.requirements?.length) problems.push(`no requirement: ${suite} :: ${name}`);
    for (const key of entry?.tracker || []) if (!map.tracker_tasks[key]) problems.push(`unknown tracker task ${key}: ${suite} :: ${name}`);
  }
}
for (const [suite, cases] of Object.entries(map.suites)) {
  for (const name of Object.keys(cases)) if (!seen.has(`${suite}::${name}`)) problems.push(`stale map entry: ${suite} :: ${name}`);
}
if (problems.length) {
  console.error(problems.join('\n'));
  process.exit(1);
}
console.log(`case map ok: ${seen.size} cases in ${files.length} suites`);
