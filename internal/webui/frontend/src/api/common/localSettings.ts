import { ApiError, get, patch, post } from "./client";

export interface LocalRedisSettings {
  enabled: boolean;
  addr?: string;
  db: number;
  tls: boolean;
  password_set: boolean;
}

export interface LocalSettingsData {
  version: number;
  fleetdb_redis: LocalRedisSettings;
  agent_runtime: LocalAgentRuntimeSettings;
  local_task_runner: LocalTaskRunnerSettings;
  runtime_credentials: RuntimeCredentialsStatus;
}

export type AgentRuntimeDefault = "local" | "daytona";

export interface LocalAgentRuntimeSettings {
  default: AgentRuntimeDefault;
}

export interface LocalTaskRunnerSettings {
  opencode_model?: string;
}

export interface RuntimeCredentialStatus {
  configured: boolean;
  usable?: boolean;
  updated_at?: string;
}

export interface RuntimeCredentialsStatus {
  daytona: RuntimeCredentialStatus;
  github: RuntimeCredentialStatus;
}

export interface UpdateLocalRedisSettings {
  enabled?: boolean;
  redis_url?: string;
  addr?: string;
  password?: string;
  clear_password?: boolean;
  db?: number;
  tls?: boolean;
}

export interface UpdateAgentRuntimeSettings {
  default?: AgentRuntimeDefault;
}

export interface UpdateLocalTaskRunnerSettings {
  opencode_model?: string;
}

export interface UpdateRuntimeCredential {
  api_key?: string;
  token?: string;
  clear?: boolean;
}

export interface UpdateRuntimeCredentialsSettings {
  daytona?: UpdateRuntimeCredential;
  github?: UpdateRuntimeCredential;
}

interface LocalSettingsEnvelope {
  success: boolean;
  data?: LocalSettingsData;
  error?: string;
  message?: string;
}

export type RuntimeCredentialProvider = "daytona" | "github";

export interface RuntimeCredentialReadiness {
  provider: RuntimeCredentialProvider;
  configured: boolean;
  usable: boolean;
}

interface RuntimeCredentialPreflightEnvelope {
  success: boolean;
  data?: RuntimeCredentialReadiness;
  error?: string;
}

export async function getLocalSettings(): Promise<LocalSettingsData> {
  const response = await get<LocalSettingsEnvelope>("/api/local/settings");
  if (!response.success || !response.data) {
    throw new ApiError(0, response.error ?? "Failed to load local settings");
  }
  return response.data;
}

export async function updateLocalRedisSettings(
  redis: UpdateLocalRedisSettings,
): Promise<LocalSettingsData> {
  const response = await patch<LocalSettingsEnvelope>("/api/local/settings", {
    fleetdb_redis: redis,
  });
  if (!response.success || !response.data) {
    throw new ApiError(0, response.error ?? "Failed to save Redis settings");
  }
  return response.data;
}

export async function updateAgentRuntimeSettings(
  agentRuntime: UpdateAgentRuntimeSettings,
): Promise<LocalSettingsData> {
  const response = await patch<LocalSettingsEnvelope>("/api/local/settings", {
    agent_runtime: agentRuntime,
  });
  if (!response.success || !response.data) {
    throw new ApiError(
      0,
      response.error ?? "Failed to save agent runtime settings",
    );
  }
  return response.data;
}

export async function updateLocalTaskRunnerSettings(
  localTaskRunner: UpdateLocalTaskRunnerSettings,
): Promise<LocalSettingsData> {
  const response = await patch<LocalSettingsEnvelope>("/api/local/settings", {
    local_task_runner: localTaskRunner,
  });
  if (!response.success || !response.data) {
    throw new ApiError(
      0,
      response.error ?? "Failed to save local task runner settings",
    );
  }
  return response.data;
}

export async function updateRuntimeCredentialsSettings(
  runtimeCredentials: UpdateRuntimeCredentialsSettings,
): Promise<LocalSettingsData> {
  const response = await patch<LocalSettingsEnvelope>("/api/local/settings", {
    runtime_credentials: runtimeCredentials,
  });
  if (!response.success || !response.data) {
    throw new ApiError(
      0,
      response.error ?? "Failed to save runtime credentials",
    );
  }
  return response.data;
}

/**
 * Verify that a configured credential can be opened by the current server
 * without returning credential material to the browser.
 */
export async function preflightRuntimeCredential(
  provider: RuntimeCredentialProvider,
): Promise<RuntimeCredentialReadiness> {
  const response = await post<RuntimeCredentialPreflightEnvelope>(
    "/api/local/settings/runtime-credentials/preflight",
    { provider },
  );
  if (!response.success || !response.data) {
    const message = response.error ?? "Failed to verify runtime credential";
    throw new ApiError(0, "Runtime credential preflight failed", {
      error: message,
    });
  }
  return response.data;
}
