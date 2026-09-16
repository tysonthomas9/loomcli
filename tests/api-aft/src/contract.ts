// Contract layer (oracle L4 + the coverage denominator).
//
// Loads BOTH OpenAPI specs, dereferences them, and indexes every operation so an
// observed exchange can be matched back to what the spec promised. The spec is a
// deliberately WEAK, ADVISORY oracle here -- see power() for why -- so nothing in
// this file is allowed to fail a run on its own.

import { readFileSync } from "node:fs";
import { parse as parseYaml } from "yaml";

export type Service = "loom" | "fleetdb";

export type Operation = {
  service: Service;
  method: string;
  pathTemplate: string;
  operationId: string;
  regex: RegExp;
  paramNames: string[];
  safe: boolean;
  prose: string;
  streamingDeclared: boolean;
  requestSchema: unknown | null;
  responses: Map<string, unknown>;
  declaredStatuses: string[];
};

export type ContractIndex = {
  service: Service;
  specPath: string;
  operations: Operation[];
  danglingRefs: { ref: string; at: string }[];
  stats: {
    operations: number;
    responsesWithSchema: number;
    bareObjectResponses: number;
    enumConstrainedProps: number;
    totalProps: number;
    patternProps: number;
    closedObjects: number;
  };
};

const MAX_DEREF_DEPTH = 24;

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** Walk the whole document collecting $ref targets that do not resolve. */
function findDanglingRefs(doc: unknown): { ref: string; at: string }[] {
  const out: { ref: string; at: string }[] = [];
  const walk = (node: unknown, path: string): void => {
    if (Array.isArray(node)) {
      node.forEach((n, i) => walk(n, `${path}[${i}]`));
      return;
    }
    if (!isObj(node)) return;
    const ref = node["$ref"];
    if (typeof ref === "string") {
      if (resolvePointer(doc, ref) === undefined) out.push({ ref, at: path });
    }
    for (const [k, v] of Object.entries(node)) {
      if (k === "$ref") continue;
      walk(v, `${path}/${k}`);
    }
  };
  walk(doc, "#");
  return out;
}

function resolvePointer(doc: unknown, ref: string): unknown {
  if (!ref.startsWith("#/")) return undefined;
  let cur: unknown = doc;
  for (const raw of ref.slice(2).split("/")) {
    const key = raw.replace(/~1/g, "/").replace(/~0/g, "~");
    if (!isObj(cur)) return undefined;
    cur = cur[key];
    if (cur === undefined) return undefined;
  }
  return cur;
}

/** Resolve $refs inline. A ref that does not resolve becomes `true` (accept-anything). */
function deref(doc: unknown, node: unknown, depth = 0): unknown {
  if (depth > MAX_DEREF_DEPTH) return true;
  if (Array.isArray(node)) return node.map((n) => deref(doc, n, depth + 1));
  if (!isObj(node)) return node;
  if (typeof node["$ref"] === "string") {
    const target = resolvePointer(doc, node["$ref"] as string);
    if (target === undefined) return true;
    return deref(doc, target, depth + 1);
  }
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(node)) out[k] = deref(doc, v, depth + 1);
  return out;
}

/**
 * Synthesize the "strict twin": the same schema with unevaluatedProperties:false at
 * every object root. `unevaluated*` rather than `additionalProperties` so allOf still
 * composes. Neither spec sets additionalProperties:false anywhere, so undocumented
 * response fields are invisible without this.
 */
export function strictify(schema: unknown, depth = 0): unknown {
  if (depth > MAX_DEREF_DEPTH) return schema;
  if (Array.isArray(schema)) return schema.map((s) => strictify(s, depth + 1));
  if (!isObj(schema)) return schema;
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(schema)) out[k] = strictify(v, depth + 1);
  const looksObject =
    out["type"] === "object" || isObj(out["properties"]) || Array.isArray(out["allOf"]);
  if (looksObject && out["unevaluatedProperties"] === undefined && out["additionalProperties"] === undefined) {
    out["unevaluatedProperties"] = false;
  }
  return out;
}

/**
 * Oracle Power Ledger: how much bug-finding force the spec actually carries for one
 * (operation, status). 0 = the spec says nothing checkable; 4 = every field typed,
 * required and enum-constrained. Reported so "N operations covered" can never be
 * read as "N operations checked".
 */
export function power(schema: unknown): number {
  if (schema === undefined || schema === null) return 0;
  if (!isObj(schema)) return 0;
  const keys = Object.keys(schema);
  if (keys.length === 0) return 0;
  if (schema["type"] === "object" && !isObj(schema["properties"])) return 0; // bare object escape
  let p = 1;
  const props = isObj(schema["properties"]) ? schema["properties"] : null;
  if (props) p = 2;
  if (Array.isArray(schema["required"]) && (schema["required"] as unknown[]).length > 0) p = 3;
  if (props) {
    const hasEnum = Object.values(props).some(
      (v) => isObj(v) && (Array.isArray(v["enum"]) || typeof v["pattern"] === "string"),
    );
    if (hasEnum && p === 3) p = 4;
  }
  return p;
}

