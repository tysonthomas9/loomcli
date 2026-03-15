/**
 * TerminalView container component.
 * Orchestrates multiple tabbed terminal sessions with a search overlay.
 * All terminal instances are mounted in the DOM but only the active one
 * is visible, preserving xterm buffers and WebSocket connections.
 */

import { useState, useRef, useCallback, useEffect, useMemo } from "react";

import type { IssueContext } from "@/api/terminal";
import { seedTerminalSession, patchTerminalState } from "@/api/terminal";
import { LoadingSkeleton } from "@/components";
import { useBackendConfig } from "@/hooks/useBackendConfig";
import { useSessionRestore } from "@/hooks/useSessionRestore";
import { useTerminalMetadata } from "@/hooks/useTerminalMetadata";

import { BackendPickerPrompt } from "./BackendPickerPrompt";
import { CopyToast } from "./CopyToast";
import { NotesBar } from "./NotesBar";
import { PasteConfirmDialog } from "./PasteConfirmDialog";
import {
  ReconnectingOverlay,
  type ReconnectOverlayState,
} from "./ReconnectingOverlay";
import { SearchBar } from "./SearchBar";
import { TerminalConnectionOverlay } from "./TerminalConnectionOverlay";
import type {
  ConnectionState,
  SearchResultInfo,
  TerminalInstanceHandle,
} from "./TerminalInstance";
import { TerminalInstance } from "./TerminalInstance";
import { TerminalTabBar } from "./TerminalTabBar";
import {
  MAX_TABS,
  BACKEND_BRAND_COLORS,
  type TabState,
  generateTabName,
  sanitizeSessionName,
} from "./terminalTabUtils";
import { useClipboard } from "./useClipboard";
import { useSessionManagement } from "./useCloseAllSessions";
import { useTabActions } from "./useTabActions";
import { useTabInit } from "./useTabInit";
import styles from "./TerminalView.module.css";

interface TerminalViewProps {
  isActive?: boolean;
  pendingIssueContext?: IssueContext | undefined;
  onIssueContextConsumed?: (() => void) | undefined;
  onActiveSessionCountChange?: (count: number) => void;
  onUnreadChange?: (hasAnyUnread: boolean) => void;
  issueId?: string;
}

