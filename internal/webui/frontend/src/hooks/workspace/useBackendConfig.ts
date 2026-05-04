/**
 * useBackendConfig - React hook for backend configuration state management.
 * Fetches config on mount, supports optimistic updates with rollback.
 */

import { useState, useCallback, useEffect, useRef } from "react";

import { getCachedBackendConfig } from "@/api/common";
import type { BackendConfigData } from "@/api/common";
import {
  getWorkspaceBackendConfig,
  updateWorkspaceBackend,
} from "@/api/workspace";

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
  /** Whether the current config is from cache (stale) */
  isCached: boolean;
  /** Update the project default backend (optimistic). Returns true on success. */
  updateBackend: (backend: string) => Promise<boolean>;
  /** Re-fetch config from the API */
  refetch: () => void;
}

/**
 * React hook for managing backend configuration state.
 *
 * Fetches from the workspace-scoped /api/workspaces/{ws}/config/backend.
 * Runtime backend config is store-backed; callers must provide a workspace ID.
 */
export function useBackendConfig(workspaceId?: string): UseBackendConfigReturn {
  const [initialCache] = useState(() => getCachedBackendConfig());
  const [config, setConfig] = useState<BackendConfigData | null>(initialCache);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  const [isCached, setIsCached] = useState(initialCache !== null);

  const mountedRef = useRef(true);
  const configRef = useRef(config);
  configRef.current = config;

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
      if (!workspaceId) {
        throw new Error("workspace ID is required");
      }
      const data = await getWorkspaceBackendConfig(workspaceId);
      if (mountedRef.current) {
        setConfig(data);
        setIsCached(false);
      }
    } catch (err) {
      if (mountedRef.current) {
        const message =
          err instanceof Error ? err.message : "Failed to load backend config";
        setError(message);
        // Keep cached config visible if available (don't null it out)
        if (configRef.current !== null) {
          setIsCached(true);
        }
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
    }
  }, [workspaceId]);

  // Fetch on mount + whenever workspaceId changes.
  useEffect(() => {
    fetchConfig();
  }, [fetchConfig]);

  const updateBackend = useCallback(
    async (backend: string): Promise<boolean> => {
      if (!config) return false;

      // Optimistic update
      const previousConfig = config;
      setConfig({ ...config, backend });
      setIsSaving(true);
      setError(null);

      try {
        if (!workspaceId) {
          throw new Error("workspace ID is required");
        }
        const updated = await updateWorkspaceBackend(workspaceId, backend).then(
          () => getWorkspaceBackendConfig(workspaceId),
        );
        if (mountedRef.current) {
          setConfig(updated);
        }
        return true;
      } catch (err) {
        if (mountedRef.current) {
          // Rollback
          setConfig(previousConfig);
          const message =
            err instanceof Error
              ? err.message
              : "Failed to save backend config";
          setError(message);
        }
        return false;
      } finally {
        if (mountedRef.current) {
          setIsSaving(false);
        }
      }
    },
    [config, workspaceId],
  );

  return {
    config,
    isLoading,
    error,
    isSaving,
    isCached,
    updateBackend,
    refetch: fetchConfig,
  };
}
