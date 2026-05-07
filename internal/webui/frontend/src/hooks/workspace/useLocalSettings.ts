import { useCallback, useEffect, useRef, useState } from "react";

import {
  getLocalSettings,
  updateLocalRedisSettings,
  type LocalSettingsData,
  type UpdateLocalRedisSettings,
} from "@/api/common";

export interface UseLocalSettingsReturn {
  settings: LocalSettingsData | null;
  isLoading: boolean;
  isSaving: boolean;
  error: string | null;
  updateRedis: (settings: UpdateLocalRedisSettings) => Promise<boolean>;
  refetch: () => void;
}

export function useLocalSettings(): UseLocalSettingsReturn {
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
    fetchSettings();
  }, [fetchSettings]);

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

  return {
    settings,
    isLoading,
    isSaving,
    error,
    updateRedis,
    refetch: fetchSettings,
  };
}
