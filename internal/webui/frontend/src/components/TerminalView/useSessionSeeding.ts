import { useEffect, useRef, useCallback } from "react";

import type { IssueContext } from "@/api/terminal";
import { seedTerminalSession } from "@/api/terminal";

import type { ConnectionState } from "./TerminalInstance";
import {
  MAX_TABS,
  type TabState,
  sanitizeSessionName,
} from "./terminalTabUtils";

interface UseSessionSeedingOptions {
  pendingIssueContext: IssueContext | undefined;
  onIssueContextConsumed?: (() => void) | undefined;
  pendingAgentName: string | undefined;
  onAgentNameConsumed?: (() => void) | undefined;
  tabs: TabState[];
  setTabs: React.Dispatch<React.SetStateAction<TabState[]>>;
  setActiveTabId: React.Dispatch<React.SetStateAction<string>>;
  createTab: (
    session: string,
    label: string,
    sortOrder: number,
  ) => Promise<void>;
  config: { backend: string } | undefined;
  initializedRef: React.MutableRefObject<boolean>;
  tabsRef: React.MutableRefObject<TabState[]>;
  workspaceIdRef: React.MutableRefObject<string>;
}

interface UseSessionSeedingReturn {
  trySeedOnConnect: (tabId: string) => void;
}

export function useSessionSeeding({
  pendingIssueContext,
  onIssueContextConsumed,
  pendingAgentName,
  onAgentNameConsumed,
  tabs,
  setTabs,
  setActiveTabId,
  createTab,
  config,
  initializedRef,
  tabsRef,
  workspaceIdRef,
}: UseSessionSeedingOptions): UseSessionSeedingReturn {
  const seededSessionsRef = useRef<Set<string>>(new Set());
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
  }, [
    pendingIssueContext,
    tabs,
    createTab,
    onIssueContextConsumed,
    initializedRef,
    setTabs,
    setActiveTabId,
    config,
  ]);

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
  }, [
    pendingAgentName,
    tabs,
    createTab,
    onAgentNameConsumed,
    initializedRef,
    setTabs,
    setActiveTabId,
  ]);

  const trySeedOnConnect = useCallback(
    (tabId: string) => {
      const tab = tabsRef.current.find((t) => t.id === tabId);
      if (!tab) return;
      if (seededSessionsRef.current.has(tab.sessionName)) return;
      const seedCtx = pendingSeedRef.current.get(tab.sessionName);
      if (!seedCtx) return;

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
    },
    [tabsRef, workspaceIdRef],
  );

  return { trySeedOnConnect };
}
