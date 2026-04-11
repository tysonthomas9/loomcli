import { useState, useRef, useCallback, useEffect } from "react";

import type { IssueContext } from "@/api/terminal";
import {
  spawnTerminalSession,
  patchTerminalState,
  getExportUrl,
} from "@/hooks/api";
import { LoadingSkeleton } from "@/components";
import { useBackendConfig } from "@/hooks/workspace";
import { useSessionRestore, useTerminalMetadata } from "@/hooks/terminal";

import {
  BackendPickerPrompt,
  NoBackendsEmptyState,
  useSplitView,
} from "./layout";
import {
  HelpPopover,
  CopyToast,
  PasteConfirmDialog,
  SearchBar,
  TerminalContextMenu,
  useContextMenuActions,
  useTerminalKeyboardShortcuts,
  useTerminalSearch,
} from "./controls";
import {
  TerminalPane,
  TerminalPaneArea,
  useClipboard,
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
  useSessionManagement,
  useTabActions,
  useTabInit,
  useUnreadTracking,
  useWorkspaceTabState,
} from "./tabs";
import styles from "./TerminalView.module.css";

/**
 * Descriptor for a tmux session that was pre-spawned on the backend (e.g. via
 * the /api/terminal/lead-session endpoint). TerminalView consumes this to
 * create a matching tab and attach to the existing session. The tab label is
 * derived from `sessionName`.
 */
export interface PendingLeadSession {
  sessionName: string;
  backend: string;
}

interface TerminalViewProps {
  isActive?: boolean;
  pendingIssueContext?: IssueContext | undefined;
  onIssueContextConsumed?: (() => void) | undefined;
  onActiveSessionCountChange?: (count: number) => void;
  onUnreadChange?: (hasAnyUnread: boolean) => void;
  onEscape?: () => void;
  issueId?: string;
  onNavigateToSettings?: () => void;
  /** When set, opens or focuses an agent's terminal tab. */
  pendingAgentName?: string | undefined;
  /** Called after pendingAgentName has been processed. */
  onAgentNameConsumed?: (() => void) | undefined;
  /**
   * When set, creates (or focuses) a tab for a lead session that was already
   * pre-spawned on the backend. The session runs `loom lead --backend X
   * --message <user text>` so no client-side seeding is required.
   */
  pendingLeadSession?: PendingLeadSession | undefined;
  /** Called after pendingLeadSession has been processed. */
  onLeadSessionConsumed?: (() => void) | undefined;
}

