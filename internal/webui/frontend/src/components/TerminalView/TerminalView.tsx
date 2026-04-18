import { useState, useRef, useCallback, useEffect, useMemo } from "react";

import type { IssueContext } from "@/hooks/api";
import { patchTerminalState } from "@/hooks/api";
import { LoadingSkeleton } from "@/components";
import { useBackendConfig } from "@/hooks/workspace";
import { useSessionRestore, useTerminalMetadata } from "@/hooks/terminal";

import {
  BackendPickerPrompt,
  NoBackendsEmptyState,
  useSplitView,
} from "./layout";
import { HelpPopover, useTerminalKeyboardShortcuts } from "./controls";
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
  useTabOrdering,
  useTabActions,
  useTabInit,
  useUnreadTracking,
  useWorkspaceTabState,
} from "./tabs";
import styles from "./TerminalView.module.css";

interface TerminalViewProps {
  isActive?: boolean;
  pendingIssueContext?: IssueContext | undefined;
  onIssueContextConsumed?: (() => void) | undefined;
  onActiveSessionCountChange?: (count: number) => void;
  onUnreadChange?: (hasAnyUnread: boolean) => void;
  onEscape?: () => void;
  onNavigateToSettings?: () => void;
  /** When set, opens or focuses an agent's terminal tab. */
  pendingAgentName?: string | undefined;
  /** Called after pendingAgentName has been processed. */
  onAgentNameConsumed?: (() => void) | undefined;
}

