const fs = require("fs");
const crypto = require("crypto");
const os = require("os");
const path = require("path");
const vm = require("vm");
const { createRequire } = require("module");
const { stripTypeScriptTypes } = require("node:module");

const moduleCache = new Map();
const cleanupRuntimeWorkspaceFiles = Symbol("cleanupRuntimeWorkspaceFiles");
const daytonaSandboxRegistry = new Map();
let activeWorkflowExecution = null;

function defineWorkflow(config) {
  return { __loomType: "workflow", ...config };
}

function createAgent(config, overrides) {
  if (typeof config === "function") {
    return { __loomType: "agent", __loomFactory: config };
  }
  if (config && isAgentProfile(config)) {
    return agentFromProfile(config, overrides || {});
  }
  return { __loomType: "agent", ...(config || {}), ...(overrides || {}) };
}

function defineAgent(config) {
  return createAgent(config);
}

function defineAgentProfile(config) {
  return { __loomType: "agent_profile", ...(config || {}) };
}

function isAgentProfile(value) {
  return value && (value.__loomType === "agent_profile" || value.__loomType === "agentProfile");
}

function agentFromProfile(profile, overrides) {
  const merged = {
    ...profile,
    ...overrides,
    __loomType: "agent",
    profileName: String(overrides.profileName || overrides.profile_name || profile.name || ""),
    profile_name: String(overrides.profileName || overrides.profile_name || profile.name || ""),
  };
  for (const key of ["skills", "tools", "allowedCommands", "deniedCommands", "repos", "env"]) {
    merged[key] = uniqueStrings([...(Array.isArray(profile[key]) ? profile[key] : []), ...(Array.isArray(overrides[key]) ? overrides[key] : [])]);
  }
  const profilePolicy = profile.policy && typeof profile.policy === "object" ? profile.policy : {};
  const overridePolicy = overrides.policy && typeof overrides.policy === "object" ? overrides.policy : {};
  merged.policy = { ...profilePolicy, ...overridePolicy };
  for (const key of ["allowedCommands", "deniedCommands"]) {
    merged.policy[key] = uniqueStrings([...(Array.isArray(profilePolicy[key]) ? profilePolicy[key] : []), ...(Array.isArray(overridePolicy[key]) ? overridePolicy[key] : [])]);
  }
  return merged;
}

function uniqueStrings(values) {
  return Array.from(new Set((values || []).map((item) => String(item || "").trim()).filter(Boolean)));
}

function defineTool(config) {
  return { __loomType: "tool", ...config };
}

const schema = new Proxy(
  {},
  {
    get(_target, prop) {
      return (...args) => ({ kind: String(prop), args });
    },
  },
);

const Type = schema;

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
  daytona(config = {}) {
    return { __loomType: "runtime", provider: "daytona", ...config };
  },
  remote(config = {}) {
    return { __loomType: "runtime", provider: config.provider || "e2b", ...config };
  },
};

function daytona(sandbox, options = {}) {
  const sandboxData = sandbox && typeof sandbox === "object" ? sandbox : {};
  const loomData = sandboxData.__loomDaytona && typeof sandboxData.__loomDaytona === "object" ? sandboxData.__loomDaytona : {};
  const sandboxId = String(
    firstPresent(
      options.sandboxId,
      options.sandbox_id,
      loomData.sandboxId,
      loomData.sandbox_id,
      sandboxData.sandboxId,
      sandboxData.sandbox_id,
      sandboxData.id,
      sandboxData.workspaceId,
      sandboxData.workspace_id,
    ) || "",
  );
  const daytonaData = sandboxData.daytona && typeof sandboxData.daytona === "object" ? sandboxData.daytona : {};
  const optionDaytonaData = options.daytona && typeof options.daytona === "object" ? options.daytona : {};
  const profileName = String(firstPresent(options.profileName, options.profile_name, options.name, loomData.profileName, loomData.profile_name, sandboxData.profileName, sandboxData.profile_name) || "");
  const cwd = String(firstPresent(options.cwd, loomData.cwd, sandboxData.cwd, sandboxData.root) || "");
  const setupCommands = [
    ...stringArray(firstPresent(options.setupCommands, options.setup_commands)),
    ...stringArray(firstPresent(options.installCommands, options.install_commands)),
  ];
  const env = stringArray(options.env);
  const repos = stringArray(options.repos);
  const envVars = firstPresent(
    options.envVars,
    options.env_vars,
    optionDaytonaData.envVars,
    optionDaytonaData.env_vars,
    daytonaData.envVars,
    daytonaData.env_vars,
  );
  return {
    __loomType: "runtime",
    provider: "daytona",
    ...(profileName ? { profileName, profile_name: profileName, name: profileName } : {}),
    ...(cwd ? { cwd } : {}),
    ...(options.model ? { model: String(options.model) } : {}),
    ...(env.length > 0 ? { env } : {}),
    ...(repos.length > 0 ? { repos } : {}),
    workspace: {
      ...(options.workspace && typeof options.workspace === "object" ? jsonSafe(options.workspace) : {}),
      ...(sandboxId ? { providerWorkspaceId: sandboxId, provider_workspace_id: sandboxId } : {}),
      provider: "daytona",
    },
    daytona: compactObject({
      ...jsonSafe(daytonaData),
      ...jsonSafe(loomData),
      ...jsonSafe(optionDaytonaData),
      sandbox_id: sandboxId,
      sandboxId,
      language: firstPresent(options.language, optionDaytonaData.language, daytonaData.language),
      image: firstPresent(options.image, optionDaytonaData.image, daytonaData.image),
      snapshot: firstPresent(options.snapshot, loomData.snapshot, sandboxData.snapshot, daytonaData.snapshot),
      resources: firstPresent(options.resources, optionDaytonaData.resources, daytonaData.resources),
      env_vars: envVars && typeof envVars === "object" ? stringRecord(envVars) : undefined,
      auto_stop_interval: firstPresent(options.autoStopInterval, options.auto_stop_interval, optionDaytonaData.autoStopInterval, optionDaytonaData.auto_stop_interval, daytonaData.autoStopInterval, daytonaData.auto_stop_interval),
      auto_archive_interval: firstPresent(options.autoArchiveInterval, options.auto_archive_interval, optionDaytonaData.autoArchiveInterval, optionDaytonaData.auto_archive_interval, daytonaData.autoArchiveInterval, daytonaData.auto_archive_interval),
      auto_delete_interval: firstPresent(options.autoDeleteInterval, options.auto_delete_interval, optionDaytonaData.autoDeleteInterval, optionDaytonaData.auto_delete_interval, daytonaData.autoDeleteInterval, daytonaData.auto_delete_interval),
      ephemeral: firstPresent(options.ephemeral, optionDaytonaData.ephemeral, daytonaData.ephemeral),
      target: firstPresent(options.target, loomData.target, sandboxData.target, daytonaData.target),
      api_key_env: firstPresent(options.apiKeyEnv, options.api_key_env, loomData.api_key_env, sandboxData.apiKeyEnv, sandboxData.api_key_env, daytonaData.api_key_env),
      apiKeyEnv: firstPresent(options.apiKeyEnv, options.api_key_env, loomData.apiKeyEnv, sandboxData.apiKeyEnv, sandboxData.api_key_env, daytonaData.apiKeyEnv),
      repo_url: firstPresent(options.repoUrl, options.repo_url, options.remoteUrl, options.remote_url),
      branch: firstPresent(options.branch, options.checkoutBranch, options.checkout_branch, options.gitBranch, options.git_branch),
      ref: firstPresent(options.ref, options.checkoutRef, options.checkout_ref, options.gitRef, options.git_ref),
      git_token_env: firstPresent(options.gitTokenEnv, options.git_token_env, options.githubTokenEnv, options.github_token_env, options.gitAuthTokenEnv, options.git_auth_token_env),
      git_username: firstPresent(options.gitUsername, options.git_username, options.githubUsername, options.github_username),
      git_deploy_key_env: firstPresent(options.gitDeployKeyEnv, options.git_deploy_key_env, options.deployKeyEnv, options.deploy_key_env, options.sshKeyEnv, options.ssh_key_env),
      openai_api_key_env: firstPresent(options.openaiApiKeyEnv, options.openai_api_key_env),
      codex_auth_file_env: firstPresent(options.codexAuthFileEnv, options.codex_auth_file_env),
      setup_commands: setupCommands.length > 0 ? setupCommands : undefined,
      create_timeout: firstPresent(options.createTimeout, options.create_timeout, options.timeout, optionDaytonaData.createTimeout, optionDaytonaData.create_timeout, optionDaytonaData.timeout, daytonaData.createTimeout, daytonaData.create_timeout, daytonaData.timeout),
      setup_timeout: firstPresent(options.setupTimeout, options.setup_timeout),
      health_timeout: firstPresent(options.healthTimeout, options.health_timeout),
      run_timeout: firstPresent(options.runTimeout, options.run_timeout, options.commandTimeout, options.command_timeout),
      build_logs: firstPresent(options.buildLogs, options.build_logs, optionDaytonaData.buildLogs, optionDaytonaData.build_logs, daytonaData.buildLogs, daytonaData.build_logs),
    }),
  };
}

