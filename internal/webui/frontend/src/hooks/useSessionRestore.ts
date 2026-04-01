import { useState, useEffect } from "react";

import { getTerminalState } from "@/api/terminal";
import { useWorkspaceContext } from "./useWorkspaceContext";

interface UseSessionRestoreReturn {
  activeTabId: string | null;
  isRestoring: boolean;
}

/**
 * Hook that fetches the persisted active tab ID from the server on mount.
 * Falls back to sessionStorage if the API call fails.
 */
export function useSessionRestore(): UseSessionRestoreReturn {
  const { workspaceId } = useWorkspaceContext();
  const [activeTabId, setActiveTabId] = useState<string | null>(null);
  const [isRestoring, setIsRestoring] = useState(true);

  useEffect(() => {
    let cancelled = false;

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
  }, [workspaceId]);

  return { activeTabId, isRestoring };
}
