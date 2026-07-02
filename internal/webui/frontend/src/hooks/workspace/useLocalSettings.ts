import { useCallback, useEffect, useRef, useState } from "react";

import {
  getLocalSettings,
  updateAgentRuntimeSettings,
  updateLocalTaskRunnerSettings,
  updateLocalRedisSettings,
  updateRuntimeCredentialsSettings,
  type LocalSettingsData,
  type UpdateAgentRuntimeSettings,
  type UpdateLocalTaskRunnerSettings,
  type UpdateLocalRedisSettings,
  type UpdateRuntimeCredentialsSettings,
} from "@/api/common";

export interface UseLocalSettingsReturn {
  settings: LocalSettingsData | null;
  isLoading: boolean;
  isSaving: boolean;
  error: string | null;
  updateRedis: (settings: UpdateLocalRedisSettings) => Promise<boolean>;
  updateAgentRuntime: (
    settings: UpdateAgentRuntimeSettings,
  ) => Promise<boolean>;
  updateLocalTaskRunner: (
    settings: UpdateLocalTaskRunnerSettings,
  ) => Promise<boolean>;
  updateRuntimeCredentials: (
    settings: UpdateRuntimeCredentialsSettings,
  ) => Promise<boolean>;
  refetch: () => void;
}

/**
 * `enabled` gates the initial fetch — pass false while the consuming surface
 * is hidden (e.g. an unopened modal) so mounting it costs no request.
 */
export function useLocalSettings(enabled = true): UseLocalSettingsReturn {
  const [settings, setSettings] = useState<LocalSettingsData | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const fetchSettings = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const data = await getLocalSettings();
      if (mountedRef.current) {
        setSettings(data);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(
          err instanceof Error ? err.message : "Failed to load local settings",
        );
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    if (enabled) void fetchSettings();
  }, [enabled, fetchSettings]);

  const updateRedis = useCallback(
    async (redis: UpdateLocalRedisSettings): Promise<boolean> => {
      setIsSaving(true);
      setError(null);
      try {
        const data = await updateLocalRedisSettings(redis);
        if (mountedRef.current) {
          setSettings(data);
        }
        return true;
      } catch (err) {
        if (mountedRef.current) {
          setError(
            err instanceof Error
              ? err.message
              : "Failed to save Redis settings",
          );
        }
        return false;
      } finally {
        if (mountedRef.current) {
          setIsSaving(false);
        }
      }
    },
    [],
  );

  const updateAgentRuntime = useCallback(
    async (agentRuntime: UpdateAgentRuntimeSettings): Promise<boolean> => {
      setIsSaving(true);
      setError(null);
      try {
        const data = await updateAgentRuntimeSettings(agentRuntime);
        if (mountedRef.current) {
          setSettings(data);
        }
        return true;
      } catch (err) {
        if (mountedRef.current) {
          setError(
            err instanceof Error
              ? err.message
              : "Failed to save agent runtime settings",
          );
        }
        return false;
      } finally {
        if (mountedRef.current) {
          setIsSaving(false);
        }
      }
    },
    [],
  );

  const updateRuntimeCredentials = useCallback(
    async (
      runtimeCredentials: UpdateRuntimeCredentialsSettings,
    ): Promise<boolean> => {
      setIsSaving(true);
      setError(null);
      try {
        const data = await updateRuntimeCredentialsSettings(runtimeCredentials);
        if (mountedRef.current) {
          setSettings(data);
        }
        return true;
      } catch (err) {
        if (mountedRef.current) {
          setError(
            err instanceof Error
              ? err.message
              : "Failed to save runtime credentials",
          );
        }
        return false;
      } finally {
        if (mountedRef.current) {
          setIsSaving(false);
        }
      }
    },
    [],
  );

  const updateLocalTaskRunner = useCallback(
    async (
      localTaskRunner: UpdateLocalTaskRunnerSettings,
    ): Promise<boolean> => {
      setIsSaving(true);
      setError(null);
      try {
        const data = await updateLocalTaskRunnerSettings(localTaskRunner);
        if (mountedRef.current) {
          setSettings(data);
        }
        return true;
      } catch (err) {
        if (mountedRef.current) {
          setError(
            err instanceof Error
              ? err.message
              : "Failed to save local task runner settings",
          );
        }
        return false;
      } finally {
        if (mountedRef.current) {
          setIsSaving(false);
        }
      }
    },
    [],
  );

  return {
    settings,
    isLoading,
    isSaving,
    error,
    updateRedis,
    updateAgentRuntime,
    updateLocalTaskRunner,
    updateRuntimeCredentials,
    refetch: fetchSettings,
  };
}
