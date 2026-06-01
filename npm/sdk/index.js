import { spawn } from "node:child_process";
import { basename, dirname, normalize } from "node:path";

export function defineConfig(config) {
  return config || {};
}

export function defineAgent(agent) {
  return agent || {};
}

export const createAgent = defineAgent;

export function defineAgentProfile(profile) {
  return profile || {};
}

export function defineSkill(skill) {
  return skill || {};
}

export function defineWorkflow(workflow) {
  return workflow || {};
}

export function defineTool(tool) {
  return tool || {};
}

export function defineRuntimeProfile(profile) {
  return { provider: "local", ...(profile || {}) };
}

export const runtime = {
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

export const trigger = {
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

export const schema = new Proxy(
  {},
  {
    get(_target, prop) {
      return (...args) => ({ kind: String(prop), args });
    },
  },
);

export const Type = schema;

export class CLILoomTransport {
  constructor(options = {}) {
    this.binary = options.binary || process.env.LOOM_BIN || "loom";
    this.cwd = options.cwd || process.cwd();
    this.env = options.env || process.env;
  }

  run(args, options = {}) {
    return new Promise((resolve, reject) => {
      const child = spawn(this.binary, args, {
        cwd: options.cwd || this.cwd,
        env: { ...this.env, ...(options.env || {}) },
        signal: options.signal,
        stdio: ["pipe", "pipe", "pipe"],
      });
      let stdout = "";
      let stderr = "";
      child.stdout.setEncoding("utf8");
      child.stderr.setEncoding("utf8");
      child.stdout.on("data", (chunk) => {
        stdout += chunk;
      });
      child.stderr.on("data", (chunk) => {
        stderr += chunk;
      });
      child.on("error", reject);
      child.on("close", (status, signal) => {
        const result = { stdout, stderr, status, signal };
        if (status === 0) {
          resolve(result);
          return;
        }
        const err = new Error(stderr.trim() || `loom exited with status ${status}`);
        err.result = result;
        reject(err);
      });
      if (options.stdin != null) {
        child.stdin.end(String(options.stdin));
      } else {
        child.stdin.end();
      }
    });
  }
}

export class FetchLoomTransport {
  constructor(options = {}) {
    this.baseURL = String(options.baseURL || options.url || "").replace(/\/+$/, "");
    if (!this.baseURL) throw new TypeError("baseURL is required");
    this.workspace = options.workspace || options.workspaceKey || options.workspace_key;
    this.apiKey = options.apiKey || options.api_key;
    this.authToken = options.authToken || options.auth_token;
    this.fetch = options.fetch || globalThis.fetch;
    if (typeof this.fetch !== "function") throw new TypeError("fetch is required");
  }

  async request(method, requestPath, options = {}) {
    const url = new URL(requestPath, `${this.baseURL}/`);
    for (const [key, value] of Object.entries(options.query || {})) {
      if (value != null && value !== "") url.searchParams.set(key, String(value));
    }
    const headers = {
      Accept: "application/json",
      ...(options.headers || {}),
    };
    let body;
    if (options.body !== undefined) {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(options.body);
    }
    if (this.apiKey) headers["X-Fleet-API-Key"] = String(this.apiKey);
    if (this.authToken) headers.Authorization = `Bearer ${this.authToken}`;
    const response = await this.fetch(url, {
      method,
      headers,
      body,
      signal: options.signal,
    });
    const text = await response.text();
    if (!response.ok) {
      const err = new Error(text || `loom HTTP ${method} ${url.pathname} failed with status ${response.status}`);
      err.status = response.status;
      err.body = text;
      throw err;
    }
    if (text.trim() === "") return null;
    return JSON.parse(text);
  }
}

export class LoomClient {
  constructor(options = {}) {
    this.transport = options.transport || (options.baseURL || options.url ? new FetchLoomTransport(options) : new CLILoomTransport(options));
    this.agents = {
      list: (request = {}) => this.#runJSON(["agentdef", "list", "--json"], request),
      get: (agent, request = {}) => this.#runJSON(["agentdef", "show", definitionName(agent), "--json"], request),
      create: (agent, request = {}) => this.#runJSON(agentCreateArgs(agent, request), request),
      remove: (agent, request = {}) => this.#runJSON(["agentdef", "remove", definitionName(agent), "--json"], request),
      start: (agent, request = {}) => this.#runJSON(["agentdef", "start", definitionName(agent), "--json"], request),
      stop: (agent, request = {}) => {
        const args = ["agentdef", "stop", definitionName(agent)];
        if (request.force) args.push("--force");
        args.push("--json");
        return this.#runJSON(args, request);
      },
    };
    this.defs = {
      plan: (request = {}) => this.#runJSON(defsArgs("plan", request), request),
      apply: (request = {}) => this.#runJSON(defsArgs("apply", request, request.start ? ["--start"] : []), request),
      exportSource: (request = {}) => {
        const extra = [];
        if (request.force) extra.push("--force");
        if (request.includeState) extra.push("--include-state");
        return this.#runJSON(defsArgs("export-source", request, extra), request);
      },
    };
    this.workflows = {
      list: (request = {}) =>
        this.#canRequest()
          ? this.#requestJSON("GET", apiWorkspacePath(this.transport, request, "/workflows"), request).then(unwrapListResponse)
          : this.#runJSON(["workflow", "list", "--json"], request),
      listRoutes: (workflow, request = {}) => {
        if (this.#canRequest()) {
          return this.#requestJSON("GET", apiWorkspacePath(this.transport, request, "/workflow-route-bindings"), request, {
            query: workflow ? { workflow: definitionName(workflow) } : {},
          }).then(unwrapListResponse);
        }
        const args = ["workflow", "route", "list"];
        if (workflow) args.push(definitionName(workflow));
        args.push("--json");
        return this.#runJSON(args, request);
      },
      bindRoute: (workflow, path, request = {}) => {
        if (this.#canRequest()) {
          return this.#requestJSON(
            "POST",
            apiWorkspacePath(this.transport, request, `/workflows/${encodeURIComponent(definitionName(workflow))}/routes`),
            request,
            { body: compactHTTPBody({ path: String(path), auth: request.auth }) },
          );
        }
        const args = ["workflow", "route", "bind", definitionName(workflow), String(path)];
        if (request.auth) args.push("--auth", String(request.auth));
        args.push("--json");
        return this.#runJSON(args, request);
      },
      unbindRoute: (workflow, path, request = {}) =>
        this.#canRequest()
          ? this.#requestJSON(
              "DELETE",
              apiWorkspacePath(
                this.transport,
                request,
                `/workflows/${encodeURIComponent(definitionName(workflow))}/routes${routeWildcardPath(path)}`,
              ),
              request,
            )
          : this.#runJSON(["workflow", "route", "remove", definitionName(workflow), String(path), "--json"], request),
      listTriggers: (workflow, request = {}) => {
        if (this.#canRequest()) {
          return this.#requestJSON("GET", apiWorkspacePath(this.transport, request, "/workflow-trigger-bindings"), request, {
            query: workflow ? { workflow: definitionName(workflow) } : {},
          }).then(unwrapListResponse);
        }
        const args = ["workflow", "trigger", "list"];
        if (workflow) args.push(definitionName(workflow));
        args.push("--json");
        return this.#runJSON(args, request);
      },
      bindTrigger: (workflow, event, request = {}) => {
        if (this.#canRequest()) {
          return this.#requestJSON(
            "POST",
            apiWorkspacePath(this.transport, request, `/workflows/${encodeURIComponent(definitionName(workflow))}/triggers`),
            request,
            { body: compactHTTPBody({ event: String(event), filter: request.filter === undefined ? undefined : inputValue(request.filter) }) },
          );
        }
        const args = ["workflow", "trigger", "bind", definitionName(workflow), String(event)];
        if (request.filter !== undefined) args.push("--filter", inputJSON(request.filter));
        args.push("--json");
        return this.#runJSON(args, request);
      },
      unbindTrigger: (workflow, event, request = {}) =>
        this.#canRequest()
          ? this.#requestJSON(
              "DELETE",
              apiWorkspacePath(
                this.transport,
                request,
                `/workflows/${encodeURIComponent(definitionName(workflow))}/triggers/${encodeURIComponent(String(event))}`,
              ),
              request,
            )
          : this.#runJSON(["workflow", "trigger", "remove", definitionName(workflow), String(event), "--json"], request),
    };
    this.runs = {
      get: (runId, request = {}) =>
        this.#canRequest()
          ? this.#requestJSON("GET", apiWorkspacePath(this.transport, request, `/workflow-runs/${encodeURIComponent(runId)}`), request)
          : this.#runJSON(["workflow", "show", runId, "--json"], request),
      events: (runId, request = {}) =>
        this.#canRequest()
          ? this.#requestJSON(
              "GET",
              apiWorkspacePath(this.transport, request, `/workflow-runs/${encodeURIComponent(runId)}/events`),
              request,
            ).then(unwrapListResponse)
          : this.#runJSON(["workflow", "logs", runId, "--json"], request),
      tasks: (runId, request = {}) =>
        this.#canRequest()
          ? this.#requestJSON(
              "GET",
              apiWorkspacePath(this.transport, request, `/workflow-runs/${encodeURIComponent(runId)}/tasks`),
              request,
            ).then(unwrapListResponse)
          : this.#runJSON(["workflow", "tasks", runId, "--json"], request),
      sessions: (runId, request = {}) =>
        this.#canRequest()
          ? this.#requestJSON(
              "GET",
              apiWorkspacePath(this.transport, request, `/workflow-runs/${encodeURIComponent(runId)}/sessions`),
              request,
            ).then(unwrapListResponse)
          : this.#runJSON(["workflow", "sessions", runId, "--json"], request),
      operations: (runId, request = {}) =>
        this.#canRequest()
          ? this.#requestJSON(
              "GET",
              apiWorkspacePath(this.transport, request, `/workflow-runs/${encodeURIComponent(runId)}/operations`),
              request,
            ).then(unwrapListResponse)
          : this.#runJSON(["workflow", "operations", runId, "--json"], request),
      toolCalls: (runId, request = {}) =>
        this.#canRequest()
          ? this.#requestJSON(
              "GET",
              apiWorkspacePath(this.transport, request, `/workflow-runs/${encodeURIComponent(runId)}/tool-calls`),
              request,
            ).then(unwrapListResponse)
          : this.#runJSON(["workflow", "tool-calls", runId, "--json"], request),
      artifacts: (runId, request = {}) => {
        if (this.#canRequest()) {
          return this.#requestJSON(
            "GET",
            apiWorkspacePath(this.transport, request, `/workflow-runs/${encodeURIComponent(runId)}/artifacts`),
            request,
            { query: request.type ? { type: request.type } : {} },
          ).then(unwrapListResponse);
        }
        const args = ["workflow", "artifacts", runId, "--json"];
        if (request.type) args.push("--type", String(request.type));
        return this.#runJSON(args, request);
      },
      cancel: (runId, request = {}) =>
        this.#canRequest()
          ? this.#requestJSON(
              "POST",
              apiWorkspacePath(this.transport, request, `/workflow-runs/${encodeURIComponent(runId)}/cancel`),
              request,
            )
          : this.#runJSON(["workflow", "cancel", runId, "--json"], request),
    };
    this.sessions = {
      list: (request = {}) => this.#workspacePlanField("agent_sessions", request),
      get: (sessionId, request = {}) =>
        this.#workspacePlanRecord("agent_sessions", sessionId, ["session_id", "sessionId", "id"], request),
      forRun: (runId, request = {}) => this.runs.sessions(runId, request),
    };
    this.operations = {
      list: (request = {}) => this.#workspacePlanField("agent_session_operations", request),
      get: (operationId, request = {}) =>
        this.#workspacePlanRecord("agent_session_operations", operationId, ["operation_id", "operationId", "id"], request),
      forRun: (runId, request = {}) => this.runs.operations(runId, request),
      cancel: (operationId, request = {}) =>
        this.#canRequest()
          ? this.#requestJSON(
              "POST",
              apiWorkspacePath(
                this.transport,
                request,
                `/agent-session-operations/${encodeURIComponent(operationId)}/cancel`,
              ),
              request,
              { body: request.reason ? { reason: String(request.reason) } : {} },
            )
          : this.#runJSON(
              request.reason
                ? ["workflow", "operation-cancel", operationId, "--reason", String(request.reason), "--json"]
                : ["workflow", "operation-cancel", operationId, "--json"],
              request,
            ),
    };
    this.toolCalls = {
      list: (request = {}) => this.#workspacePlanField("agent_session_tool_calls", request),
      get: (callId, request = {}) =>
        this.#workspacePlanRecord("agent_session_tool_calls", callId, ["call_id", "callId", "id"], request),
      forRun: (runId, request = {}) => this.runs.toolCalls(runId, request),
    };
    this.tasks = {
      list: (request = {}) => this.#workspacePlanField("task_runs", request),
      get: (taskRunId, request = {}) =>
        this.#workspacePlanRecord("task_runs", taskRunId, ["task_run_id", "taskRunId", "id"], request),
      forRun: (runId, request = {}) => this.runs.tasks(runId, request),
    };
    this.events = {
      list: (request = {}) => this.#workspacePlanField("run_events", request),
      get: (eventId, request = {}) =>
        this.#workspacePlanRecord("run_events", eventId, ["event_id", "eventId", "id"], request),
      forRun: (runId, request = {}) => this.runs.events(runId, request),
    };
    this.tools = {
      list: (request = {}) => this.#workspacePlanField("tools", request),
      get: (tool, request = {}) =>
        this.#workspacePlanRecord("tools", definitionName(tool), ["name", "definition_name", "definitionName"], request),
    };
    this.admin = {
      status: (request = {}) => this.#runJSON(workspaceOpsArgs("status", request), request),
      diagnose: (request = {}) => this.#runJSON(workspaceOpsArgs("diagnose", request), request),
      ensureRuntime: (request = {}) => this.#runJSON(workspaceOpsArgs("ensure-runtime", request), request),
      repair: (request = {}) => this.#runJSON(workspaceOpsArgs("repair", request), request),
    };
  }

  check(request = {}) {
    return this.#runJSON(withProjectDir(["check", "--json"], request), request);
  }

  connect(agent, request = {}) {
    const args = withProjectDir(["connect", definitionName(agent), request.id || request.instance || "local", "--json"], request);
    if (request.session) args.push("--session", request.session);
    if (request.envFile || request.env) args.push("--env", request.envFile || request.env);
    if (request.message != null) args.push("--message", String(request.message));
    return this.#runJSON(args, request);
  }

  run(workflow, request = {}) {
    if (this.#canRequest()) {
      return this.#requestJSON(
        "POST",
        apiWorkspacePath(this.transport, request, `/workflows/${encodeURIComponent(definitionName(workflow))}/runs`),
        request,
        {
          body: compactHTTPBody({
            input: request.input ?? request.payload ?? {},
            once: request.once,
            wait: request.wait,
          }),
        },
      );
    }
    const args = withProjectDir(["run", definitionName(workflow), "--json"], request);
    if (request.input !== undefined || request.payload !== undefined) {
      args.push("--input", inputJSON(request.input ?? request.payload));
    }
    if (request.wait) args.push("--wait");
    if (request.once === false) args.push("--once=false");
    return this.#runJSON(args, request);
  }

  #runJSON(args, request = {}) {
    if (!this.transport || typeof this.transport.run !== "function") {
      throw new TypeError("configured Loom transport does not support CLI run operations");
    }
    return this.transport
      .run(args, {
        cwd: request.cwd,
        env: request.envVars,
        signal: request.signal,
        stdin: request.stdin,
      })
      .then((result) => parseJSONResult(result, args));
  }

  #canRequest() {
    return this.transport && typeof this.transport.request === "function";
  }

  #requestJSON(method, requestPath, request = {}, options = {}) {
    return this.transport.request(method, requestPath, {
      query: options.query,
      body: options.body,
      signal: request.signal,
      headers: request.headers,
    });
  }

  #workspacePlanField(field, request = {}) {
    const planRequest = { ...request, fromWorkspace: true };
    return this.#runJSON(defsArgs("plan", planRequest), planRequest).then((plan) => {
      const value = plan && plan[field];
      return Array.isArray(value) ? value : [];
    });
  }

  #workspacePlanRecord(field, id, keys, request = {}) {
    const want = String(id || "");
    return this.#workspacePlanField(field, request).then((records) =>
      records.find((record) => keys.some((key) => String((record && record[key]) || "") === want)),
    );
  }

}

