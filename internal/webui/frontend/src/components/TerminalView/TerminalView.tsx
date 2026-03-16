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
import {
  useRegisterEscapeLayer,
  LAYER_TERMINAL_SEARCH,
} from "@/hooks/useKeyboardShortcuts";
import { useSessionRestore } from "@/hooks/useSessionRestore";
import { useTerminalMetadata } from "@/hooks/useTerminalMetadata";

import { BackendPickerPrompt } from "./BackendPickerPrompt";
import { CopyToast } from "./CopyToast";
import { CrashOverlay } from "./CrashOverlay";
import { NotesBar } from "./NotesBar";
import { PasteConfirmDialog } from "./PasteConfirmDialog";
import {
  ReconnectingOverlay,
  type ReconnectOverlayState,
} from "./ReconnectingOverlay";
import { SearchBar } from "./SearchBar";
import { TerminalConnectionOverlay } from "./TerminalConnectionOverlay";
import { TerminalContextMenu } from "./TerminalContextMenu";
import type {
  ConnectionState,
  ContextMenuEvent,
  SearchResultInfo,
  TerminalInstanceHandle,
} from "./TerminalInstance";
import { TerminalInstance } from "./TerminalInstance";
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
import { useClipboard } from "./useClipboard";
import { useSessionManagement } from "./useCloseAllSessions";
import { useSplitView } from "./useSplitView";
import { useTabActions } from "./useTabActions";
import { useTabInit } from "./useTabInit";
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
}

