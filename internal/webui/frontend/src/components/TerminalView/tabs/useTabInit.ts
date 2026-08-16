import { useEffect, useRef, type MutableRefObject } from "react";

import type { TabMetadata } from "@/hooks/api";
import type { BackendConfigData } from "@/api/common";

import type { ConnectionState } from "@/components/TerminalView/instances";
import {
  getBackendFromSessionName,
  isAgentMetadata,
  isAgentTab,
  sanitizeSessionName,
  type TabState,
} from "./terminalTabUtils";

interface TabInitArgs {
  tabMetadata: TabMetadata[];
  /**
   * True once the tab list has settled for exactly this workspace. Positive
   * readiness, not "nothing says we're loading": a timing-derived flag can be
   * read stale in the commit where the view activates, which is how an empty
   * list once clobbered a real one (auto-creating a stray lead tab).
   */
  metaReady: boolean;
  /** True when metadata storage is unavailable (404/503) — a confirmed-empty degraded mode. */
  metaUnavailable?: boolean;
  config: BackendConfigData | undefined;
  configLoading: boolean;
  createTab: (s: string, l: string, o: number) => Promise<void>;
  setTabs: React.Dispatch<React.SetStateAction<TabState[]>>;
  setActiveTabId: React.Dispatch<React.SetStateAction<string>>;
  initializedRef: MutableRefObject<boolean>;
  /** Active workspace identifier, used to namespace auto-generated session names */
  workspace?: string;
  /** Whether the Terminal view is currently visible — defer init until user navigates to Terminal */
  isViewActive: boolean;
  /** When true, do not auto-create the default lead tab for an empty workspace. */
  skipDefaultTabInit?: boolean;
  /** When true, omit agent PTY tabs (global Terminal view; Agents view keeps them). */
  excludeAgentTabs?: boolean;
}

function metadataForInit(
  tabMetadata: TabMetadata[],
  excludeAgentTabs: boolean,
): TabMetadata[] {
  if (!excludeAgentTabs) return tabMetadata;
  return tabMetadata.filter((m) => !isAgentMetadata(m));
}

export function tabStateFromMetadata(
  metadata: TabMetadata,
  defaultBackend?: string,
): TabState & { _sortOrder: number; _pinned: boolean } {
  const agentName = isAgentMetadata(metadata)
    ? (metadata.agent_id ??
      (metadata.session_name.startsWith("agent-")
        ? metadata.session_name.slice("agent-".length)
        : undefined))
    : undefined;
  return {
    id: metadata.session_name,
    label: metadata.label,
    sessionName: metadata.session_name,
    connectionState: "disconnected" as ConnectionState,
    backendName: agentName
      ? (metadata.backend ?? "agent")
      : getBackendFromSessionName(metadata.session_name, defaultBackend),
    ...(agentName ? { agentName } : {}),
    ...(metadata.kind ? { kind: metadata.kind } : {}),
    ...(metadata.role ? { role: metadata.role } : {}),
    ...(metadata.writable != null ? { writable: metadata.writable } : {}),
    ...(metadata.replaced_at ? { replacedAt: metadata.replaced_at } : {}),
    pinned: metadata.pinned,
    _sortOrder: metadata.sort_order,
    _pinned: metadata.pinned,
  };
}

function sortMetadataTabs(
  tabs: Array<TabState & { _sortOrder?: number; _pinned?: boolean }>,
): TabState[] {
  return tabs
    .sort((a, b) => {
      if (a._pinned !== b._pinned) return a._pinned ? -1 : 1;
      return (a._sortOrder ?? 999) - (b._sortOrder ?? 999);
    })
    .map(({ _sortOrder: _, _pinned: _p, ...tab }) => tab);
}

