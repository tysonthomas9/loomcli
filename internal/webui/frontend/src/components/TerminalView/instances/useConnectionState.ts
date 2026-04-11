import { useState, useCallback, useRef } from "react";

import { fetchTerminalToken, restartTerminalSession } from "@/hooks/api";

import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "./TerminalInstance";
import type { ReconnectOverlayState } from "./ReconnectingOverlay";
import type { TabState } from "@/components/TerminalView/tabs";

interface UseConnectionStateOptions {
  setTabs: React.Dispatch<React.SetStateAction<TabState[]>>;
  instanceRefs: React.MutableRefObject<Map<string, TerminalInstanceHandle>>;
  workspaceId: string;
  onTabConnected?: (tabId: string) => void;
}

interface UseConnectionStateReturn {
  tabHasConnected: Map<string, boolean>;
  tabReconnectState: Map<string, ReconnectOverlayState>;
  handleConnectionStateChange: (
    tabId: string,
    state: ConnectionState,
    hasConnected: boolean,
  ) => void;
  handleReconnectStateChange: (
    tabId: string,
    state: ReconnectOverlayState,
  ) => void;
  handleReconnect: (tabId: string) => void;
  handleBackendCrash: (tabId: string, reason: string) => void;
  handleCrashRestart: (tabId: string, sessionName: string) => void;
}

export function useConnectionState({
  setTabs,
  instanceRefs,
  workspaceId,
  onTabConnected,
}: UseConnectionStateOptions): UseConnectionStateReturn {
  const [tabHasConnected, setTabHasConnected] = useState<Map<string, boolean>>(
    () => new Map(),
  );
  const [tabReconnectState, setTabReconnectState] = useState<
    Map<string, ReconnectOverlayState>
  >(() => new Map());

  // Capture workspaceId in a ref to avoid stale closures in handleCrashRestart
  const workspaceIdRef = useRef(workspaceId);
  workspaceIdRef.current = workspaceId;

  // Capture onTabConnected in a ref for stable callbacks
  const onTabConnectedRef = useRef(onTabConnected);
  onTabConnectedRef.current = onTabConnected;

  const handleConnectionStateChange = useCallback(
    (tabId: string, state: ConnectionState, hasConnected: boolean) => {
      setTabs((prev) =>
        prev.map((t) =>
          t.id === tabId ? { ...t, connectionState: state } : t,
        ),
      );

      if (hasConnected) {
        setTabHasConnected((prev) => {
          if (prev.get(tabId)) return prev;
          const next = new Map(prev);
          next.set(tabId, true);
          return next;
        });
      }

      if (state === "connected") {
        onTabConnectedRef.current?.(tabId);
      }
    },
    [setTabs],
  );

  const handleReconnectStateChange = useCallback(
    (tabId: string, state: ReconnectOverlayState) => {
      setTabReconnectState((prev) => {
        if (prev.get(tabId) === state) return prev;
        const next = new Map(prev);
        if (state === null) next.delete(tabId);
        else next.set(tabId, state);
        return next;
      });
    },
    [],
  );

  const handleReconnect = useCallback(
    (tabId: string) => {
      instanceRefs.current.get(tabId)?.reconnect();
    },
    [instanceRefs],
  );

  const handleBackendCrash = useCallback(
    (tabId: string, reason: string) => {
      setTabs((prev) =>
        prev.map((t) => (t.id === tabId ? { ...t, crashReason: reason } : t)),
      );
    },
    [setTabs],
  );

  const handleCrashRestart = useCallback(
    (tabId: string, sessionName: string) => {
      setTabs((prev) =>
        prev.map((t) => (t.id === tabId ? { ...t, crashReason: null } : t)),
      );
      fetchTerminalToken(workspaceIdRef.current, sessionName)
        .then((token) => {
          if (!token) {
            instanceRefs.current.get(tabId)?.reconnect();
            return;
          }
          return restartTerminalSession(
            workspaceIdRef.current,
            sessionName,
            token,
          ).then(() => {
            instanceRefs.current.get(tabId)?.reconnect();
          });
        })
        .catch((err) => {
          console.error(`Failed to restart session ${sessionName}:`, err);
          instanceRefs.current.get(tabId)?.reconnect();
        });
    },
    [setTabs, instanceRefs],
  );

  return {
    tabHasConnected,
    tabReconnectState,
    handleConnectionStateChange,
    handleReconnectStateChange,
    handleReconnect,
    handleBackendCrash,
    handleCrashRestart,
  };
}
