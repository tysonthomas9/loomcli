const fs = require("fs");
const path = require("path");
const vm = require("vm");
const { stripTypeScriptTypes } = require("node:module");

const moduleCache = new Map();
const request = JSON.parse(fs.readFileSync(0, "utf8") || "{}");
const root = path.resolve(request.root || ".");

function defineTool(config) {
  const handler = config && (config.handler || (typeof config.execute === "function" ? "typescript" : undefined));
  return { __loomType: "tool", name: config && config.name, ...config, handler };
}

const Type = new Proxy(
  {},
  {
    get(_target, prop) {
      return (...args) => ({ __loomType: "schema", kind: String(prop), args });
    },
  },
);

function transformSource(src, file) {
  return stripTypeScriptSyntax(src, file)
    .replace(/import\s+type\s+[^;]+;?/g, "")
    .replace(importStatementRegex(), "")
    .replace(/export\s+type\s+[^;]+;?/g, "")
    .replace(/export\s+interface\s+[A-Za-z0-9_]+\s*\{[^}]*\}/gs, "")
    .replace(/export\s+default\s+/, "module.exports.default = ")
    .replace(/export\s+const\s+([A-Za-z0-9_]+)\s*=/g, "const $1 = module.exports.$1 =")
    .replace(/export\s+function\s+([A-Za-z0-9_]+)\s*\(/g, "function $1(");
}

function stripTypeScriptSyntax(src, file) {
  if (typeof stripTypeScriptTypes === "function") {
    return stripTypeScriptTypes(src, { mode: "strip", sourceUrl: file });
  }
  return stripTypeScriptFallback(src);
}

function stripTypeScriptFallback(src) {
  return src
    .replace(/\s+satisfies\s+[A-Za-z_$][\w$]*(?:\[\])?/g, "")
    .replace(/\s+as\s+const\b/g, "")
    .replace(/\s+as\s+[A-Za-z_$][\w$]*(?:\[\])?/g, "")
    .replace(/\)\s*:\s*[^=]+=>/g, ") =>")
    .replace(/\((\s*[A-Za-z_$][\w$]*)\??\s*:\s*[^)=,]+(\s*)\)/g, "($1$2)")
    .replace(/(\b(?:const|let|var)\s+[A-Za-z_$][\w$]*)\s*:\s*[^=;]+=/g, "$1 =");
}

function importStatementRegex() {
  return /import\s+(?!type)(?:[^'";]+?\s+from\s+)?['"][^'"]+['"](?:\s+(?:with|assert)\s+\{[^}]*\})?\s*;?/g;
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
  if (!insideRoot(base)) {
    throw new Error(`${fromFile}: import ${spec} resolves outside project root`);
  }
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

function insideRoot(file) {
  const rel = path.relative(root, file);
  return rel === "" || (!rel.startsWith("..") && !path.isAbsolute(rel));
}

function evaluateModule(file) {
  file = path.resolve(file);
  if (moduleCache.has(file)) return moduleCache.get(file);
  const src = fs.readFileSync(file, "utf8");
  const module = { exports: {} };
  const sandbox = {
    module,
    exports: module.exports,
    console,
    defineTool,
    Type,
    schema: Type,
    ...importedBindings(file, src),
  };
  const record = { value: undefined, exports: module.exports };
  moduleCache.set(file, record);
  vm.runInNewContext(transformSource(src, file), sandbox, { filename: file });
  record.value = module.exports.default;
  record.exports = module.exports;
  return record;
}

async function main() {
  const file = path.resolve(request.sourcePath || "");
  if (!insideRoot(file)) throw new Error("tool source path resolves outside project root");
  const { value, exports } = evaluateModule(file);
  const tool = value && value.__loomType === "tool" ? value : Object.values(exports).find((item) => item && item.__loomType === "tool");
  if (!tool || typeof tool.execute !== "function") {
    throw new Error("tool does not export an executable defineTool(...) handler");
  }
  const result = await tool.execute(request.arguments || {});
  process.stdout.write(JSON.stringify({ result }));
}

main().catch((err) => {
  process.stderr.write(String((err && err.stack) || err) + "\n");
  process.exit(1);
});
