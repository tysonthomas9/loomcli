import { useCallback, useEffect, useRef, type MutableRefObject } from "react";

import { closeAllSessions } from "@/api/terminal";

import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "./TerminalInstance";
import { sanitizeSessionName, type TabState } from "./terminalTabUtils";

interface SessionMgmtArgs {
  setTabs: React.Dispatch<React.SetStateAction<TabState[]>>;
  setActiveTabId: React.Dispatch<React.SetStateAction<string>>;
  instanceRefs: MutableRefObject<Map<string, TerminalInstanceHandle>>;
  initializedRef: MutableRefObject<boolean>;
  issueId?: string | undefined;
  tabs: TabState[];
  createTab: (s: string, l: string, o: number) => Promise<void>;
  backendName: string;
}

export function useSessionManagement(args: SessionMgmtArgs) {
  const {
    setTabs,
    setActiveTabId,
    instanceRefs,
    initializedRef,
    issueId,
    tabs,
    createTab,
    backendName,
  } = args;

  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;
  const createTabRef = useRef(createTab);
  createTabRef.current = createTab;
  const backendNameRef = useRef(backendName);
  backendNameRef.current = backendName;

  const handleCloseAll = useCallback(() => {
    if (!window.confirm("Close all terminal sessions? This cannot be undone."))
      return;
    closeAllSessions()
      .then(() => {
        setTabs([]);
        setActiveTabId("");
        instanceRefs.current.clear();
        initializedRef.current = false;
      })
      .catch((err) => console.error("Failed to close all sessions:", err));
  }, [setTabs, setActiveTabId, instanceRefs, initializedRef]);

  useEffect(() => {
    if (!issueId || !initializedRef.current) return;
    const sessionName = `issue-${sanitizeSessionName(issueId)}`;
    const currentTabs = tabsRef.current;
    const existingTab = currentTabs.find((t) => t.sessionName === sessionName);
    if (existingTab) {
      setActiveTabId(existingTab.id);
      return;
    }
    const newTab: TabState = {
      id: sessionName,
      label: sessionName,
      sessionName,
      connectionState: "disconnected" as ConnectionState,
      backendName: backendNameRef.current,
    };
    setTabs((prev) => {
      if (prev.find((t) => t.sessionName === sessionName)) return prev;
      return [...prev, newTab];
    });
    setActiveTabId(sessionName);
    createTabRef
      .current(sessionName, newTab.label, currentTabs.length)
      .catch((err) =>
        console.error(`Failed to persist issue tab ${sessionName}:`, err),
      );
  }, [issueId, initializedRef, setTabs, setActiveTabId]);

  return handleCloseAll;
}