export function createLoomClient(options = {}) {
  return new LoomClient(options);
}

export const loom = createLoomClient();

function defsArgs(subcommand, request, extra = []) {
  const args = withProjectDir(["defs", subcommand, "--json"], request);
  if (subcommand === "plan" && request.fromWorkspace) args.push("--from-workspace");
  args.push(...extra);
  return args;
}

function workspaceOpsArgs(subcommand, request = {}) {
  const args = ["workspace", "ops", subcommand];
  const workspace = request.workspace || request.key || request.workspaceKey || request.workspace_key;
  if (workspace) args.push(String(workspace));
  args.push("--json");
  if ((subcommand === "ensure-runtime" || subcommand === "repair") && request.timeout != null) {
    args.push("--timeout", String(request.timeout));
  }
  return args;
}

function agentCreateArgs(agent, request = {}) {
  const args = ["agentdef", "add", definitionName(agent), "--role", requiredString(request.role || request.roleName, "role")];
  pushFlag(args, "--auto", request.auto);
  pushOption(args, "--backend", request.backend);
  pushListOption(args, "--repos", request.repos);
  pushListOption(args, "--repo-groups", request.repoGroups || request.repo_groups);
  pushFlag(args, "--cross-repo", request.crossRepo || request.cross_repo);
  pushOption(args, "--parent", request.parent);
  pushOption(args, "--mode", request.mode);
  pushOption(args, "--task-filter", request.taskFilter || request.task_filter);
  if (request.maxConcurrency != null || request.max_concurrency != null) {
    args.push("--max-concurrency", String(request.maxConcurrency ?? request.max_concurrency));
  }
  pushOption(args, "--budget-policy", request.budgetPolicy || request.budget_policy);
  pushOption(args, "--task", request.task);
  pushOption(args, "--orchestrator", request.orchestrator);
  args.push("--json");
  return args;
}

