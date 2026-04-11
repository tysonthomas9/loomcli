import { useCallback, useEffect, useRef, type MutableRefObject } from "react";

import { closeAllSessions } from "@/hooks/api";

import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "@/components/TerminalView/instances";
import { sanitizeSessionName, type TabState } from "./terminalTabUtils";

interface SessionMgmtArgs {
  workspaceId: string;
  setTabs: React.Dispatch<React.SetStateAction<TabState[]>>;
  setActiveTabId: React.Dispatch<React.SetStateAction<string>>;
  instanceRefs: MutableRefObject<Map<string, TerminalInstanceHandle>>;
  initializedRef: MutableRefObject<boolean>;
  isActive: boolean;
  issueId?: string | undefined;
  tabs: TabState[];
  createTab: (s: string, l: string, o: number) => Promise<void>;
  linkToIssue: (session: string, issueId: string) => Promise<void>;
  backendName: string;
}

export function useSessionManagement(args: SessionMgmtArgs) {
  const {
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
    backendName,
  } = args;

  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;
  const createTabRef = useRef(createTab);
  createTabRef.current = createTab;
  const backendNameRef = useRef(backendName);
  backendNameRef.current = backendName;
  const linkToIssueRef = useRef(linkToIssue);
  linkToIssueRef.current = linkToIssue;

  const handleCloseAll = useCallback(() => {
    if (!window.confirm("Close all terminal sessions? This cannot be undone."))
      return;
    closeAllSessions(workspaceId)
      .then(() => {
        setTabs([]);
        setActiveTabId("");
        instanceRefs.current.clear();
        initializedRef.current = false;
      })
      .catch((err) => console.error("Failed to close all sessions:", err));
  }, [workspaceId, setTabs, setActiveTabId, instanceRefs, initializedRef]);

  useEffect(() => {
    if (!issueId || !isActive || !initializedRef.current) return;
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
      .then(() => linkToIssueRef.current(sessionName, issueId))
      .catch((err) =>
        console.error(`Failed to persist issue tab ${sessionName}:`, err),
      );
    // tabs.length ensures re-evaluation after initialization (when tabs populate
    // and initializedRef.current becomes true), since initializedRef is not reactive.
  }, [issueId, isActive, initializedRef, setTabs, setActiveTabId, tabs.length]);

  return handleCloseAll;
}
