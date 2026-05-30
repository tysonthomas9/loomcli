const fs = require("fs");
const path = require("path");
const vm = require("vm");
const { stripTypeScriptTypes } = require("node:module");

const moduleCache = new Map();

function defineWorkflow(config) {
  return { __loomType: "workflow", ...config };
}

function defineTool(config) {
  return { __loomType: "tool", ...config };
}

const trigger = {
  issueLabelAdded(config = {}) {
    return { event: "issue.label_added", filter: config };
  },
};

const runtime = {
  local(config = {}) {
    return { __loomType: "runtime", provider: "local", ...config };
  },
  podman(config = {}) {
    return { __loomType: "runtime", provider: "other", ...config };
  },
  remote(config = {}) {
    return { __loomType: "runtime", provider: config.provider || "e2b", ...config };
  },
};

function transformSource(src, file) {
  let transformed = src;
  if (typeof stripTypeScriptTypes === "function") {
    transformed = stripTypeScriptTypes(transformed, { mode: "strip", sourceUrl: file });
  }
  return transformed
    .replace(/import\s+type\s+[^;]+;?/g, "")
    .replace(importStatementRegex(), "")
    .replace(/export\s+type\s+[^;]+;?/g, "")
    .replace(/export\s+interface\s+[A-Za-z0-9_]+\s*\{[^}]*\}/gs, "")
    .replace(/export\s+default\s+/, "module.exports.default = ")
    .replace(/export\s+const\s+([A-Za-z0-9_]+)\s*=/g, "const $1 = module.exports.$1 =")
    .replace(/export\s+function\s+([A-Za-z0-9_]+)\s*\(/g, "function $1(");
}

function importStatementRegex() {
  return /import\s+(?!type)(?:[^'";]+?\s+from\s+)?['"][^'"]+['"](?:\s+(?:with|assert)\s+\{[^}]*\})?\s*;?/g;
}

function evaluateModule(file) {
  file = path.resolve(file);
  if (moduleCache.has(file)) return moduleCache.get(file);
  const src = fs.readFileSync(file, "utf8");
  const module = { exports: {} };
  const imported = importedBindings(file, src);
  const sandbox = {
    module,
    exports: module.exports,
    console,
    ...imported,
    defineWorkflow,
    defineTool,
    trigger,
    runtime,
  };
  const record = { source: src, value: undefined, exports: module.exports };
  moduleCache.set(file, record);
  vm.runInNewContext(transformSource(src, file), sandbox, { filename: file });
  record.value = module.exports.default;
  record.exports = module.exports;
  return record;
}

function importedBindings(file, src) {
  const out = {};
  const re = /import\s+(?!type)([^'";]+?)\s+from\s+['"]([^'"]+)['"](?:\s+(?:with|assert)\s+\{[^}]*\})?\s*;?/g;
  for (const match of src.matchAll(re)) {
    const clause = match[1].trim();
    const spec = match[2].trim();
    if (!spec.startsWith(".")) continue;
    const dep = evaluateModule(resolveRelativeImport(file, spec));
    bindImportClause(out, clause, dep);
  }
  return out;
}

function bindImportClause(out, clause, dep) {
  if (clause.startsWith("* as ")) {
    out[clause.slice(5).trim()] = dep.exports;
    return;
  }
  if (clause.startsWith("{")) {
    bindNamedImports(out, clause, dep);
    return;
  }
  const comma = clause.indexOf(",");
  const defaultName = (comma === -1 ? clause : clause.slice(0, comma)).trim();
  if (defaultName) out[defaultName] = dep.value;
  if (comma !== -1) {
    const rest = clause.slice(comma + 1).trim();
    if (rest.startsWith("{")) bindNamedImports(out, rest, dep);
  }
}

function bindNamedImports(out, clause, dep) {
  const inner = clause.replace(/^\{/, "").replace(/\}$/, "");
  for (const part of inner.split(",")) {
    const item = part.trim();
    if (!item) continue;
    const [imported, local] = item.split(/\s+as\s+/);
    const importedName = imported.trim();
    const localName = (local || imported).trim();
    out[localName] = dep.exports[importedName];
  }
}

function resolveRelativeImport(fromFile, spec) {
  const base = path.resolve(path.dirname(fromFile), spec);
  const candidates = [];
  if (path.extname(base)) {
    candidates.push(base);
  } else {
    candidates.push(base + ".ts", base + ".mts", base + ".js", base + ".mjs", path.join(base, "index.ts"));
  }
  for (const candidate of candidates) {
    if (fs.existsSync(candidate) && fs.statSync(candidate).isFile()) return candidate;
  }
  throw new Error(`${fromFile}: cannot resolve import ${spec}`);
}

function makeContext(request) {
  const logs = [];
  const operations = [];
  const input = request.input && typeof request.input === "object" ? request.input : {};
  const parentId = String(input.parentId || input.parent_id || "");

  const byParent = (items, requestedParent) => {
    if (!requestedParent || requestedParent === parentId) return items || [];
    return (items || []).filter((item) => !item.parent || item.parent === requestedParent);
  };

  const log = (level, message, attributes) => {
    logs.push({ level, message: String(message || ""), attributes: attributes || {} });
  };

  const ctx = {
    id: request.id,
    input,
    payload: input,
    env: request.env || {},
    log: {
      info: (message, attributes) => log("info", message, attributes),
      warn: (message, attributes) => log("warn", message, attributes),
      error: (message, attributes) => log("error", message, attributes),
    },
    workItems: {
      readyChildren: async (requestedParent) => byParent(request.readyChildren, String(requestedParent || "")),
      blockedChildren: async (requestedParent) => byParent(request.blockedChildren, String(requestedParent || "")),
      listChildren: async (requestedParent) => byParent(request.childWorkItems, String(requestedParent || "")),
    },
    taskRuns: {
      ensure: async (params) => {
        const op = { type: "taskRuns.ensure", params: params || {} };
        operations.push(op);
        return { accepted: true, ...op.params };
      },
    },
  };
  return { ctx, logs, operations };
}

async function readStdin() {
  let data = "";
  for await (const chunk of process.stdin) data += chunk;
  return JSON.parse(data || "{}");
}

async function main() {
  const request = await readStdin();
  const sourcePath = path.resolve(request.sourcePath || "");
  const { value } = evaluateModule(sourcePath);
  if (!value || value.__loomType !== "workflow") {
    throw new Error(`${sourcePath}: default export must be defineWorkflow(...)`);
  }
  if (typeof value.run !== "function") {
    throw new Error(`${sourcePath}: workflow has no run(ctx) function`);
  }
  const { ctx, logs, operations } = makeContext(request);
  const result = await value.run(ctx);
  process.stdout.write(JSON.stringify({ result: result === undefined ? null : result, logs, operations }));
}

main().catch((error) => {
  process.stderr.write(error && error.stack ? error.stack : String(error));
  process.exit(1);
});
