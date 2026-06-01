const fs = require("fs");
const path = require("path");
const crypto = require("crypto");
const vm = require("vm");
const { stripTypeScriptTypes } = require("node:module");

const root = path.resolve(process.argv[2] || ".");
const moduleCache = new Map();
const skillRegistry = new Map();
const ignoredResourceDirs = new Set([".git", ".cache", ".turbo", ".wrangler", "dist", "node_modules"]);
const ignoredResourceFiles = new Set([".DS_Store"]);
const sensitiveResourceNames = new Set([".env", ".npmrc", ".netrc", "_netrc", ".pypirc", "credentials.json", "secret", "secrets"]);

function hashSource(data) {
  return crypto.createHash("sha256").update(data).digest("hex");
}

function version(hash) {
  return hash.slice(0, 12);
}

function readEntrypoints(dir) {
  if (!fs.existsSync(dir)) return [];
  return fs
    .readdirSync(dir, { withFileTypes: true })
    .filter((entry) => entry.isFile() && entry.name.endsWith(".ts"))
    .map((entry) => path.join(dir, entry.name))
    .sort();
}

function readSkillEntrypoints(dir) {
  const out = readEntrypoints(dir);
  if (!fs.existsSync(dir)) return out;
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    const skillFile = path.join(dir, entry.name, "SKILL.md");
    if (fs.existsSync(skillFile) && fs.statSync(skillFile).isFile()) {
      out.push(skillFile);
    }
  }
  return out.sort();
}

function toolProxy(parts) {
  return new Proxy(function loomTool() {}, {
    get(_target, prop) {
      if (prop === "toJSON") return () => parts.join(".");
      if (prop === "toString") return () => parts.join(".");
      if (prop === Symbol.toPrimitive) return () => parts.join(".");
      return toolProxy(parts.concat(String(prop)));
    },
  });
}

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
  return stripTypeDeclarations(src)
    .replace(/\s+satisfies\s+[A-Za-z_$][\w$]*(?:\[\])?/g, "")
    .replace(/\s+as\s+const\b/g, "")
    .replace(/\s+as\s+[A-Za-z_$][\w$]*(?:\[\])?/g, "")
    .replace(/\)\s*:\s*[^=]+=>/g, ") =>")
    .replace(/\((\s*[A-Za-z_$][\w$]*)\??\s*:\s*[^)=,]+(\s*)\)/g, "($1$2)")
    .replace(/(\b(?:const|let|var)\s+[A-Za-z_$][\w$]*)\s*:\s*[^=;]+=/g, "$1 =");
}

function stripTypeDeclarations(src) {
  const out = [];
  let skipping = false;
  let depth = 0;
  for (const line of src.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!skipping && /^(export\s+)?(type|interface)\s+[A-Za-z_$][\w$]*/.test(trimmed)) {
      skipping = true;
      depth = braceDelta(line);
      if (typeDeclarationComplete(trimmed, depth)) skipping = false;
      continue;
    }
    if (skipping) {
      depth += braceDelta(line);
      if (typeDeclarationComplete(trimmed, depth)) skipping = false;
      continue;
    }
    out.push(line);
  }
  return out.join("\n");
}

function braceDelta(line) {
  return (line.match(/\{/g) || []).length - (line.match(/\}/g) || []).length;
}

function typeDeclarationComplete(line, depth) {
  return depth <= 0 && /[};]\s*$/.test(line);
}

