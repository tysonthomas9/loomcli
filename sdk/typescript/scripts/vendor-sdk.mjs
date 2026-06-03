// Vendor the bundled SDK into the flue template (or a chosen --out dir).
//
//   npm run vendor              → writes internal/flue/template/.flue/vendor/loom-sdk/
//   npm run bundle (--out dir)  → writes the bundle into <dir> (CI artifact / inspect)
//
// Run `npm run vendor` after changing the SDK; commit the regenerated bundle.
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import {
  bundleSdk,
  VENDOR_DIR,
  VENDOR_DTS,
  VENDOR_DTS_CONTENT,
  VENDOR_JS,
} from "./bundle-sdk.mjs";

function outDir() {
  const i = process.argv.indexOf("--out");
  return i >= 0 && process.argv[i + 1] ? process.argv[i + 1] : null;
}

const dir = outDir() ?? VENDOR_DIR;
const js = dir === VENDOR_DIR ? VENDOR_JS : join(dir, "index.js");
const dts = dir === VENDOR_DIR ? VENDOR_DTS : join(dir, "index.d.ts");

const code = await bundleSdk();
await mkdir(dir, { recursive: true });
await writeFile(js, code);
await writeFile(dts, VENDOR_DTS_CONTENT);
console.log(`Wrote ${js} (${code.length} bytes) + index.d.ts`);
