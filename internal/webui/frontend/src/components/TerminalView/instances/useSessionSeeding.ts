import { useEffect, useCallback, useRef } from "react";

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
    backend: string,
  ) => Promise<void>;
  config: { backend: string } | undefined;
  initializedRef: React.MutableRefObject<boolean>;
  tabsRef: React.MutableRefObject<TabState[]>;
  workspaceIdRef: React.MutableRefObject<string>;
}

/**
 * Manages pending issue / agent context delivery into the terminal tab
 * system by creating or switching to the appropriate tab.
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
}: UseSessionSeedingOptions): void {
  const agentResolutionRef = useRef<{
    key: string;
    promise: ReturnType<typeof ensureAgentTerminalSession>;
  } | null>(null);
  const consumedAgentKeyRef = useRef<string | null>(null);

  const mergeExistingAgentTab = useCallback(
    (existing: TabState, metadataTab: TabState): TabState => {
      const merged: TabState = {
        ...existing,
        ...metadataTab,
        connectionState: existing.connectionState,
      };
      if (existing.crashReason !== undefined) {
        merged.crashReason = existing.crashReason;
      }
      return merged;
    },
    [],
  );

  useEffect(() => {
    if (!pendingAgentName) {
      consumedAgentKeyRef.current = null;
    }
  }, [pendingAgentName]);

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

    const backend = config?.backend?.trim();
    if (!backend) return;

    const newTab: TabState = {
      id: sessionName,
      label: `issue-${sanitizeSessionName(pendingIssueContext.issue_id)}`,
      sessionName,
      connectionState: "disconnected" as ConnectionState,
      backendName: backend,
    };
    createTab(sessionName, newTab.label, tabs.length, newTab.backendName)
      .then(() => {
        setTabs((prev) => [...prev, newTab]);
        setActiveTabId(sessionName);
        onIssueContextConsumed?.();
      })
      .catch((err) =>
        console.error(`Failed to persist issue tab ${sessionName}:`, err),
      );
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
    if (!existingTab && tabs.length >= MAX_TABS) {
      onAgentNameConsumed?.();
      return;
    }

    let cancelled = false;
    const requestKey = `${workspaceIdRef.current}:${pendingAgentName}`;
    if (consumedAgentKeyRef.current === requestKey) return;

    let request = agentResolutionRef.current;
    if (!request || request.key !== requestKey) {
      request = {
        key: requestKey,
        promise: ensureAgentTerminalSession(
          workspaceIdRef.current,
          pendingAgentName,
        ),
      };
      agentResolutionRef.current = request;
    }

    request.promise
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
              tab.id === newTab.id ? mergeExistingAgentTab(tab, newTab) : tab,
            );
          }
          if (existingTab) {
            let replaced = false;
            return prev.flatMap((tab) => {
              if (tab.agentName !== agentName) return [tab];
              if (replaced) return [];
              replaced = true;
              return [{ ...tab, ...newTab }];
            });
          }
          if (prev.length >= MAX_TABS) return prev;
          return [...prev, newTab];
        });
        setActiveTabId(newTab.id);
        consumedAgentKeyRef.current = requestKey;
        onAgentNameConsumed?.();
      })
      .catch((err) => {
        if (cancelled) return;
        console.error(
          `Failed to resolve agent terminal ${pendingAgentName}:`,
          err,
        );
        consumedAgentKeyRef.current = requestKey;
        onAgentNameConsumed?.();
      })
      .finally(() => {
        if (agentResolutionRef.current?.key === requestKey) {
          agentResolutionRef.current = null;
        }
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
    mergeExistingAgentTab,
  ]);
}
