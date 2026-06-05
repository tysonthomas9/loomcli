// Verifies sdk/typescript/src/generated/openapi.ts is in sync with
// api/openapi.yaml. Mirrors the frontend's check-openapi-staleness gate.
//   exit 0 — up to date (or spec/toolchain absent)
//   exit 1 — stale (spec changed but types not regenerated)
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..");
const spec = join(root, "..", "..", "api", "openapi.yaml");
const committed = join(root, "src", "generated", "openapi.ts");
const bin = join(root, "node_modules", ".bin", "openapi-typescript");

if (!existsSync(spec)) {
  console.log("api/openapi.yaml not found — skipping SDK type staleness check");
  process.exit(0);
}
if (!existsSync(bin)) {
  console.log("openapi-typescript not installed — run `npm install` in sdk/typescript; skipping");
  process.exit(0);
}
if (!existsSync(committed)) {
  console.error("sdk/typescript/src/generated/openapi.ts is missing — run `npm run generate`");
  process.exit(1);
}

const out = join(mkdtempSync(join(tmpdir(), "loom-sdk-gen-")), "openapi.ts");
execFileSync(bin, [spec, "-o", out], { stdio: ["ignore", "ignore", "inherit"] });

if (readFileSync(committed, "utf8") !== readFileSync(out, "utf8")) {
  console.error(
    "SDK generated types are STALE.\n" +
      "  api/openapi.yaml changed but sdk/typescript/src/generated/openapi.ts was not regenerated.\n" +
      "  Run: cd sdk/typescript && npm run generate",
  );
  process.exit(1);
}
console.log("SDK generated types are up to date.");
