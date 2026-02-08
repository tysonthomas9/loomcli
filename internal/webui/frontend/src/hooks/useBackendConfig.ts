/**
 * useBackendConfig - React hook for backend configuration state management.
 * Fetches config on mount, supports optimistic updates with rollback.
 */

import { useState, useCallback, useEffect, useRef } from 'react';

import { getBackendConfig, updateBackendConfig } from '@/api/config';
import type { BackendConfigData } from '@/api/config';

/**
 * Return type for the useBackendConfig hook.
 */
export interface UseBackendConfigReturn {
  /** Current backend config, null if not loaded */
  config: BackendConfigData | null;
  /** Whether a fetch is in progress */
  isLoading: boolean;
  /** Error message from the last operation */
  error: string | null;
  /** Whether a save is in progress */
  isSaving: boolean;
  /** Update the project default backend (optimistic). Returns true on success. */
  updateBackend: (backend: string) => Promise<boolean>;
  /** Re-fetch config from the API */
  refetch: () => void;
}

/**
 * React hook for managing backend configuration state.
 * Fetches from GET /api/config/backend on mount, updates via PATCH.
 */
export function useBackendConfig(): UseBackendConfigReturn {
  const [config, setConfig] = useState<BackendConfigData | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const fetchConfig = useCallback(async () => {
    setIsLoading(true);
    setError(null);

    try {
      const data = await getBackendConfig();
      if (mountedRef.current) {
        setConfig(data);
      }
    } catch (err) {
      if (mountedRef.current) {
        const message = err instanceof Error ? err.message : 'Failed to load backend config';
        setError(message);
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
    }
  }, []);

  // Fetch on mount
  useEffect(() => {
    fetchConfig();
  }, [fetchConfig]);

  const updateBackend = useCallback(async (backend: string): Promise<boolean> => {
    if (!config) return false;

    // Optimistic update
    const previousConfig = config;
    setConfig({ ...config, backend });
    setIsSaving(true);
    setError(null);

    try {
      const updated = await updateBackendConfig(backend);
      if (mountedRef.current) {
        setConfig(updated);
      }
      return true;
    } catch (err) {
      if (mountedRef.current) {
        // Rollback
        setConfig(previousConfig);
        const message = err instanceof Error ? err.message : 'Failed to save backend config';
        setError(message);
      }
      return false;
    } finally {
      if (mountedRef.current) {
        setIsSaving(false);
      }
    }
  }, [config]);

  return {
    config,
    isLoading,
    error,
    isSaving,
    updateBackend,
    refetch: fetchConfig,
  };
}
