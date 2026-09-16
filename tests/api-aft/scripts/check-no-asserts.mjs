#!/usr/bin/env node
// Guard: scenarios must contain no assertions.
//
// This is the rule that keeps the harness's yield high. The moment a scenario is
// allowed to assert, authors write scenario-specific checks instead of invariants,
// and the auto-dispatch multiplier -- every scenario picking up every oracle -- is
// lost one file at a time.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const dir = new URL("../scenarios/", import.meta.url).pathname;
const banned = [/\bassert\b/, /\bexpect\s*\(/, /\bthrow\s+new\b/, /\.should\b/];
let bad = 0;
for (const f of readdirSync(dir).filter((f) => f.endsWith(".ts"))) {
  const src = readFileSync(join(dir, f), "utf8");
  src.split("\n").forEach((line, i) => {
    if (line.trimStart().startsWith("//")) return;
    for (const re of banned) {
      if (re.test(line)) {
        console.error(`${f}:${i + 1}: scenarios must not assert -- move this to src/invariants.ts\n    ${line.trim()}`);
        bad++;
      }
    }
  });
}
if (bad > 0) {
  console.error(`\ncheck-no-asserts: FAIL (${bad})`);
  process.exit(1);
}
console.log("check-no-asserts: PASS");
