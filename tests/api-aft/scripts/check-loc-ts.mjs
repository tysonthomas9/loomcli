#!/usr/bin/env node
// scripts/check-loc.sh only collects *.go, so this harness polices its own size.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
const LIMIT = 400;
const root = new URL("../", import.meta.url).pathname;
let bad = 0;
const walk = (d) => {
  for (const e of readdirSync(d)) {
    if (e === "node_modules" || e === "_reports" || e === "_stack") continue;
    const p = join(d, e);
    if (statSync(p).isDirectory()) { walk(p); continue; }
    if (!/\.(ts|mjs)$/.test(e)) continue;
    const n = readFileSync(p, "utf8").split("\n").length;
    if (n > LIMIT) { console.error(`${p.replace(root, "")}: ${n} lines > ${LIMIT}`); bad++; }
  }
};
walk(root);
if (bad > 0) { console.error(`check-loc-ts: FAIL (${bad})`); process.exit(1); }
console.log("check-loc-ts: PASS");
