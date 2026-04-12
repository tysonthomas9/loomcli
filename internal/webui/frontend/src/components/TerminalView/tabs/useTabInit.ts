import { useEffect, type MutableRefObject } from "react";

import type { TabMetadata } from "@/api/terminal";
import type { BackendConfigData } from "@/api/common";

import type { ConnectionState } from "@/components/TerminalView/instances";
import { getBackendFromSessionName, type TabState } from "./terminalTabUtils";

interface TabInitArgs {
  tabMetadata: TabMetadata[];
  metaLoading: boolean;
  config: BackendConfigData | undefined;
  configLoading: boolean;
  createTab: (s: string, l: string, o: number) => Promise<void>;
  setTabs: React.Dispatch<React.SetStateAction<TabState[]>>;
  setActiveTabId: React.Dispatch<React.SetStateAction<string>>;
  initializedRef: MutableRefObject<boolean>;
  /** Active workspace name, used to namespace auto-generated session names */
  workspace?: string;
  /** Whether the Terminal view is currently visible — defer init until user navigates to Terminal */
  isViewActive: boolean;
}

export function useTabInit(args: TabInitArgs) {
  const {
    tabMetadata,
    metaLoading,
    config,
    configLoading,
    createTab,
    setTabs,
    setActiveTabId,
    initializedRef,
    workspace,
    isViewActive,
  } = args;

  useEffect(() => {
    if (initializedRef.current || metaLoading || configLoading || !isViewActive)
      return;
    initializedRef.current = true;

    if (tabMetadata.length > 0) {
      const defaultBackend = config?.backend;
      const restoredTabs: TabState[] = tabMetadata
        .map((m) => ({
          id: m.session_name,
          label: m.label,
          sessionName: m.session_name,
          connectionState: "disconnected" as ConnectionState,
          backendName: getBackendFromSessionName(
            m.session_name,
            defaultBackend,
          ),
          pinned: m.pinned,
          _sortOrder: m.sort_order,
          _pinned: m.pinned,
        }))
        .sort((a, b) => {
          if (a._pinned !== b._pinned) return a._pinned ? -1 : 1;
          return (a._sortOrder ?? 999) - (b._sortOrder ?? 999);
        })
        .map(({ _sortOrder: _, _pinned: _p, ...tab }) => tab);
      setTabs(restoredTabs);

      const savedActiveId = sessionStorage.getItem("terminal-active-tab");
      const restoredTab =
        savedActiveId && restoredTabs.find((t) => t.id === savedActiveId);
      setActiveTabId(
        restoredTab ? restoredTab.id : (restoredTabs[0]?.id ?? ""),
      );
    } else {
      const backends = (config?.available ?? []).filter((b) => b !== "shell");
      if (backends.length === 0) {
        // No backends configured — render empty state (NoBackendsEmptyState)
        setTabs([]);
        setActiveTabId("");
        return;
      }

      // Workspace prefix for session names: namespace tmux sessions per workspace
      // to prevent cross-workspace session leakage. The prefix is omitted from
      // display labels for cleaner UI.
      const wsPrefix =
        workspace && workspace !== "default" ? `${workspace}--` : "";

      // Only auto-create a single tab for the default backend.
      // Users can add more backend tabs via the "+" button.
      const cfgBackend = config?.backend;
      const defaultBackend =
        cfgBackend && backends.includes(cfgBackend)
          ? cfgBackend
          : (backends[0] as string); // safe: backends.length > 0 checked above
      const label = `lead-${defaultBackend}-1`;
      const sessionName = `${wsPrefix}lead-${defaultBackend}-1`;
      const newTab: TabState = {
        id: sessionName,
        label,
        sessionName,
        connectionState: "disconnected" as ConnectionState,
        backendName: defaultBackend,
      };
      setTabs([newTab]);
      setActiveTabId(newTab.id);

      createTab(newTab.sessionName, newTab.label, 0).catch((err) =>
        console.error(
          `Failed to persist auto-created tab ${newTab.sessionName}:`,
          err,
        ),
      );
    }
  }, [
    tabMetadata,
    metaLoading,
    config,
    configLoading,
    createTab,
    workspace,
    isViewActive,
  ]);
}