export function TerminalView({
  isActive = true,
  pendingIssueContext,
  onIssueContextConsumed,
  onActiveSessionCountChange,
  onUnreadChange,
  onEscape,
  issueId,
}: TerminalViewProps): JSX.Element {
  const [tabs, setTabs] = useState<TabState[]>([]);
  const [activeTabId, setActiveTabId] = useState<string>("");
  const initializedRef = useRef(false);
  const workspace = useWorkspaceTabState({
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
    deleteTab,
    linkToIssue,
    isLoading: metaLoading,
  } = useTerminalMetadata(workspace);
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
  const [isSearchOpen, setIsSearchOpen] = useState(false);
  const [searchTerm, setSearchTerm] = useState("");
  const [caseSensitive, setCaseSensitive] = useState(false);
  const [useRegex, setUseRegex] = useState(false);
  const [searchResult, setSearchResult] = useState<SearchResultInfo | null>(
    null,
  );
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
  const instanceRefs = useRef<Map<string, TerminalInstanceHandle>>(new Map());
  const activeTabIdRef = useRef(activeTabId);
  activeTabIdRef.current = activeTabId;
  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;

  const {
    showCopyToast,
    pendingPasteText,
    handleCopyNotify,
    handlePasteRequest,
    handlePasteConfirm,
    handlePasteCancel,
  } = useClipboard(instanceRefs, activeTabIdRef);

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
  });

  // Apply server-restored active tab after initialization (only if restore completed before user interaction)
  const appliedRestoreRef = useRef(false);
  useEffect(() => {
    if (appliedRestoreRef.current || isRestoring || !restoredTabId) return;
    if (!initializedRef.current || tabs.length === 0) return;
    appliedRestoreRef.current = true;
    const match = tabs.find((t) => t.id === restoredTabId);
    if (match) setActiveTabId(restoredTabId);
  }, [restoredTabId, isRestoring, tabs]);

  // Persist active tab to sessionStorage and server (debounced)
  const patchDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    if (!activeTabId) return;
    sessionStorage.setItem("terminal-active-tab", activeTabId);
    if (patchDebounceRef.current) clearTimeout(patchDebounceRef.current);
    patchDebounceRef.current = setTimeout(() => {
      patchTerminalState({ active_tab: activeTabId }).catch(() => {});
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
        const tab = tabs.find((t) => t.id === tabId);
        if (tab && !seededSessionsRef.current.has(tab.sessionName)) {
          const seedCtx = pendingSeedRef.current.get(tab.sessionName);
          if (seedCtx) {
            seededSessionsRef.current.add(tab.sessionName);
            pendingSeedRef.current.delete(tab.sessionName);
            seedTerminalSession(tab.sessionName, seedCtx).catch((err) =>
              console.error(
                `Failed to seed terminal session ${tab.sessionName}:`,
                err,
              ),
            );
          }
        }
      }
    },
    [tabs],
  );

  // Keyboard shortcuts (only when view is active)
  useEffect(() => {
    if (!isActive) return;
    const handler = (e: KeyboardEvent) => {
      // Ctrl+Tab / Ctrl+Shift+Tab: cycle tabs
      // Alt+ArrowRight / Alt+ArrowLeft: Firefox-compatible fallback
      if (
        (e.ctrlKey && e.key === "Tab") ||
        (e.altKey && (e.key === "ArrowRight" || e.key === "ArrowLeft"))
      ) {
        e.preventDefault();
        const currentTabs = tabsRef.current;
        if (currentTabs.length <= 1) return;
        const goForward =
          e.key === "Tab" ? !e.shiftKey : e.key === "ArrowRight";
        const currentIdx = currentTabs.findIndex(
          (t) => t.id === activeTabIdRef.current,
        );
        const nextIdx = goForward
          ? currentIdx < currentTabs.length - 1
            ? currentIdx + 1
            : 0
          : currentIdx > 0
            ? currentIdx - 1
            : currentTabs.length - 1;
        const nextTab = currentTabs[nextIdx];
        if (nextTab) setActiveTabId(nextTab.id);
        return;
      }

      // Escape: return to previous view when nothing else to dismiss
      if (
        e.key === "Escape" &&
        !isSearchOpen &&
        !isSessionPromptOpen &&
        pendingPasteText === null
      ) {
        e.preventDefault();
        onEscape?.();
        return;
      }

      // Cmd+F / Ctrl+F: toggle search
      if ((e.metaKey || e.ctrlKey) && e.key === "f") {
        e.preventDefault();
        setIsSearchOpen((prev) => !prev);
      }
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [isActive, isSearchOpen, isSessionPromptOpen, pendingPasteText, onEscape]);

  // The tab targeted by search: in split mode, use the focused pane's tab
  const searchTargetTabId =
    isSplitView && focusedPane === "right" ? rightPaneTabId : activeTabId;
  const searchTargetTabIdRef = useRef(searchTargetTabId);
  searchTargetTabIdRef.current = searchTargetTabId;

  const closeTerminalSearch = useCallback(() => {
    setIsSearchOpen(false);
    instanceRefs.current.get(searchTargetTabId)?.clearSearch();
    setSearchTerm("");
    setSearchResult(null);
  }, [searchTargetTabId]);

  useRegisterEscapeLayer(
    LAYER_TERMINAL_SEARCH,
    closeTerminalSearch,
    isActive && isSearchOpen,
  );

  // Re-run search on tab switch while search is open
  useEffect(() => {
    if (isSearchOpen && searchTerm) {
      instanceRefs.current
        .get(searchTargetTabId)
        ?.search(searchTerm, { caseSensitive, regex: useRegex });
    }
  }, [searchTargetTabId, isSearchOpen, searchTerm, caseSensitive, useRegex]);

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
      fetchTerminalToken(sessionName)
        .then((token) => {
          if (!token) {
            // Token fetch failed — skip restart, just reconnect (creates new session)
            instanceRefs.current.get(tabId)?.reconnect();
            return;
          }
          return restartTerminalSession(sessionName, token).then(() => {
            instanceRefs.current.get(tabId)?.reconnect();
          });
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

  const handleNewTabClick = useCallback(() => {
    if (tabs.length >= MAX_TABS) return;
    setIsSessionPromptOpen(true);
  }, [tabs.length]);

  const handleBackendSelect = useCallback(
    (backend: string) => {
      setIsSessionPromptOpen(false);
      const name = generateTabName(backend, tabs);
      // Pre-create tmux session for shell tabs (WS handler also detects lead-shell-* as fallback)
      if (backend === "shell")
        spawnTerminalSession(name, backend).catch(() => {});
      setTabs((prev) => [
        ...prev,
        {
          id: name,
          label: name,
          sessionName: name,
          connectionState: "disconnected" as const,
          backendName: backend,
        },
      ]);
      setActiveTabId(name);
    },
    [tabs],
  );

  const handleSessionPromptCancel = useCallback(() => {
    setIsSessionPromptOpen(false);
  }, []);

  // Search handlers
  const handleSearch = useCallback(
    (term: string) => {
      setSearchTerm(term);
      instanceRefs.current
        .get(searchTargetTabId)
        ?.search(term, { caseSensitive, regex: useRegex });
    },
    [searchTargetTabId, caseSensitive, useRegex],
  );

  const handleFindNext = useCallback(() => {
    instanceRefs.current.get(searchTargetTabId)?.findNext();
  }, [searchTargetTabId]);

  const handleFindPrevious = useCallback(() => {
    instanceRefs.current.get(searchTargetTabId)?.findPrevious();
  }, [searchTargetTabId]);

  const handleSearchClose = useCallback(() => {
    setIsSearchOpen(false);
    instanceRefs.current.get(searchTargetTabId)?.clearSearch();
    setSearchTerm("");
    setSearchResult(null);
  }, [searchTargetTabId]);

  const handleToggleCaseSensitive = useCallback(() => {
    const next = !caseSensitive;
    setCaseSensitive(next);
    if (searchTerm) {
      instanceRefs.current
        .get(searchTargetTabId)
        ?.search(searchTerm, { caseSensitive: next, regex: useRegex });
    }
  }, [searchTargetTabId, searchTerm, caseSensitive, useRegex]);

  const handleToggleRegex = useCallback(() => {
    const next = !useRegex;
    setUseRegex(next);
    if (searchTerm) {
      instanceRefs.current
        .get(searchTargetTabId)
        ?.search(searchTerm, { caseSensitive, regex: next });
    }
  }, [searchTargetTabId, searchTerm, caseSensitive, useRegex]);

  // Only process search result changes from the search-targeted tab
  const handleSearchResultChange = useCallback(
    (tabId: string, result: SearchResultInfo | null) => {
      if (tabId === searchTargetTabIdRef.current) {
        setSearchResult(result);
      }
    },
    [],
  );

  // Search request from terminal (Ctrl+Shift+F)
  const handleSearchRequest = useCallback(() => {
    setIsSearchOpen((prev) => !prev);
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
    (tab: TabState, pane: "left" | "right" | null) => (
      <>
        <TerminalInstance
          ref={setInstanceRef(tab.id)}
          sessionName={tab.sessionName}
          isActive={
            pane === "right"
              ? tab.id === rightPaneTabId
              : tab.id === activeTabId
          }
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
          onTerminalFocus={
            pane === "left"
              ? setFocusedLeft
              : pane === "right"
                ? setFocusedRight
                : undefined
          }
        />
        {tab.crashReason != null ? (
          <CrashOverlay
            reason={tab.crashReason}
            onRestart={() => handleCrashRestart(tab.id, tab.sessionName)}
            onCloseTab={() => handleTabClose(tab.id)}
          />
        ) : (
          <>
            <TerminalConnectionOverlay
              connectionState={tab.connectionState}
              hasConnected={tabHasConnected.get(tab.id) ?? false}
              onReconnect={() => handleReconnect(tab.id)}
            />
            <ReconnectingOverlay
              state={tabReconnectState.get(tab.id) ?? null}
              onReconnect={() => handleReconnect(tab.id)}
            />
          </>
        )}
        <NotesBar
          notes={
            tabMetadata.find((m) => m.session_name === tab.sessionName)
              ?.notes ?? ""
          }
          onSave={(text) => updateNotes(tab.sessionName, text)}
          isLoading={metaLoading}
        />
      </>
    ),
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
            isSplitView={isSplitView}
            canSplit={canSplit}
            onToggleSplit={handleToggleSplit}
            onExport={() => {
              const t = tabs.find((x) => x.id === activeTabId);
              if (t)
                window.open(getExportUrl(t.sessionName), "_blank", "noopener");
            }}
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
    </div>
  );
}