export function TerminalView({
  isActive = true,
  pendingIssueContext,
  onIssueContextConsumed,
  onActiveSessionCountChange,
  onUnreadChange,
  onEscape,
  issueId,
  onNavigateToSettings,
  pendingAgentName,
  onAgentNameConsumed,
  pendingLeadSession,
  onLeadSessionConsumed,
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
    linkToIssue,
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
    focusedPane,
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
      // Backward compat: migrate old per-backend keys
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

  // ARIA live region for screen reader announcements (useRef avoids re-renders)
  const liveRegionRef = useRef<HTMLDivElement>(null);
  const announce = useCallback((msg: string) => {
    if (liveRegionRef.current) liveRegionRef.current.textContent = msg;
  }, []);

  const {
    showCopyToast,
    pendingPasteText,
    handleCopyNotify,
    handlePasteRequest,
    handlePasteConfirm,
    handlePasteCancel,
  } = useClipboard(instanceRefs, activeTabIdRef);

  const {
    isSearchOpen,
    searchTerm,
    caseSensitive,
    useRegex,
    searchResult,
    handleSearch,
    handleFindNext,
    handleFindPrevious,
    handleSearchClose,
    handleToggleCaseSensitive,
    handleToggleRegex,
    handleSearchResultChange,
    handleSearchRequest,
  } = useTerminalSearch({
    instanceRefs,
    activeTabId,
    isSplitView,
    focusedPane,
    rightPaneTabId,
    isActive,
  });

  // Hook ordering: useSessionSeeding before useConnectionState
  // so trySeedOnConnect is available as the onTabConnected callback.
  const { trySeedOnConnect } = useSessionSeeding({
    pendingIssueContext,
    onIssueContextConsumed,
    pendingAgentName,
    onAgentNameConsumed,
    pendingLeadSession,
    onLeadSessionConsumed,
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
    workspaceId,
    onTabConnected: trySeedOnConnect,
  });

  const { tabUnread, handleOutput, clearTabUnread } = useUnreadTracking({
    activeTabIdRef,
    isActive,
    onUnreadChange,
  });

  const {
    contextMenu,
    handleContextMenu,
    handleContextMenuClose,
    handleContextMenuCopy,
    handleContextMenuPaste,
    handleContextMenuSelectAll,
  } = useContextMenuActions({
    instanceRefs,
    activeTabId,
    handleCopyNotify,
    handlePasteRequest,
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

  // Apply server-restored active tab after initialization (only if restore completed before user interaction)
  const appliedRestoreRef = useRef(false);
  useEffect(() => {
    if (appliedRestoreRef.current || isRestoring || !restoredTabId) return;
    if (!initializedRef.current || tabs.length === 0) return;
    appliedRestoreRef.current = true;
    // If useTabInit already set the correct tab (from sessionStorage), skip
    // the redundant setActiveTabId to avoid a flicker-causing re-render.
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

  // Reset count on unmount
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
    isSearchOpen,
    isSessionPromptOpen,
    pendingPasteText,
    dismissedWelcome,
    onCycleTab: handleCycleTab,
    onSwitchTabByIndex: handleSwitchTabByIndex,
    onNewTab: handleNewTabClick,
    onCloseTab: handleCloseActiveTab,
    onToggleSearch: handleSearchRequest,
    onEscape,
    announce,
  });

  const handleBackendSelect = useCallback(
    (backend: string) => {
      setIsSessionPromptOpen(false);
      const { sessionName, label } = generateTabName(backend, tabs, workspace);
      // Pre-create tmux session for shell tabs (WS handler also detects lead-shell-* as fallback)
      if (backend === "shell")
        spawnTerminalSession(workspaceId, sessionName, backend).catch(() => {});
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
    [tabs, workspace, announce],
  );

  const handleSessionPromptCancel = useCallback(() => {
    setIsSessionPromptOpen(false);
  }, []);

  const handleCloseAll = useSessionManagement({
    workspaceId,
    setTabs,
    setActiveTabId,
    instanceRefs,
    initializedRef,
    isActive,
    issueId,
    tabs,
    createTab,
    linkToIssue,
    backendName: config?.backend ?? "unknown",
  });

  const handleDismissWelcome = useCallback(() => {
    setDismissedWelcome(true);
    try {
      localStorage.setItem("terminal-onboarding-dismissed", "1");
    } catch {
      // localStorage unavailable
    }
  }, []);

  // Auto-dismiss the welcome banner if terminal sessions already exist.
  // This prevents the popup from blocking the terminal on first visit when
  // sessions are already running from a previous page load.
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

  const renderTerminalPane = useCallback(
    (tab: TabState, pane: "left" | "right" | null) => {
      const paneIsActive =
        pane === "right" ? tab.id === rightPaneTabId : tab.id === activeTabId;
      return (
        <TerminalPane
          tab={tab}
          isActive={paneIsActive}
          instanceRef={setInstanceRef(tab.id)}
          onConnectionStateChange={(state, hasConnected) =>
            handleConnectionStateChange(tab.id, state, hasConnected)
          }
          onCopyNotify={handleCopyNotify}
          onPasteRequest={handlePasteRequest}
          onSearchRequest={handleSearchRequest}
          onContextMenu={handleContextMenu}
          onReconnectStateChange={(state) =>
            handleReconnectStateChange(tab.id, state)
          }
          onOutput={() => handleOutput(tab.id)}
          onSearchResultChange={(result) =>
            handleSearchResultChange(tab.id, result)
          }
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
          notes={
            tabMetadata.find((m) => m.session_name === tab.sessionName)
              ?.notes ?? ""
          }
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
      handleCopyNotify,
      handlePasteRequest,
      handleSearchRequest,
      handleContextMenu,
      handleReconnectStateChange,
      handleOutput,
      handleSearchResultChange,
      handleBackendCrash,
      handleCrashRestart,
      handleTabClose,
      handleReconnect,
      tabHasConnected,
      tabReconnectState,
      tabMetadata,
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
            onCloseAll={handleCloseAll}
            onTabRename={handleTabRename}
            onDuplicateTab={handleDuplicateTab}
            maxTabsReached={tabs.length >= MAX_TABS}
            onTabPin={handleTabPin}
            onCloseOthers={handleCloseOthers}
            onReorderTabs={handleReorderTabs}
            isSplitView={isSplitView}
            canSplit={canSplit}
            onToggleSplit={handleToggleSplit}
            onExport={() => {
              const t = tabs.find((x) => x.id === activeTabId);
              if (t)
                window.open(
                  getExportUrl(workspaceId, t.sessionName),
                  "_blank",
                  "noopener",
                );
            }}
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

          {isSearchOpen && (
            <SearchBar
              value={searchTerm}
              onSearch={handleSearch}
              onFindNext={handleFindNext}
              onFindPrevious={handleFindPrevious}
              onClose={handleSearchClose}
              matchIndex={searchResult?.resultIndex ?? null}
              matchCount={searchResult?.resultCount ?? null}
              caseSensitive={caseSensitive}
              regex={useRegex}
              onToggleCaseSensitive={handleToggleCaseSensitive}
              onToggleRegex={handleToggleRegex}
            />
          )}
        </>
      )}
      <BackendPickerPrompt
        isOpen={isSessionPromptOpen}
        availableBackends={config?.available ?? []}
        isLoading={configLoading}
        onSelect={handleBackendSelect}
        onCancel={handleSessionPromptCancel}
      />
      <PasteConfirmDialog
        isOpen={pendingPasteText !== null}
        text={pendingPasteText ?? ""}
        onConfirm={handlePasteConfirm}
        onCancel={handlePasteCancel}
      />
      <CopyToast visible={showCopyToast} />
      {contextMenu && (
        <TerminalContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          hasSelection={contextMenu.hasSelection}
          onCopy={handleContextMenuCopy}
          onPaste={handleContextMenuPaste}
          onSelectAll={handleContextMenuSelectAll}
          onClose={handleContextMenuClose}
        />
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
