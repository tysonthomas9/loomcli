#!/usr/bin/env node
// gen.mjs — regenerate the managed @loom/sdk driver surface from op-spec.mjs.
//
// Targets (single source of truth = op-spec.mjs):
//   - driver.js            : the `<gen:namespaces>` region (Object.freeze namespace assemblies)
//   - driver.d.ts          : the `<gen:namespaces>` region (readonly namespace types)
//   - api-surface.v1.json  : the `ops` + `client.namespaces` entries
//
// Usage: `npm run gen` (or `node gen.mjs`). The SDK contract tests
// (contract.test.mjs) and the Go op table (contract_test.go) verify the result.
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { generatedNamespaces } from "./op-spec.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const p = (f) => join(here, f);

const BEGIN = "// <gen:namespaces>";
const END = "// </gen:namespaces>";

// Replace the lines strictly between the BEGIN and END marker lines, keeping
// the markers. `body` is an array of already-indented lines.
function replaceRegion(src, body, file) {
  const lines = src.split("\n");
  const b = lines.findIndex((l) => l.includes(BEGIN));
  const e = lines.findIndex((l) => l.includes(END));
  if (b === -1 || e === -1 || e <= b) {
    throw new Error(`gen markers ${BEGIN} / ${END} not found (or out of order) in ${file}`);
  }
  return [...lines.slice(0, b + 1), ...body, ...lines.slice(e)].join("\n");
}

// driver.js runtime: this.<ns> = Object.freeze({ <method>: (input) => this.#httpCall("<op>", {...}) })
function jsNamespaceLines(ns) {
  const out = [`    this.${ns.namespace} = Object.freeze({`];
  for (const op of ns.ops) {
    const params = op.fields.map((f) => `${f.name}: input.${f.name}`).join(", ");
    out.push(`      ${op.method}: (input = {}) => this.#httpCall("${op.op}", ${params ? `{ ${params} }` : "{}"}),`);
  }
  out.push("    });");
  return out;
}

// driver.d.ts types: readonly <ns>: { <method>(input): Promise<...>; }
function dtsNamespaceLines(ns) {
  const out = [];
  if (ns.doc) out.push(`  /** ${ns.doc} */`);
  out.push(`  readonly ${ns.namespace}: {`);
  for (const op of ns.ops) {
    const anyRequired = op.fields.some((f) => f.required);
    const members = op.fields.map((f) => `${f.name}${f.required ? "" : "?"}: ${f.type}`).join("; ");
    const inputType = op.fields.length ? `{ ${members} }` : "Record<string, unknown>";
    out.push(`    ${op.method}(input${anyRequired ? "" : "?"}: ${inputType}): Promise<${op.result}>;`);
  }
  out.push("  };");
  return out;
}

function updateManifest(manifest) {
  for (const ns of generatedNamespaces) {
    for (const op of ns.ops) {
      manifest.ops[op.op] = { method: op.httpMethod, fields: op.fields.map((f) => f.name) };
    }
    manifest.client.namespaces[ns.namespace] = ns.ops.map((o) => o.method);
  }
  return manifest;
}

const jsBody = generatedNamespaces.flatMap(jsNamespaceLines);
writeFileSync(p("driver.js"), replaceRegion(readFileSync(p("driver.js"), "utf8"), jsBody, "driver.js"));

const dtsBody = generatedNamespaces.flatMap(dtsNamespaceLines);
writeFileSync(p("driver.d.ts"), replaceRegion(readFileSync(p("driver.d.ts"), "utf8"), dtsBody, "driver.d.ts"));

const manifest = updateManifest(JSON.parse(readFileSync(p("api-surface.v1.json"), "utf8")));
writeFileSync(p("api-surface.v1.json"), JSON.stringify(manifest, null, 2) + "\n");

const opCount = generatedNamespaces.reduce((n, ns) => n + ns.ops.length, 0);
console.log(`gen: driver.js + driver.d.ts + api-surface.v1.json updated (${generatedNamespaces.length} namespace(s), ${opCount} op(s))`);