function createDaytonaImage(spec) {
  const current = {
    __loomType: "daytona_image",
    ...spec,
    steps: Array.isArray(spec.steps) ? spec.steps : [],
  };
  const withStep = (step) => createDaytonaImage({ ...current, steps: current.steps.concat([compactObject(step)]) });
  return {
    ...current,
    runCommands: (...commands) => withStep({ op: "runCommands", commands: flattenStringArgs(commands) }),
    run_commands: (...commands) => withStep({ op: "runCommands", commands: flattenStringArgs(commands) }),
    pipInstall: (...packages) => withStep({ op: "pipInstall", packages: flattenStringArgs(packages) }),
    pip_install: (...packages) => withStep({ op: "pipInstall", packages: flattenStringArgs(packages) }),
    pipInstallFromRequirements: (file) => withStep({ op: "pipInstallFromRequirements", file: stringValue(file) }),
    pip_install_from_requirements: (file) => withStep({ op: "pipInstallFromRequirements", file: stringValue(file) }),
    pipInstallFromPyproject: (file, options = {}) =>
      withStep({ op: "pipInstallFromPyproject", file: stringValue(file), options: compactObject(options || {}) }),
    pip_install_from_pyproject: (file, options = {}) =>
      withStep({ op: "pipInstallFromPyproject", file: stringValue(file), options: compactObject(options || {}) }),
    addLocalFile: (source, target) => withStep({ op: "addLocalFile", source: stringValue(source), target: stringValue(target) }),
    add_local_file: (source, target) => withStep({ op: "addLocalFile", source: stringValue(source), target: stringValue(target) }),
    addLocalDir: (source, target) => withStep({ op: "addLocalDir", source: stringValue(source), target: stringValue(target) }),
    add_local_dir: (source, target) => withStep({ op: "addLocalDir", source: stringValue(source), target: stringValue(target) }),
    env: (vars = {}) => withStep({ op: "env", vars: stringRecord(vars) }),
    workdir: (dir) => withStep({ op: "workdir", dir: stringValue(dir) }),
    entrypoint: (value) => withStep({ op: "entrypoint", value: Array.isArray(value) ? stringArray(value) : stringValue(value) }),
    cmd: (value) => withStep({ op: "cmd", value: Array.isArray(value) ? stringArray(value) : stringValue(value) }),
    dockerfileCommands: (commands, contextDir) =>
      withStep({ op: "dockerfileCommands", commands: stringArray(Array.isArray(commands) ? commands : [commands]), contextDir: stringValue(contextDir) }),
    dockerfile_commands: (commands, contextDir) =>
      withStep({ op: "dockerfileCommands", commands: stringArray(Array.isArray(commands) ? commands : [commands]), contextDir: stringValue(contextDir) }),
    user: (value) => withStep({ op: "user", value: stringValue(value) }),
    toJSON() {
      const { __loomType, toJSON, ...json } = current;
      return compactObject(json);
    },
  };
}

function makeDaytonaSandbox(clientOptions = {}, createOptions = {}) {
  const execution = activeWorkflowExecution || {};
  const request = execution.request || {};
  const operations = execution.operations || [];
  const index = operations.length + 1;
  const sandboxId = String(
    firstPresent(
      createOptions.id,
      createOptions.sandboxId,
      createOptions.sandbox_id,
      createOptions.name,
      `daytona:${String(request.id || "workflow")}:${index}`,
    ),
  ).replace(/[^A-Za-z0-9_.:-]/g, "_");
  const snapshot = String(firstPresent(createOptions.snapshot, createOptions.snapshotName, createOptions.snapshot_name) || "");
  const target = String(firstPresent(createOptions.target, clientOptions.target) || "");
  const cwd = String(firstPresent(createOptions.cwd, createOptions.workdir, createOptions.workspaceRoot, "/workspace") || "/workspace");
  const sandbox = {
    __loomType: "daytona_sandbox",
    provider: "daytona",
    id: sandboxId,
    sandboxId,
    sandbox_id: sandboxId,
    workspaceId: sandboxId,
    workspace_id: sandboxId,
    cwd,
    root: cwd,
    snapshot,
    target,
    daytona: compactObject({
      sandbox_id: sandboxId,
      sandboxId,
      snapshot,
      target,
      api_key_configured: Boolean(clientOptions.apiKey || clientOptions.api_key),
      api_key_env: firstPresent(clientOptions.apiKeyEnv, clientOptions.api_key_env, createOptions.apiKeyEnv, createOptions.api_key_env),
    }),
    process: {
      executeCommand: async (command, cwd, env, timeout) => daytonaSandboxShell(sandbox, command, { cwd, env, timeout, __record: false }),
      exec: async (command, cwd, env, timeout) => daytonaSandboxShell(sandbox, command, { cwd, env, timeout, __record: false }),
    },
    async shell(commandOrOptions, maybeOptions = {}) {
      return daytonaSandboxShell(sandbox, commandOrOptions, maybeOptions);
    },
    async destroy(options = {}) {
      return daytonaSandboxLifecycle("destroy", sandbox, options);
    },
    async delete(options = {}) {
      return daytonaSandboxLifecycle("delete", sandbox, options);
    },
    toJSON() {
      return {
        provider: "daytona",
        id: sandboxId,
        sandboxId,
        sandbox_id: sandboxId,
        workspaceId: sandboxId,
        workspace_id: sandboxId,
        cwd,
        root: cwd,
        snapshot,
        target,
        daytona: this.daytona,
      };
    },
  };
  daytonaSandboxRegistry.set(sandboxId, sandbox);
  if (activeWorkflowExecution) {
    operations.push({
      type: "runtime.daytona.sandbox.create",
      params: {
        accepted: true,
        status: "admitted",
        provider: "daytona",
        sandboxId,
        sandbox_id: sandboxId,
        snapshot,
        target,
        cwd,
        apiKeyConfigured: Boolean(clientOptions.apiKey || clientOptions.api_key),
        api_key_configured: Boolean(clientOptions.apiKey || clientOptions.api_key),
        client: daytonaClientMetadata(clientOptions),
        options: redactSensitiveObject(createOptions),
        createdAt: new Date().toISOString(),
      },
    });
  }
  return sandbox;
}

function daytonaSandboxShell(sandbox, commandOrOptions, maybeOptions = {}) {
  const options =
    typeof commandOrOptions === "string"
      ? { ...(maybeOptions || {}), command: commandOrOptions }
      : commandOrOptions || {};
  const command = String(options.command || "").trim();
  if (!command) throw new Error("Daytona sandbox shell requires command");
  const params = {
    accepted: true,
    status: String(options.status || "completed"),
    provider: "daytona",
    sandboxId: sandbox.sandboxId,
    sandbox_id: sandbox.sandboxId,
    command,
    cwd: String(options.cwd || sandbox.cwd || ""),
    env: redactSensitiveObject(options.env || {}),
    exitCode: Number(options.exitCode || options.exit_code || 0),
    result: Object.prototype.hasOwnProperty.call(options, "mockResult") ? jsonSafe(options.mockResult) : undefined,
    completedAt: new Date().toISOString(),
  };
  if (activeWorkflowExecution && options.__record !== false) {
    activeWorkflowExecution.operations.push({ type: "runtime.daytona.sandbox.shell", params });
  }
  return jsonSafe(params);
}

function daytonaSandboxLifecycle(action, sandbox, options = {}) {
  const params = {
    accepted: true,
    status: "admitted",
    action,
    provider: "daytona",
    sandboxId: sandbox.sandboxId,
    sandbox_id: sandbox.sandboxId,
    reason: String(options.reason || ""),
    metadata: redactSensitiveObject(options.metadata || {}),
    requestedAt: new Date().toISOString(),
  };
  if (activeWorkflowExecution) {
    activeWorkflowExecution.operations.push({ type: "runtime.daytona.sandbox.lifecycle", params });
  }
  return jsonSafe(params);
}

const daytonaSDKShim = {
  Daytona: class Daytona {
    constructor(options = {}) {
      this.options = options || {};
    }

    async create(options = {}) {
      return makeDaytonaSandbox(this.options, options || {});
    }

    async get(id) {
      return makeDaytonaSandbox(this.options, { id });
    }
  },
  Image: {
    base: (image) => createDaytonaImage({ base: stringValue(image) }),
    debianSlim: (version) => createDaytonaImage({ base: `debian-slim:${stringValue(version)}` }),
    debian_slim: (version) => createDaytonaImage({ base: `debian-slim:${stringValue(version)}` }),
    fromDockerfile: (dockerfile) => createDaytonaImage({ dockerfile: stringValue(dockerfile) }),
    from_dockerfile: (dockerfile) => createDaytonaImage({ dockerfile: stringValue(dockerfile) }),
  },
};

function daytonaSDKModuleFor(file) {
  const real = loadRealDaytonaSDK(file);
  if (!real || typeof real.Daytona !== "function") return { ...daytonaSDKShim, __loomRealSDK: false };
  return { ...instrumentedDaytonaSDK(real), __loomRealSDK: true };
}

function loadRealDaytonaSDK(file) {
  if (String(process.env.LOOM_DAYTONA_SDK || "").toLowerCase() === "shim") return null;
  try {
    return createRequire(file)("@daytona/sdk");
  } catch {
    return null;
  }
}

function instrumentedDaytonaSDK(real) {
  return {
    ...real,
    Daytona: class LoomDaytona {
      constructor(options = {}) {
        this.__loomOptions = options || {};
        this.__realDaytona = new real.Daytona(options);
      }

      async create(params = {}, options = {}) {
        const sandbox = await this.__realDaytona.create(params || {}, options || {});
        return registerDaytonaSandbox(sandbox, this.__loomOptions, params || {}, options || {});
      }

      async get(id, ...args) {
        const sandbox = await this.__realDaytona.get(id, ...args);
        return registerDaytonaSandbox(sandbox, this.__loomOptions, { id }, {});
      }

      async delete(sandbox, timeout) {
        const result = await this.__realDaytona.delete(sandbox, timeout);
        recordDaytonaLifecycle("delete", sandbox, { timeout });
        return result;
      }

      async [Symbol.asyncDispose]() {
        if (typeof this.__realDaytona[Symbol.asyncDispose] === "function") {
          return this.__realDaytona[Symbol.asyncDispose]();
        }
      }
    },
    Image: real.Image || daytonaSDKShim.Image,
  };
}

