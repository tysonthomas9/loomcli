import { useEffect, useCallback } from "react";

import { ensureAgentTerminalSession, type IssueContext } from "@/hooks/api";

import type { ConnectionState } from "./TerminalInstance";
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

/**
 * Manages pending issue / agent context delivery into the terminal tab
 * system. The backend "seed" flow that used to inject issue prompts via
 * tmux send-keys is gone with the tmux removal; this hook now only
 * handles the tab-opening half (create or switch to the appropriate tab).
 * `trySeedOnConnect` is retained as a stable no-op so callers don't need
 * to change — it's the natural extension point if client-side seeding is
 * re-introduced later.
 */
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
  workspaceIdRef,
}: UseSessionSeedingOptions): UseSessionSeedingReturn {
  // Handle pending issue context: create or switch to issue tab.
  useEffect(() => {
    if (!pendingIssueContext || !initializedRef.current) return;

    const sessionName = `issue-${sanitizeSessionName(pendingIssueContext.issue_id)}`;

    const existingTab = tabs.find((t) => t.sessionName === sessionName);
    if (existingTab) {
      setActiveTabId(existingTab.id);
      onIssueContextConsumed?.();
      return;
    }

    const newTab: TabState = {
      id: sessionName,
      label: `issue-${sanitizeSessionName(pendingIssueContext.issue_id)}`,
      sessionName,
      connectionState: "disconnected" as ConnectionState,
      backendName: config?.backend ?? "unknown",
    };
    setTabs((prev) => [...prev, newTab]);
    setActiveTabId(sessionName);

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

  // Handle pending agent name: resolve or switch to the agent's PTY terminal.
  useEffect(() => {
    if (!pendingAgentName || !initializedRef.current) return;

    const existingTab = tabs.find((t) => t.agentName === pendingAgentName);
    if (existingTab) {
      if (existingTab.kind !== "agent") {
        setTabs((prev) =>
          prev.map((tab) =>
            tab.id === existingTab.id
              ? {
                  ...tab,
                  kind: "agent",
                  agentName: pendingAgentName,
                }
              : tab,
          ),
        );
      }
      setActiveTabId(existingTab.id);
      onAgentNameConsumed?.();
      return;
    }

    if (tabs.length >= MAX_TABS) {
      onAgentNameConsumed?.();
      return;
    }

    let cancelled = false;
    ensureAgentTerminalSession(workspaceIdRef.current, pendingAgentName)
      .then((meta) => {
        if (cancelled) return;
        const agentName = meta.agent_id ?? pendingAgentName;
        const newTab: TabState = {
          id: meta.session_name,
          label: meta.label,
          sessionName: meta.session_name,
          connectionState: "disconnected" as ConnectionState,
          backendName: meta.backend ?? "agent",
          kind: meta.kind ?? "agent",
          agentName,
          writable: meta.writable ?? true,
          pinned: meta.pinned,
          ...(meta.role ? { role: meta.role } : {}),
        };
        setTabs((prev) => {
          if (prev.some((tab) => tab.id === newTab.id)) {
            return prev.map((tab) =>
              tab.id === newTab.id ? { ...tab, ...newTab } : tab,
            );
          }
          return [...prev, newTab];
        });
        setActiveTabId(newTab.id);
        onAgentNameConsumed?.();
      })
      .catch((err) => {
        if (cancelled) return;
        console.error(
          `Failed to resolve agent terminal ${pendingAgentName}:`,
          err,
        );
        onAgentNameConsumed?.();
      });

    return () => {
      cancelled = true;
    };
  }, [
    pendingAgentName,
    tabs,
    onAgentNameConsumed,
    initializedRef,
    setTabs,
    setActiveTabId,
    workspaceIdRef,
  ]);

  const trySeedOnConnect = useCallback((_tabId: string) => {
    // No-op: backend-side seeding was removed with the tmux migration.
    // Extension point for future client-side prompt injection.
  }, []);

  return { trySeedOnConnect };
}
