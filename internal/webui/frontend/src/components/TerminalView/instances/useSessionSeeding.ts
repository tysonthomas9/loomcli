import { useEffect, useRef, useCallback } from "react";

import type { IssueContext } from "@/api/terminal";
import { seedTerminalSession, scheduleSessionKill } from "@/hooks/api";

import type { ConnectionState } from "./TerminalInstance";
import type { PendingLeadSession } from "@/components/TerminalView/TerminalView";
import {
  MAX_TABS,
  type TabState,
  sanitizeSessionName,
} from "@/components/TerminalView/tabs";

interface UseSessionSeedingOptions {
  pendingIssueContext: IssueContext | undefined;
  onIssueContextConsumed?: (() => void) | undefined;
  pendingAgentName: string | undefined;
  onAgentNameConsumed?: (() => void) | undefined;
  pendingLeadSession?: PendingLeadSession | undefined;
  onLeadSessionConsumed?: (() => void) | undefined;
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
  pendingLeadSession,
  onLeadSessionConsumed,
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

  // Handle pending lead session: create or focus a tab for a backend-spawned
  // `loom lead --message <...>` session. The backend already seeded the
  // user's request as part of the tmux invocation, so no client-side
  // send-keys seeding is required. If the tab limit has been reached, tear
  // down the orphaned tmux session so it does not leak.
  useEffect(() => {
    if (!pendingLeadSession || !initializedRef.current) return;

    const { sessionName, backend } = pendingLeadSession;
    const currentTabs = tabsRef.current;

    const existingTab = currentTabs.find((t) => t.sessionName === sessionName);
    if (existingTab) {
      setActiveTabId(existingTab.id);
      onLeadSessionConsumed?.();
      return;
    }

    if (currentTabs.length >= MAX_TABS) {
      scheduleSessionKill(workspaceIdRef.current, sessionName, true).catch(
        (err) =>
          console.error(
            `Failed to kill orphaned lead session ${sessionName}:`,
            err,
          ),
      );
      onLeadSessionConsumed?.();
      return;
    }

    const newTab: TabState = {
      id: sessionName,
      label: sessionName,
      sessionName,
      connectionState: "disconnected" as ConnectionState,
      backendName: backend,
    };
    setTabs((prev) => [...prev, newTab]);
    setActiveTabId(sessionName);

    createTab(sessionName, newTab.label, currentTabs.length).catch((err) =>
      console.error(`Failed to persist lead tab ${sessionName}:`, err),
    );

    onLeadSessionConsumed?.();
  }, [
    pendingLeadSession,
    createTab,
    onLeadSessionConsumed,
    initializedRef,
    setTabs,
    setActiveTabId,
    tabsRef,
    workspaceIdRef,
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