export function useTabInit(args: TabInitArgs) {
  const {
    tabMetadata,
    metaReady,
    config,
    configLoading,
    createTab,
    setTabs,
    setActiveTabId,
    initializedRef,
    workspace,
    isViewActive,
    skipDefaultTabInit = false,
    excludeAgentTabs = false,
  } = args;
  const initializedMetadataRef = useRef<TabMetadata[] | null>(null);

  useEffect(() => {
    if (initializedRef.current || configLoading || !isViewActive) return;
    // No decision from an unconfirmed tab list. metaReady means "settled for
    // this workspace", which subsumes the old !metaLoading check: the
    // auto-create branch below is only ever reached with a confirmed-empty
    // list.
    if (!metaReady) return;
    initializedRef.current = true;

    const initMetadata = metadataForInit(tabMetadata, excludeAgentTabs);

    if (initMetadata.length > 0) {
      initializedMetadataRef.current = tabMetadata;
      const defaultBackend = config?.backend;
      const restoredTabs: TabState[] = initMetadata
        .map((m) => tabStateFromMetadata(m, defaultBackend))
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
      if (skipDefaultTabInit) {
        setTabs([]);
        setActiveTabId("");
        return;
      }

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
      const safeWorkspace = workspace ? sanitizeSessionName(workspace) : "";
      const wsPrefix =
        safeWorkspace && safeWorkspace !== "default"
          ? `${safeWorkspace}--`
          : "";

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
    metaReady,
    config,
    configLoading,
    createTab,
    initializedRef,
    setActiveTabId,
    setTabs,
    workspace,
    isViewActive,
    skipDefaultTabInit,
    excludeAgentTabs,
  ]);

  useEffect(() => {
    if (!initializedRef.current || configLoading || !isViewActive) return;
    if (!metaReady) return;
    if (tabMetadata.length === 0) return;
    if (initializedMetadataRef.current === tabMetadata) return;

    const syncMetadata = metadataForInit(tabMetadata, excludeAgentTabs);
    if (syncMetadata.length === 0) return;

    const defaultBackend = config?.backend;
    const metadataTabs = sortMetadataTabs(
      syncMetadata.map((m) => tabStateFromMetadata(m, defaultBackend)),
    );

    setTabs((current) => {
      const currentBySession = new Map(current.map((t) => [t.sessionName, t]));
      const metadataSessionNames = new Set(
        metadataTabs.map((t) => t.sessionName),
      );
      const nextTabs = metadataTabs.map((metadataTab) => {
        const existing = currentBySession.get(metadataTab.sessionName);
        if (!existing) return metadataTab;
        return {
          ...existing,
          label: metadataTab.label,
          backendName: metadataTab.backendName,
          ...(metadataTab.pinned !== undefined && {
            pinned: metadataTab.pinned,
          }),
        };
      });

      for (const tab of current) {
        if (excludeAgentTabs && isAgentTab(tab)) continue;
        if (!metadataSessionNames.has(tab.sessionName)) {
          nextTabs.push(tab);
        }
      }

      const filtered = excludeAgentTabs
        ? nextTabs.filter((t) => !isAgentTab(t))
        : nextTabs;
      const unchanged =
        filtered.length === current.length &&
        filtered.every((tab, index) => {
          const currentTab = current[index];
          return (
            currentTab != null &&
            tab.id === currentTab.id &&
            tab.label === currentTab.label &&
            tab.sessionName === currentTab.sessionName &&
            tab.connectionState === currentTab.connectionState &&
            tab.backendName === currentTab.backendName &&
            tab.pinned === currentTab.pinned
          );
        });
      return unchanged ? current : filtered;
    });

    setActiveTabId((current) => {
      if (current && metadataTabs.some((tab) => tab.id === current)) {
        return current;
      }
      const savedActiveId = sessionStorage.getItem("terminal-active-tab");
      const restoredTab =
        savedActiveId && metadataTabs.find((t) => t.id === savedActiveId);
      return restoredTab ? restoredTab.id : (metadataTabs[0]?.id ?? "");
    });
  }, [
    tabMetadata,
    metaReady,
    config,
    configLoading,
    initializedRef,
    setActiveTabId,
    setTabs,
    isViewActive,
    excludeAgentTabs,
  ]);
}
