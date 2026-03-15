/**
 * TerminalView container component.
 * Orchestrates multiple tabbed terminal sessions with a search overlay.
 * All terminal instances are mounted in the DOM but only the active one
 * is visible, preserving xterm buffers and WebSocket connections.
 */

import { useState, useRef, useCallback, useEffect } from "react";

import type { IssueContext } from "@/api/terminal";
import { seedTerminalSession } from "@/api/terminal";
import { useBackendConfig } from "@/hooks/useBackendConfig";
import { useTerminalMetadata } from "@/hooks/useTerminalMetadata";

import { BackendPickerPrompt } from "./BackendPickerPrompt";
import { NotesBar } from "./NotesBar";
import { SearchBar } from "./SearchBar";
import { TerminalConnectionOverlay } from "./TerminalConnectionOverlay";
import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "./TerminalInstance";
import { TerminalInstance } from "./TerminalInstance";
import { TerminalTabBar } from "./TerminalTabBar";
import styles from "./TerminalView.module.css";

const MAX_TABS = 8;

/** Brand colors for each known backend. */
const BACKEND_BRAND_COLORS: Record<string, string> = {
  claude: "#D97706",
  codex: "#22c55e",
  opencode: "#3B82F6",
};
/** When backend is not in the map, leave brandColor undefined so CSS fallbacks apply. */

/**
 * Extract backend name from a session name.
 * Parses `lead-{backend}-{n}` pattern; falls back to defaultBackend.
 */
function getBackendFromSessionName(
  sessionName: string,
  defaultBackend?: string,
): string {
  const match = sessionName.match(/^lead-(.+)-\d+$/);
  if (match?.[1]) return match[1];
  return defaultBackend ?? "unknown";
}

/**
 * Generate an auto-incremented tab name for a given backend.
 * Returns `lead-{backend}-{n}` where n is max existing number + 1.
 */
function generateTabName(backend: string, existingTabs: TabState[]): string {
  const prefix = `lead-${backend}-`;
  let max = 0;
  for (const tab of existingTabs) {
    if (tab.sessionName.startsWith(prefix)) {
      const num = parseInt(tab.sessionName.slice(prefix.length), 10);
      if (!isNaN(num) && num > max) {
        max = num;
      }
    }
  }
  return `${prefix}${max + 1}`;
}

interface TabState {
  id: string;
  label: string;
  sessionName: string;
  connectionState: ConnectionState;
  backendName: string;
}

/**
 * Sanitize an issue ID into a valid session name.
 * Replaces dots with dashes, strips non-alphanumeric/hyphen/underscore chars.
 */
function sanitizeSessionName(issueId: string): string {
  return issueId.replace(/\./g, "-").replace(/[^a-zA-Z0-9_-]/g, "");
}

interface TerminalViewProps {
  isActive?: boolean;
  pendingIssueContext?: IssueContext | undefined;
  onIssueContextConsumed?: (() => void) | undefined;
}