function pushFlag(args, flag, enabled) {
  if (enabled) args.push(flag);
}

function pushOption(args, flag, value) {
  if (value == null || value === "") return;
  args.push(flag, String(value));
}

function pushListOption(args, flag, value) {
  if (value == null) return;
  const joined = Array.isArray(value) ? value.filter((item) => item != null && item !== "").join(",") : String(value);
  if (joined !== "") args.push(flag, joined);
}

function requiredString(value, name) {
  if (value == null || String(value).trim() === "") {
    throw new TypeError(`${name} is required`);
  }
  return String(value);
}

function stringRecord(value = {}) {
  return Object.fromEntries(
    Object.entries(value || {})
      .filter(([, v]) => v != null)
      .map(([k, v]) => [k, String(v)]),
  );
}

function withProjectDir(args, request = {}) {
  const dir = projectDir(request);
  if (dir) args.push("--dir", dir);
  return args;
}

function projectDir(request = {}) {
  if (request.dir) return request.dir;
  if (!request.source) return undefined;
  return sourceToProjectDir(request.source);
}

export function sourceToProjectDir(source) {
  const normalized = normalize(String(source || "."));
  return basename(normalized) === ".loom" ? dirname(normalized) || "." : normalized;
}

function definitionName(definition) {
  if (typeof definition === "string") return definition;
  if (definition && typeof definition.name === "string" && definition.name.trim() !== "") {
    return definition.name;
  }
  throw new TypeError("expected a definition name string or an object with a name");
}