function registerDaytonaSandbox(sandbox, clientOptions = {}, createOptions = {}, sdkOptions = {}) {
  if (!sandbox || typeof sandbox !== "object") return sandbox;
  const sandboxId = daytonaSandboxId(sandbox, createOptions.id || createOptions.name || "");
  const recordedCreateOptions = daytonaRecordableCreateParams(createOptions);
  const loomData = compactObject({
    sandbox_id: sandboxId,
    sandboxId,
    snapshot: firstPresent(createOptions.snapshot, createOptions.snapshotName, createOptions.snapshot_name, sandbox.snapshot),
    target: firstPresent(createOptions.target, clientOptions.target, sandbox.target),
    cwd: firstPresent(createOptions.cwd, createOptions.workdir, createOptions.workspaceRoot, sandbox.cwd, sandbox.root),
    api_key_configured: Boolean(clientOptions.apiKey || clientOptions.api_key),
    api_key_env: firstPresent(clientOptions.apiKeyEnv, clientOptions.api_key_env, createOptions.apiKeyEnv, createOptions.api_key_env),
  });
  if (sandboxId) daytonaSandboxRegistry.set(sandboxId, sandbox);
  try {
    Object.defineProperty(sandbox, "__loomDaytona", {
      value: loomData,
      enumerable: false,
      configurable: true,
    });
  } catch {
    // Some SDK objects may be non-extensible; the registry still carries them.
  }
  if (activeWorkflowExecution) {
    activeWorkflowExecution.operations.push({
      type: "runtime.daytona.sandbox.create",
      params: {
        accepted: true,
        status: "completed",
        provider: "daytona",
        sandboxId,
        sandbox_id: sandboxId,
        snapshot: String(loomData.snapshot || ""),
        target: String(loomData.target || ""),
        cwd: String(loomData.cwd || ""),
        apiKeyConfigured: Boolean(clientOptions.apiKey || clientOptions.api_key),
        api_key_configured: Boolean(clientOptions.apiKey || clientOptions.api_key),
        client: daytonaClientMetadata(clientOptions),
        options: redactSensitiveObject(recordedCreateOptions),
        sdkOptions: redactSensitiveObject(sdkOptions),
        createdAt: new Date().toISOString(),
        realSDK: true,
      },
    });
  }
  return sandbox;
}

function daytonaSandboxId(sandbox, fallback = "") {
  if (!sandbox || typeof sandbox !== "object") return String(fallback || "");
  const loomData = sandbox.__loomDaytona && typeof sandbox.__loomDaytona === "object" ? sandbox.__loomDaytona : {};
  return String(
    firstPresent(
      loomData.sandboxId,
      loomData.sandbox_id,
      sandbox.id,
      sandbox.sandboxId,
      sandbox.sandbox_id,
      sandbox.workspaceId,
      sandbox.workspace_id,
      fallback,
    ) || "",
  );
}

function recordDaytonaLifecycle(action, sandbox, options = {}) {
  if (!activeWorkflowExecution) return;
  daytonaSandboxLifecycle(action, {
    sandboxId: daytonaSandboxId(sandbox),
  }, options || {});
}

async function materializeDaytonaWorkspace(request, workspace, runtimeWorkspace, lifecycleParams, options = {}, operations) {
  const provider = String(runtimeWorkspace && runtimeWorkspace.provider || "").toLowerCase();
  if (provider !== "daytona") return null;
  const sdk = daytonaSDKModuleFor(String(request.sourcePath || ""));
  const daytonaConfig = runtimeWorkspace.daytona && typeof runtimeWorkspace.daytona === "object" ? runtimeWorkspace.daytona : {};
  const apiKeyEnv = String(firstPresent(options.apiKeyEnv, options.api_key_env, daytonaConfig.api_key_env, daytonaConfig.apiKeyEnv, "DAYTONA_API_KEY") || "");
  const clientOptions = compactObject({
    apiKey: apiKeyEnv && request.env ? request.env[apiKeyEnv] : undefined,
    apiUrl: firstPresent(options.apiUrl, options.api_url, daytonaConfig.api_url, daytonaConfig.apiUrl),
    target: firstPresent(options.target, daytonaConfig.target),
    apiKeyEnv,
  });
  const createParams = daytonaCreateParams(runtimeWorkspace, daytonaConfig, request.env || {}, options || {});
  const createOptions = compactObject({
    timeout: firstPresent(options.timeout, options.createTimeout, options.create_timeout, daytonaConfig.create_timeout, daytonaConfig.timeout),
    onSnapshotCreateLogs: daytonaSnapshotLogHandler(firstPresent(options.onSnapshotCreateLogs, options.on_snapshot_create_logs, options.buildLogs, options.build_logs, daytonaConfig.buildLogs, daytonaConfig.build_logs)),
  });
  const sdkCreateParams = daytonaCreateParamsForSDK(createParams, sdk);
  let sandbox;
  try {
    const client = new sdk.Daytona(clientOptions);
    sandbox = await client.create(sdkCreateParams, createOptions);
  } catch (err) {
    const params = {
      ...lifecycleParams,
      providerBacked: false,
      materialized: false,
      realSDK: Boolean(sdk.__loomRealSDK),
      status: "failed",
      errorMessage: err && err.message ? String(err.message) : String(err),
      daytona: redactSensitiveObject(daytonaConfig),
    };
    operations.push({ type: "runtime.workspace.materialize", params });
    return { accepted: false, status: "failed", ...jsonSafe(params) };
  }
  const sandboxId = daytonaSandboxId(sandbox);
  const realSDK = Boolean(sdk.__loomRealSDK);
  runtimeWorkspace.providerWorkspaceId = sandboxId;
  runtimeWorkspace.provider_workspace_id = sandboxId;
  runtimeWorkspace.daytona = compactObject({
    ...daytonaConfig,
    sandbox_id: sandboxId,
    sandboxId,
    snapshot: firstPresent(daytonaConfig.snapshot, createParams.snapshot),
    target: firstPresent(daytonaConfig.target, createParams.target),
    api_key_env: apiKeyEnv,
  });
  workspace.runtime = runtimeWorkspace;
  const params = {
    ...lifecycleParams,
    providerBacked: realSDK,
    materialized: realSDK,
    realSDK,
    providerWorkspaceId: sandboxId,
    provider_workspace_id: sandboxId,
    daytona: redactSensitiveObject(runtimeWorkspace.daytona),
    createParams: redactSensitiveObject(createParams),
  };
  operations.push({ type: "runtime.workspace.materialize", params });
  return { accepted: true, status: "admitted", ...jsonSafe(params) };
}

function daytonaCreateParams(runtimeWorkspace, daytonaConfig, env, options = {}) {
  const envVars = {
    ...(daytonaConfig.env_vars && typeof daytonaConfig.env_vars === "object" ? stringRecord(daytonaConfig.env_vars) : {}),
    ...runtimeEnvBindings(runtimeWorkspace.env || [], env || {}),
    ...(options.envVars && typeof options.envVars === "object" ? stringRecord(options.envVars) : {}),
    ...(options.env_vars && typeof options.env_vars === "object" ? stringRecord(options.env_vars) : {}),
  };
  return compactObject({
    language: firstPresent(options.language, daytonaConfig.language),
    image: firstPresent(options.image, daytonaConfig.image),
    snapshot: firstPresent(options.snapshot, daytonaConfig.snapshot),
    resources: firstPresent(options.resources, daytonaConfig.resources),
    envVars,
    autoStopInterval: firstPresent(options.autoStopInterval, options.auto_stop_interval, daytonaConfig.auto_stop_interval),
    autoArchiveInterval: firstPresent(options.autoArchiveInterval, options.auto_archive_interval, daytonaConfig.auto_archive_interval),
    autoDeleteInterval: firstPresent(options.autoDeleteInterval, options.auto_delete_interval, daytonaConfig.auto_delete_interval),
    ephemeral: firstPresent(options.ephemeral, daytonaConfig.ephemeral),
    target: firstPresent(options.target, daytonaConfig.target),
    cwd: firstPresent(options.cwd, runtimeWorkspace.cwd),
  });
}

function daytonaCreateParamsForSDK(params, sdk) {
  const out = { ...(params || {}) };
  if (out.image !== undefined) {
    out.image = daytonaImageForSDK(out.image, sdk);
  }
  return out;
}

function daytonaImageForSDK(image, sdk) {
  if (image == null || typeof image === "string") return image;
  if (typeof image !== "object") return image;
  if (typeof image.runCommands === "function" || typeof image.pipInstall === "function" || typeof image.workdir === "function") {
    return image;
  }
  if (!sdk || !sdk.Image) return image;

  let built;
  const metadata = daytonaImageMetadata(image);
  const base = String(metadata.base || "");
  const dockerfile = String(metadata.dockerfile || "");
  if (base && /^debian-slim:/i.test(base) && typeof sdk.Image.debianSlim === "function") {
    built = sdk.Image.debianSlim(base.replace(/^debian-slim:/i, ""));
  } else if (base && typeof sdk.Image.base === "function") {
    built = sdk.Image.base(base);
  } else if (dockerfile && typeof sdk.Image.fromDockerfile === "function") {
    built = sdk.Image.fromDockerfile(dockerfile);
  }
  if (!built) return image;

  for (const step of Array.isArray(metadata.steps) ? metadata.steps : []) {
    built = applyDaytonaImageStep(built, step);
  }
  attachDaytonaImageMetadata(built, metadata);
  return built;
}

function applyDaytonaImageStep(image, step = {}) {
  const op = String(step.op || "").trim();
  if (!op) return image;
  switch (op) {
    case "runCommands":
      return callDaytonaImageMethod(image, "runCommands", flattenStringArgs(step.commands || []));
    case "dockerfileCommands":
      return callDaytonaImageMethod(image, "dockerfileCommands", [stringArray(step.commands || []), stringValue(step.contextDir)]);
    case "pipInstall": {
      const packages = stringArray(step.packages || []);
      const packageArg = packages.length === 1 ? packages[0] : packages;
      return callDaytonaImageMethod(image, "pipInstall", [packageArg, compactObject(step.options || {})]);
    }
    case "pipInstallFromRequirements":
      return callDaytonaImageMethod(image, "pipInstallFromRequirements", [stringValue(step.file), compactObject(step.options || {})]);
    case "pipInstallFromPyproject":
      return callDaytonaImageMethod(image, "pipInstallFromPyproject", [stringValue(step.file), compactObject(step.options || {})]);
    case "addLocalFile":
      return callDaytonaImageMethod(image, "addLocalFile", [stringValue(step.source), stringValue(step.target)]);
    case "addLocalDir":
      return callDaytonaImageMethod(image, "addLocalDir", [stringValue(step.source), stringValue(step.target)]);
    case "env":
      return callDaytonaImageMethod(image, "env", [stringRecord(step.vars || {})]);
    case "workdir":
      return callDaytonaImageMethod(image, "workdir", [stringValue(step.dir)]);
    case "entrypoint":
      return callDaytonaImageMethod(image, "entrypoint", [Array.isArray(step.value) ? stringArray(step.value) : [stringValue(step.value)]]);
    case "cmd":
      return callDaytonaImageMethod(image, "cmd", [Array.isArray(step.value) ? stringArray(step.value) : [stringValue(step.value)]]);
    case "user":
      return callDaytonaImageMethod(image, "user", [stringValue(step.value)]);
    default:
      return image;
  }
}

