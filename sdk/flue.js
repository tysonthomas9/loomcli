import { spawnSync } from "node:child_process";

export class FlueDriverClient {
  static fromEnv(options = {}) {
    return new FlueDriverClient({
      env: options.env || process.env,
      input: options.input,
      command: options.command,
    });
  }

  constructor(options = {}) {
    this.env = options.env || process.env;
    this.input = options.input || {};
    this.command = options.command || execTaskCommand(this.env);
    this.workspace = pickEnv(this.env, "LOOM_DRIVER_WORKSPACE");
    this.driverRunId = pickEnv(this.env, "LOOM_DRIVER_RUN_ID");
    this.taskRunResultsByTaskId = new Map();
    this.taskRunResultsByRunId = new Map();
    this.epics = Object.freeze({
      get: (input = {}) => this.getEpic(input),
      snapshot: (input = {}) => this.epicSnapshot(input),
    });
    this.agents = Object.freeze({
      list: (input = {}) => this.listAgents(input),
      orchestrationSession: (input = {}) => this.agentOrchestrationSession(input),
      updateParent: (input = {}) => this.updateAgentParent(input),
      deliverAssignment: (input = {}) => this.deliverLeadAssignment(input),
      message: (input = {}) => this.messageAgent(input),
    });
    this.tasks = Object.freeze({
      claimReady: (input = {}) => this.claimReady(input),
      complete: (input = {}) => this.completeTask(input),
      release: (input = {}) => this.releaseTask(input),
    });
    this.taskRuns = Object.freeze({
      request: (input = {}) => this.requestTaskRun(input),
      active: (input = {}) => this.activeTaskRuns(input),
      recoverStale: (input = {}) => this.recoverStaleTaskRuns(input),
    });
  }

  completed(input = {}) {
    return { status: "completed", summary: input.summary || "completed" };
  }

  failed(input = {}) {
    return {
      status: "failed",
      summary: input.summary || "failed",
      errorClass: input.errorClass || input.error_class || "driver_failed",
    };
  }

  needsReview(input = {}) {
    return {
      status: "needs_review",
      summary: input.summary || "needs review",
      errorClass: input.errorClass || input.error_class || "needs_review",
      taskRunId: input.taskRunId || input.task_run_id,
      logsRef: input.logsRef || input.logs_ref,
      artifactsRef: input.artifactsRef || input.artifacts_ref,
    };
  }

  async claimReady(input = {}) {
    const args = this.#baseArgs("claim-ready");
    const epicId = input.epicId || input.epic_id || this.input.epicId || this.input.epic_id || "";
    appendStringFlag(args, "--epic-id", epicId);
    return this.#run(args);
  }

  async getEpic(input = {}) {
    const args = this.#baseArgs("epic-get");
    const epicId = input.epicId || input.epic_id || this.input.epicId || this.input.epic_id || "";
    appendStringFlag(args, "--epic-id", epicId);
    return this.#run(args);
  }

  async epicSnapshot(input = {}) {
    const args = this.#baseArgs("epic-snapshot");
    const epicId = input.epicId || input.epic_id || this.input.epicId || this.input.epic_id || "";
    appendStringFlag(args, "--epic-id", epicId);
    return this.#run(args);
  }

  async listAgents(_input = {}) {
    return this.#run(this.#baseArgs("list-agents"));
  }

  async agentOrchestrationSession(input = {}) {
    const agent = input.agent || input.agentName || input.agent_name || input.name || "";
    if (!agent) {
      throw new Error("agents.orchestrationSession requires agent");
    }
    const args = this.#baseArgs("agent-orchestration-session");
    args.push("--agent", String(agent));
    return this.#run(args);
  }

  async updateAgentParent(input = {}) {
    const agent = input.agent || input.agentName || input.agent_name || input.name || "";
    const parent = input.parent || input.parentEpicId || input.parent_epic_id || "";
    if (!agent || !parent) {
      throw new Error("agents.updateParent requires agent and parent");
    }
    const args = this.#baseArgs("update-agent-parent");
    args.push("--agent", String(agent), "--parent", String(parent));
    appendStringFlag(args, "--expect-parent", input.expectParent || input.expect_parent || "");
    return this.#run(args);
  }

  async deliverLeadAssignment(input = {}) {
    const agent = input.agent || input.agentName || input.agent_name || input.name || "";
    if (!agent) {
      throw new Error("agents.deliverAssignment requires agent");
    }
    const args = this.#baseArgs("deliver-lead-assignment");
    args.push("--agent", String(agent));
    return this.#run(args);
  }

