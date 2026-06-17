import { ApiError, get, patch } from "./client";

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
  runtime_credentials: RuntimeCredentialsStatus;
}

export type AgentRuntimeDefault = "local" | "daytona";

export interface LocalAgentRuntimeSettings {
  default: AgentRuntimeDefault;
}

export interface RuntimeCredentialStatus {
  configured: boolean;
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