function callDaytonaImageMethod(image, method, args = []) {
  if (!image || typeof image[method] !== "function") return image;
  const result = image[method](...args.filter((arg) => arg !== undefined && arg !== ""));
  return result || image;
}

function attachDaytonaImageMetadata(image, metadata) {
  if (!image || typeof image !== "object") return;
  try {
    Object.defineProperty(image, "__loomDaytonaImage", {
      value: jsonSafe(metadata),
      enumerable: false,
      configurable: true,
    });
  } catch {
    // SDK images may be non-extensible; command execution still receives the SDK image object.
  }
}

function daytonaRecordableCreateParams(params = {}) {
  const out = { ...(params || {}) };
  if (out.image !== undefined) {
    out.image = daytonaImageMetadata(out.image);
  }
  return out;
}

function daytonaImageMetadata(image) {
  if (image == null || typeof image === "string") return image;
  if (image && typeof image === "object" && image.__loomDaytonaImage) return image.__loomDaytonaImage;
  if (!image || typeof image !== "object") return image;
  if (image.__loomType === "daytona_image" || image.base || image.dockerfile || Array.isArray(image.steps)) {
    return jsonSafe({
      ...(image.base ? { base: image.base } : {}),
      ...(image.dockerfile ? { dockerfile: image.dockerfile } : {}),
      ...(Array.isArray(image.steps) ? { steps: image.steps } : {}),
    });
  }
  try {
    if (typeof image.dockerfile === "string" && image.dockerfile) {
      return { dockerfile: image.dockerfile };
    }
  } catch {
    // Some SDK image getters can throw before the image is fully initialized.
  }
  return { type: "sdk-image" };
}

function daytonaSnapshotLogHandler(value) {
  if (typeof value === "function") return value;
  const mode = String(value || "").trim().toLowerCase();
  if (mode === "inherit" || mode === "stdout" || mode === "console") {
    return (chunk) => process.stdout.write(String(chunk));
  }
  return undefined;
}

function runtimeEnvBindings(names, env) {
  const out = {};
  for (const name of Array.isArray(names) ? names : []) {
    const key = String(name || "").trim();
    if (!key || env[key] == null) continue;
    out[key] = String(env[key]);
  }
  return out;
}

function transformSource(src, file) {
  return stripTypeScriptSyntax(src, file)
    .replace(/import\s+type\s+[^;]+;?/g, "")
    .replace(importStatementRegex(), "")
    .replace(/export\s+type\s+[^;]+;?/g, "")
    .replace(/export\s+interface\s+[A-Za-z0-9_]+\s*\{[^}]*\}/gs, "")
    .replace(/export\s+default\s+/, "module.exports.default = ")
    .replace(/export\s+const\s+([A-Za-z0-9_]+)\s*=/g, "const $1 = module.exports.$1 =")
    .replace(/export\s+async\s+function\s+([A-Za-z0-9_]+)\s*\(/g, "module.exports.$1 = async function $1(")
    .replace(/export\s+function\s+([A-Za-z0-9_]+)\s*\(/g, "module.exports.$1 = function $1(");
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
    .replace(/\((\s*\{[^)]*\})\s*:\s*[^)]+?\)/g, "($1)")
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
    daytona,
    schema,
    Type,
  };
  const record = { source: src, value: undefined, exports: module.exports };
  moduleCache.set(file, record);
  vm.runInNewContext(transformSource(src, file), sandbox, { filename: file });
  record.value = module.exports.default || implicitWorkflowFromExports(file, src, module.exports);
  record.exports = module.exports;
  return record;
}

function implicitWorkflowFromExports(file, source, exports) {
  if (!exports || typeof exports.run !== "function") return undefined;
  return defineWorkflow({
    name: String(exports.name || path.basename(file, path.extname(file)) || "workflow"),
    ...(exports.route ? { route: exports.route } : {}),
    ...(exports.runtimeProfile ? { runtimeProfile: exports.runtimeProfile } : {}),
    ...(exports.runtime_profile ? { runtime_profile: exports.runtime_profile } : {}),
    env: implicitWorkflowEnv(source, exports),
    run: exports.run,
  });
}

function implicitWorkflowEnv(source, exports) {
  const explicit = stringArray(exports.env);
  if (Object.prototype.hasOwnProperty.call(exports, "env")) return explicit;
  if (/@daytona\/sdk/.test(String(source || ""))) return ["DAYTONA_API_KEY"];
  return [];
}

function importedBindings(file, src) {
  const out = {};
  const re = /import\s+(?!type)([^'";]+?)\s+from\s+['"]([^'"]+)['"](?:\s+(?:with|assert)\s+\{[^}]*\})?\s*;?/g;
  for (const match of src.matchAll(re)) {
    const clause = match[1].trim();
    const spec = match[2].trim();
    if (!spec.startsWith(".")) {
      if (spec === "@loom/runtime" || spec === "@loom/sdk" || spec === "@flue/runtime") {
        const loomRuntime = loomRuntimeModule();
        bindImportClause(out, clause, { value: loomRuntime, exports: loomRuntime });
      }
      if (spec === "@daytona/sdk") {
        const sdk = daytonaSDKModuleFor(file);
        bindImportClause(out, clause, { value: sdk, exports: sdk });
      }
      continue;
    }
    const dep = evaluateModule(resolveRelativeImport(file, spec));
    bindImportClause(out, clause, dep);
  }
  return out;
}

function loomRuntimeModule() {
  return {
    Type,
    createAgent,
    daytona,
    defineAgent,
    defineAgentProfile,
    defineTool,
    defineWorkflow,
    runtime,
    schema,
    trigger,
  };
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
    if (item === "type" || item.startsWith("type ")) continue;
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

function makeRuntimeWorkspaceFiles(request, operations, workspace) {
  const files = new Map();
  const writtenPaths = new Set();
  const checksum = (text) => `sha256:${crypto.createHash("sha256").update(text).digest("hex")}`;
  const runtimeWorkspace = workspace && workspace.runtime && typeof workspace.runtime === "object" ? workspace.runtime : {};
  const providerWorkspaceId = String(runtimeWorkspace.providerWorkspaceId || request.id || "runtime-workspace").replace(/[^A-Za-z0-9_.:-]/g, "_");
  const runtimeRoot = String(request.runtimeWorkspaceRoot || "");
  const uriFor = (rel) => `runtime-workspace://${encodeURIComponent(providerWorkspaceId)}/${rel}`;
  const commonParams = (rel, text, options = {}) => ({
    path: rel,
    uri: uriFor(rel),
    type: options.type || options.artifactType || options.artifact_type || "runtime_workspace_file",
    summary: options.summary,
    mimeType: options.mimeType || options.mime_type || "text/plain; charset=utf-8",
    sizeBytes: Buffer.byteLength(text, "utf8"),
    checksum: checksum(text),
    runtimeProfileName: String(runtimeWorkspace.profileName || ""),
    provider: String(runtimeWorkspace.provider || ""),
    providerWorkspaceId,
    providerBacked: Boolean(runtimeRoot),
    filesystem: jsonSafe(runtimeWorkspace.filesystem || {}),
    metadata: {
      ...(options.metadata || {}),
      path: rel,
      source: "runtime_workspace_filesystem",
      provider_backed: String(Boolean(runtimeRoot)),
    },
    visibility: "runtime_workspace",
  });
  const writeText = async (relativePath, content, options = {}) => {
    const rel = safeRelativePath(relativePath);
    const text = String(content ?? "");
    files.set(rel, text);
    writtenPaths.add(rel);
    if (runtimeRoot) {
      const abs = path.join(runtimeRoot, rel);
      fs.mkdirSync(path.dirname(abs), { recursive: true });
      fs.writeFileSync(abs, text, "utf8");
    }
    const params = commonParams(rel, text, options);
    operations.push({ type: "runtime.workspace.files.write", params });
    return { accepted: true, ...params };
  };
  const readText = async (relativePath) => {
    const rel = safeRelativePath(relativePath);
    let text;
    if (runtimeRoot) {
      const abs = path.join(runtimeRoot, rel);
      if (fs.existsSync(abs) && fs.statSync(abs).isFile()) {
        text = fs.readFileSync(abs, "utf8");
      }
    }
    if (text === undefined) {
      if (!files.has(rel)) throw new Error(`runtime workspace file not found: ${rel}`);
      text = files.get(rel);
    }
    operations.push({
      type: "runtime.workspace.files.read",
      params: commonParams(rel, text, { type: "runtime_workspace_file" }),
    });
    return text;
  };
  const cleanupWrittenFiles = (options = {}) => {
    if (!runtimeRoot) {
      return {
        cleanupEnforced: false,
        cleanupScope: "admitted_only",
        cleanedFiles: 0,
        providerBacked: false,
        reconcileRequested: runtimeWorkspaceCleanupReconcileRequested(options, runtimeWorkspace),
        reconciled: false,
      };
    }
    const reconcileRequested = runtimeWorkspaceCleanupReconcileRequested(options, runtimeWorkspace);
    const remoteProviderBacked = runtimeWorkspaceHasRemoteProviderBackedRoot(runtimeRoot, runtimeWorkspace);
    const reconcile = reconcileRequested && remoteProviderBacked;
    const cleanupPaths = reconcile ? listRuntimeWorkspaceFilePaths(runtimeRoot) : Array.from(writtenPaths);
    let cleanedFiles = 0;
    for (const rel of cleanupPaths) {
      const abs = path.join(runtimeRoot, rel);
      if (!path.resolve(abs).startsWith(path.resolve(runtimeRoot) + path.sep)) {
        throw new Error(`runtime workspace cleanup path escapes root: ${rel}`);
      }
      if (!fs.existsSync(abs)) continue;
      const stat = fs.lstatSync(abs);
      if (!stat.isFile() && !stat.isSymbolicLink()) continue;
      fs.unlinkSync(abs);
      removeEmptyParents(abs, runtimeRoot);
      cleanedFiles += 1;
    }
    return {
      cleanupEnforced: true,
      cleanupScope: reconcile ? "runtime_workspace_reconcile" : "current_run_runtime_files",
      cleanedFiles,
      providerBacked: true,
      reconcileRequested,
      reconciled: reconcile,
    };
  };
  return {
    writeText,
    readText,
    [cleanupRuntimeWorkspaceFiles]: cleanupWrittenFiles,
    writeJSON: (relativePath, value, options = {}) =>
      writeText(relativePath, JSON.stringify(value ?? null, null, 2), {
        mimeType: "application/json",
        ...options,
      }),
    readJSON: async (relativePath) => JSON.parse(await readText(relativePath)),
  };
}

function runtimeWorkspaceCleanupReconcileRequested(options = {}, runtimeWorkspace = {}) {
  const cleanup =
    options.cleanup && typeof options.cleanup === "object"
      ? options.cleanup
      : runtimeWorkspace.cleanup && typeof runtimeWorkspace.cleanup === "object"
        ? runtimeWorkspace.cleanup
        : {};
  const scope = String(
    options.scope ||
      options.cleanupScope ||
      options.cleanup_scope ||
      cleanup.scope ||
      cleanup.cleanupScope ||
      cleanup.cleanup_scope ||
      "",
  ).toLowerCase();
  return (
    options.reconcile === true ||
    options.cleanupReconcile === true ||
    options.cleanup_reconcile === true ||
    cleanup.reconcile === true ||
    scope === "runtime_workspace" ||
    scope === "provider_workspace" ||
    scope === "workspace" ||
    scope === "all"
  );
}

function runtimeWorkspaceHasRemoteProviderBackedRoot(runtimeRoot, runtimeWorkspace = {}) {
  const provider = String(runtimeWorkspace.provider || "").toLowerCase();
  return Boolean(runtimeRoot) && provider !== "" && provider !== "local";
}

function listRuntimeWorkspaceFilePaths(root) {
  const resolvedRoot = path.resolve(root);
  if (!fs.existsSync(resolvedRoot)) return [];
  const out = [];
  const walk = (dir) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const abs = path.join(dir, entry.name);
      const resolved = path.resolve(abs);
      if (resolved !== resolvedRoot && !resolved.startsWith(resolvedRoot + path.sep)) {
        throw new Error(`runtime workspace cleanup path escapes root: ${abs}`);
      }
      if (entry.isDirectory()) {
        walk(abs);
        continue;
      }
      if (entry.isFile() || entry.isSymbolicLink()) {
        out.push(path.relative(resolvedRoot, resolved).split(path.sep).join("/"));
      }
    }
  };
  walk(resolvedRoot);
  return out.sort();
}