  async messageAgent(input = {}) {
    const agent = input.agent || input.agentName || input.agent_name || input.name || "";
    const message = input.message || input.text || input.body || "";
    if (!agent || !message) {
      throw new Error("agents.message requires agent and message");
    }
    const args = this.#baseArgs("deliver-agent-message");
    args.push("--agent", String(agent), "--message", String(message));
    return this.#run(args);
  }

  async requestTaskRun(input = {}) {
    const taskId = input.taskId || input.task_id;
    if (!taskId) {
      throw new Error("taskRuns.request requires taskId");
    }
    const args = this.#baseArgs("exec-task");
    args.push("--task-id", String(taskId));
    appendStringFlag(args, "--provider-profile", input.providerProfile || input.provider_profile || "");
    appendStringFlag(args, "--task-run-id", input.taskRunId || input.task_run_id || "");
    appendStringFlag(args, "--worker-profile-id", input.workerProfileId || input.worker_profile_id || "");
    appendStringFlag(args, "--parent-session-id", input.parentSessionId || input.parent_session_id || "");
    appendStringFlag(args, "--node-id", input.nodeId || input.node_id || "");
    appendStringFlag(args, "--runner-id", input.runnerId || input.runner_id || "");
    appendRepeatedFlag(args, "--supported-provider", input.supportedProviders || input.supported_providers || []);
    appendRepeatedFlag(args, "--capability", input.capabilities || []);
    const sandboxPlacement = input.sandboxPlacement || input.sandbox_placement || {};
    appendStringFlag(args, "--sandbox-provider", sandboxPlacement.provider || input.sandboxProvider || input.sandbox_provider || "");
    appendStringFlag(args, "--sandbox-id", sandboxPlacement.sandbox_id || sandboxPlacement.sandboxId || input.sandboxId || input.sandbox_id || "");
    appendStringFlag(args, "--sandbox-cwd", sandboxPlacement.cwd || input.sandboxCwd || input.sandbox_cwd || "");
    appendStringFlag(args, "--sandbox-repo-ref", sandboxPlacement.repo_ref || sandboxPlacement.repoRef || input.sandboxRepoRef || input.sandbox_repo_ref || "");
    args.push("--defer-completion");
    const result = this.#run(args);
    rememberTaskRunResult(this, result || {});
    return result;
  }

  async activeTaskRuns(input = {}) {
    const args = this.#baseArgs("active-task-runs");
    const epicId = input.epicId || input.epic_id || this.input.epicId || this.input.epic_id || "";
    appendStringFlag(args, "--epic-id", epicId);
    appendStringFlag(args, "--limit", input.limit || "");
    return this.#run(args);
  }

  async recoverStaleTaskRuns(input = {}) {
    const args = this.#baseArgs("recover-stale-tasks");
    appendStringFlag(args, "--stale-before", input.staleBefore || input.stale_before || "");
    appendStringFlag(args, "--max-age-seconds", input.maxAgeSeconds || input.max_age_seconds || "");
    appendStringFlag(args, "--error-class", input.errorClass || input.error_class || "");
    appendStringFlag(args, "--error-message", input.errorMessage || input.error_message || "");
    return this.#run(args);
  }

  async completeTask(input = {}) {
    const taskId = taskPayloadID(input);
    const requestedTaskRunId = input.taskRunId || input.task_run_id || "";
    const remembered = requestedTaskRunId
      ? this.taskRunResultsByRunId.get(String(requestedTaskRunId))
      : this.taskRunResultsByTaskId.get(String(taskId));
    const taskRunId = requestedTaskRunId || remembered?.taskRunId || remembered?.task_run_id || remembered?.id || "";
    if (!taskId && !taskRunId) {
      throw new Error("tasks.complete requires taskId or taskRunId");
    }
    const args = this.#baseArgs("complete-task");
    appendStringFlag(args, "--task-id", taskId);
    appendStringFlag(args, "--task-run-id", taskRunId);
    appendStringFlag(args, "--reason", input.reason || "");
    appendStringFlag(args, "--completion-id", input.completionId || input.completion_id || "");
    appendStringFlag(args, "--lease-token", input.leaseToken || input.lease_token || remembered?.leaseToken || remembered?.lease_token || "");
    appendStringFlag(args, "--logs-ref", input.logsRef || input.logs_ref || remembered?.logsRef || remembered?.logs_ref || "");
    appendStringFlag(args, "--artifacts-ref", input.artifactsRef || input.artifacts_ref || remembered?.artifactsRef || remembered?.artifacts_ref || "");
    appendRepeatedFlag(args, "--artifact-id", input.artifactIds || input.artifact_ids || remembered?.artifactIds || remembered?.artifact_ids || []);
    return this.#run(args);
  }