function importStatementRegex() {
  return /import\s+(?!type)(?:[^'";]+?\s+from\s+)?['"][^'"]+['"](?:\s+(?:with|assert)\s+\{[^}]*\})?\s*;?/g;
}

function defineConfig(config) {
  return config || {};
}

function defineAgent(config) {
  return { __loomType: "agent", ...config };
}

function createAgent(config, overrides) {
  if (typeof config === "function") {
    return { __loomType: "agent", __loomFactory: config };
  }
  if (config && config.__loomType === "agent_profile") {
    return agentFromProfile(config, overrides || {});
  }
  return { __loomType: "agent", ...(config || {}), ...(overrides || {}) };
}

function defineAgentProfile(config) {
  return { __loomType: "agent_profile", ...config };
}

function agentFromProfile(profile, overrides) {
  const merged = {
    ...profile,
    ...overrides,
    __loomType: "agent",
    profileName: stringValue(overrides.profileName || overrides.profile_name || profile.name),
    profile_name: stringValue(overrides.profileName || overrides.profile_name || profile.name),
  };
  for (const key of ["skills", "tools", "allowedCommands", "deniedCommands", "repos", "env"]) {
    merged[key] = uniqueStrings(stringArray(profile[key]).concat(stringArray(overrides[key])));
  }
  const profilePolicy = profile.policy && typeof profile.policy === "object" ? profile.policy : {};
  const overridePolicy = overrides.policy && typeof overrides.policy === "object" ? overrides.policy : {};
  merged.policy = { ...profilePolicy, ...overridePolicy };
  for (const key of ["allowedCommands", "deniedCommands"]) {
    merged.policy[key] = uniqueStrings(stringArray(profilePolicy[key]).concat(stringArray(overridePolicy[key])));
  }
  return merged;
}

function defineSkill(config) {
  return { __loomType: "skill", ...config };
}

function defineWorkflow(config) {
  return { __loomType: "workflow", ...config };
}

function defineTool(config) {
  const handler = config && (config.handler || (typeof config.execute === "function" ? "typescript" : undefined));
  return { __loomType: "tool", name: config && config.name, ...config, handler };
}

function defineRuntimeProfile(config) {
  return { __loomType: "runtime", provider: "local", ...(config || {}) };
}

const runtime = {
  local(config = {}) {
    return defineRuntimeProfile({ provider: "local", ...config });
  },
  podman(config = {}) {
    return defineRuntimeProfile({ provider: "other", ...config });
  },
  remote(config = {}) {
    return defineRuntimeProfile({ provider: config.provider || "e2b", ...config });
  },
};

const schema = new Proxy(
  {},
  {
    get(_target, prop) {
      return (...args) => ({ __loomType: "schema", kind: String(prop), args });
    },
  },
);

const Type = schema;

const trigger = {
  issueLabelAdded(config = {}) {
    return trigger.event("issue.label_added", config);
  },
  event(event, filter = {}) {
    return { event: String(event), filter: stringRecord(filter) };
  },
  cron(schedule, filter = {}) {
    return trigger.event("schedule.cron", { ...filter, schedule });
  },
  webhook(provider, filter = {}) {
    return trigger.event(`webhook.${provider}`, filter);
  },
  github(event, filter = {}) {
    return trigger.event(`github.${event}`, filter);
  },
  datadogAlert(filter = {}) {
    return trigger.event("datadog.alert", filter);
  },
  chat(provider, filter = {}) {
    return trigger.event(`chat.${provider}`, filter);
  },
};

function stringRecord(value = {}) {
  return Object.fromEntries(
    Object.entries(value || {})
      .filter(([, v]) => v != null)
      .map(([k, v]) => [k, String(v)]),
  );
}

function evaluateModule(file) {
  file = path.resolve(file);
  if (moduleCache.has(file)) return moduleCache.get(file);
  if (file.endsWith(".md")) {
    const markdownRecord = evaluateMarkdownSkill(file);
    moduleCache.set(file, markdownRecord);
    return markdownRecord;
  }
  const src = fs.readFileSync(file, "utf8");
  const module = { exports: {} };
  const imported = importedBindings(file, src);
  const sandbox = {
    module,
    exports: module.exports,
    console,
    ...imported,
    defineConfig,
    defineAgent,
    createAgent,
    defineAgentProfile,
    defineSkill,
    defineWorkflow,
    defineTool,
    defineRuntimeProfile,
    runtime,
    schema,
    Type,
    trigger,
    github: toolProxy(["github"]),
    fleetdb: toolProxy(["fleetdb"]),
  };
  const record = { source: src, value: undefined, exports: module.exports };
  moduleCache.set(file, record);
  vm.runInNewContext(transformSource(src, file), sandbox, { filename: file });
  record.value = module.exports.default;
  record.exports = module.exports;
  registerExportedSkills(file, src, module.exports);
  return record;
}

function importedBindings(file, src) {
  const out = {};
  const re = /import\s+(?!type)([^'";]+?)\s+from\s+['"]([^'"]+)['"](?:\s+(?:with|assert)\s+\{[^}]*\})?\s*;?/g;
  for (const match of src.matchAll(re)) {
    const clause = match[1].trim();
    const spec = match[2].trim();
    if (!spec.startsWith(".")) {
      if (spec === "valibot") bindNonRelativeModule(out, clause, schema);
      continue;
    }
    const dep = evaluateModule(resolveRelativeImport(file, spec));
    bindImportClause(out, clause, dep);
  }
  return out;
}

function bindNonRelativeModule(out, clause, value) {
  if (clause.startsWith("* as ")) {
    out[clause.slice(5).trim()] = value;
    return;
  }
  if (!clause.startsWith("{")) {
    const name = clause.split(",", 1)[0].trim();
    if (name) out[name] = value;
  }
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
    candidates.push(base + ".ts", base + ".mts", base + ".js", base + ".mjs", path.join(base, "index.ts"), path.join(base, "SKILL.md"));
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

function evaluateMarkdownSkill(file) {
  const source = fs.readFileSync(file, "utf8");
  const skill = registerSkill(markdownSkill(file, source));
  return { source, value: skill, exports: { default: skill } };
}

function markdownSkill(file, source) {
  const parsed = parseMarkdownSkill(source);
  const dirName = path.basename(path.dirname(file));
  const name = stringValue(parsed.frontmatter.name || dirName);
  const resources = listSkillResources(file);
  const sourceHash = hashSkillBundle(file, source, resources);
  return {
    __loomType: "skill",
    name,
    description: stringValue(parsed.frontmatter.description),
    source_path: file,
    source_hash: sourceHash,
    version: version(sourceHash),
    instructions: parsed.body.trim(),
    resources,
  };
}

function parseMarkdownSkill(source) {
  if (!source.startsWith("---")) return { frontmatter: {}, body: source };
  const close = source.indexOf("\n---", 3);
  if (close === -1) return { frontmatter: {}, body: source };
  const frontmatterSource = source.slice(3, close).trim();
  const body = source.slice(source.indexOf("\n", close + 1) + 1);
  const frontmatter = {};
  for (const line of frontmatterSource.split(/\r?\n/)) {
    const match = line.match(/^([A-Za-z0-9_-]+)\s*:\s*(.*)$/);
    if (!match) continue;
    frontmatter[match[1]] = match[2].trim().replace(/^["']|["']$/g, "");
  }
  return { frontmatter, body };
}

function listSkillResources(skillFile) {
  const dir = path.dirname(skillFile);
  const out = [];
  const walk = (current) => {
    for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
      const full = path.join(current, entry.name);
      const rel = path.relative(dir, full).split(path.sep).join("/");
      if (entry.isSymbolicLink()) throw new Error(`${skillFile}: skill resources must not include symlinks: ${rel}`);
      if (entry.isDirectory()) {
        if (ignoredResourceDirs.has(entry.name) || entry.name === ".aws" || entry.name === ".ssh" || entry.name === ".gnupg") continue;
        walk(full);
        continue;
      }
      if (!entry.isFile() || full === skillFile || ignoredResourceFiles.has(entry.name)) continue;
      rejectSensitiveResource(skillFile, rel, entry.name);
      out.push(rel);
    }
  };
  walk(dir);
  return out.sort();
}

function rejectSensitiveResource(skillFile, rel, name) {
  if (
    sensitiveResourceNames.has(name) ||
    name.startsWith(".env.") ||
    name.startsWith(".dev.vars") ||
    name.endsWith(".key") ||
    name.endsWith(".pem") ||
    name.endsWith(".p12") ||
    name.endsWith(".pfx") ||
    name.startsWith("secret.") ||
    name.startsWith("secrets.")
  ) {
    throw new Error(`${skillFile}: refusing to package sensitive-looking skill resource ${rel}`);
  }
}

function hashSkillBundle(skillFile, source, resources) {
  const h = crypto.createHash("sha256");
  h.update("SKILL.md\0");
  h.update(source);
  const dir = path.dirname(skillFile);
  for (const resource of resources) {
    h.update("\0");
    h.update(resource);
    h.update("\0");
    h.update(fs.readFileSync(path.join(dir, resource)));
  }
  return h.digest("hex");
}

function registerExportedSkills(file, source, exports) {
  const hash = hashSource(source);
  const candidates = [exports.default, ...Object.values(exports)];
  for (const candidate of candidates) {
    if (!candidate || candidate.__loomType !== "skill") continue;
    registerSkill({
      ...candidate,
      source_path: candidate.source_path || file,
      source_hash: candidate.source_hash || hash,
      version: candidate.version || version(hash),
      instructions: stringValue(candidate.instructions),
      resources: stringArray(candidate.resources),
    });
  }
}

function projectConfig() {
  const configFile = path.join(root, "loom.config.ts");
  if (!fs.existsSync(configFile)) return {};
  const { value } = evaluateModule(configFile);
  return value || {};
}

function sourceRootDir(config = projectConfig()) {
  const sourceRoot = stringValue(config.sourceRoot || ".loom");
  if (sourceRoot === "" || path.isAbsolute(sourceRoot)) {
    throw new Error("loom.config.ts sourceRoot must be a relative path");
  }
  return path.join(root, sourceRoot);
}

function rejectMixedSourceRoots(selectedRoot) {
  const selected = path.resolve(selectedRoot);
  const candidates = [path.join(root, ".loom"), root];
  for (const candidate of candidates) {
    const resolved = path.resolve(candidate);
    if (resolved === selected) continue;
    const entries = authoredEntrypoints(resolved);
    if (entries.length === 0) continue;
    const selectedLabel = sourceRootLabel(selected);
    const candidateLabel = resolved === root ? "project root" : path.relative(root, resolved).split(path.sep).join("/");
    throw new Error(
      `mixed Loom TypeScript source roots: selected ${selectedLabel} but found ${candidateLabel} entrypoints: ${entries.join(", ")}. Move them under ${selectedLabel} or set exactly one sourceRoot in loom.config.ts.`,
    );
  }
}

function authoredEntrypoints(base) {
  const out = [];
  for (const dir of ["agents", "workflows", "runtimes", "tools"]) {
    for (const file of immediateTypeScriptFiles(path.join(base, dir))) {
      out.push(path.relative(root, file).split(path.sep).join("/"));
    }
  }
  const skillsDir = path.join(base, "skills");
  for (const file of immediateTypeScriptFiles(skillsDir)) {
    out.push(path.relative(root, file).split(path.sep).join("/"));
  }
  if (fs.existsSync(skillsDir)) {
    for (const entry of fs.readdirSync(skillsDir, { withFileTypes: true })) {
      if (!entry.isDirectory()) continue;
      const skillFile = path.join(skillsDir, entry.name, "SKILL.md");
      if (fs.existsSync(skillFile) && fs.statSync(skillFile).isFile()) {
        out.push(path.relative(root, skillFile).split(path.sep).join("/"));
      }
    }
  }
  return out.sort();
}

function immediateTypeScriptFiles(dir) {
  if (!fs.existsSync(dir)) return [];
  return fs
    .readdirSync(dir, { withFileTypes: true })
    .filter((entry) => entry.isFile() && entry.name.endsWith(".ts"))
    .map((entry) => path.join(dir, entry.name));
}

function sourceRootLabel(sourceRoot) {
  const rel = path.relative(root, sourceRoot).split(path.sep).join("/");
  return rel || "project root";
}

function modelPolicy(config = {}) {
  const models =
    config.models && typeof config.models === "object"
      ? config.models
      : config.modelPolicy && typeof config.modelPolicy === "object"
        ? config.modelPolicy
        : {};
  return compactObject({
    allowed_models: stringArray(
      models.allowed || models.allow || models.allowedModels || models.allowed_models || config.allowedModels || config.allowed_models,
    ),
    allowed_providers: stringArray(
      models.providers ||
        models.allowedProviders ||
        models.allowed_providers ||
        config.allowedModelProviders ||
        config.allowed_model_providers,
    ),
    allow_unknown: boolValue(models.allowUnknown || models.allow_unknown || config.allowUnknownModels || config.allow_unknown_models),
  });
}

function stringValue(v) {
  if (v == null) return "";
  if (typeof v === "object" && v.name != null) return String(v.name).trim();
  return String(v).trim();
}

function stringArray(v) {
  if (!Array.isArray(v)) return [];
  return v.map((item) => stringValue(item)).filter(Boolean);
}

function uniqueStrings(values) {
  return Array.from(new Set((values || []).map((item) => stringValue(item)).filter(Boolean)));
}

function skillArray(v) {
  if (!Array.isArray(v)) return [];
  return v
    .map((item) => {
      if (item && item.__loomType === "skill") registerSkill(item);
      return stringValue(item);
    })
    .filter(Boolean);
}

function registerSkill(skill) {
  if (!skill || skill.__loomType !== "skill") return skill;
  const name = stringValue(skill.name);
  if (!name) return skill;
  const normalized = {
    name,
    description: stringValue(skill.description),
    version: stringValue(skill.version),
    source_path: stringValue(skill.source_path),
    source_hash: stringValue(skill.source_hash),
    instructions: stringValue(skill.instructions),
    resources: stringArray(skill.resources),
  };
  if (!normalized.version && normalized.source_hash) normalized.version = version(normalized.source_hash);
  skillRegistry.set(name, normalized);
  return { __loomType: "skill", ...normalized };
}

function numberValue(v) {
  return typeof v === "number" && Number.isFinite(v) ? v : 0;
}

function boolValue(v) {
  return v === true;
}

function singletonPolicy(source, value) {
  if (typeof value.singleton === "string") return value.singleton;
  const match = source.match(/singleton\s*:\s*\([^)]*\)\s*=>\s*`([^`]+)`/s);
  return match ? match[1] : "";
}

function workflowTrigger(value) {
  if (Array.isArray(value.triggers) && value.triggers.length > 0) {
    const first = value.triggers[0] || {};
    return {
      event: stringValue(first.event || first.eventType),
      filter: first.filter || {},
    };
  }
  if (value.issueLabelAdded) {
    return {
      event: "issue.label_added",
      filter: value.issueLabelAdded,
    };
  }
  return { event: stringValue(value.triggerEvent || value.eventType), filter: value.triggerFilter || {} };
}

function route(value) {
  const http = value.expose && value.expose.http ? value.expose.http : {};
  return {
    path: stringValue(value.path || value.routePath || http.path),
    auth: stringValue(value.auth || value.routeAuth || http.auth),
  };
}

function runtimeProfileName(value) {
  const runtimeRef = value.runtimeProfile || value.runtime_profile || value.runtime;
  if (runtimeRef == null) return "";
  if (typeof runtimeRef === "string") return stringValue(runtimeRef);
  if (typeof runtimeRef === "object") return stringValue(runtimeRef.name);
  return "";
}

function agentModule(file) {
  const data = fs.readFileSync(file, "utf8");
  const hash = hashSource(data);
  const { value } = evaluateModule(file);
  if (!value || value.__loomType !== "agent") {
    throw new Error(`${file}: default export must be defineAgent(...) or createAgent(...)`);
  }
  const rt = value.runtime || {};
  return {
    name: stringValue(value.name),
    description: stringValue(value.description),
    backend: stringValue(value.backend),
    model: stringValue(value.model),
    profile_name: stringValue(value.profileName || value.profile_name),
    source_path: file,
    source_hash: hash,
    version: version(hash),
    instructions: stringValue(value.instructions),
    skills: skillArray(value.skills),
    tools: stringArray(value.tools),
    allowed_commands: stringArray(value.allowedCommands || (value.policy && value.policy.allowedCommands)),
    denied_commands: stringArray(value.deniedCommands || (value.policy && value.policy.deniedCommands)),
    repos: stringArray(value.repos).concat(stringArray(rt.repos)),
    env: stringArray(value.env).concat(stringArray(rt.env)),
    max_concurrency: numberValue(value.maxConcurrency || (value.policy && value.policy.maxConcurrency)),
    max_budget_usd:
      typeof value.maxBudgetUSD === "number"
        ? value.maxBudgetUSD
        : typeof value.policy === "object" && typeof value.policy.maxBudgetUSD === "number"
          ? value.policy.maxBudgetUSD
          : undefined,
    read_only: boolValue(value.readOnly || (value.policy && value.policy.readOnly)),
  };
}

function skillModule(file) {
  const data = fs.readFileSync(file, "utf8");
  const hash = hashSource(data);
  const { value, exports } = evaluateModule(file);
  const skill = value && value.__loomType === "skill" ? value : Object.values(exports).find((item) => item && item.__loomType === "skill");
  if (!skill) {
    throw new Error(`${file}: default export must be defineSkill(...)`);
  }
  return registerSkill({
    ...skill,
    source_path: file,
    source_hash: hash,
    version: version(hash),
    instructions: stringValue(skill.instructions),
    resources: stringArray(skill.resources),
  });
}

function toolModule(file) {
  const data = fs.readFileSync(file, "utf8");
  const hash = hashSource(data);
  const { value, exports } = evaluateModule(file);
  const tool = value && value.__loomType === "tool" ? value : Object.values(exports).find((item) => item && item.__loomType === "tool");
  if (!tool) {
    throw new Error(`${file}: default export must be defineTool(...)`);
  }
  const handler = stringValue(tool.handler || (typeof tool.execute === "function" ? "typescript" : ""));
  return {
    name: stringValue(tool.name),
    description: stringValue(tool.description),
    source_path: file,
    source_hash: hash,
    version: version(hash),
    parameters: tool.parameters || {},
    handler,
    runtime: stringValue(tool.runtime),
    timeout: stringValue(tool.timeout || tool.deadline || tool.timeoutMs || tool.timeout_ms),
    cancellable: boolValue(tool.cancellable || tool.cancellation),
    repos: stringArray(tool.repos),
    env: stringArray(tool.env),
    read_only: boolValue(tool.readOnly),
  };
}

function runtimeModule(file) {
  const data = fs.readFileSync(file, "utf8");
  const hash = hashSource(data);
  const { value } = evaluateModule(file);
  if (!value || value.__loomType !== "runtime") {
    throw new Error(`${file}: default export must be runtime.local/podman/remote(...)`);
  }
  const module = {
    name: stringValue(value.name || path.basename(file, ".ts")),
    version: version(hash),
    source_path: file,
    source_hash: hash,
    provider: stringValue(value.provider || "local"),
    image: stringValue(value.image),
    repos: stringArray(value.repos),
    env: stringArray(value.env),
    cpu: stringValue(value.cpu),
    memory: stringValue(value.memory),
    cwd: stringValue(value.cwd),
    workspace_skill_dirs: stringArray(value.workspaceSkillDirs || value.workspace_skill_dirs),
    workspace: runtimeWorkspacePolicy(value),
  };
  module.capabilities = runtimeCapabilities(value, module);
  return module;
}

function runtimeWorkspacePolicy(value) {
  const workspace = value.workspace && typeof value.workspace === "object" ? value.workspace : {};
  const cleanup =
    workspace.cleanup && typeof workspace.cleanup === "object"
      ? workspace.cleanup
      : value.cleanup && typeof value.cleanup === "object"
        ? value.cleanup
        : value.cleanupPolicy && typeof value.cleanupPolicy === "object"
          ? value.cleanupPolicy
          : {};
  const filesystem =
    workspace.filesystem && typeof workspace.filesystem === "object"
      ? workspace.filesystem
      : value.filesystem && typeof value.filesystem === "object"
        ? value.filesystem
        : {};
  return compactObject({
    provider_workspace_id: stringValue(
      workspace.providerWorkspaceId ||
        workspace.provider_workspace_id ||
        workspace.workspaceId ||
        workspace.workspace_id ||
        workspace.id ||
        value.providerWorkspaceId ||
        value.provider_workspace_id ||
        value.workspaceId ||
        value.workspace_id,
    ),
    owner: stringValue(workspace.owner || value.workspaceOwner || value.workspace_owner),
    cleanup: compactObject({
      mode: stringValue(cleanup.mode || value.cleanupMode || value.cleanup_mode),
      ttl: stringValue(cleanup.ttl || value.cleanupTTL || value.cleanup_ttl),
      retention: stringValue(cleanup.retention || value.cleanupRetention || value.cleanup_retention),
    }),
    filesystem: compactObject({
      persistence: stringValue(filesystem.persistence || value.filesystemPersistence || value.filesystem_persistence),
      durability: stringValue(filesystem.durability || value.filesystemDurability || value.filesystem_durability),
      retention: stringValue(filesystem.retention || value.filesystemRetention || value.filesystem_retention),
    }),
  });
}

function runtimeCapabilities(value, module) {
  const provider = stringValue(module.provider || value.provider || "local");
  const local = provider === "local";
  const policy = local ? "local" : "provider_default";
  const caps = value.capabilities && typeof value.capabilities === "object" ? value.capabilities : {};
  const filesystem = caps.filesystem && typeof caps.filesystem === "object" ? caps.filesystem : {};
  const shell = caps.shell && typeof caps.shell === "object" ? caps.shell : {};
  const network = caps.network && typeof caps.network === "object" ? caps.network : {};
  const lifecycle = caps.lifecycle && typeof caps.lifecycle === "object" ? caps.lifecycle : {};
  const workspaceCaps = caps.workspace && typeof caps.workspace === "object" ? caps.workspace : {};
  const workspace = module.workspace && typeof module.workspace === "object" ? module.workspace : {};
  const workspaceFilesystem = workspace.filesystem && typeof workspace.filesystem === "object" ? workspace.filesystem : {};
  return compactObjectKeepFalse({
    filesystem: compactObjectKeepFalse({
      read: boolOr(filesystem.read, local),
      write: boolOr(filesystem.write, local),
      artifact_uri: boolOr(filesystem.artifactURI ?? filesystem.artifact_uri, true),
      policy: stringValue(filesystem.policy || policy),
      persistence: stringValue(filesystem.persistence || workspaceFilesystem.persistence),
      durability: stringValue(filesystem.durability || workspaceFilesystem.durability),
      retention: stringValue(filesystem.retention || workspaceFilesystem.retention),
    }),
    shell: compactObjectKeepFalse({
      enabled: boolOr(shell.enabled, local),
      commands: stringArray(shell.commands || value.shellCommands || value.shell_commands),
      policy: stringValue(shell.policy || policy),
    }),
    network: compactObjectKeepFalse({
      enabled: boolOr(network.enabled, local),
      policy: stringValue(network.policy || policy),
    }),
    env: compactObjectKeepFalse({
      forwarded: stringArray(module.env),
      policy: "allowlist",
    }),
    workspace: compactObjectKeepFalse({
      provider_workspace_id: stringValue(
        workspaceCaps.providerWorkspaceId ||
          workspaceCaps.provider_workspace_id ||
          workspace.provider_workspace_id,
      ),
      owner: stringValue(workspaceCaps.owner || workspace.owner),
      cwd: stringValue(workspaceCaps.cwd || module.cwd),
      repos: stringArray(workspaceCaps.repos || module.repos),
      skill_dirs: stringArray(workspaceCaps.skillDirs || workspaceCaps.skill_dirs || module.workspace_skill_dirs),
    }),
    lifecycle: compactObjectKeepFalse({
      materialize: boolOr(lifecycle.materialize, local),
      cleanup: boolOr(lifecycle.cleanup, local),
      release: boolOr(lifecycle.release, local),
      cancellation: boolOr(lifecycle.cancellation, true),
      default_timeout: stringValue(lifecycle.defaultTimeout || lifecycle.default_timeout),
      policy: stringValue(lifecycle.policy || policy),
    }),
  });
}

function boolOr(value, fallback) {
  return typeof value === "boolean" ? value : fallback;
}

function workflowModule(file) {
  const data = fs.readFileSync(file, "utf8");
  const hash = hashSource(data);
  const { source, value } = evaluateModule(file);
  if (!value || value.__loomType !== "workflow") {
    throw new Error(`${file}: default export must be defineWorkflow(...)`);
  }
  const r = route(value);
  const t = workflowTrigger(value);
  const rt = value.runtime && typeof value.runtime === "object" ? value.runtime : {};
  return {
    name: stringValue(value.name),
    description: stringValue(value.description),
    source_path: file,
    source_hash: hash,
    version: version(hash),
    singleton_policy: singletonPolicy(source, value),
    runtime_profile_name: runtimeProfileName(value),
    builtin: stringValue(value.builtin),
    runner: typeof value.run === "function" ? "workflow-context-v1" : stringValue(value.runner),
    route_path: r.path,
    route_auth: r.auth,
    trigger_event: t.event,
    trigger_filter: t.filter,
    tools: stringArray(value.tools),
    env: stringArray(value.env).concat(stringArray(rt.env)),
    repos: stringArray(value.repos).concat(stringArray(rt.repos)),
  };
}

function compactArrayObjects(items) {
  return items.map((item) => compactObject(item));
}

function compactObject(item) {
  const out = {};
  for (const [k, v] of Object.entries(item || {})) {
    if (v === undefined) continue;
    if (Array.isArray(v) && v.length === 0) continue;
    if (v && typeof v === "object" && !Array.isArray(v) && Object.keys(v).length === 0) continue;
    if (v === "" || v === false || v === 0) continue;
    out[k] = v;
  }
  return out;
}

function compactObjectKeepFalse(item) {
  const out = {};
  for (const [k, v] of Object.entries(item || {})) {
    if (v === undefined || v === null) continue;
    if (Array.isArray(v) && v.length === 0) continue;
    if (v && typeof v === "object" && !Array.isArray(v) && Object.keys(v).length === 0) continue;
    if (v === "" || v === 0) continue;
    out[k] = v;
  }
  return out;
}

const config = projectConfig();
const sourceRoot = sourceRootDir(config);
rejectMixedSourceRoots(sourceRoot);

readSkillEntrypoints(path.join(sourceRoot, "skills")).forEach(skillModule);
const tools = compactArrayObjects(readEntrypoints(path.join(sourceRoot, "tools")).map(toolModule));
const agents = compactArrayObjects(readEntrypoints(path.join(sourceRoot, "agents")).map(agentModule));
const workflows = compactArrayObjects(readEntrypoints(path.join(sourceRoot, "workflows")).map(workflowModule));
const runtimes = compactArrayObjects(readEntrypoints(path.join(sourceRoot, "runtimes")).map(runtimeModule));
const skills = compactArrayObjects(Array.from(skillRegistry.values()).sort((a, b) => a.name.localeCompare(b.name)));

const plan = {
  root,
  model_policy: modelPolicy(config),
  agents,
  workflows,
  runtimes,
  skills,
  tools,
};

process.stdout.write(JSON.stringify(plan));