function inputJSON(input) {
  if (typeof input === "string") return input;
  return JSON.stringify(input ?? {});
}

function inputValue(input) {
  if (typeof input !== "string") return input ?? {};
  return JSON.parse(input);
}

function apiWorkspacePath(transport, request = {}, suffix) {
  const workspace =
    request.workspace || request.key || request.workspaceKey || request.workspace_key || transport.workspace || transport.workspaceKey || transport.workspace_key;
  if (!workspace) throw new TypeError("workspace is required for Loom HTTP API operations");
  return `/api/workspaces/${encodeURIComponent(String(workspace))}${suffix}`;
}

function routeWildcardPath(routePath) {
  const clean = String(routePath || "").trim().replace(/^\/+/, "");
  if (!clean) return "/";
  return `/${clean
    .split("/")
    .filter((part) => part !== "")
    .map((part) => encodeURIComponent(part))
    .join("/")}`;
}

function compactHTTPBody(body) {
  return Object.fromEntries(Object.entries(body).filter(([, value]) => value !== undefined));
}

function unwrapListResponse(response) {
  if (response && Array.isArray(response.data)) return response.data;
  return response;
}

function parseJSONResult(result, args) {
  const text = String(result.stdout || "").trim();
  if (text === "") return null;
  try {
    return JSON.parse(text);
  } catch (cause) {
    const err = new Error(`loom ${args.join(" ")} did not return JSON`);
    err.cause = cause;
    err.result = result;
    throw err;
  }
}