  async releaseTask(input = {}) {
    const taskId = taskPayloadID(input);
    if (!taskId) {
      throw new Error("tasks.release requires taskId");
    }
    const args = this.#baseArgs("release-task");
    args.push("--task-id", String(taskId));
    return this.#run(args);
  }

  #baseArgs(command) {
    if (!this.driverRunId) {
      throw new Error("LOOM_DRIVER_RUN_ID is required");
    }
    const args = ["driver", command, "--driver-run-id", this.driverRunId, "--json"];
    appendStringFlag(args, "--workspace-key", this.workspace);
    return args;
  }

  #run(args) {
    const proc = spawnSync(this.command[0], this.command.slice(1).concat(args), {
      encoding: "utf8",
      env: driverCommandEnv(this.env),
    });
    if (proc.error) {
      throw proc.error;
    }
    if (proc.status !== 0) {
      const detail = (proc.stderr || proc.stdout || "").trim();
      throw new Error("loom " + args.join(" ") + " failed" + (detail ? ": " + detail : ""));
    }
    const stdout = (proc.stdout || "").trim();
    return stdout ? JSON.parse(stdout) : null;
  }
}

export function createLoomDriverClient(options = {}) {
  if (options && !("input" in options) && !("env" in options) && !("command" in options)) {
    return FlueDriverClient.fromEnv({ input: options });
  }
  return FlueDriverClient.fromEnv(options);
}

export const createLoomClient = createLoomDriverClient;

function execTaskCommand(env) {
  const encoded = pickEnv(env, "LOOM_DRIVER_EXEC_TASK_CMD_JSON");
  if (encoded) {
    const parsed = JSON.parse(encoded);
    if (!Array.isArray(parsed) || parsed.length === 0) {
      throw new Error("LOOM_DRIVER_EXEC_TASK_CMD_JSON must be a non-empty string array");
    }
    return parsed.map(String);
  }
  return [pickEnv(env, "LOOM_DRIVER_EXEC_TASK_CMD") || "loom"];
}

function driverCommandEnv(env) {
  const out = { ...env };
  if (out.LOOM_DRIVER_FLEET_DB_URL && !out.LOOM_FLEET_DB_URL) {
    out.LOOM_FLEET_DB_URL = out.LOOM_DRIVER_FLEET_DB_URL;
  }
  if (out.LOOM_DRIVER_FLEET_DB_API_KEY && !out.LOOM_FLEET_DB_API_KEY) {
    out.LOOM_FLEET_DB_API_KEY = out.LOOM_DRIVER_FLEET_DB_API_KEY;
  }
  if (out.LOOM_DRIVER_FLEET_DB_ACTOR && !out.LOOM_FLEET_DB_ACTOR) {
    out.LOOM_FLEET_DB_ACTOR = out.LOOM_DRIVER_FLEET_DB_ACTOR;
  }
  delete out.LOOM_DRIVER_FLEET_DB_URL;
  delete out.LOOM_DRIVER_FLEET_DB_API_KEY;
  delete out.LOOM_DRIVER_FLEET_DB_ACTOR;
  return out;
}

function appendStringFlag(args, flag, value) {
  if (value !== undefined && value !== null && String(value).trim() !== "") {
    args.push(flag, String(value));
  }
}

function appendRepeatedFlag(args, flag, values) {
  const list = Array.isArray(values) ? values : values ? [values] : [];
  for (const value of list) {
    appendStringFlag(args, flag, value);
  }
}

function rememberTaskRunResult(client, result = {}) {
  const runId = result.taskRunId || result.task_run_id || result.id || "";
  const taskId = result.taskId || result.task_id || "";
  if (runId) {
    client.taskRunResultsByRunId.set(String(runId), result);
  }
  if (taskId) {
    client.taskRunResultsByTaskId.set(String(taskId), result);
  }
}

function taskPayloadID(input) {
  if (typeof input === "string") {
    return input;
  }
  return input.taskId || input.task_id || input.id || "";
}

function pickEnv(env, key) {
  return String(env?.[key] || "").trim();
}