export function TerminalView({
  isActive = true,
  pendingIssueContext,
  onIssueContextConsumed,
}: TerminalViewProps): JSX.Element {
  const {
    tabs: tabMetadata,
    createTab,
    updateNotes,
    isLoading: metaLoading,
  } = useTerminalMetadata();
  const { config, isLoading: configLoading } = useBackendConfig();

  const [tabs, setTabs] = useState<TabState[]>([]);
  const [activeTabId, setActiveTabId] = useState<string>("");
  const [isFullHeight, setIsFullHeight] = useState(false);
  const [isSearchOpen, setIsSearchOpen] = useState(false);
  const [searchTerm, setSearchTerm] = useState("");
  const [isSessionPromptOpen, setIsSessionPromptOpen] = useState(false);
  const [tabHasConnected, setTabHasConnected] = useState<Map<string, boolean>>(
    () => new Map(),
  );
  const instanceRefs = useRef<Map<string, TerminalInstanceHandle>>(new Map());
  const initializedRef = useRef(false);
  const activeTabIdRef = useRef(activeTabId);
  activeTabIdRef.current = activeTabId;

  // Initialize tabs from persisted metadata or auto-create from backend config
  useEffect(() => {
    if (initializedRef.current || metaLoading || configLoading) return;
    initializedRef.current = true;

    if (tabMetadata.length > 0) {
      // Returning user: restore tabs from persisted metadata, sorted by sort_order
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
          _sortOrder: m.sort_order,
        }))
        .sort((a, b) => (a._sortOrder ?? 999) - (b._sortOrder ?? 999))
        .map(({ _sortOrder: _, ...tab }) => tab);
      setTabs(restoredTabs);

      // Restore active tab from sessionStorage, falling back to first tab
      const savedActiveId = sessionStorage.getItem("terminal-active-tab");
      const restoredTab =
        savedActiveId && restoredTabs.find((t) => t.id === savedActiveId);
      setActiveTabId(
        restoredTab ? restoredTab.id : (restoredTabs[0]?.id ?? ""),
      );
    } else {
      // First open: auto-create one tab per available backend
      const backends = config?.available ?? [];
      if (backends.length === 0) {
        // Fallback: create a single default tab
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

      // Set claude tab as active, or first tab if claude unavailable
      const claudeTab = newTabs.find((t) =>
        t.sessionName.startsWith("lead-claude-"),
      );
      setActiveTabId(claudeTab?.id ?? newTabs[0]?.id ?? "");

      // Persist each auto-created tab via PUT (fire-and-forget with error logging)
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

  // Persist active tab to sessionStorage so it survives page refreshes
  useEffect(() => {
    if (activeTabId) {
      sessionStorage.setItem("terminal-active-tab", activeTabId);
    }
  }, [activeTabId]);

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
      if (e.key === "Escape" && isSearchOpen && !isSessionPromptOpen) {
        setIsSearchOpen(false);
        instanceRefs.current.get(activeTabId)?.clearSearch();
        setSearchTerm("");
      }
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [isActive, isSearchOpen, activeTabId, isSessionPromptOpen]);

  // Re-run search on tab switch while search is open
  useEffect(() => {
    if (isSearchOpen && searchTerm) {
      instanceRefs.current.get(activeTabId)?.search(searchTerm);
    }
  }, [activeTabId, isSearchOpen, searchTerm]);

  // Body scroll lock for full-height mode
  useEffect(() => {
    if (isFullHeight) {
      document.body.style.overflow = "hidden";
    }
    return () => {
      document.body.style.overflow = "";
    };
  }, [isFullHeight]);

  const handleReconnect = useCallback((tabId: string) => {
    instanceRefs.current.get(tabId)?.reconnect();
  }, []);

  const handleTabChange = useCallback((tabId: string) => {
    setActiveTabId(tabId);
  }, []);

  const handleTabClose = useCallback((tabId: string) => {
    setTabs((prev) => {
      if (prev.length <= 1) return prev;
      const idx = prev.findIndex((t) => t.id === tabId);
      if (idx === -1) return prev;
      const next = prev.filter((t) => t.id !== tabId);

      if (tabId === activeTabIdRef.current) {
        const newActiveIdx = idx > 0 ? idx - 1 : 0;
        const newActive = next[newActiveIdx];
        if (newActive) {
          setActiveTabId(newActive.id);
        }
      }

      return next;
    });
    instanceRefs.current.delete(tabId);
  }, []);

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
      instanceRefs.current.get(activeTabId)?.search(term);
    },
    [activeTabId],
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
  }, [activeTabId]);

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
        <div className={styles.loading}>Loading sessions...</div>
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
              };
            })}
            activeTabId={activeTabId}
            onTabChange={handleTabChange}
            onTabClose={handleTabClose}
            onNewTab={handleNewTabClick}
            onToggleFullHeight={handleToggleFullHeight}
            isFullHeight={isFullHeight}
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
                />
                <TerminalConnectionOverlay
                  connectionState={tab.connectionState}
                  hasConnected={tabHasConnected.get(tab.id) ?? false}
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
    </div>
  );
}
