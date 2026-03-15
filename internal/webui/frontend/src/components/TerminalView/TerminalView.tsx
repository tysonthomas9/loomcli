/**
 * TerminalView container component.
 * Orchestrates multiple tabbed terminal sessions with a search overlay.
 * All terminal instances are mounted in the DOM but only the active one
 * is visible, preserving xterm buffers and WebSocket connections.
 */

import { useState, useRef, useCallback, useEffect } from "react";

import { useBackendConfig } from "@/hooks/useBackendConfig";
import { useTerminalMetadata } from "@/hooks/useTerminalMetadata";

import { BackendPickerPrompt } from "./BackendPickerPrompt";
import { NotesBar } from "./NotesBar";
import { SearchBar } from "./SearchBar";
import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "./TerminalInstance";
import { TerminalInstance } from "./TerminalInstance";
import { TerminalTabBar } from "./TerminalTabBar";
import styles from "./TerminalView.module.css";

const MAX_TABS = 8;

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
}

interface TerminalViewProps {
  isActive?: boolean;
}

export function TerminalView({
  isActive = true,
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
  const instanceRefs = useRef<Map<string, TerminalInstanceHandle>>(new Map());
  const initializedRef = useRef(false);
  const activeTabIdRef = useRef(activeTabId);
  activeTabIdRef.current = activeTabId;

  // Initialize tabs from persisted metadata or auto-create from backend config
  useEffect(() => {
    if (initializedRef.current || metaLoading || configLoading) return;
    initializedRef.current = true;

    if (tabMetadata.length > 0) {
      // Returning user: restore tabs from persisted metadata
      const restoredTabs: TabState[] = tabMetadata.map((m) => ({
        id: m.session_name,
        label: m.label,
        sessionName: m.session_name,
        connectionState: "disconnected" as ConnectionState,
      }));
      setTabs(restoredTabs);
      setActiveTabId(restoredTabs[0]?.id ?? "");
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
