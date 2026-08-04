import { useState, useEffect } from "react";

import { getTerminalState } from "@/api/terminal";
import { useWorkspaceContext } from "@/hooks/workspace";

interface UseSessionRestoreReturn {
  activeTabId: string | null;
  isRestoring: boolean;
}

interface UseSessionRestoreOptions {
  enabled?: boolean;
}

/**
 * Hook that fetches the persisted active tab ID from the server on mount.
 * Falls back to sessionStorage if the API call fails.
 */
export function useSessionRestore(
  options: UseSessionRestoreOptions = {},
): UseSessionRestoreReturn {
  const enabled = options.enabled ?? true;
  const { workspaceId } = useWorkspaceContext();
  const [activeTabId, setActiveTabId] = useState<string | null>(null);
  const [isRestoring, setIsRestoring] = useState(enabled);

  useEffect(() => {
    if (!enabled) {
      setIsRestoring(false);
      return;
    }
    let cancelled = false;
    setIsRestoring(true);

    getTerminalState(workspaceId)
      .then((state) => {
        if (cancelled) return;
        setActiveTabId(state.active_tab || null);
      })
      .catch(() => {
        if (cancelled) return;
        // Fall back to sessionStorage
        const saved = sessionStorage.getItem("terminal-active-tab");
        setActiveTabId(saved || null);
      })
      .finally(() => {
        if (!cancelled) setIsRestoring(false);
      });

    return () => {
      cancelled = true;
    };
  }, [enabled, workspaceId]);

  return { activeTabId, isRestoring };
}
