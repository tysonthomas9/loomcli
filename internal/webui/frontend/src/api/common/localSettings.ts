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
