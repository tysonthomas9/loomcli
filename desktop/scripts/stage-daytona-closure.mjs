#!/usr/bin/env node
// stage-daytona-closure.mjs — assemble a self-contained, symlink-free @daytona/sdk
// tree for the offline authoring kit.
//
// The epic-runner workflow source imports @daytona/sdk, so the Flue build must be
// able to resolve it AND its full transitive closure (@daytona/api-client, axios,
// @aws-sdk/*, @opentelemetry/*, …). A bare `cp` of the @daytona/sdk package is not
// enough: in the pnpm store its dependencies are external symlinks into the
// virtual store, so the closure is lost the moment the package is copied out.
// pnpm's own `deploy` can't help here — @daytona/sdk is not a workspace package;
// the only workspace package that declares it (examples/hello-world) also pulls in
// the Cloudflare `agents` closure that the kit deliberately prunes.
//
// So we walk @daytona/sdk's dependency graph ourselves, resolving each package the
// way Node would (from its parent, then the store's hoisted view), and copy every
// one dereferenced (cp -RL) into a flat nested node_modules. The result:
//
//   <out>/package.json, <out>/cjs/…              (the @daytona/sdk package itself)
//   <out>/node_modules/<dep>/…                   (its full closure, flat, real files)
//
// linkFlueBuildDependencies stages <out> at build_root/node_modules/@daytona/sdk,
// and Node resolution from daytona's own files walks up into that nested
// node_modules, so the whole closure resolves at build time.
//
//   usage: node stage-daytona-closure.mjs <flue-source-root> <out-dir>
//   exit:  0 ok · 2 @daytona/sdk unresolvable · 3 closure incomplete · 64 usage
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import path from "node:path";
import fs from "node:fs";

const [, , sourceArg, outArg] = process.argv;
if (!sourceArg || !outArg) {
  console.error("usage: node stage-daytona-closure.mjs <flue-source-root> <out-dir>");
  process.exit(64);
}
const SRC = path.resolve(sourceArg);
const OUT = path.resolve(outArg);
// pnpm's isolated store keeps a hoisted view of every installed package here.
const STORE_NM = path.join(SRC, "node_modules", ".pnpm", "node_modules");
const PNPM_ROOT = path.join(SRC, "node_modules", ".pnpm");
if (!fs.existsSync(STORE_NM)) {
  console.error(`pnpm store not found under ${SRC} (expected node_modules/.pnpm)`);
  process.exit(2);
}

// resolvePkgDir finds a package's real directory: node resolution from the parent
// first (nested deps), then the store's hoisted view, then a scan of the versioned
// .pnpm dirs as a last resort.
function resolvePkgDir(name, fromDir) {
  for (const base of [fromDir, STORE_NM]) {
    try {
      const req = createRequire(path.join(base, "index.js"));
      return path.dirname(req.resolve(name + "/package.json"));
    } catch {}
  }
  try {
    for (const entry of fs.readdirSync(PNPM_ROOT)) {
      const cand = path.join(PNPM_ROOT, entry, "node_modules", name);
      if (fs.existsSync(path.join(cand, "package.json"))) return cand;
    }
  } catch {}
  return null;
}
function readPkg(dir) {
  try {
    return JSON.parse(fs.readFileSync(path.join(dir, "package.json"), "utf8"));
  } catch {
    return {};
  }
}
function depNames(pkg) {
  return Object.keys({ ...(pkg.dependencies || {}), ...(pkg.optionalDependencies || {}) });
}
function copyDereferenced(from, to) {
  fs.mkdirSync(path.dirname(to), { recursive: true });
  execFileSync("cp", ["-RL", from + "/.", to + "/"]);
}

fs.rmSync(OUT, { recursive: true, force: true });
fs.mkdirSync(OUT, { recursive: true });

const rootDir = resolvePkgDir("@daytona/sdk", STORE_NM);
if (!rootDir) {
  console.error("cannot resolve @daytona/sdk from the Flue store");
  process.exit(2);
}
// The @daytona/sdk package itself → OUT/ (drop any copied node_modules; we rebuild
// the closure flat below so no store symlinks survive).
copyDereferenced(rootDir, OUT);
fs.rmSync(path.join(OUT, "node_modules"), { recursive: true, force: true });
fs.mkdirSync(path.join(OUT, "node_modules"), { recursive: true });

const seen = new Set(["@daytona/sdk"]);
const queue = depNames(readPkg(rootDir)).map((n) => ({ name: n, fromDir: rootDir }));
const missing = [];
let copied = 0;
while (queue.length) {
  const { name, fromDir } = queue.shift();
  if (seen.has(name)) continue;
  seen.add(name);
  const dir = resolvePkgDir(name, fromDir);
  if (!dir) {
    missing.push(name);
    continue;
  }
  copyDereferenced(dir, path.join(OUT, "node_modules", name));
  copied++;
  for (const dn of depNames(readPkg(dir))) if (!seen.has(dn)) queue.push({ name: dn, fromDir: dir });
}
// Belt and braces: no symlink may survive into a kit input (the packager rejects
// them so an input can never escape its declared tree).
execFileSync("find", [OUT, "-type", "l", "-delete"]);

if (missing.length) {
  console.error(`incomplete @daytona/sdk closure; unresolvable: ${missing.join(", ")}`);
  process.exit(3);
}
console.error(`[authoring-kit] staged @daytona/sdk closure: ${copied} dependency packages`);