function templateToRegex(t: string): { regex: RegExp; paramNames: string[] } {
  const names: string[] = [];
  const src = t.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace(/\\\{([^}]+)\\\}/g, (_m, n) => {
    names.push(String(n));
    return "([^/]+)";
  });
  return { regex: new RegExp(`^${src}$`), paramNames: names };
}

export function compile(specPath: string, service: Service): ContractIndex {
  const doc = parseYaml(readFileSync(specPath, "utf8")) as Record<string, unknown>;
  const dangling = findDanglingRefs(doc);
  const operations: Operation[] = [];
  const stats = {
    operations: 0,
    responsesWithSchema: 0,
    bareObjectResponses: 0,
    enumConstrainedProps: 0,
    totalProps: 0,
    patternProps: 0,
    closedObjects: 0,
  };

  const paths = isObj(doc["paths"]) ? doc["paths"] : {};
  for (const [pathTemplate, itemRaw] of Object.entries(paths)) {
    if (!isObj(itemRaw)) continue;
    for (const method of ["get", "post", "put", "patch", "delete"]) {
      const opRaw = itemRaw[method];
      if (!isObj(opRaw)) continue;
      stats.operations++;
      const responses = new Map<string, unknown>();
      const respRaw = isObj(opRaw["responses"]) ? opRaw["responses"] : {};
      for (const [code, r] of Object.entries(respRaw)) {
        if (!isObj(r)) continue;
        const content = isObj(r["content"]) ? r["content"] : {};
        const json = content["application/json"];
        const schema = isObj(json) ? json["schema"] : undefined;
        if (schema === undefined) continue;
        stats.responsesWithSchema++;
        const d = deref(doc, schema);
        if (isObj(d) && d["type"] === "object" && !isObj(d["properties"])) stats.bareObjectResponses++;
        responses.set(code, d);
      }
      let requestSchema: unknown | null = null;
      const rb = opRaw["requestBody"];
      if (isObj(rb)) {
        const c = isObj(rb["content"]) ? rb["content"] : {};
        const j = c["application/json"];
        if (isObj(j) && j["schema"] !== undefined) requestSchema = deref(doc, j["schema"]);
      }
      const prose = `${String(opRaw["summary"] ?? "")} ${String(opRaw["description"] ?? "")}`.toLowerCase();
      // The spec often says outright that an operation streams or long-polls; believe it.
      const streamingDeclared =
        /server-sent|event stream|sse\b|long-poll|long poll|blocks until|websocket/.test(prose) ||
        JSON.stringify(respRaw).includes("text/event-stream");
      const { regex, paramNames } = templateToRegex(pathTemplate);
      operations.push({
        service,
        method: method.toUpperCase(),
        pathTemplate,
        operationId: String(opRaw["operationId"] ?? `${method}:${pathTemplate}`),
        regex,
        paramNames,
        safe: method === "get",
        prose,
        streamingDeclared,
        requestSchema,
        responses,
        declaredStatuses: [...responses.keys()],
      });
    }
  }

  // Schema-quality census over components -- the evidence for treating L4 as advisory.
  const comps = resolvePointer(doc, "#/components/schemas");
  const countProps = (n: unknown, depth = 0): void => {
    if (depth > 12) return;
    if (Array.isArray(n)) return n.forEach((x) => countProps(x, depth + 1));
    if (!isObj(n)) return;
    if (n["additionalProperties"] === false || n["unevaluatedProperties"] === false) stats.closedObjects++;
    if (isObj(n["properties"])) {
      for (const v of Object.values(n["properties"])) {
        stats.totalProps++;
        if (isObj(v) && Array.isArray(v["enum"])) stats.enumConstrainedProps++;
        if (isObj(v) && typeof v["pattern"] === "string") stats.patternProps++;
      }
    }
    for (const v of Object.values(n)) countProps(v, depth + 1);
  };
  countProps(comps);

  return { service, specPath, operations, danglingRefs: dangling, stats };
}

/** Match an observed request back to the operation the spec declares for it. */
export function matchOperation(
  index: ContractIndex,
  method: string,
  pathname: string,
): { op: Operation; params: Record<string, string> } | null {
  let best: { op: Operation; params: Record<string, string> } | null = null;
  for (const op of index.operations) {
    if (op.method !== method.toUpperCase()) continue;
    const m = op.regex.exec(pathname);
    if (!m) continue;
    const params: Record<string, string> = {};
    op.paramNames.forEach((n, i) => (params[n] = decodeURIComponent(m[i + 1] ?? "")));
    // Prefer the least-parameterised template (a literal beats a {param}).
    if (!best || op.paramNames.length < best.op.paramNames.length) best = { op, params };
  }
  return best;
}
