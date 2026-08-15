import { useState, useRef, useCallback, useEffect, useMemo } from "react";

import type { IssueContext, TerminalSetupResult } from "@/hooks/api";
import { patchTerminalState, startTerminalSetup } from "@/hooks/api";
import { LoadingSkeleton } from "@/components";
import { useBackendConfig, useBackends } from "@/hooks/workspace";
import { useSessionRestore, useTerminalMetadata } from "@/hooks/terminal";
import {
  CLI_SETUP_REQUEST_EVENT,
  clearPendingCliSetupRequest,
  getCliSetupInstructions,
  readPendingCliSetupRequest,
  type CliSetupInstructions,
  type CliSetupRequest,
} from "@/utils/cliSetup";
import {
  publishTerminalSidebarState,
  TERMINAL_SIDEBAR_NEW_TAB_EVENT,
  TERMINAL_SIDEBAR_SELECT_EVENT,
} from "@/utils/terminalSidebarBridge";

import { NoBackendsEmptyState, useTabEditorGroups } from "./layout";
import groupStyles from "./layout/TerminalGroupLayout.module.css";
import {
  TerminalPane,
  TerminalPaneArea,
  useConnectionState,
  useSessionSeeding,
} from "./instances";
import type { TerminalInstanceHandle } from "./instances";
import {
  TerminalTabBar,
  MAX_TABS,
  BACKEND_BRAND_COLORS,
  type TabState,
  generateTabName,
  isAgentTab,
  isAgentMetadata,
  sanitizeSessionName,
  tabStateFromMetadata,
  useTabOrdering,
  useTabActions,
  useTabInit,
  useUnreadTracking,
  useWorkspaceTabState,
} from "./tabs";
import styles from "./TerminalView.module.css";

export interface TerminalInputRequest {
  id: string;
  text: string;
  targetAgentName?: string | undefined;
}

/** Split controls surfaced to parent chrome when hideTabs is true. */
export interface TerminalSplitControls {
  canSplit: boolean;
  onSplitRight: () => void;
}

interface TerminalViewProps {
  isActive?: boolean;
  pendingIssueContext?: IssueContext | undefined;
  onIssueContextConsumed?: (() => void) | undefined;
  pendingTerminalInput?: TerminalInputRequest | undefined;
  onTerminalInputConsumed?: (() => void) | undefined;
  onActiveSessionCountChange?: (count: number) => void;
  onUnreadChange?: (hasAnyUnread: boolean) => void;
  onTabLimitReached?: (message: string) => void;
  onNavigateToSettings?: () => void;
  /** When set, opens or focuses an agent's terminal tab. */
  pendingAgentName?: string | undefined;
  /** Called after pendingAgentName has been processed. */
  onAgentNameConsumed?: (() => void) | undefined;
  /**
   * When true, hide the tab bar above the terminal pane. Used by embedded
   * surfaces (e.g. the /agents view) where the parent already provides agent
   * selection — tabs would just be a redundant second picker.
   */
  hideTabs?: boolean;
  /** Called when hideTabs is true so the parent can render split controls. */
  onSplitControlsChange?: (controls: TerminalSplitControls | null) => void;
}

interface CliSetupGuide extends CliSetupRequest {
  tabId: string;
  instructions: CliSetupInstructions;
  hasRun: boolean;
  status: "starting" | "running" | "manual" | "failed";
  error?: string | undefined;
}

function buildCliSetupSessionName(
  workspaceId: string,
  backendName: string,
): string {
  const safeWorkspace = workspaceId ? sanitizeSessionName(workspaceId) : "";
  const wsPrefix =
    safeWorkspace && safeWorkspace !== "default" ? `${safeWorkspace}--` : "";
  const safeBackend = sanitizeSessionName(backendName) || "cli";
  return `${wsPrefix}lead-shell-setup-${safeBackend}`;
}

function setupErrorMessage(error: unknown): string {
  return error instanceof Error
    ? error.message
    : "Failed to start setup terminal";
}

function setupInstructionsFromResult(
  request: CliSetupRequest,
  result: TerminalSetupResult,
): CliSetupInstructions {
  const fallback = getCliSetupInstructions(request);
  const command = result.command || fallback.command;
  const instructions: CliSetupInstructions = {
    title: result.title || fallback.title,
    description: result.message || fallback.description,
    buttonLabel: result.manual ? "Show steps again" : fallback.buttonLabel,
  };
  if (command) {
    instructions.command = command;
  }
  return instructions;
}

