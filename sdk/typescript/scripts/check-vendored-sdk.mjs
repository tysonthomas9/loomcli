// Staleness gate: the SDK bundle vendored into the flue template must match the
// SDK source. Re-bundles and diffs against the committed copy (like
// check-generated.mjs for the OpenAPI types). Fails CI on drift so the runner
// can never ship a stale @loom/sdk. Run `npm run vendor` to refresh.
import { readFile } from "node:fs/promises";

let mod;
try {
  mod = await import("./bundle-sdk.mjs");
} catch (err) {
  // esbuild (a devDependency) not installed — skip rather than fail hard, so a
  // partial checkout doesn't break unrelated work. CI runs `npm ci` first.
  console.log(`Skipping vendored-SDK check (toolchain unavailable): ${err.message}`);
  process.exit(0);
}

const {
  bundleSdk,
  VENDOR_JS,
  VENDOR_DTS,
  VENDOR_DTS_CONTENT,
  EXPECTED_EXPORTS,
} = mod;

function fail(msg) {
  console.error(`\n✗ Vendored @loom/sdk is stale: ${msg}`);
  console.error("  Run: cd sdk/typescript && npm run vendor   (then commit the result)\n");
  process.exit(1);
}

const fresh = await bundleSdk();

let committedJs;
try {
  committedJs = await readFile(VENDOR_JS, "utf8");
} catch {
  fail(`missing vendored bundle at ${VENDOR_JS}`);
}

if (committedJs !== fresh) {
  fail(`${VENDOR_JS} does not match a fresh bundle of the SDK source`);
}

// The runtime bundle must actually export the public surface (guards against a
// silent rename that the byte-diff alone would pass after a re-vendor).
const missing = EXPECTED_EXPORTS.filter((name) => !fresh.includes(name));
if (missing.length) {
  fail(`bundle is missing expected exports: ${missing.join(", ")}`);
}

let committedDts;
try {
  committedDts = await readFile(VENDOR_DTS, "utf8");
} catch {
  fail(`missing vendored types at ${VENDOR_DTS}`);
}
if (committedDts !== VENDOR_DTS_CONTENT) {
  fail(`${VENDOR_DTS} does not match the maintained façade in bundle-sdk.mjs`);
}

console.log("Vendored @loom/sdk bundle is up to date.");
