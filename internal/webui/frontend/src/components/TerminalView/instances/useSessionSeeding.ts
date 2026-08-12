import { useEffect, useCallback, useRef, useState } from "react";

import {
  ensureAgentTerminalSession,
  isStartingTerminalSessionError,
  type IssueContext,
} from "@/hooks/api";
import { calculateBackoffDelay } from "@/utils/reconnectBackoff";

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
  agentResolutionState: "idle" | "waking" | "failed";
  agentResolutionError: string | null;
}

const AGENT_ENSURE_RETRY_CONFIG = {
  baseDelay: 1000,
  maxDelay: 30000,
  maxAttempts: 10,
  jitterFactor: 0,
};

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
  tabsRef,
  workspaceIdRef,
}: UseSessionSeedingOptions): UseSessionSeedingReturn {
  const agentResolutionRef = useRef<{
    key: string;
    promise: ReturnType<typeof ensureAgentTerminalSession>;
  } | null>(null);
  const consumedAgentKeyRef = useRef<string | null>(null);
  const agentRetryRef = useRef({ key: "", attempt: 0 });
  const onAgentNameConsumedRef = useRef(onAgentNameConsumed);
  onAgentNameConsumedRef.current = onAgentNameConsumed;
  const [readyAgentKey, setReadyAgentKey] = useState<string | null>(null);
  const [agentResolutionState, setAgentResolutionState] = useState<
    "idle" | "waking" | "failed"
  >("idle");
  const [agentResolutionError, setAgentResolutionError] = useState<
    string | null
  >(null);

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
      agentRetryRef.current = { key: "", attempt: 0 };
      setAgentResolutionState("idle");
      setAgentResolutionError(null);
      setReadyAgentKey(null);
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

  // Preserve the pre-feature initialization gate without coupling the retry
  // lifecycle to tab changes. Tab initialization may change `tabs`; once the
  // key is ready, subsequent churn only reassigns the same state value.
  useEffect(() => {
    if (!pendingAgentName || !initializedRef.current) return;
    setReadyAgentKey(`${workspaceIdRef.current}:${pendingAgentName}`);
  }, [pendingAgentName, tabs, initializedRef, workspaceIdRef]);

  // Handle pending agent name: resolve or switch to the agent's PTY terminal.
  useEffect(() => {
    if (!pendingAgentName) return;
    const requestKey = `${workspaceIdRef.current}:${pendingAgentName}`;
    if (readyAgentKey !== requestKey) return;

    const currentTabs = tabsRef.current;
    const existingTab = currentTabs.find(
      (tab) => tab.agentName === pendingAgentName,
    );
    if (!existingTab && currentTabs.length >= MAX_TABS) {
      onAgentNameConsumedRef.current?.();
      return;
    }

    let cancelled = false;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    if (consumedAgentKeyRef.current === requestKey) return;

    if (agentRetryRef.current.key !== requestKey) {
      agentRetryRef.current = { key: requestKey, attempt: 0 };
      setAgentResolutionState("idle");
      setAgentResolutionError(null);
    }

    const consumeFailure = (err: unknown) => {
      console.error(
        `Failed to resolve agent terminal ${pendingAgentName}:`,
        err,
      );
      setAgentResolutionState("idle");
      setAgentResolutionError(null);
      consumedAgentKeyRef.current = requestKey;
      onAgentNameConsumedRef.current?.();
    };

    const resolveAgent = () => {
      if (cancelled) return;
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
            if (prev.some((tab) => tab.agentName === agentName)) {
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
          setAgentResolutionState("idle");
          setAgentResolutionError(null);
          agentRetryRef.current = { key: requestKey, attempt: 0 };
          consumedAgentKeyRef.current = requestKey;
          onAgentNameConsumedRef.current?.();
        })
        .catch((err) => {
          if (cancelled) return;
          if (isStartingTerminalSessionError(err)) {
            setAgentResolutionState("waking");
            setAgentResolutionError(null);
            const retry = agentRetryRef.current;
            if (retry.attempt < AGENT_ENSURE_RETRY_CONFIG.maxAttempts) {
              const delay = calculateBackoffDelay(
                retry.attempt,
                AGENT_ENSURE_RETRY_CONFIG,
              );
              retry.attempt += 1;
              retryTimer = setTimeout(resolveAgent, delay);
              return;
            }
            const message =
              "Lead sandbox did not become ready. Try opening the agent again.";
            console.error(
              `Failed to resolve agent terminal ${pendingAgentName}:`,
              err,
            );
            setAgentResolutionState("failed");
            setAgentResolutionError(message);
            consumedAgentKeyRef.current = requestKey;
            return;
          }
          consumeFailure(err);
        })
        .finally(() => {
          if (agentResolutionRef.current?.key === requestKey) {
            agentResolutionRef.current = null;
          }
        });
    };

    resolveAgent();

    return () => {
      cancelled = true;
      if (retryTimer !== null) clearTimeout(retryTimer);
    };
  }, [
    pendingAgentName,
    readyAgentKey,
    mergeExistingAgentTab,
    setActiveTabId,
    setTabs,
    tabsRef,
    workspaceIdRef,
  ]);

  const trySeedOnConnect = useCallback((_tabId: string) => {
    // No-op: backend-side seeding was removed with the tmux migration.
    // Extension point for future client-side prompt injection.
  }, []);

  return {
    trySeedOnConnect,
    agentResolutionState,
    agentResolutionError,
  };
}