export function TerminalView({
  isActive = true,
  pendingIssueContext,
  onIssueContextConsumed,
  pendingTerminalInput,
  onTerminalInputConsumed,
  onActiveSessionCountChange,
  onUnreadChange,
  onTabLimitReached,
  onNavigateToSettings,
  pendingAgentName,
  onAgentNameConsumed,
  hideTabs = false,
  onSplitControlsChange,
}: TerminalViewProps): JSX.Element {
  const [tabs, setTabs] = useState<TabState[]>([]);
  const [activeTabId, setActiveTabId] = useState<string>("");
  const initializedRef = useRef(false);
  const { id: workspaceId } = useWorkspaceTabState({
    setTabs,
    setActiveTabId,
    initializedRef,
  });
  const {
    tabs: tabMetadata,
    createTab,
    updateLabel,
    updatePinned,
    deleteTab,
    reorderTabs: reorderTabMeta,
    isLoading: metaLoading,
    loadedFor: metaLoadedFor,
    unavailable: metaUnavailable,
    error: metaError,
    refetch: refetchTabMetadata,
    // The app-level instance (hideTabs === false) is mounted for the whole
    // session and merely hidden when another view is on screen, and the
    // sidebar's Terminals section renders its tab list — so fetch regardless
    // of which view is active. Embedded instances (hideTabs) keep the gate.
    //
    // DELIBERATE ASYMMETRY: fetching metadata is decoupled from view
    // activity, but tab INITIALISATION stays gated on isViewActive inside
    // useTabInit. A user who never opens Terminal must not have a session
    // auto-created, and TerminalPanes must not attach or spawn PTYs for a
    // view that was never opened.
  } = useTerminalMetadata(workspaceId, { enabled: isActive || !hideTabs });
  const { config, isLoading: configLoading } = useBackendConfig(workspaceId, {
    enabled: isActive,
  });
  const { refetch: refetchAiBackends } = useBackends();
  const { activeTabId: restoredTabId, isRestoring } = useSessionRestore({
    enabled: isActive,
  });

  const [, setFocusedPane] = useState<"left" | "right">("left");
  /** Global Terminal hides agent PTYs; Agents embed keeps them. */
  const visibleTabs = useMemo(
    () => (hideTabs ? tabs : tabs.filter((tab) => !isAgentTab(tab))),
    [hideTabs, tabs],
  );
  const tabIds = useMemo(() => visibleTabs.map((tab) => tab.id), [visibleTabs]);
  const {
    groups,
    isSplit,
    splitActiveTab,
    activateInGroup,
    handleGroupDragStart,
    handleGroupDragEnd,
    handleGroupDragOver,
    moveTabToGroup,
  } = useTabEditorGroups(tabIds, activeTabId, workspaceId);

  useEffect(() => {
    if (!hideTabs || !onSplitControlsChange) return;
    onSplitControlsChange({
      canSplit: tabs.length >= 2,
      onSplitRight: splitActiveTab,
    });
    return () => onSplitControlsChange(null);
  }, [hideTabs, onSplitControlsChange, tabs.length, splitActiveTab]);
  const [cliSetupGuide, setCliSetupGuide] = useState<CliSetupGuide | null>(
    null,
  );
  const instanceRefs = useRef<Map<string, TerminalInstanceHandle>>(new Map());
  const processedCliSetupIdRef = useRef<string | null>(null);
  const activeTabIdRef = useRef(activeTabId);
  activeTabIdRef.current = activeTabId;
  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;
  const workspaceIdRef = useRef(workspaceId);
  workspaceIdRef.current = workspaceId;
  const backendStatusPollRef = useRef<ReturnType<typeof setInterval> | null>(
    null,
  );

  // ARIA live region for screen reader announcements
  const liveRegionRef = useRef<HTMLDivElement>(null);
  const announce = useCallback((msg: string) => {
    if (liveRegionRef.current) liveRegionRef.current.textContent = msg;
  }, []);

  const stopBackendStatusPolling = useCallback(() => {
    if (backendStatusPollRef.current == null) return;
    clearInterval(backendStatusPollRef.current);
    backendStatusPollRef.current = null;
  }, []);

  const startBackendStatusPolling = useCallback(() => {
    stopBackendStatusPolling();
    refetchAiBackends();
    let remainingTicks = 24;
    backendStatusPollRef.current = setInterval(() => {
      refetchAiBackends();
      remainingTicks -= 1;
      if (remainingTicks <= 0) {
        stopBackendStatusPolling();
      }
    }, 5000);
  }, [refetchAiBackends, stopBackendStatusPolling]);

  useEffect(() => {
    return () => {
      stopBackendStatusPolling();
    };
  }, [stopBackendStatusPolling]);

  const refreshCliStatus = useCallback(() => {
    refetchAiBackends();
    announce("CLI status refresh started");
  }, [announce, refetchAiBackends]);

  const handleTabLimitReached = useCallback(() => {
    const message = `Maximum terminal tabs reached (${MAX_TABS}). Close a tab before opening another.`;
    announce(message);
    onTabLimitReached?.(message);
  }, [announce, onTabLimitReached]);

  // Hook ordering: useSessionSeeding before useConnectionState so
  // trySeedOnConnect is available as the onTabConnected callback.
  const { trySeedOnConnect } = useSessionSeeding({
    pendingIssueContext,
    onIssueContextConsumed,
    pendingAgentName,
    onAgentNameConsumed,
    tabs,
    setTabs,
    setActiveTabId,
    createTab,
    config: config ?? undefined,
    initializedRef,
    tabsRef,
    workspaceIdRef,
  });

  const {
    tabHasConnected,
    tabReconnectState,
    handleConnectionStateChange,
    handleReconnectStateChange,
    handleReconnect,
    handleBackendCrash,
    handleCrashRestart,
  } = useConnectionState({
    setTabs,
    instanceRefs,
    onTabConnected: trySeedOnConnect,
  });

  const { tabUnread, handleOutput, clearTabUnread } = useUnreadTracking({
    activeTabIdRef,
    isActive,
    onUnreadChange,
  });
  const hasPendingCliSetupRequest =
    !hideTabs && readPendingCliSetupRequest() != null;

  // Positive readiness: the tab list has settled for exactly this workspace.
  const metaReady = Boolean(workspaceId) && metaLoadedFor === workspaceId;

  useTabInit({
    tabMetadata,
    metaReady,
    metaUnavailable,
    config: config ?? undefined,
    configLoading,
    createTab,
    setTabs,
    setActiveTabId,
    initializedRef,
    workspace: workspaceId,
    isViewActive: isActive ?? false,
    skipDefaultTabInit: hasPendingCliSetupRequest,
    excludeAgentTabs: !hideTabs,
  });

  // Apply server-restored active tab after initialization.
  const appliedRestoreRef = useRef(false);
  useEffect(() => {
    if (appliedRestoreRef.current || isRestoring || !restoredTabId) return;
    if (!initializedRef.current || tabs.length === 0) return;
    appliedRestoreRef.current = true;
    if (restoredTabId === activeTabId) return;
    const match = tabs.find((t) => t.id === restoredTabId);
    if (match) setActiveTabId(restoredTabId);
  }, [restoredTabId, isRestoring, tabs, activeTabId]);

  // Persist active tab to sessionStorage and server (debounced)
  const patchDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    if (!activeTabId) return;
    sessionStorage.setItem("terminal-active-tab", activeTabId);
    if (patchDebounceRef.current) clearTimeout(patchDebounceRef.current);
    patchDebounceRef.current = setTimeout(() => {
      patchTerminalState(workspaceId, { active_tab: activeTabId }).catch(
        () => {},
      );
    }, 300);
    return () => {
      if (patchDebounceRef.current) clearTimeout(patchDebounceRef.current);
    };
  }, [activeTabId, workspaceId]);

  // Report active (connected) session count to parent
  useEffect(() => {
    const count = visibleTabs.filter(
      (t) => t.connectionState === "connected",
    ).length;
    onActiveSessionCountChange?.(count);
  }, [visibleTabs, onActiveSessionCountChange]);

  // Drop agent tabs from the active selection in global Terminal view.
  useEffect(() => {
    if (hideTabs) return;
    const active = tabs.find((tab) => tab.id === activeTabId);
    if (!active || !isAgentTab(active)) return;
    const fallback = visibleTabs[0];
    if (fallback) setActiveTabId(fallback.id);
  }, [hideTabs, activeTabId, tabs, visibleTabs, setActiveTabId]);

  useEffect(() => {
    return () => {
      onActiveSessionCountChange?.(0);
    };
  }, [onActiveSessionCountChange]);

  const handleTabChange = useCallback(
    (tabId: string) => {
      setActiveTabId(tabId);
      clearTabUnread(tabId);
    },
    [clearTabUnread],
  );

  const handleGroupTabChange = useCallback(
    (groupIndex: number, tabId: string) => {
      activateInGroup(groupIndex, tabId);
      handleTabChange(tabId);
    },
    [activateInGroup, handleTabChange],
  );

  const handleGroupDrop = useCallback(
    (groupIndex: number) => {
      const movedTabId = moveTabToGroup(groupIndex);
      if (movedTabId) {
        handleGroupTabChange(groupIndex, movedTabId);
      }
    },
    [moveTabToGroup, handleGroupTabChange],
  );

  const toTerminalTabs = useCallback(
    (subset: TabState[]) =>
      subset.map((tab) => {
        const color = BACKEND_BRAND_COLORS[tab.backendName];
        return {
          id: tab.id,
          label: tab.label,
          connectionState: tab.connectionState,
          ...(color != null && { brandColor: color }),
          ...(tabUnread.get(tab.id) && { hasUnread: true }),
          ...(tab.pinned && { isPinned: true }),
        };
      }),
    [tabUnread],
  );

  const tabsForGroup = useCallback(
    (groupTabIds: string[]) =>
      visibleTabs.filter((tab) => groupTabIds.includes(tab.id)),
    [visibleTabs],
  );

  const { handleTabClose, handleDuplicateTab, handleTabRename } = useTabActions(
    {
      workspaceId,
      tabs,
      setTabs,
      setActiveTabId,
      activeTabIdRef,
      instanceRefs,
      createTab,
      updateLabel,
      deleteTab,
    },
  );

  const { handleTabPin, handleCloseOthers, handleReorderTabs } = useTabOrdering(
    {
      tabs,
      setTabs,
      setActiveTabId,
      handleTabClose,
      updatePinned,
      reorderTabMeta,
      deleteTab,
    },
  );

  const applyCliSetupResult = useCallback(
    (request: CliSetupRequest, result: TerminalSetupResult) => {
      const instructions = setupInstructionsFromResult(request, result);
      setCliSetupGuide((current) =>
        current?.id === request.id
          ? {
              ...current,
              tabId: result.session_name || current.tabId,
              instructions,
              hasRun: true,
              status: result.manual ? "manual" : "running",
              error: undefined,
            }
          : current,
      );
      startBackendStatusPolling();
      announce(
        result.manual
          ? `${request.displayName} setup steps shown`
          : `${request.displayName} setup command started`,
      );
    },
    [announce, startBackendStatusPolling],
  );

  const startCliSetup = useCallback(
    (request: CliSetupRequest) => {
      setCliSetupGuide((current) =>
        current?.id === request.id
          ? { ...current, status: "starting", error: undefined }
          : current,
      );
      void startTerminalSetup(
        workspaceIdRef.current,
        request.backendName,
        request.action,
      ).then(
        (result) => {
          applyCliSetupResult(request, result);
        },
        (error) => {
          const message = setupErrorMessage(error);
          setCliSetupGuide((current) =>
            current?.id === request.id
              ? { ...current, status: "failed", error: message }
              : current,
          );
          announce(`${request.displayName} setup failed`);
        },
      );
    },
    [announce, applyCliSetupResult],
  );

  const processCliSetupRequest = useCallback(
    (request: CliSetupRequest): boolean => {
      if (!initializedRef.current) return false;
      if (processedCliSetupIdRef.current === request.id) {
        clearPendingCliSetupRequest(request.id);
        return true;
      }

      const currentTabs = tabsRef.current;
      const sessionName = buildCliSetupSessionName(
        workspaceIdRef.current,
        request.backendName,
      );
      const label = `${request.displayName} setup`;
      const existingTab = currentTabs.find(
        (tab) => tab.sessionName === sessionName,
      );
      const existingMetadata = tabMetadata.find(
        (tab) => tab.session_name === sessionName,
      );
      const tabCount = Math.max(currentTabs.length, tabMetadata.length);

      if (!existingTab && !existingMetadata && tabCount >= MAX_TABS) {
        processedCliSetupIdRef.current = request.id;
        clearPendingCliSetupRequest(request.id);
        handleTabLimitReached();
        return true;
      }

      if (existingTab || existingMetadata) {
        const tabId = existingTab?.id ?? sessionName;
        const existingLabel = existingTab?.label ?? existingMetadata?.label;
        setActiveTabId(tabId);
        if (existingLabel !== label) {
          setTabs((prev) =>
            prev.map((tab) => (tab.id === tabId ? { ...tab, label } : tab)),
          );
          updateLabel(sessionName, label).catch((err) =>
            console.error(
              `Failed to persist setup tab label ${sessionName}:`,
              err,
            ),
          );
        }
      } else {
        const newTab: TabState = {
          id: sessionName,
          label,
          sessionName,
          connectionState: "disconnected" as const,
          backendName: "shell",
        };
        setTabs((prev) =>
          prev.some((tab) => tab.sessionName === sessionName)
            ? prev
            : [...prev, newTab],
        );
        setActiveTabId(sessionName);
      }

      try {
        sessionStorage.setItem("terminal-active-tab", sessionName);
      } catch {
        // sessionStorage unavailable
      }

      processedCliSetupIdRef.current = request.id;
      clearPendingCliSetupRequest(request.id);
      setCliSetupGuide({
        ...request,
        tabId: sessionName,
        instructions: getCliSetupInstructions(request),
        hasRun: false,
        status: "starting",
      });
      startCliSetup(request);
      announce(`${request.displayName} setup opened`);
      return true;
    },
    [
      announce,
      handleTabLimitReached,
      setActiveTabId,
      setTabs,
      startCliSetup,
      tabMetadata,
      updateLabel,
    ],
  );

  useEffect(() => {
    if (hideTabs || !isActive || !initializedRef.current) return;
    const request = readPendingCliSetupRequest();
    if (request) processCliSetupRequest(request);
  }, [hideTabs, isActive, tabs, processCliSetupRequest]);

  useEffect(() => {
    const handleCliSetupRequest = (event: Event) => {
      const request =
        (event as CustomEvent<CliSetupRequest>).detail ??
        readPendingCliSetupRequest();
      if (!request || hideTabs || !isActive || !initializedRef.current) return;
      processCliSetupRequest(request);
    };
    window.addEventListener(CLI_SETUP_REQUEST_EVENT, handleCliSetupRequest);
    return () => {
      window.removeEventListener(
        CLI_SETUP_REQUEST_EVENT,
        handleCliSetupRequest,
      );
    };
  }, [hideTabs, isActive, processCliSetupRequest]);

  const runCliSetupCommand = useCallback(
    (guide: CliSetupGuide) => {
      setActiveTabId(guide.tabId);
      instanceRefs.current.get(guide.tabId)?.focus();
      startCliSetup(guide);
      announce(`${guide.displayName} setup command requested`);
    },
    [announce, startCliSetup],
  );

  const handleNewTabLimit = useCallback(() => {
    if (visibleTabs.length >= MAX_TABS) {
      handleTabLimitReached();
    }
  }, [handleTabLimitReached, visibleTabs.length]);

  const selectableBackends = useMemo(() => {
    const aiBackends = (config?.available ?? []).filter((b) => b !== "shell");
    return ["shell", ...aiBackends];
  }, [config?.available]);

  const handleBackendSelect = useCallback(
    (backend: string) => {
      if (visibleTabs.length >= MAX_TABS) {
        handleTabLimitReached();
        return;
      }
      const { sessionName, label } = generateTabName(
        backend,
        tabs,
        workspaceId,
      );
      // Persist the tab so it survives a refresh. The WS handler spawns
      // the PTY on connect; this PUT is just metadata so the server can
      // return the tab in ListTabs on reload.
      createTab(sessionName, label, tabs.length).catch((err) =>
        console.error(`Failed to persist new tab ${sessionName}:`, err),
      );
      setTabs((prev) => [
        ...prev,
        {
          id: sessionName,
          label,
          sessionName,
          connectionState: "disconnected" as const,
          backendName: backend,
        },
      ]);
      setActiveTabId(sessionName);
      announce(`New tab ${label} created`);
    },
    [
      createTab,
      handleTabLimitReached,
      tabs,
      visibleTabs.length,
      workspaceId,
      announce,
    ],
  );

  // Sessions derived from server metadata, for the window before the Terminal
  // view has ever been opened (useTabInit has not run, so `tabs` is empty).
  // Same agent-tab exclusion as visibleTabs.
  const metadataSidebarTabs = useMemo(
    () =>
      tabMetadata
        .filter((meta) => !isAgentMetadata(meta))
        .map((meta) => tabStateFromMetadata(meta, config?.backend))
        .sort((a, b) => {
          if (a._pinned !== b._pinned) return a._pinned ? -1 : 1;
          return a._sortOrder - b._sortOrder;
        }),
    [tabMetadata, config?.backend],
  );

  useEffect(() => {
    if (hideTabs) return;
    // Publish the live tabs once initialised; before that fall back to the
    // settled metadata, so the sidebar's Terminals section lists the real
    // sessions from the moment the workspace loads rather than going blank
    // whenever another view is on screen. Only an unconfirmed tab list
    // publishes nothing.
    if (initializedRef.current && tabs.length > 0) {
      publishTerminalSidebarState({
        tabs: visibleTabs.map((tab) => ({
          id: tab.id,
          label: tab.label,
          backendName: tab.backendName,
          connectionState: tab.connectionState,
          ...(tab.pinned !== undefined ? { pinned: tab.pinned } : {}),
        })),
        activeTabId,
      });
      return;
    }
    if (!metaReady) return;
    publishTerminalSidebarState({
      tabs: metadataSidebarTabs.map((tab) => ({
        id: tab.id,
        label: tab.label,
        backendName: tab.backendName,
        connectionState: tab.connectionState,
        ...(tab.pinned !== undefined ? { pinned: tab.pinned } : {}),
      })),
      activeTabId: "",
    });
  }, [
    hideTabs,
    tabs,
    visibleTabs,
    activeTabId,
    metaReady,
    metadataSidebarTabs,
    initializedRef,
  ]);

  useEffect(() => {
    if (hideTabs || !isActive) return;

    const onSelect = (event: Event) => {
      const tabId = (event as CustomEvent<{ tabId: string }>).detail?.tabId;
      if (tabId) handleTabChange(tabId);
    };
    const onNewTab = () => {
      handleBackendSelect(selectableBackends[0] ?? "shell");
    };

    window.addEventListener(TERMINAL_SIDEBAR_SELECT_EVENT, onSelect);
    window.addEventListener(TERMINAL_SIDEBAR_NEW_TAB_EVENT, onNewTab);
    return () => {
      window.removeEventListener(TERMINAL_SIDEBAR_SELECT_EVENT, onSelect);
      window.removeEventListener(TERMINAL_SIDEBAR_NEW_TAB_EVENT, onNewTab);
    };
  }, [
    hideTabs,
    isActive,
    handleTabChange,
    handleBackendSelect,
    selectableBackends,
  ]);

  const setInstanceRef = useCallback(
    (tabId: string) => (handle: TerminalInstanceHandle | null) => {
      if (handle) {
        instanceRefs.current.set(tabId, handle);
      } else {
        instanceRefs.current.delete(tabId);
      }
    },
    [],
  );

  useEffect(() => {
    if (!pendingTerminalInput) return;
    if (tabs.length === 0) return;

    const targetTab = pendingTerminalInput.targetAgentName
      ? tabs.find(
          (tab) => tab.agentName === pendingTerminalInput.targetAgentName,
        )
      : tabs.find((tab) => tab.id === activeTabId);

    if (!targetTab) return;
    if (targetTab.id !== activeTabId) {
      setActiveTabId(targetTab.id);
      return;
    }
    if (targetTab.connectionState !== "connected") return;
    if (targetTab.writable === false) return;

    const handle = instanceRefs.current.get(targetTab.id);
    if (!handle) return;

    handle.pasteText(pendingTerminalInput.text);
    handle.focus();
    onTerminalInputConsumed?.();
  }, [
    pendingTerminalInput,
    tabs,
    activeTabId,
    setActiveTabId,
    onTerminalInputConsumed,
  ]);

  const setFocusedLeft = useCallback(
    () => setFocusedPane("left"),
    [setFocusedPane],
  );
  const setFocusedRight = useCallback(
    () => setFocusedPane("right"),
    [setFocusedPane],
  );

  const metaBySession = useMemo(
    () => new Map(tabMetadata.map((m) => [m.session_name, m])),
    [tabMetadata],
  );
  const paneTabs = useMemo(() => {
    if (!hideTabs) return visibleTabs;

    const activeTab = tabs.find((tab) => tab.id === activeTabId);
    const targetAgentName = pendingAgentName ?? activeTab?.agentName;
    if (targetAgentName) {
      const agentTab = tabs.find((tab) => tab.agentName === targetAgentName);
      return agentTab ? [agentTab] : [];
    }

    return activeTab ? [activeTab] : [];
  }, [activeTabId, hideTabs, pendingAgentName, tabs, visibleTabs]);

  const paneActiveTabId = paneTabs.some((tab) => tab.id === activeTabId)
    ? activeTabId
    : (paneTabs[0]?.id ?? activeTabId);
  const showEditorSplit = !hideTabs && isSplit;
  const canSplitRight = visibleTabs.length >= 2 && !isSplit;
  const splitContainerRef = useRef<HTMLDivElement>(null);
  const renderTerminalPane = useCallback(
    (
      tab: TabState,
      pane: "left" | "right" | null,
      isActiveOverride?: boolean,
    ) => {
      // A selected tab is only layout-active while the Terminal route itself
      // is visible. Propagating route activity keeps renderer observers and
      // PTY resizing suspended while App preserves TerminalView under
      // display:none for session continuity.
      const paneIsActive =
        isActive && (isActiveOverride ?? tab.id === paneActiveTabId);
      const meta = metaBySession.get(tab.sessionName);
      // Undefined while metadata is still loading — preserves connect-on-
      // mount. Only concrete `false` gates auto-attach.
      const ptyAlive = meta?.pty_alive;
      return (
        <TerminalPane
          tab={tab}
          isActive={paneIsActive}
          instanceRef={setInstanceRef(tab.id)}
          ptyAlive={ptyAlive}
          autoStartStaleSession={false}
          autoReconnect
          onConnectionStateChange={(state, hasConnected) =>
            handleConnectionStateChange(tab.id, state, hasConnected)
          }
          onReconnectStateChange={(state) =>
            handleReconnectStateChange(tab.id, state)
          }
          onOutput={() => handleOutput(tab.id)}
          onBackendCrash={(reason) => handleBackendCrash(tab.id, reason)}
          onCrashRestart={() => handleCrashRestart(tab.id, tab.sessionName)}
          onCloseTab={() => handleTabClose(tab.id)}
          onReconnect={() => handleReconnect(tab.id)}
          onTerminalFocus={
            pane === "left"
              ? setFocusedLeft
              : pane === "right"
                ? setFocusedRight
                : undefined
          }
          hasConnected={tabHasConnected.get(tab.id) ?? false}
          reconnectState={tabReconnectState.get(tab.id) ?? null}
        />
      );
    },
    [
      setInstanceRef,
      handleConnectionStateChange,
      handleReconnectStateChange,
      handleOutput,
      handleBackendCrash,
      handleCrashRestart,
      handleTabClose,
      handleReconnect,
      tabHasConnected,
      tabReconnectState,
      metaBySession,
      setFocusedLeft,
      setFocusedRight,
      paneActiveTabId,
      isActive,
    ],
  );

  const containerClassName = styles.container;
  return (
    <div className={containerClassName} data-testid="terminal-view">
      {(metaLoading || configLoading) && visibleTabs.length === 0 ? (
        <LoadingSkeleton.Terminal />
      ) : metaError && visibleTabs.length === 0 ? (
        // The tab list failed to load (a genuine failure — 404/503 is the
        // supported "no metadata storage" mode and settles as empty). Offer a
        // retry rather than an empty terminal: inventing tabs here is exactly
        // what the readiness gate exists to prevent.
        <div
          className={styles.metaErrorState}
          data-testid="terminal-metadata-error"
          role="alert"
        >
          <h2 className={styles.metaErrorHeading}>
            Couldn&apos;t load terminal tabs
          </h2>
          <p className={styles.metaErrorDescription}>{metaError.message}</p>
          <button
            type="button"
            className={styles.metaErrorRetry}
            onClick={() => void refetchTabMetadata()}
            data-testid="terminal-metadata-retry"
          >
            Retry
          </button>
        </div>
      ) : visibleTabs.length === 0 ? (
        <NoBackendsEmptyState
          {...(onNavigateToSettings != null && {
            onGoToSettings: onNavigateToSettings,
          })}
        />
      ) : hideTabs && paneTabs.length === 0 ? (
        <LoadingSkeleton.Terminal />
      ) : (
        <>
          {!hideTabs && !showEditorSplit && (
            <>
              <TerminalTabBar
                tabs={toTerminalTabs(visibleTabs)}
                activeTabId={activeTabId}
                onTabChange={handleTabChange}
                onTabClose={handleTabClose}
                onNewTab={handleNewTabLimit}
                availableBackends={selectableBackends}
                backendsLoading={configLoading}
                onBackendSelect={handleBackendSelect}
                onTabRename={handleTabRename}
                onDuplicateTab={handleDuplicateTab}
                maxTabsReached={visibleTabs.length >= MAX_TABS}
                onTabPin={handleTabPin}
                onCloseOthers={handleCloseOthers}
                onReorderTabs={handleReorderTabs}
                canSplitRight={canSplitRight}
                onSplitRight={splitActiveTab}
                totalTabCount={visibleTabs.length}
              />
              {cliSetupGuide && (
                <div
                  className={styles.cliSetupBanner}
                  data-testid="cli-setup-banner"
                >
                  <span
                    className={styles.cliSetupDot}
                    style={{ backgroundColor: cliSetupGuide.brandColor }}
                    aria-hidden="true"
                  />
                  <div className={styles.cliSetupBody}>
                    <div className={styles.cliSetupHeader}>
                      <strong>{cliSetupGuide.instructions.title}</strong>
                      <span>{cliSetupGuide.provider}</span>
                    </div>
                    <p>{cliSetupGuide.instructions.description}</p>
                    {cliSetupGuide.instructions.command && (
                      <code>{cliSetupGuide.instructions.command}</code>
                    )}
                    <span
                      className={`${styles.cliSetupStatus} ${
                        styles[cliSetupGuide.status]
                      }`}
                    >
                      {cliSetupGuide.status === "starting"
                        ? "Starting in terminal..."
                        : cliSetupGuide.status === "failed"
                          ? `Failed: ${cliSetupGuide.error ?? "setup did not start"}`
                          : cliSetupGuide.status === "manual"
                            ? "Manual steps shown in terminal"
                            : "Running in terminal"}
                    </span>
                  </div>
                  <div className={styles.cliSetupActions}>
                    <button
                      type="button"
                      className={styles.cliSetupButton}
                      onClick={() => runCliSetupCommand(cliSetupGuide)}
                      disabled={cliSetupGuide.status === "starting"}
                    >
                      {cliSetupGuide.status === "manual"
                        ? cliSetupGuide.instructions.buttonLabel
                        : cliSetupGuide.hasRun
                          ? "Run again"
                          : cliSetupGuide.instructions.buttonLabel}
                    </button>
                    <button
                      type="button"
                      className={styles.cliSetupDismiss}
                      onClick={refreshCliStatus}
                    >
                      Recheck
                    </button>
                    <button
                      type="button"
                      className={styles.cliSetupDismiss}
                      onClick={() => setCliSetupGuide(null)}
                      aria-label="Dismiss CLI setup"
                    >
                      Dismiss
                    </button>
                  </div>
                </div>
              )}
            </>
          )}
          {!hideTabs && showEditorSplit && (
            <div
              className={`${groupStyles.panes} ${groupStyles.split}`}
              data-testid="terminal-editor-groups"
              data-split="true"
            >
              {groups.map((group, groupIndex) => {
                const groupTabs = tabsForGroup(group.tabIds);
                const paneSide: "left" | "right" =
                  groupIndex === 0 ? "left" : "right";
                return (
                  <div
                    key={groupIndex}
                    className={groupStyles.paneCol}
                    onDragOver={handleGroupDragOver}
                    onDrop={() => handleGroupDrop(groupIndex)}
                  >
                    <TerminalTabBar
                      tabs={toTerminalTabs(groupTabs)}
                      activeTabId={group.activeTabId}
                      onTabChange={(tabId) =>
                        handleGroupTabChange(groupIndex, tabId)
                      }
                      onTabClose={handleTabClose}
                      onNewTab={handleNewTabLimit}
                      availableBackends={selectableBackends}
                      backendsLoading={configLoading}
                      onBackendSelect={handleBackendSelect}
                      onTabRename={handleTabRename}
                      onDuplicateTab={handleDuplicateTab}
                      maxTabsReached={visibleTabs.length >= MAX_TABS}
                      onTabPin={handleTabPin}
                      onCloseOthers={handleCloseOthers}
                      showToolbarActions={groupIndex === 0}
                      groupDrag={{
                        onDragStart: (tabId) =>
                          handleGroupDragStart(groupIndex, tabId),
                        onDragEnd: handleGroupDragEnd,
                      }}
                      dropTarget={{
                        onDragOver: handleGroupDragOver,
                        onDrop: () => handleGroupDrop(groupIndex),
                      }}
                      totalTabCount={visibleTabs.length}
                    />
                    {groupIndex === 0 && cliSetupGuide && (
                      <div
                        className={styles.cliSetupBanner}
                        data-testid="cli-setup-banner"
                      >
                        <span
                          className={styles.cliSetupDot}
                          style={{
                            backgroundColor: cliSetupGuide.brandColor,
                          }}
                          aria-hidden="true"
                        />
                        <div className={styles.cliSetupBody}>
                          <div className={styles.cliSetupHeader}>
                            <strong>{cliSetupGuide.instructions.title}</strong>
                            <span>{cliSetupGuide.provider}</span>
                          </div>
                          <p>{cliSetupGuide.instructions.description}</p>
                          {cliSetupGuide.instructions.command && (
                            <code>{cliSetupGuide.instructions.command}</code>
                          )}
                        </div>
                      </div>
                    )}
                    <div className={groupStyles.paneBody}>
                      {groupTabs.map((tab) => (
                        <div
                          key={tab.id}
                          className={groupStyles.paneSlot}
                          data-hidden={
                            group.activeTabId !== tab.id ? "true" : undefined
                          }
                          role="tabpanel"
                          aria-hidden={group.activeTabId !== tab.id}
                        >
                          {renderTerminalPane(
                            tab,
                            paneSide,
                            group.activeTabId === tab.id,
                          )}
                        </div>
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
          {!showEditorSplit && (
            <TerminalPaneArea
              tabs={paneTabs}
              activeTabId={paneActiveTabId}
              isSplitView={false}
              rightPaneTabId=""
              splitRatio={0.5}
              splitContainerRef={splitContainerRef}
              onSplitRatioChange={() => {}}
              onRightPaneTabChange={() => {}}
              renderPane={renderTerminalPane}
            />
          )}
        </>
      )}
      <div
        ref={liveRegionRef}
        className={styles.visuallyHidden}
        role="status"
        aria-live="polite"
        aria-atomic="true"
      />
    </div>
  );
}
