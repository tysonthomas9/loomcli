import { useState, useRef, useCallback, useEffect, useMemo } from "react";

import type { IssueContext } from "@/api/terminal";
import {
  seedTerminalSession,
  spawnTerminalSession,
  patchTerminalState,
  restartTerminalSession,
  fetchTerminalToken,
  getExportUrl,
} from "@/api/terminal";
import { LoadingSkeleton } from "@/components";
import { useBackendConfig } from "@/hooks/useBackendConfig";
import { useSessionRestore } from "@/hooks/useSessionRestore";
import { useTerminalMetadata } from "@/hooks/useTerminalMetadata";

import { BackendPickerPrompt } from "./BackendPickerPrompt";
import { HelpPopover } from "./HelpPopover";
import { NoBackendsEmptyState } from "./NoBackendsEmptyState";
import { CopyToast } from "./CopyToast";
import { PasteConfirmDialog } from "./PasteConfirmDialog";
import type { ReconnectOverlayState } from "./ReconnectingOverlay";
import { SearchBar } from "./SearchBar";
import { TerminalContextMenu } from "./TerminalContextMenu";
import type {
  ConnectionState,
  ContextMenuEvent,
  TerminalInstanceHandle,
} from "./TerminalInstance";
import { TerminalPane } from "./TerminalPane";
import { TerminalTabBar } from "./TerminalTabBar";
import { SplitDivider } from "./SplitDivider";
import { SplitPaneSelector } from "./SplitPaneSelector";
import {
  MAX_TABS,
  BACKEND_BRAND_COLORS,
  type TabState,
  generateTabName,
  sanitizeSessionName,
} from "./terminalTabUtils";
import { useTabOrdering } from "./useTabOrdering";
import { useClipboard } from "./useClipboard";
import { useSessionManagement } from "./useCloseAllSessions";
import { useSplitView } from "./useSplitView";
import { useTabActions } from "./useTabActions";
import { useTabInit } from "./useTabInit";
import { useTerminalKeyboardShortcuts } from "./useTerminalKeyboardShortcuts";
import { useTerminalSearch } from "./useTerminalSearch";
import { useWorkspaceTabState } from "./useWorkspaceTabState";
import styles from "./TerminalView.module.css";

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
  const { activeTabId: restoredTabId, isRestoring } =
    useSessionRestore(workspaceId);

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
  const [contextMenu, setContextMenu] = useState<ContextMenuEvent | null>(null);
  const [tabHasConnected, setTabHasConnected] = useState<Map<string, boolean>>(
    () => new Map(),
  );
  const [tabReconnectState, setTabReconnectState] = useState<
    Map<string, ReconnectOverlayState>
  >(() => new Map());
  const [tabUnread, setTabUnread] = useState<Map<string, boolean>>(
    () => new Map(),
  );
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

  const handleOutput = useCallback((tabId: string) => {
    if (tabId !== activeTabIdRef.current) {
      setTabUnread((prev) => {
        if (prev.get(tabId)) return prev;
        const next = new Map(prev);
        next.set(tabId, true);
        return next;
      });
    }
  }, []);

  // Compute aggregate unread and notify parent
  const hasAnyUnread = useMemo(() => {
    for (const val of tabUnread.values()) {
      if (val) return true;
    }
    return false;
  }, [tabUnread]);

  useEffect(() => {
    onUnreadChange?.(hasAnyUnread);
  }, [hasAnyUnread, onUnreadChange]);

  // When view becomes active, clear unread on the currently active tab
  const prevIsActiveRef = useRef(isActive);
  useEffect(() => {
    if (isActive && !prevIsActiveRef.current) {
      setTabUnread((prev) => {
        const currentTab = activeTabIdRef.current;
        if (!prev.get(currentTab)) return prev;
        const next = new Map(prev);
        next.delete(currentTab);
        return next;
      });
    }
    prevIsActiveRef.current = isActive;
  }, [isActive]);

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

  // Track sessions that have been seeded so we don't re-seed on reconnect
  const seededSessionsRef = useRef<Set<string>>(new Set());
  // Store pending seed context by session name (survives across renders)
  const pendingSeedRef = useRef<Map<string, IssueContext>>(new Map());

  // Handle pending issue context: create or switch to issue tab, then seed
  useEffect(() => {
    if (!pendingIssueContext || !initializedRef.current) return;

    const sessionName = `issue-${sanitizeSessionName(pendingIssueContext.issue_id)}`;

    // Check if tab already exists — switch to it without re-seeding
    const existingTab = tabs.find((t) => t.sessionName === sessionName);
    if (existingTab) {
      setActiveTabId(existingTab.id);
      onIssueContextConsumed?.();
      return;
    }

    // Store seed context in ref before consuming the prop
    pendingSeedRef.current.set(sessionName, pendingIssueContext);

    // Create new tab
    const newTab: TabState = {
      id: sessionName,
      label: `issue-${sanitizeSessionName(pendingIssueContext.issue_id)}`,
      sessionName,
      connectionState: "disconnected" as ConnectionState,
      backendName: config?.backend ?? "unknown",
    };
    setTabs((prev) => [...prev, newTab]);
    setActiveTabId(sessionName);

    // Persist tab metadata (fire-and-forget)
    createTab(sessionName, newTab.label, tabs.length).catch((err) =>
      console.error(`Failed to persist issue tab ${sessionName}:`, err),
    );

    onIssueContextConsumed?.();
  }, [pendingIssueContext, tabs, createTab, onIssueContextConsumed]);

  // Handle pending agent name: create or switch to agent terminal tab
  useEffect(() => {
    if (!pendingAgentName || !initializedRef.current) return;

    const sessionName = `agent-${sanitizeSessionName(pendingAgentName)}`;

    // Check if tab already exists — switch to it
    const existingTab = tabs.find((t) => t.sessionName === sessionName);
    if (existingTab) {
      setActiveTabId(existingTab.id);
      onAgentNameConsumed?.();
      return;
    }

    // Max tabs check
    if (tabs.length >= MAX_TABS) {
      onAgentNameConsumed?.();
      return;
    }

    // Create new agent terminal tab
    const newTab: TabState = {
      id: sessionName,
      label: `agent-${pendingAgentName}`,
      sessionName,
      connectionState: "disconnected" as ConnectionState,
      backendName: "agent",
      agentName: pendingAgentName,
    };
    setTabs((prev) => [...prev, newTab]);
    setActiveTabId(sessionName);

    // Persist tab metadata (fire-and-forget)
    createTab(sessionName, newTab.label, tabs.length).catch((err) =>
      console.error(`Failed to persist agent tab ${sessionName}:`, err),
    );

    onAgentNameConsumed?.();
  }, [pendingAgentName, tabs, createTab, onAgentNameConsumed]);

  // Seed the session when it connects for the first time
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

      // If this tab just connected and has pending seed data, seed it
      if (state === "connected") {
        const tab = tabsRef.current.find((t) => t.id === tabId);
        if (tab && !seededSessionsRef.current.has(tab.sessionName)) {
          const seedCtx = pendingSeedRef.current.get(tab.sessionName);
          if (seedCtx) {
            seededSessionsRef.current.add(tab.sessionName);
            pendingSeedRef.current.delete(tab.sessionName);
            seedTerminalSession(
              workspaceIdRef.current,
              tab.sessionName,
              seedCtx,
            ).catch((err) =>
              console.error(
                `Failed to seed terminal session ${tab.sessionName}:`,
                err,
              ),
            );
          }
        }
      }
    },
    [],
  );

  // Body scroll lock for full-height mode
  useEffect(() => {
    if (isFullHeight) document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = "";
    };
  }, [isFullHeight]);

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

  const handleReconnect = useCallback((tabId: string) => {
    instanceRefs.current.get(tabId)?.reconnect();
  }, []);

  const handleBackendCrash = useCallback((tabId: string, reason: string) => {
    setTabs((prev) =>
      prev.map((t) => (t.id === tabId ? { ...t, crashReason: reason } : t)),
    );
  }, []);

  const handleCrashRestart = useCallback(
    (tabId: string, sessionName: string) => {
      // Clear crash state, call restart endpoint, then reconnect
      setTabs((prev) =>
        prev.map((t) => (t.id === tabId ? { ...t, crashReason: null } : t)),
      );
      fetchTerminalToken(workspaceId, sessionName)
        .then((token) => {
          if (!token) {
            // Token fetch failed — skip restart, just reconnect (creates new session)
            instanceRefs.current.get(tabId)?.reconnect();
            return;
          }
          return restartTerminalSession(workspaceId, sessionName, token).then(
            () => {
              instanceRefs.current.get(tabId)?.reconnect();
            },
          );
        })
        .catch((err) => {
          console.error(`Failed to restart session ${sessionName}:`, err);
          // Still try to reconnect — the WebSocket reconnect will create a new session
          instanceRefs.current.get(tabId)?.reconnect();
        });
    },
    [],
  );

  const handleTabChange = useCallback((tabId: string) => {
    setActiveTabId(tabId);
    setTabUnread((prev) => {
      if (!prev.get(tabId)) return prev;
      const next = new Map(prev);
      next.delete(tabId);
      return next;
    });
  }, []);

  const { handleTabClose, handleDuplicateTab, handleTabRename } = useTabActions(
    {
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

  // Context menu handlers
  const handleContextMenu = useCallback((event: ContextMenuEvent) => {
    setContextMenu(event);
  }, []);

  const handleContextMenuClose = useCallback(() => {
    setContextMenu(null);
  }, []);

  const handleContextMenuCopy = useCallback(() => {
    const instance = instanceRefs.current.get(activeTabId);
    if (instance) {
      const sel = instance.getSelection();
      if (sel) {
        navigator.clipboard
          .writeText(sel)
          .then(() => handleCopyNotify())
          .catch(() => {});
      }
    }
    setContextMenu(null);
    instanceRefs.current.get(activeTabId)?.focus();
  }, [activeTabId, handleCopyNotify]);

  const handleContextMenuPaste = useCallback(() => {
    setContextMenu(null);
    handlePasteRequest();
  }, [handlePasteRequest]);

  const handleContextMenuSelectAll = useCallback(() => {
    instanceRefs.current.get(activeTabId)?.selectAll();
    setContextMenu(null);
  }, [activeTabId]);

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
    if (!dismissedWelcome && !metaLoading && tabMetadata.length > 0) {
      handleDismissWelcome();
    }
  }, [dismissedWelcome, metaLoading, tabMetadata.length, handleDismissWelcome]);

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
          <div className={styles.terminalsContainer}>
            {isSplitView && rightPaneTabId ? (
              <div
                ref={splitContainerRef}
                className={styles.splitContainer}
                style={{
                  gridTemplateColumns: `${splitRatio}fr auto ${1 - splitRatio}fr`,
                }}
                data-testid="split-container"
              >
                <div className={styles.splitPaneLeft}>
                  {tabs.map((tab) => (
                    <div
                      key={tab.id}
                      className={styles.terminalPaneSplit}
                      style={{
                        display: tab.id === activeTabId ? "flex" : "none",
                      }}
                      role="tabpanel"
                      id={`terminal-panel-${tab.id}`}
                      aria-labelledby={`terminal-tab-${tab.id}`}
                    >
                      {renderTerminalPane(tab, "left")}
                    </div>
                  ))}
                </div>
                <SplitDivider
                  onRatioChange={handleSplitRatioChange}
                  containerRef={splitContainerRef}
                />
                <div className={styles.splitPaneRight}>
                  <SplitPaneSelector
                    tabs={tabs.map((t) => ({
                      id: t.id,
                      label: t.label,
                      brandColor: BACKEND_BRAND_COLORS[t.backendName],
                    }))}
                    activeLeftTabId={activeTabId}
                    rightPaneTabId={rightPaneTabId}
                    onTabChange={handleRightPaneTabChange}
                  />
                  {tabs.map((tab) => (
                    <div
                      key={tab.id}
                      className={styles.terminalPaneSplit}
                      style={{
                        display: tab.id === rightPaneTabId ? "flex" : "none",
                      }}
                      role="tabpanel"
                      id={`terminal-panel-right-${tab.id}`}
                    >
                      {renderTerminalPane(tab, "right")}
                    </div>
                  ))}
                </div>
              </div>
            ) : (
              tabs.map((tab) => (
                <div
                  key={tab.id}
                  className={styles.terminalPane}
                  style={{
                    display: tab.id === activeTabId ? "flex" : "none",
                  }}
                  role="tabpanel"
                  id={`terminal-panel-${tab.id}`}
                  aria-labelledby={`terminal-tab-${tab.id}`}
                >
                  {renderTerminalPane(tab, null)}
                </div>
              ))
            )}
          </div>
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
