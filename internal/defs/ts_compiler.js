const fs = require("fs");
const path = require("path");
const crypto = require("crypto");
const vm = require("vm");
const { stripTypeScriptTypes } = require("node:module");

const root = path.resolve(process.argv[2] || ".");

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
  let transformed = src;
  if (typeof stripTypeScriptTypes === "function") {
    transformed = stripTypeScriptTypes(transformed, { mode: "strip", sourceUrl: file });
  }
  return transformed
    .replace(/import\s+type\s+[^;]+;?/g, "")
    .replace(/import\s+[^;]+from\s+['"][^'"]+['"];?/g, "")
    .replace(/export\s+type\s+[^;]+;?/g, "")
    .replace(/export\s+interface\s+[A-Za-z0-9_]+\s*\{[^}]*\}/gs, "")
    .replace(/export\s+default\s+/, "module.exports.default = ")
    .replace(/export\s+const\s+([A-Za-z0-9_]+)\s*=/g, "const $1 =")
    .replace(/export\s+function\s+([A-Za-z0-9_]+)\s*\(/g, "function $1(");
}

function defineConfig(config) {
  return config || {};
}

function defineAgent(config) {
  return { __loomType: "agent", ...config };
}

const createAgent = defineAgent;

function defineAgentProfile(config) {
  return { __loomType: "agent_profile", ...config };
}

function defineWorkflow(config) {
  return { __loomType: "workflow", ...config };
}

function defineTool(config) {
  return { __loomType: "tool", name: config && config.name, ...config };
}

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

const schema = new Proxy(
  {},
  {
    get(_target, prop) {
      return (...args) => ({ __loomType: "schema", kind: String(prop), args });
    },
  },
);

const trigger = {
  issueLabelAdded(config = {}) {
    return { event: "issue.label_added", filter: config };
  },
};

function evaluateModule(file) {
  const src = fs.readFileSync(file, "utf8");
  const module = { exports: {} };
  const sandbox = {
    module,
    exports: module.exports,
    console,
    defineConfig,
    defineAgent,
    createAgent,
    defineAgentProfile,
    defineWorkflow,
    defineTool,
    runtime,
    schema,
    trigger,
    github: toolProxy(["github"]),
    fleetdb: toolProxy(["fleetdb"]),
  };
  vm.runInNewContext(transformSource(src, file), sandbox, { filename: file });
  return { source: src, value: module.exports.default };
}

function projectConfig() {
  const configFile = path.join(root, "loom.config.ts");
  if (!fs.existsSync(configFile)) return {};
  const { value } = evaluateModule(configFile);
  return value || {};
}

function sourceRootDir() {
  const config = projectConfig();
  const sourceRoot = stringValue(config.sourceRoot || ".loom");
  if (sourceRoot === "" || path.isAbsolute(sourceRoot)) {
    throw new Error("loom.config.ts sourceRoot must be a relative path");
  }
  return path.join(root, sourceRoot);
}

function stringValue(v) {
  if (v == null) return "";
  return String(v).trim();
}

function stringArray(v) {
  if (!Array.isArray(v)) return [];
  return v.map((item) => stringValue(item)).filter(Boolean);
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
    source_path: file,
    source_hash: hash,
    version: version(hash),
    instructions: stringValue(value.instructions),
    skills: stringArray(value.skills),
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

function runtimeModule(file) {
  const data = fs.readFileSync(file, "utf8");
  const hash = hashSource(data);
  const { value } = evaluateModule(file);
  if (!value || value.__loomType !== "runtime") {
    throw new Error(`${file}: default export must be runtime.local/podman/remote(...)`);
  }
  return {
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
  };
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
  return {
    name: stringValue(value.name),
    description: stringValue(value.description),
    source_path: file,
    source_hash: hash,
    version: version(hash),
    singleton_policy: singletonPolicy(source, value),
    builtin: stringValue(value.builtin),
    route_path: r.path,
    route_auth: r.auth,
    trigger_event: t.event,
    trigger_filter: t.filter,
    tools: stringArray(value.tools),
    env: stringArray(value.env),
    repos: stringArray(value.repos),
  };
}

function compactArrayObjects(items) {
  return items.map((item) => {
    const out = {};
    for (const [k, v] of Object.entries(item)) {
      if (v === undefined) continue;
      if (Array.isArray(v) && v.length === 0) continue;
      if (v && typeof v === "object" && !Array.isArray(v) && Object.keys(v).length === 0) continue;
      if (v === "" || v === false || v === 0) continue;
      out[k] = v;
    }
    return out;
  });
}

const sourceRoot = sourceRootDir();

const plan = {
  root,
  agents: compactArrayObjects(readEntrypoints(path.join(sourceRoot, "agents")).map(agentModule)),
  workflows: compactArrayObjects(readEntrypoints(path.join(sourceRoot, "workflows")).map(workflowModule)),
  runtimes: compactArrayObjects(readEntrypoints(path.join(sourceRoot, "runtimes")).map(runtimeModule)),
};

process.stdout.write(JSON.stringify(plan));