export function TerminalView({
  isActive = true,
  pendingIssueContext,
  onIssueContextConsumed,
  onActiveSessionCountChange,
  onUnreadChange,
  issueId,
}: TerminalViewProps): JSX.Element {
  const {
    tabs: tabMetadata,
    createTab,
    updateLabel,
    updateNotes,
    deleteTab,
    linkToIssue,
    isLoading: metaLoading,
  } = useTerminalMetadata();
  const { config, isLoading: configLoading } = useBackendConfig();
  const { activeTabId: restoredTabId, isRestoring } = useSessionRestore();

  const [tabs, setTabs] = useState<TabState[]>([]);
  const [activeTabId, setActiveTabId] = useState<string>("");
  const [isFullHeight, setIsFullHeight] = useState(false);
  const [isSearchOpen, setIsSearchOpen] = useState(false);
  const [searchTerm, setSearchTerm] = useState("");
  const [caseSensitive, setCaseSensitive] = useState(false);
  const [useRegex, setUseRegex] = useState(false);
  const [searchResult, setSearchResult] = useState<SearchResultInfo | null>(
    null,
  );
  const [isSessionPromptOpen, setIsSessionPromptOpen] = useState(false);
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
  const initializedRef = useRef(false);
  const activeTabIdRef = useRef(activeTabId);
  activeTabIdRef.current = activeTabId;

  const {
    showCopyToast,
    pendingPasteText,
    handleCopyNotify,
    handlePasteRequest,
    handlePasteConfirm,
    handlePasteCancel,
  } = useClipboard(instanceRefs, activeTabIdRef);

  // Track output on non-active tabs as unread
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

  // Cmd+F / Ctrl+F intercept (only when view is active)
  useEffect(() => {
    if (!isActive) return;
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "f") {
        e.preventDefault();
        setIsSearchOpen((prev) => !prev);
      }
      if (
        e.key === "Escape" &&
        isSearchOpen &&
        !isSessionPromptOpen &&
        pendingPasteText === null
      ) {
        setIsSearchOpen(false);
        instanceRefs.current.get(activeTabId)?.clearSearch();
        setSearchTerm("");
        setSearchResult(null);
      }
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [
    isActive,
    isSearchOpen,
    activeTabId,
    isSessionPromptOpen,
    pendingPasteText,
  ]);

  // Re-run search on tab switch while search is open
  useEffect(() => {
    if (isSearchOpen && searchTerm) {
      instanceRefs.current
        .get(activeTabId)
        ?.search(searchTerm, { caseSensitive, regex: useRegex });
    }
  }, [activeTabId, isSearchOpen, searchTerm, caseSensitive, useRegex]);

  // Body scroll lock for full-height mode
  useEffect(() => {
    if (isFullHeight) {
      document.body.style.overflow = "hidden";
    }
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
        .get(activeTabId)
        ?.search(term, { caseSensitive, regex: useRegex });
    },
    [activeTabId, caseSensitive, useRegex],
  );

  const handleFindNext = useCallback(() => {
    instanceRefs.current.get(activeTabId)?.findNext();
  }, [activeTabId]);

  const handleFindPrevious = useCallback(() => {
    instanceRefs.current.get(activeTabId)?.findPrevious();
  }, [activeTabId]);

  const handleSearchClose = useCallback(() => {
    setIsSearchOpen(false);
    instanceRefs.current.get(activeTabId)?.clearSearch();
    setSearchTerm("");
    setSearchResult(null);
  }, [activeTabId]);

  const handleToggleCaseSensitive = useCallback(() => {
    const next = !caseSensitive;
    setCaseSensitive(next);
    if (searchTerm) {
      instanceRefs.current
        .get(activeTabId)
        ?.search(searchTerm, { caseSensitive: next, regex: useRegex });
    }
  }, [activeTabId, searchTerm, caseSensitive, useRegex]);

  const handleToggleRegex = useCallback(() => {
    const next = !useRegex;
    setUseRegex(next);
    if (searchTerm) {
      instanceRefs.current
        .get(activeTabId)
        ?.search(searchTerm, { caseSensitive, regex: next });
    }
  }, [activeTabId, searchTerm, caseSensitive, useRegex]);

  // Only process search result changes from the active tab
  const handleSearchResultChange = useCallback(
    (tabId: string, result: SearchResultInfo | null) => {
      if (tabId === activeTabIdRef.current) {
        setSearchResult(result);
      }
    },
    [],
  );

  // Search request from terminal (Ctrl+Shift+F)
  const handleSearchRequest = useCallback(() => {
    setIsSearchOpen((prev) => !prev);
  }, []);

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
          />
          <div className={styles.terminalsContainer}>
            {tabs.map((tab) => (
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
                <TerminalInstance
                  ref={setInstanceRef(tab.id)}
                  sessionName={tab.sessionName}
                  isActive={tab.id === activeTabId}
                  onConnectionStateChange={(state, hasConnected) =>
                    handleConnectionStateChange(tab.id, state, hasConnected)
                  }
                  onCopyNotify={handleCopyNotify}
                  onPasteRequest={handlePasteRequest}
                  onSearchRequest={handleSearchRequest}
                  onReconnectStateChange={(state) =>
                    handleReconnectStateChange(tab.id, state)
                  }
                  onOutput={() => handleOutput(tab.id)}
                  onSearchResultChange={(result) =>
                    handleSearchResultChange(tab.id, result)
                  }
                />
                <TerminalConnectionOverlay
                  connectionState={tab.connectionState}
                  hasConnected={tabHasConnected.get(tab.id) ?? false}
                  onReconnect={() => handleReconnect(tab.id)}
                />
                <ReconnectingOverlay
                  state={tabReconnectState.get(tab.id) ?? null}
                  onReconnect={() => handleReconnect(tab.id)}
                />
                <NotesBar
                  notes={
                    tabMetadata.find((m) => m.session_name === tab.sessionName)
                      ?.notes ?? ""
                  }
                  onSave={(text) => updateNotes(tab.sessionName, text)}
                  isLoading={metaLoading}
                />
              </div>
            ))}
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
    </div>
  );
}
