const fs = require("fs");
const crypto = require("crypto");
const os = require("os");
const path = require("path");
const vm = require("vm");
const { stripTypeScriptTypes } = require("node:module");

const moduleCache = new Map();

function defineWorkflow(config) {
  return { __loomType: "workflow", ...config };
}

function createAgent(config) {
  if (typeof config === "function") {
    return { __loomType: "agent", __loomFactory: config };
  }
  return { __loomType: "agent", ...(config || {}) };
}

function defineAgent(config) {
  return createAgent(config);
}

function defineAgentProfile(config) {
  return { __loomType: "agentProfile", ...(config || {}) };
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
    createAgent,
    defineAgent,
    defineAgentProfile,
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

function safeRelativePath(value) {
  const raw = String(value || "").trim();
  if (!raw) throw new Error("staging path is required");
  if (path.isAbsolute(raw)) throw new Error(`staging path must be relative: ${raw}`);
  const normalized = path.posix.normalize(raw.replace(/\\/g, "/"));
  if (normalized === "." || normalized === ".." || normalized.startsWith("../")) {
    throw new Error(`staging path escapes workflow staging root: ${raw}`);
  }
  return normalized;
}

function makeWorkflowFiles(request, operations) {
  let stagingRoot = "";
  const ensureRoot = () => {
    if (!stagingRoot) {
      const runPart = String(request.id || "workflow").replace(/[^A-Za-z0-9_.-]/g, "_");
      stagingRoot = fs.mkdtempSync(path.join(os.tmpdir(), `loom-workflow-${runPart}-`));
    }
    return stagingRoot;
  };
  const checksum = (text) => `sha256:${crypto.createHash("sha256").update(text).digest("hex")}`;
  const writeText = async (relativePath, content, options = {}) => {
    const rel = safeRelativePath(relativePath);
    const text = String(content ?? "");
    const abs = path.join(ensureRoot(), rel);
    fs.mkdirSync(path.dirname(abs), { recursive: true });
    fs.writeFileSync(abs, text, "utf8");
    const params = {
      path: rel,
      uri: `file://${abs}`,
      type: options.type || options.artifactType || options.artifact_type || "staging",
      summary: options.summary,
      mimeType: options.mimeType || options.mime_type || "text/plain; charset=utf-8",
      sizeBytes: Buffer.byteLength(text, "utf8"),
      checksum: checksum(text),
      metadata: {
        ...(options.metadata || {}),
        path: rel,
        source: "workflow_context_staging",
      },
      visibility: "controller",
    };
    operations.push({ type: "files.write", params });
    return { accepted: true, ...params };
  };
  const readText = async (relativePath) => {
    const rel = safeRelativePath(relativePath);
    const abs = path.join(ensureRoot(), rel);
    const text = fs.readFileSync(abs, "utf8");
    operations.push({
      type: "files.read",
      params: {
        path: rel,
        uri: `file://${abs}`,
        sizeBytes: Buffer.byteLength(text, "utf8"),
        checksum: checksum(text),
        visibility: "controller",
      },
    });
    return text;
  };
  return {
    writeText,
    readText,
    writeJSON: (relativePath, value, options = {}) =>
      writeText(relativePath, JSON.stringify(value ?? null, null, 2), {
        mimeType: "application/json",
        ...options,
      }),
    readJSON: async (relativePath) => JSON.parse(await readText(relativePath)),
  };
}

function makeWorkflowShell(request, operations) {
  const run = async (commandOrOptions, maybeOptions = {}) => {
    const options =
      typeof commandOrOptions === "string"
        ? { ...(maybeOptions || {}), command: commandOrOptions }
        : commandOrOptions || {};
    const command = String(options.command || "").trim();
    if (!command) throw new Error("ctx.shell.run requires command");
    const startedAt = new Date().toISOString();
    const params = {
      operationId: `op:${String(request.id || "workflow")}:shell:${operations.length + 1}`,
      command,
      args: jsonSafe(options.args || []),
      cwd: options.cwd ? String(options.cwd) : "",
      env: jsonSafe(options.env || {}),
      timeoutMs: Number(options.timeoutMs || options.timeout_ms || 0),
      metadata: jsonSafe(options.metadata || {}),
      visibility: "controller",
      startedAt,
    };
    const result = controllerShellResult(options, params);
    const completedAt = new Date().toISOString();
    params.completedAt = completedAt;
    params.durationMs = Date.parse(completedAt) - Date.parse(startedAt);
    params.status = String(options.status || "completed");
    params.result = jsonSafe(result);
    operations.push({ type: "shell.run", params });
    return result;
  };
  return { run };
}

function controllerShellResult(options, params) {
  if (Object.prototype.hasOwnProperty.call(options, "mockResult")) {
    return jsonSafe(options.mockResult);
  }
  if (Object.prototype.hasOwnProperty.call(options, "response")) {
    return jsonSafe(options.response);
  }
  return {
    accepted: true,
    command: params.command,
    cwd: params.cwd,
    exitCode: Number(options.exitCode || options.exit_code || 0),
  };
}

function makeContext(request, workflow) {
  const logs = [];
  const operations = [];
  const input = request.input && typeof request.input === "object" ? request.input : {};
  const parentId = String(input.parentId || input.parent_id || "");
  const workflowState = request.workflow && typeof request.workflow === "object" ? request.workflow : {};
  const taskRuns = Array.isArray(request.taskRuns) ? request.taskRuns : [];
  const taskClaims = Array.isArray(request.taskClaims) ? request.taskClaims : [];
  const workItems = uniqueWorkItems([
    ...(Array.isArray(request.readyChildren) ? request.readyChildren : []),
    ...(Array.isArray(request.blockedChildren) ? request.blockedChildren : []),
    ...(Array.isArray(request.childWorkItems) ? request.childWorkItems : []),
  ]);

  const byParent = (items, requestedParent, options) => {
    let out;
    if (!requestedParent || requestedParent === parentId) {
      out = items || [];
    } else {
      out = (items || []).filter(
        (item) => !item.parent || item.parent === requestedParent,
      );
    }
    const limit = Number(options && options.limit);
    if (Number.isFinite(limit) && limit > 0) return out.slice(0, limit);
    return out;
  };

  const log = (level, message, attributes) => {
    logs.push({ level, message: String(message || ""), attributes: attributes || {} });
  };
  const tools = workflowTools(workflow, operations);
  const files = makeWorkflowFiles(request, operations);
  const shell = makeWorkflowShell(request, operations);
  const requestContext = request.request || {};
  const taskRunFilter = (options = {}) => {
    let out = taskRuns.slice();
    const status = String(options.status || "");
    if (status) out = out.filter((run) => String(run.status || "") === status);
    const workItemId = String(options.workItemId || options.work_item_id || "");
    if (workItemId) out = out.filter((run) => String(run.work_item_id || run.workItemId || "") === workItemId);
    const role = String(options.role || options.roleName || options.role_name || "");
    if (role) out = out.filter((run) => String(run.role_name || run.roleName || "") === role);
    if (options.live === true) out = out.filter(isLiveTaskRun);
    const limit = Number(options.limit);
    if (Number.isFinite(limit) && limit > 0) out = out.slice(0, limit);
    return out;
  };
  const taskClaimFilter = (options = {}) => {
    let out = taskClaims.slice();
    const workItemId = String(options.workItemId || options.work_item_id || "");
    if (workItemId) out = out.filter((claim) => String(claim.work_item_id || claim.workItemId || "") === workItemId);
    const taskRunId = String(options.taskRunId || options.task_run_id || "");
    if (taskRunId) out = out.filter((claim) => String(claim.task_run_id || claim.taskRunId || "") === taskRunId);
    const actor = String(options.actor || options.claimActor || options.claim_actor || "");
    if (actor) out = out.filter((claim) => String(claim.claim_actor || claim.claimActor || "") === actor);
    if (options.active === true) out = out.filter((claim) => Boolean(claim.active));
    const limit = Number(options.limit);
    if (Number.isFinite(limit) && limit > 0) out = out.slice(0, limit);
    return out;
  };
  const materializeAgent = (agent) => {
    if (typeof agent === "string") {
      return { name: agent };
    }
    if (typeof agent === "function") {
      return agent({ id: request.id, input, payload: input, env: request.env || {}, req: requestContext, request: requestContext }) || {};
    }
    if (agent && agent.__loomFactory) {
      return agent.__loomFactory({ id: request.id, input, payload: input, env: request.env || {}, req: requestContext, request: requestContext }) || {};
    }
    return agent || {};
  };
  const sessionFor = (base) => async (nameOrOptions, maybeOptions) => {
    const hasName = typeof nameOrOptions === "string";
    const sessionName = hasName ? nameOrOptions : "default";
    const options = hasName ? maybeOptions || {} : nameOrOptions || {};
    const metadata = { ...(base.metadata || {}), ...((options && options.metadata) || {}) };
    const op = {
      type: "agents.session",
      params: {
        kind: "task",
        ...base,
        ...(options || {}),
        metadata,
        sessionName,
      },
    };
    operations.push(op);
    return sessionHandle(operations, {
      accepted: true,
      agentId: op.params.agentId || op.params.agent_id,
      harness: op.params.harness,
      sessionName,
      sessionId: op.params.sessionId || op.params.session_id || op.params.id,
      ...op.params,
    });
  };

  const ctx = {
    id: request.id,
    input,
    payload: input,
    env: request.env || {},
    req: requestContext,
    request: requestContext,
    workflow: {
      status: async () => ({ ...workflowState }),
      cancelRequested: async () => Boolean(workflowState.cancelRequested),
      waitUntil: async (condition, metadata) => {
        const op = {
          type: "workflow.waitUntil",
          params: {
            condition: String(condition || ""),
            metadata: jsonSafe(metadata || {}),
          },
        };
        operations.push(op);
        return { accepted: true, ...op.params };
      },
      cancel: async (reasonOrOptions, metadata) => {
        const options =
          reasonOrOptions && typeof reasonOrOptions === "object"
            ? reasonOrOptions
            : { reason: String(reasonOrOptions || "") };
        const op = {
          type: "workflow.cancel",
          params: {
            ...jsonSafe(options || {}),
            metadata: jsonSafe(metadata || options.metadata || {}),
            requestedAt: new Date().toISOString(),
          },
        };
        operations.push(op);
        return { accepted: true, ...op.params };
      },
    },
    init: async (agent, options = {}) => {
      const materialized = materializeAgent(agent);
      const harness = String(options.name || options.harness || "default");
      const agentId = String(
        options.agentId ||
          options.agent_id ||
          materialized.name ||
          materialized.id ||
          workflow.name ||
          "workflow-agent",
      );
      const base = {
        agentId,
        harness,
        model: materialized.model,
        backend: materialized.backend,
        metadata: {
          ...(options.metadata || {}),
          ...(materialized.name ? { source_agent_name: String(materialized.name) } : {}),
        },
      };
      return {
        agentId,
        harness,
        session: sessionFor(base),
        sessions: {
          create: sessionFor(base),
          get: sessionFor(base),
        },
      };
    },
    log: {
      info: (message, attributes) => log("info", message, attributes),
      warn: (message, attributes) => log("warn", message, attributes),
      error: (message, attributes) => log("error", message, attributes),
    },
    workItems: {
      get: async (id) => {
        const workItemId = String(id || "");
        const item = findWorkItem(workItems, workItemId);
        operations.push({
          type: "workItems.get",
          params: { workItemId, found: Boolean(item) },
        });
        return item || null;
      },
      comment: async (idOrOptions, body, metadata) => {
        const options =
          idOrOptions && typeof idOrOptions === "object"
            ? idOrOptions
            : { workItemId: idOrOptions, body, metadata };
        const workItemId = String(options.workItemId || options.work_item_id || options.id || "");
        const text = String(options.body || options.text || options.comment || "");
        if (!workItemId) throw new Error("ctx.workItems.comment requires workItemId");
        if (!text.trim()) throw new Error("ctx.workItems.comment requires body");
        const params = {
          workItemId,
          body: text,
          metadata: jsonSafe(options.metadata || metadata || {}),
        };
        operations.push({ type: "workItems.comment", params });
        return { accepted: true, ...params };
      },
      readyChildren: async (requestedParent, options) =>
        byParent(request.readyChildren, String(requestedParent || ""), options),
      blockedChildren: async (requestedParent, options) =>
        byParent(
          request.blockedChildren,
          String(requestedParent || ""),
          options,
        ),
      listChildren: async (requestedParent, options) =>
        byParent(request.childWorkItems, String(requestedParent || ""), options),
    },
    taskRuns: {
      list: async (options = {}) => taskRunFilter(options || {}),
      wait: async (options = {}) => {
        const matches = taskRunFilter(options || {});
        const liveCount = matches.filter(isLiveTaskRun).length;
        operations.push({
          type: "taskRuns.wait",
          params: {
            ...(options || {}),
            matched: matches.length,
            liveCount,
            wait: liveCount > 0,
          },
        });
        return matches;
      },
      ensure: async (params) => {
        const op = { type: "taskRuns.ensure", params: params || {} };
        operations.push(op);
        return { accepted: true, ...op.params };
      },
    },
    taskClaims: {
      list: async (options = {}) => taskClaimFilter(options || {}),
      get: async (idOrOptions = {}) => {
        const options =
          typeof idOrOptions === "string"
            ? { workItemId: idOrOptions }
            : idOrOptions || {};
        return taskClaimFilter({ ...options, limit: 1 })[0] || null;
      },
      wait: async (options = {}) => {
        const matches = taskClaimFilter(options || {});
        const activeCount = matches.filter((claim) => Boolean(claim.active)).length;
        operations.push({
          type: "taskClaims.wait",
          params: {
            ...(options || {}),
            matched: matches.length,
            activeCount,
            wait: activeCount > 0,
          },
        });
        return matches;
      },
    },
    artifacts: {
      record: async (params) => {
        const op = { type: "artifacts.record", params: params || {} };
        operations.push(op);
        return { accepted: true, ...op.params };
      },
      create: async (params) => {
        const op = { type: "artifacts.record", params: params || {} };
        operations.push(op);
        return { accepted: true, ...op.params };
      },
    },
    shell,
    setup: {
      shell,
    },
    files,
    staging: files,
    agents: {
      session: async (params) => {
        const op = { type: "agents.session", params: params || {} };
        operations.push(op);
        return sessionHandle(operations, { accepted: true, ...op.params });
      },
      dispatch: async (agent, input = {}) => {
        const materialized = materializeAgent(agent);
        const agentId = String(
          input.agentId ||
            input.agent_id ||
            materialized.name ||
            materialized.id ||
            "",
        );
        const op = {
          type: "agents.dispatch",
          params: {
            agentId,
            input: jsonSafe(input || {}),
            admittedAt: new Date().toISOString(),
          },
        };
        operations.push(op);
        return {
          accepted: true,
          agentId,
          input: op.params.input,
        };
      },
    },
    tools,
    tool: async (name, args) => {
      const fn = tools[String(name || "")];
      if (!fn) throw new Error(`workflow tool ${String(name || "")} is not declared`);
      return fn(args || {});
    },
  };
  return { ctx, logs, operations };
}

function isLiveTaskRun(run) {
  const status = String(run && run.status || "");
  return !["passed", "failed", "cancelled", "expired"].includes(status);
}

function uniqueWorkItems(items) {
  const byId = new Map();
  for (const item of items || []) {
    const id = String(item && (item.id || item.ID) || "");
    if (id && !byId.has(id)) byId.set(id, item);
  }
  return Array.from(byId.values());
}

function findWorkItem(items, id) {
  const wanted = String(id || "");
  if (!wanted) return null;
  return (items || []).find((item) => String(item && (item.id || item.ID) || "") === wanted) || null;
}

function sessionHandle(operations, session) {
  const call = async (operation, input = {}) => {
    const startedAt = new Date().toISOString();
    const operationId = sessionOperationId(session, operation, operations.length);
    const params = {
      operationId,
      agentId: session.agentId || session.agent_id,
      sessionId: session.sessionId || session.session_id || session.id,
      harness: session.harness,
      sessionName: session.sessionName || session.session_name,
      taskId: session.taskId || session.task_id || session.workItemId || session.work_item_id,
      operation,
      input: jsonSafe(input || {}),
      startedAt,
    };
    const result = sessionOperationResult(operation, input, params);
    const completedAt = new Date().toISOString();
    params.completedAt = completedAt;
    params.durationMs = Date.parse(completedAt) - Date.parse(startedAt);
    params.status = "completed";
    params.result = jsonSafe(result);
    operations.push({ type: "agents.session.operation", params });
    return result;
  };
  return {
    ...session,
    prompt: (input) => call("prompt", input),
    skill: (input) => call("skill", input),
    task: (input) => call("task", input),
    shell: (input) => call("shell", input),
    compact: (input) => call("compact", input),
  };
}

function sessionOperationId(session, operation, index) {
  const sessionId = String(session.sessionId || session.session_id || session.id || session.sessionName || "session");
  return `op:${sessionId}:${operation}:${index + 1}`;
}

function sessionOperationResult(operation, input, params) {
  if (input && Object.prototype.hasOwnProperty.call(input, "mockResult")) {
    return jsonSafe(input.mockResult);
  }
  if (input && Object.prototype.hasOwnProperty.call(input, "response")) {
    return jsonSafe(input.response);
  }
  return {
    accepted: true,
    operation,
    agentId: params.agentId,
    sessionId: params.sessionId,
    sessionName: params.sessionName,
  };
}

function workflowTools(workflow, operations) {
  const tools = {};
  for (const tool of Array.isArray(workflow && workflow.tools) ? workflow.tools : []) {
    if (!tool || tool.__loomType !== "tool") continue;
    const name = String(tool.name || "").trim();
    if (!name) continue;
    if (tools[name]) throw new Error(`duplicate workflow tool ${name}`);
    tools[name] = async (args = {}) => {
      if (typeof tool.execute !== "function") {
        throw new Error(`workflow tool ${name} has no executable handler`);
      }
      const startedAt = new Date().toISOString();
      const result = await tool.execute(args);
      operations.push({
        type: "tools.call",
        params: {
          name,
          args: jsonSafe(args),
          result: jsonSafe(result),
          startedAt,
          completedAt: new Date().toISOString(),
        },
      });
      return result;
    };
  }
  return tools;
}

function jsonSafe(value) {
  if (value === undefined) return null;
  return JSON.parse(JSON.stringify(value));
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
  const { ctx, logs, operations } = makeContext(request, value);
  const result = await value.run(ctx);
  process.stdout.write(JSON.stringify({ result: result === undefined ? null : result, logs, operations }));
}

main().catch((error) => {
  process.stderr.write(error && error.stack ? error.stack : String(error));
  process.exit(1);
});
