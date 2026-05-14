import { useState, useRef, useCallback, useEffect, useMemo } from "react";

import type { IssueContext, TerminalSetupResult } from "@/hooks/api";
import { patchTerminalState, startTerminalSetup } from "@/hooks/api";
import { LoadingSkeleton } from "@/components";
import { useBackendConfig, useBackends } from "@/hooks/workspace";
import { useSessionRestore, useTerminalMetadata } from "@/hooks/terminal";
import {
  CLI_SETUP_REQUEST_EVENT,
  clearPendingCliSetupRequest,
  getCliSetupInstructions,
  readPendingCliSetupRequest,
  type CliSetupInstructions,
  type CliSetupRequest,
} from "@/utils/cliSetup";

import {
  BackendPickerPrompt,
  NoBackendsEmptyState,
  useSplitView,
} from "./layout";
import { HelpPopover } from "./controls";
import {
  TerminalPane,
  TerminalPaneArea,
  useConnectionState,
  useSessionSeeding,
} from "./instances";
import type { TerminalInstanceHandle } from "./instances";
import {
  TerminalTabBar,
  MAX_TABS,
  BACKEND_BRAND_COLORS,
  type TabState,
  generateTabName,
  sanitizeSessionName,
  useTabOrdering,
  useTabActions,
  useTabInit,
  useUnreadTracking,
  useWorkspaceTabState,
} from "./tabs";
import styles from "./TerminalView.module.css";

interface TerminalViewProps {
  isActive?: boolean;
  pendingIssueContext?: IssueContext | undefined;
  onIssueContextConsumed?: (() => void) | undefined;
  onActiveSessionCountChange?: (count: number) => void;
  onUnreadChange?: (hasAnyUnread: boolean) => void;
  onTabLimitReached?: (message: string) => void;
  onNavigateToSettings?: () => void;
  /** When set, opens or focuses an agent's terminal tab. */
  pendingAgentName?: string | undefined;
  /** Called after pendingAgentName has been processed. */
  onAgentNameConsumed?: (() => void) | undefined;
}

interface CliSetupGuide extends CliSetupRequest {
  tabId: string;
  instructions: CliSetupInstructions;
  hasRun: boolean;
  status: "starting" | "running" | "manual" | "failed";
  error?: string | undefined;
}

function isLeadSessionName(sessionName: string): boolean {
  return /(?:^|--)lead-[^-]+-\d+$/.test(sessionName);
}

function buildCliSetupSessionName(
  workspaceId: string,
  backendName: string,
): string {
  const safeWorkspace = workspaceId ? sanitizeSessionName(workspaceId) : "";
  const wsPrefix =
    safeWorkspace && safeWorkspace !== "default" ? `${safeWorkspace}--` : "";
  const safeBackend = sanitizeSessionName(backendName) || "cli";
  return `${wsPrefix}lead-shell-setup-${safeBackend}`;
}

function setupErrorMessage(error: unknown): string {
  return error instanceof Error
    ? error.message
    : "Failed to start setup terminal";
}

function setupInstructionsFromResult(
  request: CliSetupRequest,
  result: TerminalSetupResult,
): CliSetupInstructions {
  const fallback = getCliSetupInstructions(request);
  const command = result.command || fallback.command;
  const instructions: CliSetupInstructions = {
    title: result.title || fallback.title,
    description: result.message || fallback.description,
    buttonLabel: result.manual ? "Show steps again" : fallback.buttonLabel,
  };
  if (command) {
    instructions.command = command;
  }
  return instructions;
}