function removeEmptyParents(abs, root) {
  const resolvedRoot = path.resolve(root);
  let dir = path.dirname(path.resolve(abs));
  while (dir.startsWith(resolvedRoot + path.sep) && dir !== resolvedRoot) {
    try {
      fs.rmdirSync(dir);
    } catch {
      return;
    }
    dir = path.dirname(dir);
  }
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
  const runtimeProfile = request.runtimeProfile && typeof request.runtimeProfile === "object" ? request.runtimeProfile : null;
  const workspace =
    request.workspace && typeof request.workspace === "object"
      ? request.workspace
      : {
          key: String(requestContext.workspaceKey || ""),
          workflow: {
            name: String(requestContext.workflowName || ""),
            version: String(requestContext.workflowVersion || ""),
          },
        };
  const taskRuns = Array.isArray(request.taskRuns) ? request.taskRuns : [];
  const taskClaims = Array.isArray(request.taskClaims) ? request.taskClaims : [];
  const workspaceSkills = Array.isArray(workspace.skills) ? workspace.skills : [];
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
  const runtimeFiles = makeRuntimeWorkspaceFiles(request, operations, workspace);
  const requestContext = request.request || {};
  const skillFilter = (options = {}) => {
    let out = workspaceSkills.slice();
    const name = String(options.name || "");
    if (name) out = out.filter((skill) => String(skill.name || "") === name);
    if (Array.isArray(options.names) && options.names.length > 0) {
      const names = new Set(options.names.map((item) => String(item || "")));
      out = out.filter((skill) => names.has(String(skill.name || "")));
    }
    const source = String(options.source || "");
    if (source) out = out.filter((skill) => String(skill.source || "") === source);
    const compatibility = String(options.compatibility || "");
    if (compatibility) {
      out = out.filter((skill) => String(skill.compatibility || "") === compatibility);
    }
    const limit = Number(options.limit);
    if (Number.isFinite(limit) && limit > 0) out = out.slice(0, limit);
    return out;
  };
  const listSkills = async (options = {}) => {
    const query = options || {};
    const matches = skillFilter(query);
    operations.push({
      type: "runtime.skills",
      params: {
        action: "list",
        source: String(query.source || "runtime_workspace"),
        count: matches.length,
        names: matches.map((skill) => String(skill.name || "")),
        query: jsonSafe(query),
      },
    });
    return jsonSafe(matches);
  };
  const getSkill = async (nameOrOptions = {}) => {
    const query =
      typeof nameOrOptions === "string"
        ? { name: nameOrOptions }
        : nameOrOptions || {};
    const match = skillFilter({ ...query, limit: 1 })[0] || null;
    operations.push({
      type: "runtime.skills",
      params: {
        action: "get",
        source: String(query.source || "runtime_workspace"),
        name: String(query.name || ""),
        found: Boolean(match),
        count: match ? 1 : 0,
        query: jsonSafe(query),
      },
    });
    return match ? jsonSafe(match) : null;
  };
  const runtimeWorkspacePolicy = () => {
    const runtimeWorkspace = workspace.runtime && typeof workspace.runtime === "object" ? workspace.runtime : {};
    return {
      runtimeWorkspace,
      providerWorkspaceId: String(runtimeWorkspace.providerWorkspaceId || ""),
      owner: String(runtimeWorkspace.owner || ""),
      cleanup: jsonSafe(runtimeWorkspace.cleanup || {}),
      filesystem: jsonSafe(runtimeWorkspace.filesystem || {}),
    };
  };
  const runtimeWorkspaceLifecycleParams = (action, options = {}) => {
    const policy = runtimeWorkspacePolicy();
    return {
      action,
      runtimeProfileName: String(policy.runtimeWorkspace.profileName || ""),
      provider: String(policy.runtimeWorkspace.provider || ""),
      providerWorkspaceId: String(options.providerWorkspaceId || options.provider_workspace_id || policy.providerWorkspaceId),
      owner: String(options.owner || policy.owner),
      cleanup: jsonSafe(options.cleanup || policy.cleanup || {}),
      filesystem: jsonSafe(options.filesystem || policy.filesystem || {}),
      reason: String(options.reason || ""),
      idempotencyKey: String(options.idempotencyKey || options.idempotency_key || `${action}:${request.id}`),
      metadata: jsonSafe(options.metadata || {}),
      requestedAt: new Date().toISOString(),
    };
  };
  const materializeWorkspace = async (options = {}) => {
    const params = runtimeWorkspaceLifecycleParams("materialize", options || {});
    const daytonaReceipt = await materializeDaytonaWorkspace(request, workspace, runtimeWorkspacePolicy().runtimeWorkspace, params, options, operations);
    if (daytonaReceipt) return daytonaReceipt;
    const providerBacked = Boolean(request.runtimeWorkspaceRoot);
    if (providerBacked) {
      fs.mkdirSync(String(request.runtimeWorkspaceRoot), { recursive: true });
    }
    params.providerBacked = providerBacked;
    params.materialized = providerBacked;
    operations.push({ type: "runtime.workspace.materialize", params });
    return { accepted: true, status: "admitted", ...jsonSafe(params) };
  };
  const cleanupWorkspace = async (reasonOrOptions = {}, metadata) => {
    const options =
      typeof reasonOrOptions === "string"
        ? { reason: reasonOrOptions, metadata: metadata || {} }
        : reasonOrOptions || {};
    const params = runtimeWorkspaceLifecycleParams("cleanup", options);
    Object.assign(params, runtimeFiles[cleanupRuntimeWorkspaceFiles](options));
    operations.push({ type: "runtime.workspace.cleanup", params });
    return { accepted: true, status: "admitted", ...jsonSafe(params) };
  };
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
  const profileNameFor = (agent) => {
    if (!agent || typeof agent !== "object") return "";
    return String(agent.profileName || agent.profile_name || (isAgentProfile(agent) ? agent.name : "") || "");
  };
  const runtimeReferenceFor = (agent, options = {}) => {
    const value = firstPresent(options.runtime, options.runtimeProfile, options.runtime_profile, agent && agent.runtime, agent && agent.sandbox, runtimeProfile);
    if (!value) return null;
    if (typeof value === "string") return { profileName: String(value) };
    if (typeof value !== "object") return null;
    const workspaceRuntime =
      workspace && workspace.runtime && typeof workspace.runtime === "object"
        ? workspace.runtime
        : {};
    return jsonSafe({
      profileName: firstPresent(value.profileName, value.profile_name, value.name, workspaceRuntime.profileName),
      provider: firstPresent(value.provider, workspaceRuntime.provider),
      version: firstPresent(value.version, workspaceRuntime.version),
      image: firstPresent(value.image),
      repos: firstPresent(value.repos, workspaceRuntime.repos),
      env: firstPresent(value.env, workspaceRuntime.env),
      cwd: firstPresent(value.cwd, value.cwdPath, agent && agent.cwd),
      workspace: firstPresent(value.workspace, workspaceRuntime),
      daytona: firstPresent(value.daytona, workspaceRuntime.daytona),
    });
  };
  const runtimeMetadataFor = (runtimeRef) => {
    if (!runtimeRef || typeof runtimeRef !== "object") return {};
    const daytona = runtimeRef.daytona && typeof runtimeRef.daytona === "object" ? runtimeRef.daytona : {};
    return {
      ...(runtimeRef.profileName ? { runtime_profile_name: String(runtimeRef.profileName) } : {}),
      ...(runtimeRef.provider ? { runtime_provider: String(runtimeRef.provider) } : {}),
      ...(runtimeRef.version ? { runtime_profile_version: String(runtimeRef.version) } : {}),
      ...(daytona.snapshot ? { daytona_snapshot: String(daytona.snapshot) } : {}),
      ...(daytona.target ? { daytona_target: String(daytona.target) } : {}),
      ...(daytona.api_key_env ? { daytona_api_key_env: String(daytona.api_key_env) } : {}),
      ...(daytona.sandbox_id ? { daytona_sandbox_id: String(daytona.sandbox_id) } : {}),
    };
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
    workspace: jsonSafe(workspace),
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
    runtime: {
      workspace: async () => {
        const runtimeWorkspace = workspace.runtime && typeof workspace.runtime === "object" ? workspace.runtime : {};
        const repos = Array.isArray(workspace.repos) ? workspace.repos : [];
        const params = {
          key: String(workspace.key || requestContext.workspaceKey || ""),
          name: String(workspace.name || ""),
          state: String(workspace.state || ""),
          defaultBranch: String(workspace.defaultBranch || ""),
          workflowName: String((workspace.workflow && workspace.workflow.name) || requestContext.workflowName || ""),
          workflowVersion: String((workspace.workflow && workspace.workflow.version) || requestContext.workflowVersion || ""),
          runtimeProfileName: String(runtimeWorkspace.profileName || ""),
          provider: String(runtimeWorkspace.provider || ""),
          providerWorkspaceId: String(runtimeWorkspace.providerWorkspaceId || ""),
          cwd: String(runtimeWorkspace.cwd || ""),
          owner: String(runtimeWorkspace.owner || ""),
          daytona: jsonSafe(runtimeWorkspace.daytona || {}),
          cleanup: jsonSafe(runtimeWorkspace.cleanup || {}),
          filesystem: jsonSafe(runtimeWorkspace.filesystem || {}),
          capabilities: jsonSafe(runtimeWorkspace.capabilities || {}),
          selectedRepos: jsonSafe(workspace.selectedRepos || runtimeWorkspace.repos || []),
          repoCount: repos.length,
          skillCount: workspaceSkills.length,
          skillNames: workspaceSkills.map((skill) => String(skill.name || "")),
          env: jsonSafe(workspace.env || []),
        };
        operations.push({ type: "runtime.workspace", params });
        return jsonSafe(workspace);
      },
      profile: async () => {
        operations.push({
          type: "runtime.profile",
          params: {
            found: Boolean(runtimeProfile),
            name: runtimeProfile ? String(runtimeProfile.name || "") : "",
            provider: runtimeProfile ? String(runtimeProfile.provider || "") : "",
            version: runtimeProfile ? String(runtimeProfile.version || "") : "",
            repos: runtimeProfile ? jsonSafe(runtimeProfile.repos || []) : [],
            env: runtimeProfile ? jsonSafe(runtimeProfile.env || []) : [],
            daytona: runtimeProfile ? jsonSafe(runtimeProfile.daytona || {}) : {},
            workspace: runtimeProfile ? jsonSafe(runtimeProfile.workspace || {}) : {},
            capabilities: runtimeProfile ? jsonSafe(runtimeProfile.capabilities || {}) : {},
          },
        });
        return runtimeProfile ? jsonSafe(runtimeProfile) : null;
      },
      skills: listSkills,
      materializeWorkspace,
      cleanupWorkspace,
      releaseWorkspace: cleanupWorkspace,
      workspaceLifecycle: {
        materialize: materializeWorkspace,
        cleanup: cleanupWorkspace,
        release: cleanupWorkspace,
      },
      files: runtimeFiles,
      filesystem: runtimeFiles,
    },
    skills: {
      list: listSkills,
      get: getSkill,
    },
    init: async (agent, options = {}) => {
      const materialized = materializeAgent(agent);
      const runtimeRef = runtimeReferenceFor(materialized, options);
      const profileName = String(
        options.profileName ||
          options.profile_name ||
          (runtimeRef && runtimeRef.profileName) ||
          profileNameFor(materialized),
      );
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
        profileName,
        model: materialized.model,
        backend: materialized.backend,
        runtime: runtimeRef || undefined,
        runtimeProfileName: runtimeRef && runtimeRef.profileName,
        runtimeProvider: runtimeRef && runtimeRef.provider,
        metadata: {
          ...(options.metadata || {}),
          ...runtimeMetadataFor(runtimeRef),
          ...(materialized.name ? { source_agent_name: String(materialized.name) } : {}),
          ...(profileName ? { source_agent_profile: profileName, profile_name: profileName } : {}),
        },
      };
      return {
        agentId,
        harness,
        profileName,
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
        const ensureParams = params && typeof params === "object" ? { ...params } : {};
        const workItemId = String(ensureParams.workItemId || ensureParams.work_item_id || ensureParams.id || "");
        const metadata =
          ensureParams.metadata && typeof ensureParams.metadata === "object"
            ? { ...jsonSafe(ensureParams.metadata) }
            : {};
        const item = findWorkItem(workItems, workItemId);
        const sourceRepo = String(
          ensureParams.sourceRepo ||
            ensureParams.source_repo ||
            ensureParams.repo ||
            metadata.source_repo ||
            metadata.sourceRepo ||
            metadata.repo ||
            (item && (item.source_repo || item.sourceRepo || item.repo)) ||
            "",
        );
        if (sourceRepo && !metadata.source_repo) metadata.source_repo = sourceRepo;
        if (Object.keys(metadata).length > 0) ensureParams.metadata = metadata;
        const op = { type: "taskRuns.ensure", params: ensureParams };
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
        const dispatchInput = input && typeof input === "object" ? input : {};
        const materialized = materializeAgent(agent);
        const agentId = String(
          dispatchInput.agentId ||
            dispatchInput.agent_id ||
            materialized.name ||
            materialized.id ||
            "",
        );
        const claim = findDispatchTaskClaim(taskClaims, agentId, dispatchInput);
        const workItemId = stringPresent(
          dispatchInput.workItemId,
          dispatchInput.work_item_id,
          claim && claim.work_item_id,
          claim && claim.workItemId,
        );
        const taskRunId = stringPresent(
          dispatchInput.taskRunId,
          dispatchInput.task_run_id,
          claim && claim.task_run_id,
          claim && claim.taskRunId,
        );
        const sessionId = stringPresent(
          dispatchInput.sessionId,
          dispatchInput.session_id,
          claim && claim.session_id,
          claim && claim.sessionId,
        );
        const sessionName = stringPresent(dispatchInput.sessionName, dispatchInput.session_name);
        const dispatchId = String(firstPresent(dispatchInput.dispatchId, dispatchInput.dispatch_id, dispatchOperationId(request, agentId, operations.length)));
        const operationId = String(firstPresent(dispatchInput.operationId, dispatchInput.operation_id, `op:${dispatchId}`));
        const admittedAt = new Date().toISOString();
        const op = {
          type: "agents.dispatch",
          params: {
            accepted: true,
            status: "admitted",
            dispatchId,
            operationId,
            agentId,
            sessionId,
            sessionName,
            taskRunId,
            taskId: workItemId,
            workItemId,
            model: firstPresent(dispatchInput.model, materialized.model),
            provider: firstPresent(dispatchInput.provider, materialized.provider, materialized.backend),
            providerModel: firstPresent(
              dispatchInput.providerModel,
              dispatchInput.provider_model,
              materialized.providerModel,
              materialized.provider_model,
            ),
            idempotencyKey: firstPresent(dispatchInput.idempotencyKey, dispatchInput.idempotency_key),
            metadata: Object.prototype.hasOwnProperty.call(dispatchInput, "metadata") ? jsonSafe(dispatchInput.metadata) : undefined,
            input: jsonSafe(dispatchInput),
            admittedAt,
            source: "workflow_context",
            correlation: jsonSafe({
              workflowRunId: request.id,
              agentId,
              dispatchId,
              operationId,
              taskRunId,
              workItemId,
              sessionId,
            }),
          },
        };
        operations.push(op);
        return {
          accepted: true,
          status: "admitted",
          dispatchId,
          operationId,
          agentId,
          sessionId,
          sessionName,
          taskRunId,
          taskId: workItemId,
          workItemId,
          model: op.params.model,
          provider: op.params.provider,
          providerModel: op.params.providerModel,
          idempotencyKey: op.params.idempotencyKey,
          metadata: op.params.metadata,
          input: op.params.input,
          admittedAt,
          correlation: op.params.correlation,
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
    const operationInput = input && typeof input === "object" ? input : {};
    const startedAt = new Date().toISOString();
    const operationId = sessionOperationId(session, operation, operations.length);
    const params = {
      operationId,
      agentId: session.agentId || session.agent_id,
      sessionId: session.sessionId || session.session_id || session.id,
      harness: session.harness,
      sessionName: session.sessionName || session.session_name,
      profileName: session.profileName || session.profile_name,
      taskId: session.taskId || session.task_id || session.workItemId || session.work_item_id,
      taskRunId: session.taskRunId || session.task_run_id,
      model: firstPresent(operationInput.model, session.model),
      provider: firstPresent(operationInput.provider, session.provider, session.backend, session.runtimeProvider, session.runtime_provider),
      providerModel: firstPresent(operationInput.providerModel, operationInput.provider_model, session.providerModel, session.provider_model),
      runtime: session.runtime,
      runtimeProfileName: session.runtimeProfileName || session.runtime_profile_name,
      runtimeProvider: session.runtimeProvider || session.runtime_provider,
      usage: Object.prototype.hasOwnProperty.call(operationInput, "usage") ? jsonSafe(operationInput.usage) : undefined,
      metadata: Object.prototype.hasOwnProperty.call(operationInput, "metadata") ? jsonSafe(operationInput.metadata) : undefined,
      toolCalls: Object.prototype.hasOwnProperty.call(operationInput, "toolCalls")
        ? jsonSafe(operationInput.toolCalls)
        : Object.prototype.hasOwnProperty.call(operationInput, "tool_calls")
          ? jsonSafe(operationInput.tool_calls)
          : undefined,
      operation,
      input: jsonSafe(operationInput),
      startedAt,
    };
    const result = (await providerSessionOperationResult(session, operation, operationInput, params, operations)) || sessionOperationResult(operation, operationInput, params);
    const completedAt = new Date().toISOString();
    params.completedAt = completedAt;
    params.durationMs = Date.parse(completedAt) - Date.parse(startedAt);
    params.status = String(result.status || "completed");
    if (result.providerExecution) params.providerExecution = jsonSafe(result.providerExecution);
    result.completedAt = completedAt;
    result.durationMs = params.durationMs;
    params.result = jsonSafe(result);
    operations.push({ type: "agents.session.operation", params });
    return result;
  };
  return {
    ...session,
    prompt: (input, options) => call("prompt", typeof input === "string" ? { ...(options || {}), prompt: input } : input),
    skill: (input) => call("skill", input),
    task: (input) => call("task", input),
    shell: (input, options) => call("shell", typeof input === "string" ? { ...(options || {}), command: input } : input),
    compact: (input) => call("compact", input),
  };
}

async function providerSessionOperationResult(session, operation, input, params, operations) {
  if (!["shell", "prompt"].includes(operation)) return null;
  const runtimeProvider = String(session.runtimeProvider || session.runtime_provider || (session.runtime && session.runtime.provider) || "");
  if (runtimeProvider !== "daytona") return null;
  const runtime = session.runtime && typeof session.runtime === "object" ? session.runtime : {};
  const daytonaData = runtime.daytona && typeof runtime.daytona === "object" ? runtime.daytona : {};
  const sandboxId = String(firstPresent(daytonaData.sandbox_id, daytonaData.sandboxId, runtime.workspace && runtime.workspace.providerWorkspaceId) || "");
  const sandbox = sandboxId ? daytonaSandboxRegistry.get(sandboxId) : null;
  if (!sandbox) {
    return sessionOperationResult(operation, {
      ...input,
      status: "result_unavailable",
      result_unavailable: {
        reason: "daytona sandbox is not available in this runner process",
        sandboxId,
      },
    }, params);
  }
  const promptSpec = operation === "prompt" ? daytonaPromptCommand(session, input || {}) : null;
  const command = operation === "prompt" ? promptSpec.command : String(firstPresent(input.command, input.cmd, input.prompt) || "").trim();
  if (!command) throw new Error(`Daytona-backed session.${operation} requires ${operation === "prompt" ? "prompt" : "command"}`);
  const commandEnv = { ...(promptSpec && promptSpec.env ? promptSpec.env : {}), ...(input.env || {}) };
  const redactions = sensitiveRedactionValues(
    activeWorkflowExecution && activeWorkflowExecution.request ? activeWorkflowExecution.request.env || {} : {},
    commandEnv,
  );
  const startedAt = new Date().toISOString();
  const response = await executeDaytonaCommand(sandbox, command, {
    ...(input || {}),
    cwd: firstPresent(input.cwd, input.workdir, promptSpec && promptSpec.cwd),
    env: commandEnv,
  });
  const completedAt = new Date().toISOString();
  const normalized = redactDaytonaCommandResult(normalizeDaytonaCommandResult(response), redactions);
  const redactedCommand = redactSensitiveText(command, redactions);
  params.input = redactSensitiveObject(params.input, redactions);
  if (params.metadata) params.metadata = redactSensitiveObject(params.metadata, redactions);
  if (params.toolCalls) params.toolCalls = redactSensitiveObject(params.toolCalls, redactions);
  const providerParams = {
    accepted: true,
    status: normalized.exitCode === 0 ? "completed" : "failed",
    provider: "daytona",
    sandboxId,
    sandbox_id: sandboxId,
    command: redactedCommand,
    backend: promptSpec && promptSpec.backend,
    promptHash: promptSpec && promptSpec.promptHash,
    cwd: String(firstPresent(input.cwd, input.workdir, promptSpec && promptSpec.cwd, runtime.cwd) || ""),
    env: redactSensitiveObject(input.env || {}, redactions),
    exitCode: normalized.exitCode,
    result: normalized.result,
    stdout: normalized.stdout,
    stderr: normalized.stderr,
    startedAt,
    completedAt,
    durationMs: Date.parse(completedAt) - Date.parse(startedAt),
    realExecution: Boolean(!sandbox.__loomType),
  };
  operations.push({ type: operation === "prompt" ? "runtime.daytona.agent.prompt" : "runtime.daytona.sandbox.shell", params: providerParams });
  return jsonSafe({
    accepted: providerParams.status === "completed",
    status: providerParams.status,
    operation,
    operationId: params.operationId,
    agentId: params.agentId,
    sessionId: params.sessionId,
    sessionName: params.sessionName,
    profileName: params.profileName,
    model: params.model,
    provider: params.provider,
    runtime: params.runtime,
    runtimeProfileName: params.runtimeProfileName,
    runtimeProvider: params.runtimeProvider,
    startedAt: params.startedAt,
    text: normalized.text,
    data: normalized.result,
    result: normalized.result,
    eventType: "agent_session_operation",
    providerExecution: {
      provider: "daytona",
      sandboxId,
      command: redactedCommand,
      backend: promptSpec && promptSpec.backend,
      exitCode: normalized.exitCode,
      realExecution: providerParams.realExecution,
    },
  });
}

async function executeDaytonaCommand(sandbox, command, options = {}) {
  const cwd = firstPresent(options.cwd, options.workdir);
  const env = options.env && typeof options.env === "object" ? stringRecord(options.env) : undefined;
  const timeout = firstPresent(options.timeout, options.timeoutSeconds, options.timeout_seconds, options.timeoutMs, options.timeout_ms);
  if (sandbox.process && typeof sandbox.process.executeCommand === "function") {
    try {
      return await sandbox.process.executeCommand(command, cwd, env, timeout);
    } catch (err) {
      if (!daytonaCommandObjectFallbackAllowed(err)) throw err;
      return sandbox.process.executeCommand(command, compactObject({ cwd, env, timeout }));
    }
  }
  if (sandbox.process && typeof sandbox.process.exec === "function") {
    return sandbox.process.exec(command, cwd, env, timeout);
  }
  if (typeof sandbox.shell === "function") {
    return sandbox.shell(command, compactObject({ cwd, env, timeout }));
  }
  throw new Error("Daytona sandbox does not expose process.executeCommand, process.exec, or shell");
}

function daytonaCommandObjectFallbackAllowed(err) {
  const message = err && err.message ? String(err.message) : String(err || "");
  return /argument|parameter|cwd|env|timeout|object/i.test(message);
}

function daytonaPromptCommand(session, input = {}) {
  const prompt = String(firstPresent(input.prompt, input.instruction, input.message, input.text) || "").trim();
  if (!prompt) return { command: "", backend: "", cwd: "", env: {} };
  const backend = normalizeBackendName(firstPresent(input.backend, input.provider, session.backend, session.provider, backendFromModel(firstPresent(input.model, session.model)), "codex"));
  const runtime = session.runtime && typeof session.runtime === "object" ? session.runtime : {};
  const cwd = String(firstPresent(input.cwd, input.workdir, runtime.cwd, "/workspace/project") || "/workspace/project");
  const env = compactObject({
    LOOM_AGENT_NAME: firstPresent(session.agentId, session.agent_id),
    LOOM_WORKTREE_PATH: cwd,
  });
  const explicit = firstPresent(input.command, input.promptCommand, input.prompt_command);
  const command = explicit ? renderPromptCommandTemplate(String(explicit), prompt, cwd, backend) : backendPromptCommand(backend, prompt, cwd);
  return {
    backend,
    command,
    cwd,
    env,
    promptHash: `sha256:${crypto.createHash("sha256").update(prompt).digest("hex")}`,
  };
}

function normalizeBackendName(value) {
  const raw = String(value || "").toLowerCase().trim();
  if (raw === "openai" || raw === "gpt" || raw === "gpt-5" || raw === "gpt-5.5") return "codex";
  if (raw === "anthropic" || raw === "claude-code") return "claude";
  if (raw === "open-code" || raw === "opencode-ai") return "opencode";
  return raw || "codex";
}

function backendFromModel(model) {
  const raw = String(model || "").toLowerCase();
  if (raw.startsWith("openai/") || raw.includes("gpt-")) return "codex";
  if (raw.startsWith("anthropic/") || raw.includes("claude")) return "claude";
  if (raw.includes("gemini")) return "gemini";
  return "";
}

function backendPromptCommand(backend, prompt, cwd) {
  const quotedPrompt = shellQuote(prompt);
  switch (backend) {
    case "claude":
      return `claude -p --verbose --output-format stream-json --dangerously-skip-permissions ${quotedPrompt}`;
    case "opencode":
      return `opencode run --format json --dir ${shellQuote(cwd)} --dangerously-skip-permissions ${quotedPrompt}`;
    case "gemini":
      return `gemini --approval-mode=yolo -p ${quotedPrompt} -o stream-json`;
    case "cursor":
      return `cursor -p --output-format stream-json --force ${quotedPrompt}`;
    case "codex":
    default:
      return `codex exec --json --dangerously-bypass-approvals-and-sandbox ${quotedPrompt}`;
  }
}

function renderPromptCommandTemplate(template, prompt, cwd, backend) {
  return template
    .replaceAll("{prompt}", shellQuote(prompt))
    .replaceAll("{rawPrompt}", prompt)
    .replaceAll("{cwd}", shellQuote(cwd))
    .replaceAll("{backend}", shellQuote(backend));
}

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'\\''`)}'`;
}

function normalizeDaytonaCommandResult(response) {
  const raw = response && typeof response === "object" ? response : { result: response };
  const result = firstPresent(raw.result, raw.output, raw.stdout, raw.text, response);
  const stdout = String(firstPresent(raw.stdout, raw.output, typeof result === "string" ? result : "") || "");
  const stderr = String(firstPresent(raw.stderr, raw.error, "") || "");
  const exitCode = Number(firstPresent(raw.exitCode, raw.exit_code, raw.code, 0) || 0);
  return {
    exitCode: Number.isFinite(exitCode) ? exitCode : 0,
    stdout,
    stderr,
    text: typeof result === "string" ? result : stdout || undefined,
    result: jsonSafe(raw),
  };
}

function redactDaytonaCommandResult(normalized, redactions = []) {
  return {
    ...normalized,
    stdout: redactSensitiveText(normalized.stdout || "", redactions),
    stderr: redactSensitiveText(normalized.stderr || "", redactions),
    text: normalized.text === undefined ? undefined : redactSensitiveText(String(normalized.text), redactions),
    result: redactSensitiveObject(normalized.result, redactions),
  };
}

function sessionOperationId(session, operation, index) {
  const sessionId = String(session.sessionId || session.session_id || session.id || session.sessionName || "session");
  return `op:${sessionId}:${operation}:${index + 1}`;
}

function dispatchOperationId(request, agentId, index) {
  const runId = String((request && request.id) || "workflow");
  const target = String(agentId || "agent");
  return `dispatch:${runId}:${target}:${index + 1}`;
}

function findDispatchTaskClaim(taskClaims, agentId, input) {
  const workItemId = stringPresent(input.workItemId, input.work_item_id);
  const taskRunId = stringPresent(input.taskRunId, input.task_run_id);
  const sessionId = stringPresent(input.sessionId, input.session_id);
  const matches = (taskClaims || []).filter((claim) => {
    if (!claim) return false;
    if (agentId && stringPresent(claim.agent_id, claim.agentId, claim.claim_actor, claim.claimActor) !== agentId) {
      return false;
    }
    if (workItemId && stringPresent(claim.work_item_id, claim.workItemId) !== workItemId) return false;
    if (taskRunId && stringPresent(claim.task_run_id, claim.taskRunId) !== taskRunId) return false;
    if (sessionId && stringPresent(claim.session_id, claim.sessionId) !== sessionId) return false;
    return Boolean(workItemId || taskRunId || sessionId || agentId);
  });
  return matches.find((claim) => Boolean(claim.active)) || matches[0] || null;
}

function sessionOperationResult(operation, input, params) {
  const status = sessionOperationStatus(input);
  const raw = sessionOperationRawResult(operation, input, params);
  const rawObject = raw && typeof raw === "object" && !Array.isArray(raw) ? raw : null;
  const envelope = {};
  if (rawObject) {
    Object.assign(envelope, jsonSafe(rawObject));
  }
  Object.assign(envelope, {
    accepted: status === "completed",
    status,
    operation,
    operationId: params.operationId,
    agentId: params.agentId,
    sessionId: params.sessionId,
    sessionName: params.sessionName,
    profileName: params.profileName,
    model: params.model,
    provider: params.provider,
    providerModel: params.providerModel,
    runtime: params.runtime,
    runtimeProfileName: params.runtimeProfileName,
    runtimeProvider: params.runtimeProvider,
    usage: params.usage,
    startedAt: params.startedAt,
    text: sessionOperationText(raw),
    data: jsonSafe(raw),
    result: jsonSafe(raw),
    eventType: "agent_session_operation",
  });
  if (Object.prototype.hasOwnProperty.call(input, "result")) {
    envelope.validation = {
      requested: true,
      status: "not_validated",
      reason: "constrained workflow runner records result schema requests but does not execute provider output validation",
    };
  }
  const toolCalls = sessionOperationToolCalls(input);
  if (toolCalls) {
    envelope.toolCalls = jsonSafe(toolCalls);
    envelope.tool_calls = jsonSafe(toolCalls);
  }
  const unavailable = sessionOperationUnavailable(input);
  if (unavailable) {
    envelope.resultUnavailable = unavailable;
    envelope.result_unavailable = unavailable;
  }
  const cancellation = sessionOperationCancellation(input);
  if (cancellation) {
    envelope.cancellation = cancellation;
  }
  const failure = sessionOperationFailure(input);
  if (failure) {
    envelope.failure = failure;
  }
  return jsonSafe(envelope);
}

function sessionOperationToolCalls(input) {
  if (!input) return null;
  if (Object.prototype.hasOwnProperty.call(input, "toolCalls")) return input.toolCalls;
  if (Object.prototype.hasOwnProperty.call(input, "tool_calls")) return input.tool_calls;
  return null;
}

function sessionOperationRawResult(operation, input, params) {
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
    profileName: params.profileName,
  };
}

function sessionOperationStatus(input) {
  const explicit = String((input && input.status) || "").toLowerCase();
  if (["completed", "failed", "cancelled", "result_unavailable"].includes(explicit)) return explicit;
  if (sessionOperationCancellation(input)) return "cancelled";
  if (sessionOperationUnavailable(input)) return "result_unavailable";
  if (sessionOperationFailure(input)) return "failed";
  return "completed";
}

function sessionOperationText(raw) {
  if (typeof raw === "string") return raw;
  if (!raw || typeof raw !== "object") return undefined;
  const text = raw.text || raw.summary || raw.message;
  return typeof text === "string" ? text : undefined;
}

function sessionOperationUnavailable(input) {
  if (!input) return null;
  const value = Object.prototype.hasOwnProperty.call(input, "resultUnavailable")
    ? input.resultUnavailable
    : Object.prototype.hasOwnProperty.call(input, "result_unavailable")
      ? input.result_unavailable
      : null;
  if (!value) return null;
  if (typeof value === "string") return { reason: value };
  if (typeof value === "object") return jsonSafe(value);
  return { reason: "operation result unavailable" };
}

function sessionOperationCancellation(input) {
  if (!input) return null;
  const value =
    input.cancellation ||
    input.cancelled ||
    input.cancelReason ||
    input.cancel_reason ||
    (String(input.status || "").toLowerCase() === "cancelled" ? "cancelled" : null);
  if (!value) return null;
  if (typeof value === "string") return { reason: value };
  if (typeof value === "object") return jsonSafe(value);
  return { reason: "operation cancelled" };
}

function sessionOperationFailure(input) {
  if (!input) return null;
  const value =
    input.failure ||
    input.error ||
    (String(input.status || "").toLowerCase() === "failed" ? "operation failed" : null);
  if (!value) return null;
  if (typeof value === "string") return { message: value };
  if (typeof value === "object") return jsonSafe(value);
  return { message: "operation failed" };
}

function firstPresent(...values) {
  for (const value of values) {
    if (value !== undefined && value !== null && value !== "") return value;
  }
  return undefined;
}

function stringValue(value) {
  return String(value ?? "").trim();
}

function stringArray(value) {
  if (!Array.isArray(value)) return [];
  return value.map((item) => stringValue(item)).filter(Boolean);
}

function flattenStringArgs(args) {
  return args.flatMap((arg) => (Array.isArray(arg) ? flattenStringArgs(arg) : [stringValue(arg)])).filter(Boolean);
}

function stringRecord(value = {}) {
  return Object.fromEntries(
    Object.entries(value || {})
      .filter(([, v]) => v != null)
      .map(([k, v]) => [k, String(v)]),
  );
}

function compactObject(value = {}) {
  return Object.fromEntries(Object.entries(value || {}).filter(([, v]) => v !== undefined && v !== null && v !== ""));
}

function daytonaClientMetadata(options = {}) {
  return compactObject({
    apiUrl: firstPresent(options.apiUrl, options.api_url),
    api_url: firstPresent(options.apiUrl, options.api_url),
    apiKeyConfigured: Boolean(options.apiKey || options.api_key),
    api_key_configured: Boolean(options.apiKey || options.api_key),
    apiKeyEnv: firstPresent(options.apiKeyEnv, options.api_key_env),
    api_key_env: firstPresent(options.apiKeyEnv, options.api_key_env),
    target: firstPresent(options.target),
  });
}

function sensitiveObjectKey(key) {
  return /api[_-]?key|token|secret|password|authorization|credential|auth/i.test(String(key));
}

function addSensitiveValue(out, value) {
  const text = String(value == null ? "" : value).trim();
  if (text.length >= 4 && !out.includes(text)) out.push(text);
}

function collectSensitiveValues(value, out, force = false) {
  if (Array.isArray(value)) {
    for (const item of value) collectSensitiveValues(item, out, force);
    return out;
  }
  if (value && typeof value === "object") {
    for (const [key, item] of Object.entries(value)) {
      const sensitive = force || sensitiveObjectKey(key);
      if (item && typeof item === "object") {
        collectSensitiveValues(item, out, sensitive);
      } else if (sensitive) {
        addSensitiveValue(out, item);
      }
    }
    return out;
  }
  if (force) addSensitiveValue(out, value);
  return out;
}

function sensitiveRedactionValues(...values) {
  const out = [];
  for (const value of values) collectSensitiveValues(value, out, false);
  return out.sort((a, b) => b.length - a.length);
}

function redactSensitiveText(value, redactions = []) {
  let text = String(value == null ? "" : value);
  for (const secret of redactions || []) {
    if (secret) text = text.split(secret).join("[redacted]");
  }
  return text;
}

function redactSensitiveObject(value, redactions = []) {
  if (typeof value === "string") return redactSensitiveText(value, redactions);
  if (Array.isArray(value)) return value.map((item) => redactSensitiveObject(item, redactions));
  if (!value || typeof value !== "object") return value;
  const out = {};
  for (const [key, item] of Object.entries(value)) {
    if (sensitiveObjectKey(key)) {
      out[key] = item == null || item === "" ? item : "[redacted]";
      continue;
    }
    out[key] = redactSensitiveObject(item, redactions);
  }
  return out;
}

function stringPresent(...values) {
  const value = firstPresent(...values);
  return value === undefined ? "" : String(value);
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
  const previousExecution = activeWorkflowExecution;
  activeWorkflowExecution = { request, operations };
  let result;
  try {
    result = await value.run(ctx);
  } finally {
    activeWorkflowExecution = previousExecution;
  }
  process.stdout.write(JSON.stringify({ result: result === undefined ? null : result, logs, operations }));
}

main().catch((error) => {
  process.stderr.write(error && error.stack ? error.stack : String(error));
  process.exit(1);
});