export function TerminalView({
  isActive = true,
  pendingIssueContext,
  onIssueContextConsumed,
  onActiveSessionCountChange,
  onUnreadChange,
  onEscape,
  onNavigateToSettings,
  pendingAgentName,
  onAgentNameConsumed,
}: TerminalViewProps): JSX.Element {
  const [tabs, setTabs] = useState<TabState[]>([]);
  const [activeTabId, setActiveTabId] = useState<string>("");
  const initializedRef = useRef(false);
  const { name: workspace, id: workspaceId } = useWorkspaceTabState({
    tabs,
    activeTabId,
    setTabs,
    setActiveTabId,
    initializedRef,
  });
  const {
    tabs: tabMetadata,
    createTab,
    updateLabel,
    updateNotes,
    updatePinned,
    deleteTab,
    reorderTabs: reorderTabMeta,
    isLoading: metaLoading,
  } = useTerminalMetadata(workspaceId);
  const { config, isLoading: configLoading } = useBackendConfig();
  const { activeTabId: restoredTabId, isRestoring } = useSessionRestore();

  const [isFullHeight, setIsFullHeight] = useState(false);
  const {
    isSplitView,
    splitRatio,
    rightPaneTabId,
    focusedPane: _focusedPane,
    setFocusedPane,
    canSplit,
    handleToggleSplit,
    handleSplitRatioChange,
    handleRightPaneTabChange,
  } = useSplitView({ tabs, activeTabId });
  const splitContainerRef = useRef<HTMLDivElement>(null);
  const [isSessionPromptOpen, setIsSessionPromptOpen] = useState(false);
  const [dismissedWelcome, setDismissedWelcome] = useState<boolean>(() => {
    try {
      if (localStorage.getItem("terminal-onboarding-dismissed") === "1")
        return true;
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i);
        if (key?.startsWith("terminal-welcome-dismissed-")) {
          localStorage.setItem("terminal-onboarding-dismissed", "1");
          return true;
        }
      }
    } catch {
      // localStorage unavailable — show banners every session
    }
    return false;
  });
  const [isHelpOpen, setIsHelpOpen] = useState(false);
  const instanceRefs = useRef<Map<string, TerminalInstanceHandle>>(new Map());
  const activeTabIdRef = useRef(activeTabId);
  activeTabIdRef.current = activeTabId;
  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;
  const workspaceIdRef = useRef(workspaceId);
  workspaceIdRef.current = workspaceId;

  // ARIA live region for screen reader announcements
  const liveRegionRef = useRef<HTMLDivElement>(null);
  const announce = useCallback((msg: string) => {
    if (liveRegionRef.current) liveRegionRef.current.textContent = msg;
  }, []);

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

  useTabInit({
    tabMetadata,
    metaLoading,
    config: config ?? undefined,
    configLoading,
    createTab,
    setTabs,
    setActiveTabId,
    initializedRef,
    workspace,
    isViewActive: isActive ?? false,
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
  }, [activeTabId]);

  // Report active (connected) session count to parent
  useEffect(() => {
    const count = tabs.filter((t) => t.connectionState === "connected").length;
    onActiveSessionCountChange?.(count);
  }, [tabs, onActiveSessionCountChange]);

  useEffect(() => {
    return () => {
      onActiveSessionCountChange?.(0);
    };
  }, [onActiveSessionCountChange]);

  // Body scroll lock for full-height mode
  useEffect(() => {
    if (isFullHeight) document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = "";
    };
  }, [isFullHeight]);

  const handleTabChange = useCallback(
    (tabId: string) => {
      setActiveTabId(tabId);
      clearTabUnread(tabId);
    },
    [clearTabUnread],
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

  const handleNewTabClick = useCallback(() => {
    if (tabs.length >= MAX_TABS) return;
    setIsSessionPromptOpen(true);
  }, [tabs.length]);

  const handleCycleTab = useCallback(
    (direction: "forward" | "backward") => {
      const currentTabs = tabsRef.current;
      if (currentTabs.length <= 1) return;
      const currentIdx = currentTabs.findIndex(
        (t) => t.id === activeTabIdRef.current,
      );
      const nextIdx =
        direction === "forward"
          ? currentIdx < currentTabs.length - 1
            ? currentIdx + 1
            : 0
          : currentIdx > 0
            ? currentIdx - 1
            : currentTabs.length - 1;
      const nextTab = currentTabs[nextIdx];
      if (nextTab) setActiveTabId(nextTab.id);
    },
    [tabsRef, activeTabIdRef],
  );

  const handleSwitchTabByIndex = useCallback(
    (index: number) => {
      const currentTabs = tabsRef.current;
      const targetTab = currentTabs[index];
      if (targetTab) handleTabChange(targetTab.id);
    },
    [tabsRef, handleTabChange],
  );

  const handleCloseActiveTab = useCallback(() => {
    handleTabClose(activeTabIdRef.current);
  }, [handleTabClose, activeTabIdRef]);

  useTerminalKeyboardShortcuts({
    isActive,
    tabsRef,
    activeTabIdRef,
    isSessionPromptOpen,
    dismissedWelcome,
    onCycleTab: handleCycleTab,
    onSwitchTabByIndex: handleSwitchTabByIndex,
    onNewTab: handleNewTabClick,
    onCloseTab: handleCloseActiveTab,
    onEscape,
    announce,
  });

  const handleBackendSelect = useCallback(
    (backend: string) => {
      setIsSessionPromptOpen(false);
      const { sessionName, label } = generateTabName(backend, tabs, workspace);
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
    [createTab, tabs, workspace, announce],
  );

  const handleSessionPromptCancel = useCallback(() => {
    setIsSessionPromptOpen(false);
  }, []);

  const handleDismissWelcome = useCallback(() => {
    setDismissedWelcome(true);
    try {
      localStorage.setItem("terminal-onboarding-dismissed", "1");
    } catch {
      // localStorage unavailable
    }
  }, []);

  // Auto-dismiss the welcome banner when sessions already exist.
  useEffect(() => {
    if (
      !dismissedWelcome &&
      !metaLoading &&
      tabMetadata &&
      tabMetadata.length > 0
    ) {
      handleDismissWelcome();
    }
  }, [
    dismissedWelcome,
    metaLoading,
    tabMetadata?.length,
    handleDismissWelcome,
  ]);

  const handleToggleHelp = useCallback(() => {
    setIsHelpOpen((prev) => !prev);
  }, []);

  const handleToggleFullHeight = useCallback(() => {
    setIsFullHeight((prev) => !prev);
  }, []);

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
  const renderTerminalPane = useCallback(
    (tab: TabState, pane: "left" | "right" | null) => {
      const paneIsActive =
        pane === "right" ? tab.id === rightPaneTabId : tab.id === activeTabId;
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
          dismissedWelcome={dismissedWelcome}
          onDismissWelcome={handleDismissWelcome}
          onExampleClick={(text) => {
            instanceRefs.current.get(tab.id)?.pasteText(text);
            handleDismissWelcome();
          }}
          notes={meta?.notes ?? ""}
          onSaveNotes={(text) => updateNotes(tab.sessionName, text)}
          isMetaLoading={metaLoading}
        />
      );
    },
    [
      activeTabId,
      rightPaneTabId,
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
      updateNotes,
      metaLoading,
      setFocusedLeft,
      setFocusedRight,
      dismissedWelcome,
      handleDismissWelcome,
    ],
  );

  const containerClassName = [
    styles.container,
    isFullHeight && styles.fullHeight,
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div className={containerClassName} data-testid="terminal-view">
      {(metaLoading || configLoading) && tabs.length === 0 ? (
        <LoadingSkeleton.Terminal />
      ) : tabs.length === 0 ? (
        <NoBackendsEmptyState
          {...(onNavigateToSettings != null && {
            onGoToSettings: onNavigateToSettings,
          })}
        />
      ) : (
        <>
          <TerminalTabBar
            tabs={tabs.map((t) => {
              const color = BACKEND_BRAND_COLORS[t.backendName];
              return {
                id: t.id,
                label: t.label,
                connectionState: t.connectionState,
                ...(color != null && { brandColor: color }),
                ...(tabUnread.get(t.id) && { hasUnread: true }),
                ...(t.pinned && { isPinned: true }),
              };
            })}
            activeTabId={activeTabId}
            onTabChange={handleTabChange}
            onTabClose={handleTabClose}
            onNewTab={handleNewTabClick}
            onToggleFullHeight={handleToggleFullHeight}
            isFullHeight={isFullHeight}
            onTabRename={handleTabRename}
            onDuplicateTab={handleDuplicateTab}
            maxTabsReached={tabs.length >= MAX_TABS}
            onTabPin={handleTabPin}
            onCloseOthers={handleCloseOthers}
            onReorderTabs={handleReorderTabs}
            isSplitView={isSplitView}
            canSplit={canSplit}
            onToggleSplit={handleToggleSplit}
            onHelpClick={handleToggleHelp}
          />
          <HelpPopover
            isOpen={isHelpOpen}
            onClose={() => setIsHelpOpen(false)}
          />
          <TerminalPaneArea
            tabs={tabs}
            activeTabId={activeTabId}
            isSplitView={isSplitView}
            rightPaneTabId={rightPaneTabId}
            splitRatio={splitRatio}
            splitContainerRef={splitContainerRef}
            onSplitRatioChange={handleSplitRatioChange}
            onRightPaneTabChange={handleRightPaneTabChange}
            renderPane={renderTerminalPane}
          />
        </>
      )}
      <BackendPickerPrompt
        isOpen={isSessionPromptOpen}
        availableBackends={config?.available ?? []}
        isLoading={configLoading}
        onSelect={handleBackendSelect}
        onCancel={handleSessionPromptCancel}
      />
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
