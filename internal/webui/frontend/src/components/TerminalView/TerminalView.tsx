/**
 * TerminalView container component.
 * Orchestrates multiple tabbed terminal sessions with a search overlay.
 * All terminal instances are mounted in the DOM but only the active one
 * is visible, preserving xterm buffers and WebSocket connections.
 */

import { useState, useRef, useCallback, useEffect } from "react";

import { useTerminalMetadata } from "@/hooks/useTerminalMetadata";
import { useTerminalSessions } from "@/hooks/useTerminalSessions";

import { NotesBar } from "./NotesBar";
import { SearchBar } from "./SearchBar";
import { SessionNamePrompt } from "./SessionNamePrompt";
import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "./TerminalInstance";
import { TerminalInstance } from "./TerminalInstance";
import { TerminalTabBar } from "./TerminalTabBar";
import styles from "./TerminalView.module.css";

const MAX_TABS = 8;

interface TabState {
  id: string;
  label: string;
  sessionName: string;
  connectionState: ConnectionState;
}

interface TerminalViewProps {
  isActive?: boolean;
}

export function TerminalView({
  isActive = true,
}: TerminalViewProps): JSX.Element {
  const { sessions, isLoading } = useTerminalSessions();
  const {
    tabs: tabMetadata,
    updateNotes,
    isLoading: metaLoading,
  } = useTerminalMetadata();

  const [tabs, setTabs] = useState<TabState[]>([]);
  const [activeTabId, setActiveTabId] = useState<string>("");
  const [isFullHeight, setIsFullHeight] = useState(false);
  const [isSearchOpen, setIsSearchOpen] = useState(false);
  const [searchTerm, setSearchTerm] = useState("");
  const [isSessionPromptOpen, setIsSessionPromptOpen] = useState(false);
  const instanceRefs = useRef<Map<string, TerminalInstanceHandle>>(new Map());
  const initializedRef = useRef(false);
  const activeTabIdRef = useRef(activeTabId);
  activeTabIdRef.current = activeTabId;
  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;

  // Initialize tabs from hook sessions on first successful load
  useEffect(() => {
    if (initializedRef.current || isLoading || sessions.length === 0) return;
    initializedRef.current = true;
    const initialTabs: TabState[] = sessions.map((s) => ({
      id: s.name,
      label: s.label,
      sessionName: s.name,
      connectionState: "disconnected" as ConnectionState,
    }));
    setTabs(initialTabs);
    const first = initialTabs[0];
    if (first) {
      setActiveTabId(first.id);
    }
  }, [sessions, isLoading]);

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

  const handleConnectionStateChange = useCallback(
    (tabId: string, state: ConnectionState) => {
      setTabs((prev) =>
        prev.map((t) =>
          t.id === tabId ? { ...t, connectionState: state } : t,
        ),
      );
    },
    [],
  );

  const handleNewTabClick = useCallback(() => {
    if (tabsRef.current.length >= MAX_TABS) return;
    setIsSessionPromptOpen(true);
  }, []);

  const handleSessionNameConfirm = useCallback((name: string) => {
    setIsSessionPromptOpen(false);
    setTabs((prev) => {
      if (prev.some((t) => t.sessionName === name)) return prev;
      return [
        ...prev,
        {
          id: name,
          label: name,
          sessionName: name,
          connectionState: "disconnected" as const,
        },
      ];
    });
    setActiveTabId(name);
  }, []);

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
      {isLoading && tabs.length === 0 ? (
        <div className={styles.loading}>Loading sessions...</div>
      ) : (
        <>
          <TerminalTabBar
            tabs={tabs.map((t) => ({
              id: t.id,
              label: t.label,
              connectionState: t.connectionState,
            }))}
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
                  onConnectionStateChange={(state) =>
                    handleConnectionStateChange(tab.id, state)
                  }
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
      <SessionNamePrompt
        isOpen={isSessionPromptOpen}
        existingNames={tabs.map((t) => t.sessionName)}
        onConfirm={handleSessionNameConfirm}
        onCancel={handleSessionPromptCancel}
      />
    </div>
  );
}
