import { useEffect, type MutableRefObject } from "react";

import type { TabMetadata } from "@/api/terminal";
import type { BackendConfigData } from "@/api/config";

import type { ConnectionState } from "./TerminalInstance";
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
  } = args;

  useEffect(() => {
    if (initializedRef.current || metaLoading || configLoading) return;
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
        const fallbackTab: TabState = {
          id: "talk-to-lead",
          label: "talk-to-lead",
          sessionName: "talk-to-lead",
          connectionState: "disconnected" as ConnectionState,
          backendName: config?.backend ?? "unknown",
        };
        setTabs([fallbackTab]);
        setActiveTabId(fallbackTab.id);
        createTab("talk-to-lead", "talk-to-lead", 0).catch((err) =>
          console.error("Failed to persist fallback tab:", err),
        );
        return;
      }

      const newTabs: TabState[] = backends.map((backend) => {
        const name = `lead-${backend}-1`;
        return {
          id: name,
          label: name,
          sessionName: name,
          connectionState: "disconnected" as ConnectionState,
          backendName: backend,
        };
      });
      setTabs(newTabs);

      const claudeTab = newTabs.find((t) =>
        t.sessionName.startsWith("lead-claude-"),
      );
      setActiveTabId(claudeTab?.id ?? newTabs[0]?.id ?? "");

      newTabs.forEach((tab, i) => {
        createTab(tab.sessionName, tab.label, i).catch((err) =>
          console.error(
            `Failed to persist auto-created tab ${tab.sessionName}:`,
            err,
          ),
        );
      });
    }
  }, [tabMetadata, metaLoading, config, configLoading, createTab]);
}
