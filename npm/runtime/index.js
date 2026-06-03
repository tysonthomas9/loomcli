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
  daytona(config = {}) {
    return defineRuntimeProfile({ provider: "daytona", ...config });
  },
  remote(config = {}) {
    return defineRuntimeProfile({ provider: config.provider || "e2b", ...config });
  },
};

export function daytona(sandbox, options = {}) {
  const sandboxData = sandbox && typeof sandbox === "object" ? sandbox : {};
  const loomData = sandboxData.__loomDaytona && typeof sandboxData.__loomDaytona === "object" ? sandboxData.__loomDaytona : {};
  const daytonaData = sandboxData.daytona && typeof sandboxData.daytona === "object" ? sandboxData.daytona : {};
  const optionDaytonaData = options.daytona && typeof options.daytona === "object" ? options.daytona : {};
  const sandboxId = String(
    options.sandboxId ||
      options.sandbox_id ||
      loomData.sandboxId ||
      loomData.sandbox_id ||
      sandboxData.sandboxId ||
      sandboxData.sandbox_id ||
      sandboxData.id ||
      sandboxData.workspaceId ||
      sandboxData.workspace_id ||
      "",
  );
  const profileName = String(options.profileName || options.profile_name || options.name || loomData.profileName || loomData.profile_name || sandboxData.profileName || sandboxData.profile_name || "");
  const cwd = String(options.cwd || loomData.cwd || sandboxData.cwd || sandboxData.root || "");
  const setupCommands = [
    ...(Array.isArray(options.setupCommands) ? options.setupCommands : Array.isArray(options.setup_commands) ? options.setup_commands : []),
    ...(Array.isArray(options.installCommands) ? options.installCommands : Array.isArray(options.install_commands) ? options.install_commands : []),
  ].map((item) => String(item).trim()).filter(Boolean);
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
    provider: "daytona",
    ...(profileName ? { profileName, profile_name: profileName, name: profileName } : {}),
    ...(cwd ? { cwd } : {}),
    ...(env.length > 0 ? { env } : {}),
    ...(repos.length > 0 ? { repos } : {}),
    workspace: {
      ...(options.workspace || {}),
      ...(sandboxId ? { providerWorkspaceId: sandboxId, provider_workspace_id: sandboxId } : {}),
      provider: "daytona",
    },
    daytona: {
      ...daytonaData,
      ...loomData,
      ...optionDaytonaData,
      ...(sandboxId ? { sandbox_id: sandboxId, sandboxId } : {}),
      ...(firstPresent(options.language, optionDaytonaData.language, daytonaData.language) ? { language: firstPresent(options.language, optionDaytonaData.language, daytonaData.language) } : {}),
      ...(firstPresent(options.image, optionDaytonaData.image, daytonaData.image) ? { image: firstPresent(options.image, optionDaytonaData.image, daytonaData.image) } : {}),
      ...(options.snapshot || loomData.snapshot || sandboxData.snapshot || daytonaData.snapshot ? { snapshot: options.snapshot || loomData.snapshot || sandboxData.snapshot || daytonaData.snapshot } : {}),
      ...(firstPresent(options.resources, optionDaytonaData.resources, daytonaData.resources) ? { resources: firstPresent(options.resources, optionDaytonaData.resources, daytonaData.resources) } : {}),
      ...(envVars ? { env_vars: stringRecord(envVars) } : {}),
      ...(firstPresent(options.autoStopInterval, options.auto_stop_interval, optionDaytonaData.autoStopInterval, optionDaytonaData.auto_stop_interval, daytonaData.autoStopInterval, daytonaData.auto_stop_interval) !== undefined
        ? { auto_stop_interval: firstPresent(options.autoStopInterval, options.auto_stop_interval, optionDaytonaData.autoStopInterval, optionDaytonaData.auto_stop_interval, daytonaData.autoStopInterval, daytonaData.auto_stop_interval) }
        : {}),
      ...(firstPresent(options.autoArchiveInterval, options.auto_archive_interval, optionDaytonaData.autoArchiveInterval, optionDaytonaData.auto_archive_interval, daytonaData.autoArchiveInterval, daytonaData.auto_archive_interval) !== undefined
        ? { auto_archive_interval: firstPresent(options.autoArchiveInterval, options.auto_archive_interval, optionDaytonaData.autoArchiveInterval, optionDaytonaData.auto_archive_interval, daytonaData.autoArchiveInterval, daytonaData.auto_archive_interval) }
        : {}),
      ...(firstPresent(options.autoDeleteInterval, options.auto_delete_interval, optionDaytonaData.autoDeleteInterval, optionDaytonaData.auto_delete_interval, daytonaData.autoDeleteInterval, daytonaData.auto_delete_interval) !== undefined
        ? { auto_delete_interval: firstPresent(options.autoDeleteInterval, options.auto_delete_interval, optionDaytonaData.autoDeleteInterval, optionDaytonaData.auto_delete_interval, daytonaData.autoDeleteInterval, daytonaData.auto_delete_interval) }
        : {}),
      ...(firstPresent(options.ephemeral, optionDaytonaData.ephemeral, daytonaData.ephemeral) !== undefined ? { ephemeral: firstPresent(options.ephemeral, optionDaytonaData.ephemeral, daytonaData.ephemeral) } : {}),
      ...(options.target || loomData.target || sandboxData.target || daytonaData.target ? { target: options.target || loomData.target || sandboxData.target || daytonaData.target } : {}),
      ...(options.apiKeyEnv || options.api_key_env || loomData.api_key_env || sandboxData.apiKeyEnv || sandboxData.api_key_env || daytonaData.api_key_env
        ? { api_key_env: options.apiKeyEnv || options.api_key_env || loomData.api_key_env || sandboxData.apiKeyEnv || sandboxData.api_key_env || daytonaData.api_key_env }
        : {}),
      ...(options.repoUrl || options.repo_url || options.remoteUrl || options.remote_url ? { repo_url: options.repoUrl || options.repo_url || options.remoteUrl || options.remote_url } : {}),
      ...(options.branch || options.checkoutBranch || options.checkout_branch || options.gitBranch || options.git_branch
        ? { branch: options.branch || options.checkoutBranch || options.checkout_branch || options.gitBranch || options.git_branch }
        : {}),
      ...(options.ref || options.checkoutRef || options.checkout_ref || options.gitRef || options.git_ref ? { ref: options.ref || options.checkoutRef || options.checkout_ref || options.gitRef || options.git_ref } : {}),
      ...(options.gitTokenEnv || options.git_token_env || options.githubTokenEnv || options.github_token_env || options.gitAuthTokenEnv || options.git_auth_token_env
        ? { git_token_env: options.gitTokenEnv || options.git_token_env || options.githubTokenEnv || options.github_token_env || options.gitAuthTokenEnv || options.git_auth_token_env }
        : {}),
      ...(options.gitUsername || options.git_username || options.githubUsername || options.github_username ? { git_username: options.gitUsername || options.git_username || options.githubUsername || options.github_username } : {}),
      ...(options.gitDeployKeyEnv || options.git_deploy_key_env || options.deployKeyEnv || options.deploy_key_env || options.sshKeyEnv || options.ssh_key_env
        ? { git_deploy_key_env: options.gitDeployKeyEnv || options.git_deploy_key_env || options.deployKeyEnv || options.deploy_key_env || options.sshKeyEnv || options.ssh_key_env }
        : {}),
      ...(options.openaiApiKeyEnv || options.openai_api_key_env ? { openai_api_key_env: options.openaiApiKeyEnv || options.openai_api_key_env } : {}),
      ...(options.codexAuthFileEnv || options.codex_auth_file_env ? { codex_auth_file_env: options.codexAuthFileEnv || options.codex_auth_file_env } : {}),
      ...(setupCommands.length > 0 ? { setup_commands: setupCommands } : {}),
      ...(firstPresent(options.createTimeout, options.create_timeout, options.timeout, optionDaytonaData.createTimeout, optionDaytonaData.create_timeout, optionDaytonaData.timeout, daytonaData.createTimeout, daytonaData.create_timeout, daytonaData.timeout) !== undefined
        ? { create_timeout: firstPresent(options.createTimeout, options.create_timeout, options.timeout, optionDaytonaData.createTimeout, optionDaytonaData.create_timeout, optionDaytonaData.timeout, daytonaData.createTimeout, daytonaData.create_timeout, daytonaData.timeout) }
        : {}),
      ...(options.setupTimeout ?? options.setup_timeout ? { setup_timeout: options.setupTimeout ?? options.setup_timeout } : {}),
      ...(options.healthTimeout ?? options.health_timeout ? { health_timeout: options.healthTimeout ?? options.health_timeout } : {}),
      ...(options.runTimeout ?? options.run_timeout ?? options.commandTimeout ?? options.command_timeout ? { run_timeout: options.runTimeout ?? options.run_timeout ?? options.commandTimeout ?? options.command_timeout } : {}),
      ...(firstPresent(options.buildLogs, options.build_logs, optionDaytonaData.buildLogs, optionDaytonaData.build_logs, daytonaData.buildLogs, daytonaData.build_logs) ? { build_logs: firstPresent(options.buildLogs, options.build_logs, optionDaytonaData.buildLogs, optionDaytonaData.build_logs, daytonaData.buildLogs, daytonaData.build_logs) } : {}),
    },
  };
}

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

function stringRecord(value = {}) {
  return Object.fromEntries(
    Object.entries(value || {})
      .filter(([, v]) => v != null)
      .map(([k, v]) => [k, String(v)]),
  );
}

function stringArray(value = []) {
  if (Array.isArray(value)) return value.map((item) => String(item).trim()).filter(Boolean);
  if (typeof value === "string" && value.trim() !== "") return [value.trim()];
  return [];
}

function firstPresent(...values) {
  for (const value of values) {
    if (value !== undefined && value !== null && value !== "") return value;
  }
  return undefined;
}