export function TerminalView({
  isActive = true,
  pendingIssueContext,
  onIssueContextConsumed,
  onActiveSessionCountChange,
  onUnreadChange,
  onTabLimitReached,
  onNavigateToSettings,
  pendingAgentName,
  onAgentNameConsumed,
}: TerminalViewProps): JSX.Element {
  const [tabs, setTabs] = useState<TabState[]>([]);
  const [activeTabId, setActiveTabId] = useState<string>("");
  const initializedRef = useRef(false);
  const { id: workspaceId } = useWorkspaceTabState({
    tabs,
    activeTabId,
    setTabs,
    setActiveTabId,
    initializedRef,
  });
  const {
    tabs: tabMetadata,
    createTab,
    updateLabel,
    updateNotes,
    updatePinned,
    deleteTab,
    reorderTabs: reorderTabMeta,
    isLoading: metaLoading,
  } = useTerminalMetadata(workspaceId, { enabled: isActive });
  const { config, isLoading: configLoading } = useBackendConfig(workspaceId, {
    enabled: isActive,
  });
  const { refetch: refetchAiBackends } = useBackends();
  const { activeTabId: restoredTabId, isRestoring } = useSessionRestore({
    enabled: isActive,
  });

  const [isFullHeight, setIsFullHeight] = useState(false);
  const {
    isSplitView,
    splitRatio,
    rightPaneTabId,
    focusedPane: _focusedPane,
    setFocusedPane,
    canSplit,
    handleToggleSplit,
    handleSplitRatioChange,
    handleRightPaneTabChange,
  } = useSplitView({ tabs, activeTabId });
  const splitContainerRef = useRef<HTMLDivElement>(null);
  const [isSessionPromptOpen, setIsSessionPromptOpen] = useState(false);
  const [isHelpOpen, setIsHelpOpen] = useState(false);
  const [cliSetupGuide, setCliSetupGuide] = useState<CliSetupGuide | null>(
    null,
  );
  const instanceRefs = useRef<Map<string, TerminalInstanceHandle>>(new Map());
  const processedCliSetupIdRef = useRef<string | null>(null);
  const activeTabIdRef = useRef(activeTabId);
  activeTabIdRef.current = activeTabId;
  const tabsRef = useRef(tabs);
  tabsRef.current = tabs;
  const workspaceIdRef = useRef(workspaceId);
  workspaceIdRef.current = workspaceId;
  const backendStatusPollRef = useRef<ReturnType<typeof setInterval> | null>(
    null,
  );

  // ARIA live region for screen reader announcements
  const liveRegionRef = useRef<HTMLDivElement>(null);
  const announce = useCallback((msg: string) => {
    if (liveRegionRef.current) liveRegionRef.current.textContent = msg;
  }, []);

  const stopBackendStatusPolling = useCallback(() => {
    if (backendStatusPollRef.current == null) return;
    clearInterval(backendStatusPollRef.current);
    backendStatusPollRef.current = null;
  }, []);

  const startBackendStatusPolling = useCallback(() => {
    stopBackendStatusPolling();
    refetchAiBackends();
    let remainingTicks = 24;
    backendStatusPollRef.current = setInterval(() => {
      refetchAiBackends();
      remainingTicks -= 1;
      if (remainingTicks <= 0) {
        stopBackendStatusPolling();
      }
    }, 5000);
  }, [refetchAiBackends, stopBackendStatusPolling]);

  useEffect(() => {
    return () => {
      stopBackendStatusPolling();
    };
  }, [stopBackendStatusPolling]);

  const refreshCliStatus = useCallback(() => {
    refetchAiBackends();
    announce("CLI status refresh started");
  }, [announce, refetchAiBackends]);

  const handleTabLimitReached = useCallback(() => {
    const message = `Maximum terminal tabs reached (${MAX_TABS}). Close a tab before opening another.`;
    announce(message);
    onTabLimitReached?.(message);
  }, [announce, onTabLimitReached]);

  // Hook ordering: useSessionSeeding before useConnectionState so
  // trySeedOnConnect is available as the onTabConnected callback.
  const { trySeedOnConnect } = useSessionSeeding({
    pendingIssueContext,
    onIssueContextConsumed,
    pendingAgentName,
    onAgentNameConsumed,
    tabs,
    setTabs,
    setActiveTabId,
    createTab,
    config: config ?? undefined,
    initializedRef,
    tabsRef,
    workspaceIdRef,
  });

  const {
    tabHasConnected,
    tabReconnectState,
    handleConnectionStateChange,
    handleReconnectStateChange,
    handleReconnect,
    handleBackendCrash,
    handleCrashRestart,
  } = useConnectionState({
    setTabs,
    instanceRefs,
    onTabConnected: trySeedOnConnect,
  });

  const { tabUnread, handleOutput, clearTabUnread } = useUnreadTracking({
    activeTabIdRef,
    isActive,
    onUnreadChange,
  });
  const hasPendingCliSetupRequest = readPendingCliSetupRequest() != null;

  useTabInit({
    tabMetadata,
    metaLoading,
    config: config ?? undefined,
    configLoading,
    createTab,
    setTabs,
    setActiveTabId,
    initializedRef,
    workspace: workspaceId,
    isViewActive: isActive ?? false,
    skipDefaultTabInit: hasPendingCliSetupRequest,
  });

  // Apply server-restored active tab after initialization.
  const appliedRestoreRef = useRef(false);
  useEffect(() => {
    if (appliedRestoreRef.current || isRestoring || !restoredTabId) return;
    if (!initializedRef.current || tabs.length === 0) return;
    appliedRestoreRef.current = true;
    if (restoredTabId === activeTabId) return;
    const match = tabs.find((t) => t.id === restoredTabId);
    if (match) setActiveTabId(restoredTabId);
  }, [restoredTabId, isRestoring, tabs, activeTabId]);

  // Persist active tab to sessionStorage and server (debounced)
  const patchDebounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    if (!activeTabId) return;
    sessionStorage.setItem("terminal-active-tab", activeTabId);
    if (patchDebounceRef.current) clearTimeout(patchDebounceRef.current);
    patchDebounceRef.current = setTimeout(() => {
      patchTerminalState(workspaceId, { active_tab: activeTabId }).catch(
        () => {},
      );
    }, 300);
    return () => {
      if (patchDebounceRef.current) clearTimeout(patchDebounceRef.current);
    };
  }, [activeTabId, workspaceId]);

  // Report active (connected) session count to parent
  useEffect(() => {
    const count = tabs.filter((t) => t.connectionState === "connected").length;
    onActiveSessionCountChange?.(count);
  }, [tabs, onActiveSessionCountChange]);

  useEffect(() => {
    return () => {
      onActiveSessionCountChange?.(0);
    };
  }, [onActiveSessionCountChange]);

  // Body scroll lock for full-height mode
  useEffect(() => {
    if (isFullHeight) document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = "";
    };
  }, [isFullHeight]);

  const handleTabChange = useCallback(
    (tabId: string) => {
      setActiveTabId(tabId);
      clearTabUnread(tabId);
    },
    [clearTabUnread],
  );

  const { handleTabClose, handleDuplicateTab, handleTabRename } = useTabActions(
    {
      workspaceId,
      tabs,
      setTabs,
      setActiveTabId,
      activeTabIdRef,
      instanceRefs,
      createTab,
      updateLabel,
      deleteTab,
    },
  );

  const { handleTabPin, handleCloseOthers, handleReorderTabs } = useTabOrdering(
    {
      tabs,
      setTabs,
      setActiveTabId,
      handleTabClose,
      updatePinned,
      reorderTabMeta,
      deleteTab,
    },
  );

  const applyCliSetupResult = useCallback(
    (request: CliSetupRequest, result: TerminalSetupResult) => {
      const instructions = setupInstructionsFromResult(request, result);
      setCliSetupGuide((current) =>
        current?.id === request.id
          ? {
              ...current,
              tabId: result.session_name || current.tabId,
              instructions,
              hasRun: true,
              status: result.manual ? "manual" : "running",
              error: undefined,
            }
          : current,
      );
      startBackendStatusPolling();
      announce(
        result.manual
          ? `${request.displayName} setup steps shown`
          : `${request.displayName} setup command started`,
      );
    },
    [announce, startBackendStatusPolling],
  );

  const startCliSetup = useCallback(
    (request: CliSetupRequest) => {
      setCliSetupGuide((current) =>
        current?.id === request.id
          ? { ...current, status: "starting", error: undefined }
          : current,
      );
      void startTerminalSetup(
        workspaceIdRef.current,
        request.backendName,
        request.action,
      ).then(
        (result) => {
          applyCliSetupResult(request, result);
        },
        (error) => {
          const message = setupErrorMessage(error);
          setCliSetupGuide((current) =>
            current?.id === request.id
              ? { ...current, status: "failed", error: message }
              : current,
          );
          announce(`${request.displayName} setup failed`);
        },
      );
    },
    [announce, applyCliSetupResult],
  );

  const processCliSetupRequest = useCallback(
    (request: CliSetupRequest): boolean => {
      if (!initializedRef.current) return false;
      if (processedCliSetupIdRef.current === request.id) {
        clearPendingCliSetupRequest(request.id);
        return true;
      }

      const currentTabs = tabsRef.current;
      const sessionName = buildCliSetupSessionName(
        workspaceIdRef.current,
        request.backendName,
      );
      const label = `${request.displayName} setup`;
      const existingTab = currentTabs.find(
        (tab) => tab.sessionName === sessionName,
      );
      const existingMetadata = tabMetadata.find(
        (tab) => tab.session_name === sessionName,
      );
      const tabCount = Math.max(currentTabs.length, tabMetadata.length);

      if (!existingTab && !existingMetadata && tabCount >= MAX_TABS) {
        processedCliSetupIdRef.current = request.id;
        clearPendingCliSetupRequest(request.id);
        handleTabLimitReached();
        return true;
      }

      if (existingTab || existingMetadata) {
        const tabId = existingTab?.id ?? sessionName;
        const existingLabel = existingTab?.label ?? existingMetadata?.label;
        setActiveTabId(tabId);
        if (existingLabel !== label) {
          setTabs((prev) =>
            prev.map((tab) => (tab.id === tabId ? { ...tab, label } : tab)),
          );
          updateLabel(sessionName, label).catch((err) =>
            console.error(
              `Failed to persist setup tab label ${sessionName}:`,
              err,
            ),
          );
        }
      } else {
        const newTab: TabState = {
          id: sessionName,
          label,
          sessionName,
          connectionState: "disconnected" as const,
          backendName: "shell",
        };
        setTabs((prev) =>
          prev.some((tab) => tab.sessionName === sessionName)
            ? prev
            : [...prev, newTab],
        );
        setActiveTabId(sessionName);
      }

      try {
        sessionStorage.setItem("terminal-active-tab", sessionName);
      } catch {
        // sessionStorage unavailable
      }

      processedCliSetupIdRef.current = request.id;
      clearPendingCliSetupRequest(request.id);
      setCliSetupGuide({
        ...request,
        tabId: sessionName,
        instructions: getCliSetupInstructions(request),
        hasRun: false,
        status: "starting",
      });
      startCliSetup(request);
      announce(`${request.displayName} setup opened`);
      return true;
    },
    [
      announce,
      handleTabLimitReached,
      setActiveTabId,
      setTabs,
      startCliSetup,
      tabMetadata,
      updateLabel,
    ],
  );

  useEffect(() => {
    if (!isActive || !initializedRef.current) return;
    const request = readPendingCliSetupRequest();
    if (request) processCliSetupRequest(request);
  }, [isActive, tabs, processCliSetupRequest]);

  useEffect(() => {
    const handleCliSetupRequest = (event: Event) => {
      const request =
        (event as CustomEvent<CliSetupRequest>).detail ??
        readPendingCliSetupRequest();
      if (!request || !isActive || !initializedRef.current) return;
      processCliSetupRequest(request);
    };
    window.addEventListener(CLI_SETUP_REQUEST_EVENT, handleCliSetupRequest);
    return () => {
      window.removeEventListener(
        CLI_SETUP_REQUEST_EVENT,
        handleCliSetupRequest,
      );
    };
  }, [isActive, processCliSetupRequest]);

  const runCliSetupCommand = useCallback(
    (guide: CliSetupGuide) => {
      setActiveTabId(guide.tabId);
      instanceRefs.current.get(guide.tabId)?.focus();
      startCliSetup(guide);
      announce(`${guide.displayName} setup command requested`);
    },
    [announce, startCliSetup],
  );

  const handleNewTabClick = useCallback(() => {
    if (tabs.length >= MAX_TABS) {
      handleTabLimitReached();
      return;
    }
    setIsSessionPromptOpen(true);
  }, [handleTabLimitReached, tabs.length]);

  const handleBackendSelect = useCallback(
    (backend: string) => {
      setIsSessionPromptOpen(false);
      const { sessionName, label } = generateTabName(
        backend,
        tabs,
        workspaceId,
      );
      // Persist the tab so it survives a refresh. The WS handler spawns
      // the PTY on connect; this PUT is just metadata so the server can
      // return the tab in ListTabs on reload.
      createTab(sessionName, label, tabs.length).catch((err) =>
        console.error(`Failed to persist new tab ${sessionName}:`, err),
      );
      setTabs((prev) => [
        ...prev,
        {
          id: sessionName,
          label,
          sessionName,
          connectionState: "disconnected" as const,
          backendName: backend,
        },
      ]);
      setActiveTabId(sessionName);
      announce(`New tab ${label} created`);
    },
    [createTab, tabs, workspaceId, announce],
  );

  const handleSessionPromptCancel = useCallback(() => {
    setIsSessionPromptOpen(false);
  }, []);

  const handleToggleHelp = useCallback(() => {
    setIsHelpOpen((prev) => !prev);
  }, []);

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

  const setFocusedLeft = useCallback(
    () => setFocusedPane("left"),
    [setFocusedPane],
  );
  const setFocusedRight = useCallback(
    () => setFocusedPane("right"),
    [setFocusedPane],
  );

  const metaBySession = useMemo(
    () => new Map(tabMetadata.map((m) => [m.session_name, m])),
    [tabMetadata],
  );
  const renderTerminalPane = useCallback(
    (tab: TabState, pane: "left" | "right" | null) => {
      const paneIsActive =
        pane === "right" ? tab.id === rightPaneTabId : tab.id === activeTabId;
      const meta = metaBySession.get(tab.sessionName);
      // Undefined while metadata is still loading — preserves connect-on-
      // mount. Only concrete `false` gates auto-attach.
      const ptyAlive = meta?.pty_alive;
      return (
        <TerminalPane
          tab={tab}
          isActive={paneIsActive}
          instanceRef={setInstanceRef(tab.id)}
          ptyAlive={ptyAlive}
          autoStartStaleSession={isLeadSessionName(tab.sessionName)}
          onConnectionStateChange={(state, hasConnected) =>
            handleConnectionStateChange(tab.id, state, hasConnected)
          }
          onReconnectStateChange={(state) =>
            handleReconnectStateChange(tab.id, state)
          }
          onOutput={() => handleOutput(tab.id)}
          onBackendCrash={(reason) => handleBackendCrash(tab.id, reason)}
          onCrashRestart={() => handleCrashRestart(tab.id, tab.sessionName)}
          onCloseTab={() => handleTabClose(tab.id)}
          onReconnect={() => handleReconnect(tab.id)}
          onTerminalFocus={
            pane === "left"
              ? setFocusedLeft
              : pane === "right"
                ? setFocusedRight
                : undefined
          }
          hasConnected={tabHasConnected.get(tab.id) ?? false}
          reconnectState={tabReconnectState.get(tab.id) ?? null}
          notes={meta?.notes ?? ""}
          onSaveNotes={(text) => updateNotes(tab.sessionName, text)}
          isMetaLoading={metaLoading}
        />
      );
    },
    [
      activeTabId,
      rightPaneTabId,
      setInstanceRef,
      handleConnectionStateChange,
      handleReconnectStateChange,
      handleOutput,
      handleBackendCrash,
      handleCrashRestart,
      handleTabClose,
      handleReconnect,
      tabHasConnected,
      tabReconnectState,
      metaBySession,
      updateNotes,
      metaLoading,
      setFocusedLeft,
      setFocusedRight,
    ],
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
        <LoadingSkeleton.Terminal />
      ) : tabs.length === 0 ? (
        <NoBackendsEmptyState
          {...(onNavigateToSettings != null && {
            onGoToSettings: onNavigateToSettings,
          })}
        />
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
                ...(tabUnread.get(t.id) && { hasUnread: true }),
                ...(t.pinned && { isPinned: true }),
              };
            })}
            activeTabId={activeTabId}
            onTabChange={handleTabChange}
            onTabClose={handleTabClose}
            onNewTab={handleNewTabClick}
            onToggleFullHeight={handleToggleFullHeight}
            isFullHeight={isFullHeight}
            onTabRename={handleTabRename}
            onDuplicateTab={handleDuplicateTab}
            maxTabsReached={tabs.length >= MAX_TABS}
            onTabPin={handleTabPin}
            onCloseOthers={handleCloseOthers}
            onReorderTabs={handleReorderTabs}
            isSplitView={isSplitView}
            canSplit={canSplit}
            onToggleSplit={handleToggleSplit}
            onHelpClick={handleToggleHelp}
          />
          {cliSetupGuide && (
            <div
              className={styles.cliSetupBanner}
              data-testid="cli-setup-banner"
            >
              <span
                className={styles.cliSetupDot}
                style={{ backgroundColor: cliSetupGuide.brandColor }}
                aria-hidden="true"
              />
              <div className={styles.cliSetupBody}>
                <div className={styles.cliSetupHeader}>
                  <strong>{cliSetupGuide.instructions.title}</strong>
                  <span>{cliSetupGuide.provider}</span>
                </div>
                <p>{cliSetupGuide.instructions.description}</p>
                {cliSetupGuide.instructions.command && (
                  <code>{cliSetupGuide.instructions.command}</code>
                )}
                <span
                  className={`${styles.cliSetupStatus} ${
                    styles[cliSetupGuide.status]
                  }`}
                >
                  {cliSetupGuide.status === "starting"
                    ? "Starting in terminal..."
                    : cliSetupGuide.status === "failed"
                      ? `Failed: ${cliSetupGuide.error ?? "setup did not start"}`
                      : cliSetupGuide.status === "manual"
                        ? "Manual steps shown in terminal"
                        : "Running in terminal"}
                </span>
              </div>
              <div className={styles.cliSetupActions}>
                <button
                  type="button"
                  className={styles.cliSetupButton}
                  onClick={() => runCliSetupCommand(cliSetupGuide)}
                  disabled={cliSetupGuide.status === "starting"}
                >
                  {cliSetupGuide.status === "manual"
                    ? cliSetupGuide.instructions.buttonLabel
                    : cliSetupGuide.hasRun
                      ? "Run again"
                      : cliSetupGuide.instructions.buttonLabel}
                </button>
                <button
                  type="button"
                  className={styles.cliSetupDismiss}
                  onClick={refreshCliStatus}
                >
                  Recheck
                </button>
                <button
                  type="button"
                  className={styles.cliSetupDismiss}
                  onClick={() => setCliSetupGuide(null)}
                  aria-label="Dismiss CLI setup"
                >
                  Dismiss
                </button>
              </div>
            </div>
          )}
          <HelpPopover
            isOpen={isHelpOpen}
            onClose={() => setIsHelpOpen(false)}
          />
          <TerminalPaneArea
            tabs={tabs}
            activeTabId={activeTabId}
            isSplitView={isSplitView}
            rightPaneTabId={rightPaneTabId}
            splitRatio={splitRatio}
            splitContainerRef={splitContainerRef}
            onSplitRatioChange={handleSplitRatioChange}
            onRightPaneTabChange={handleRightPaneTabChange}
            renderPane={renderTerminalPane}
          />
        </>
      )}
      <BackendPickerPrompt
        isOpen={isSessionPromptOpen}
        availableBackends={config?.available ?? []}
        preferredBackend={config?.backend}
        isLoading={configLoading}
        onSelect={handleBackendSelect}
        onCancel={handleSessionPromptCancel}
      />
      <div
        ref={liveRegionRef}
        className={styles.visuallyHidden}
        role="status"
        aria-live="polite"
        aria-atomic="true"
      />
    </div>
  );
}
