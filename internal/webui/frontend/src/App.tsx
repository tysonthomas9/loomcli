/**
 * Main App component. Wires data hooks to views with filtering, URL sync,
 * and optimistic updates. Manages view switching, deep-linking, and panels.
 */

import {
  useState,
  useCallback,
  useEffect,
  useRef,
  useMemo,
  lazy,
  Suspense,
} from "react";

import { updateIssue, addComment, closeIssue } from "@/api";
import type { IssueContext } from "@/api/terminal";
import { buildShareUrl } from "@/utils/buildShareUrl";
import { getReviewType } from "@/utils/issueCategory";
import {
  AppLayout,
  WorkspaceBreadcrumb,
  SwimLaneBoard,
  IssueTable,
  LoadingSkeleton,
  ErrorDisplay,
  ErrorBoundary,
  ConnectionStatus,
  StaleDataBanner,
  DaemonUnavailableOverlay,
  ToastContainer,
  FilterBar,
  MoreFiltersMenu,
  SearchInput,
  IssueDetailPanel,
  IssueDetailView,
  AgentDetailPanel,
  AgentsSidebar,
  WorkspaceTree,
  AssigneePrompt,
  BulkActionToolbar,
  TalkToLeadButton,
  NavRail,
  ThemeToggle,
} from "@/components";
import { SearchTermProvider } from "@/contexts/SearchTermContext";
import type { BlockedInfo } from "@/components/KanbanBoard";
import type { ViewMode } from "@/components/ViewSwitcher";
import {
  useIssues,
  useRepoFilter,
  useViewState,
  useFilterState,
  DEFAULT_GROUP_BY,
  useIssueFilter,
  useDebounce,
  useBlockedIssues,
  useIssueDetail,
  useToast,
  useRecentAssignees,
  useSelection,
  useAgentContext,
  useTheme,
  useWorkspaceContext,
  useWorkspaceState,
  useWorkspaceParam,
  useDaemonHealth,
  usePanelManager,
} from "@/hooks";
import type { Issue, IssueDetails, Status } from "@/types";

import styles from "./App.module.css";

// Lazy load GraphView (React Flow ~100KB)
const GraphView = lazy(() =>
  import("@/components/GraphView").then((m) => ({ default: m.GraphView })),
);

// Lazy load MonitorDashboard (multi-agent operator view)
const MonitorDashboard = lazy(() =>
  import("@/components/MonitorDashboard").then((m) => ({
    default: m.MonitorDashboard,
  })),
);

// Lazy load SettingsView (backend config settings)
const SettingsView = lazy(() =>
  import("@/components/SettingsView").then((m) => ({
    default: m.SettingsView,
  })),
);

// Lazy load ObservabilityDashboard (observability metrics view)
const ObservabilityDashboard = lazy(() =>
  import("@/components/ObservabilityDashboard").then((m) => ({
    default: m.ObservabilityDashboard,
  })),
);

// Lazy load TerminalView (xterm.js ~100KB)
const TerminalView = lazy(() =>
  import("@/components/TerminalView").then((m) => ({
    default: m.TerminalView,
  })),
);

// Lazy load WorkspaceView (multi-repo workspace)
const WorkspaceView = lazy(() =>
  import("@/components/WorkspaceView").then((m) => ({
    default: m.WorkspaceView,
  })),
);

// Lazy load FileExplorer (CodeMirror 6 ~100KB)
const FileExplorer = lazy(() =>
  import("@/components/FileExplorer").then((m) => ({
    default: m.FileExplorer,
  })),
);

function App() {
  // Daemon health monitoring
  const {
    isDaemonAvailable,
    connectionMode,
    retryCountdown,
    lastError,
    retryNow: daemonRetryNow,
  } = useDaemonHealth();

  // Theme state
  const { theme, toggleTheme } = useTheme();

  // Workspace context for breadcrumb, single-repo guard, and workspace selection
  const {
    workspace,
    isMultiRepo,
    repos: workspaceRepos,
    selectedRepoNames,
    selectAll,
    selectRepos,
  } = useWorkspaceContext();

  // Workspace URL param sync (deep linking for workspace selection)
  const [workspaceParam, setWorkspaceParam] = useWorkspaceParam();

  // Available repo names for repo selector
  const availableRepoNames = useMemo(
    () => workspaceRepos.map((r) => r.name),
    [workspaceRepos],
  );

  // Scroll position cache for restoring scroll on back navigation
  const scrollPositionCache = useRef<Map<string, number>>(new Map());

  // View state must be read before useIssues to determine fetch mode
  const {
    view: activeView,
    setView: setActiveView,
    navigateToView,
    urlIssueId: selectedIssueId,
  } = useViewState({
    onPopState: useCallback((state: Record<string, unknown> | null) => {
      // Restore previousView from history state on browser back/forward
      // so the back button label is correct when navigating forward into issue-detail
      if (state?.previousView && typeof state.previousView === "string") {
        setPreviousView(state.previousView as ViewMode);
      }
    }, []),
  });

  // Repo filter for multi-repo workspaces
  const [selectedRepos, setSelectedRepos] = useRepoFilter();

  const {
    issues,
    isLoading,
    error,
    connectionState,
    reconnectAttempts,
    refetch,
    updateIssueStatus,
    retryConnection,
    showStaleBanner: sseShowStaleBanner,
    connectionLost: sseConnectionLost,
    disconnectedSince: sseDisconnectedSince,
    pendingIds,
  } = useIssues({
    mode:
      activeView === "graph"
        ? "graph"
        : activeView === "kanban"
          ? "kanban"
          : "ready",
    sourceRepos: selectedRepos.length > 0 ? selectedRepos : undefined,
  });

  // Filter state with URL synchronization
  const [filters, filterActions] = useFilterState();

  // Local search state with debouncing
  const [searchValue, setSearchValue] = useState(filters.search ?? "");
  const debouncedSearch = useDebounce(searchValue, 300);

  // Sync debounced search to filter state
  useEffect(() => {
    filterActions.setSearch(debouncedSearch || undefined);
  }, [debouncedSearch, filterActions]);

  // Sync search value from filter state (e.g., when Clear filters is clicked)
  useEffect(() => {
    const filterSearch = filters.search ?? "";
    // Only sync if it's an external change (differs from both local states)
    if (filterSearch !== searchValue && filterSearch !== debouncedSearch) {
      setSearchValue(filterSearch);
    }
  }, [filters.search, searchValue, debouncedSearch]);

  // Apply filters to issues
  // Build filter options conditionally to satisfy exactOptionalPropertyTypes
  const filterOptions: Parameters<typeof useIssueFilter>[1] = {};
  if (filters.search !== undefined) filterOptions.searchTerm = filters.search;
  if (filters.priority !== undefined) filterOptions.priority = filters.priority;
  if (filters.type !== undefined) filterOptions.issueType = filters.type;
  if (filters.labels !== undefined) filterOptions.labels = filters.labels;

  const { filteredIssues } = useIssueFilter(issues, filterOptions);

  // Only fetch blocked issues separately when NOT in kanban mode (kanban mode includes it inline)
  const { data: blockedIssuesData } = useBlockedIssues({
    enabled: activeView !== "kanban",
  });

  // Derive blockedIssuesMap from enriched issue data (kanban mode) or separate fetch
  const blockedIssuesMap = useMemo(() => {
    if (activeView === "kanban") {
      // In kanban mode, blocked info is already in the issue data
      const map = new Map<string, BlockedInfo>();
      for (const issue of issues) {
        if (issue.is_blocked) {
          map.set(issue.id, {
            blockedByCount: issue.blocked_by_count ?? 0,
            blockedBy: issue.blocked_by ?? [],
            ...(issue.blocked_by_details !== undefined && {
              blockedByDetails: issue.blocked_by_details,
            }),
          });
        }
      }
      return map.size > 0 ? map : undefined;
    }
    // Fallback: use separately fetched blocked data
    if (!blockedIssuesData) return undefined;
    const map = new Map<string, BlockedInfo>();
    for (const issue of blockedIssuesData) {
      map.set(issue.id, {
        blockedByCount: issue.blocked_by_count,
        blockedBy: issue.blocked_by,
      });
    }
    return map;
  }, [activeView, issues, blockedIssuesData]);

  const { toasts, showToast, dismissToast } = useToast();
  const mountedRef = useRef(true);

  // Centralized panel state (issue detail panel + agent detail panel).
  // Enforces mutual exclusivity with 300ms close-then-open transitions.
  const { activePanel, pendingPanel, openPanel, closePanel, isOpen } =
    usePanelManager();

  // Derive backwards-compatible booleans from panel state.
  const isPanelOpen = activePanel?.type === "issue";
  const isAgentPanelOpen = activePanel?.type === "agent";
  const selectedAgentName =
    activePanel?.type === "agent"
      ? activePanel.name
      : pendingPanel?.type === "agent"
        ? pendingPanel.name
        : null;

  // Workspace snapshot ref — updated synchronously during render (not in an effect)
  const wsState = { view: activeView, filters, searchValue, selectedIssueId };
  const latestStateRef = useRef(wsState);
  latestStateRef.current = wsState;

  // Bulk selection state for Table view
  const {
    selectedIds,
    toggleSelection,
    deselectAll: clearSelection,
  } = useSelection({ visibleItems: filteredIssues });

  const {
    issueDetails,
    isLoading: isLoadingDetails,
    error: detailError,
    fetchIssue,
    clearIssue,
  } = useIssueDetail();

  // Previous view state for issue-detail back navigation
  const [previousView, setPreviousView] = useState<ViewMode>("kanban");

  // Pending issue context for terminal seeding
  const [pendingIssueContext, setPendingIssueContext] = useState<
    IssueContext | undefined
  >(undefined);

  // Active terminal session count for badge display
  const [activeSessionCount, setActiveSessionCount] = useState(0);

  // Terminal unread output indicator
  const [hasTerminalUnread, setHasTerminalUnread] = useState(false);

  // Agent data (shared via AgentProvider — single polling loop)
  const {
    agents,
    agentTasks,
    showStaleBanner: agentShowStaleBanner,
    connectionLost: agentConnectionLost,
    disconnectedSince: agentDisconnectedSince,
    retryNow: agentRetryNow,
  } = useAgentContext();

  // Combine SSE and agent stale data states (show banner if either is stale)
  const showStaleBanner = sseShowStaleBanner || agentShowStaleBanner;
  const isConnectionLost = sseConnectionLost || agentConnectionLost;
  const staleBannerDisconnectedSince =
    sseDisconnectedSince ?? agentDisconnectedSince;
  const staleBannerRetry = sseConnectionLost
    ? retryConnection
    : agentConnectionLost
      ? agentRetryNow
      : retryConnection;

  // Assignee prompt state for Ready → In Progress drag
  const { recentAssignees, addRecentAssignee } = useRecentAssignees();
  const [pendingDragData, setPendingDragData] = useState<{
    issueId: string;
    newStatus: Status;
    oldStatus: Status;
  } | null>(null);

  // Track mount state for async operations (must set true in setup for StrictMode compatibility)
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Clear terminal unread when switching to terminal view
  useEffect(() => {
    if (activeView === "terminal") {
      setHasTerminalUnread(false);
    }
  }, [activeView]);

  // Redirect away from workspace view in single-repo mode (e.g. stale URL bookmark)
  useEffect(() => {
    if (!isMultiRepo && activeView === "workspace") {
      setActiveView("kanban");
    }
  }, [isMultiRepo, activeView, setActiveView]);

  // Sync workspace URL param → repo selection (mount deep-link + popstate back/forward)
  useEffect(() => {
    if (workspaceParam === null) {
      // null means "all workspaces" — only call selectAll if currently filtered
      if (
        selectedRepoNames.size !== workspaceRepos.length &&
        workspaceRepos.length > 0
      ) {
        selectAll();
      }
      return;
    }
    if (
      !(selectedRepoNames.size === 1 && selectedRepoNames.has(workspaceParam))
    ) {
      selectRepos([workspaceParam]);
    }
  }, [workspaceParam]); // eslint-disable-line react-hooks/exhaustive-deps

  // Deep-link: auto-fetch issue from URL; handle back/forward via popstate → useViewState
  useEffect(() => {
    if (selectedIssueId) fetchIssue(selectedIssueId);
    else if (activeView !== "issue-detail") clearIssue();
  }, [selectedIssueId]); // eslint-disable-line react-hooks/exhaustive-deps

  // Deep-link error: toast + navigate away when a deep-linked issue fails to load
  useEffect(() => {
    if (!detailError || activeView !== "issue-detail" || !selectedIssueId)
      return;
    showToast("Issue not found", { type: "error" });
    setActiveView("kanban");
  }, [detailError, activeView, selectedIssueId, showToast, setActiveView]);

  // Restore scroll position when returning from issue-detail view
  useEffect(() => {
    if (activeView !== "issue-detail") {
      const savedPosition = scrollPositionCache.current.get(activeView);
      if (savedPosition !== undefined) {
        requestAnimationFrame(() => {
          const mainEl = document.getElementById("main-content");
          if (mainEl) {
            mainEl.scrollTop = savedPosition;
          }
        });
        scrollPositionCache.current.delete(activeView);
      }
    }
  }, [activeView]);

  // Copy link handler: copies a clean shareable URL to clipboard
  const handleCopyLink = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(
        buildShareUrl({ view: activeView, issue: selectedIssueId }),
      );
      showToast("Link copied to clipboard", { type: "success" });
    } catch {
      showToast("Failed to copy link", { type: "error" });
    }
  }, [activeView, selectedIssueId, showToast]);

  const handleDragEnd = useCallback(
    async (issueId: string, newStatus: Status, oldStatus: Status) => {
      // Check if dragging from Ready (open) to In Progress (in_progress)
      // If so, show the assignee prompt instead of updating immediately
      if (oldStatus === "open" && newStatus === "in_progress") {
        setPendingDragData({ issueId, newStatus, oldStatus });
        return;
      }

      // Normal drag - update status directly
      // Toast on error is handled by updateIssueStatus rollback
      try {
        await updateIssueStatus(issueId, newStatus);
      } catch {
        // Rollback + error toast handled by useOptimisticUpdate
      }
    },
    [updateIssueStatus],
  );

  // Handle assignee prompt confirmation
  const handleAssigneeConfirm = useCallback(
    async (assignee: string) => {
      if (!pendingDragData) return;

      const { issueId, newStatus } = pendingDragData;
      setPendingDragData(null);

      // Extract the name without [H] prefix for storing in recent (we add it back when selecting)
      const nameWithoutPrefix = assignee.replace(/^\[H\]\s*/, "");
      addRecentAssignee(nameWithoutPrefix);

      try {
        // Update both status and assignee
        await updateIssue(issueId, { status: newStatus, assignee });
      } catch (err) {
        if (!mountedRef.current) return;
        const message =
          err instanceof Error ? err.message : "Failed to update status";
        showToast(message, { type: "error" });
      }
    },
    [pendingDragData, addRecentAssignee, showToast],
  );

  // Handle assignee prompt skip
  const handleAssigneeSkip = useCallback(async () => {
    if (!pendingDragData) return;

    const { issueId, newStatus } = pendingDragData;
    setPendingDragData(null);

    try {
      // Update status only (no assignee)
      await updateIssueStatus(issueId, newStatus);
    } catch {
      // Rollback + error toast handled by useOptimisticUpdate
    }
  }, [pendingDragData, updateIssueStatus]);

  // Handle search clear to sync both local and filter state
  const handleSearchClear = useCallback(() => {
    setSearchValue("");
    filterActions.setSearch(undefined);
  }, [filterActions]);

  // Handle issue click from SwimLaneBoard/IssueTable
  const handleIssueClick = useCallback(
    (issue: Issue) => {
      if (activeView === "issue-detail") {
        // Already in full-page detail — navigate to different issue within the view
        if (issue.id === selectedIssueId) return;
        navigateToView("issue-detail", {
          previousView,
          issueId: issue.id,
        });
        fetchIssue(issue.id);
        return;
      }

      // From list/graph/monitor views — open panel overlay
      // (mutual exclusivity + no-op guard handled by usePanelManager)
      openPanel({ type: "issue", id: issue.id });
      fetchIssue(issue.id);
    },
    [
      activeView,
      selectedIssueId,
      previousView,
      navigateToView,
      fetchIssue,
      openPanel,
    ],
  );

  // Handle panel close
  const handlePanelClose = useCallback(() => {
    closePanel();
    // Clear issue details after close animation completes
    setTimeout(() => {
      if (!mountedRef.current) return;
      clearIssue();
    }, 300);
  }, [closePanel, clearIssue]);

  // Handle back from issue detail view — use history.back() so browser
  // history is naturally traversed (popstate handler restores the previous view)
  const handleBackFromDetail = useCallback(() => {
    window.history.back();
  }, []);

  // Handle approve button click on review cards
  const handleApprove = useCallback(
    async (issue: Issue) => {
      try {
        const reviewType = getReviewType(issue);

        if (reviewType === "code") {
          // Code review: Close the issue (PR was reviewed and approved)
          await closeIssue(issue.id, "PR approved after code review");
          await refetch();
        } else if (reviewType === "plan") {
          // Plan review: Move to open (ready for implementation)
          await updateIssueStatus(issue.id, "open");
        } else if (reviewType === "help") {
          // Needs help: Move to in_progress (unblock)
          await updateIssueStatus(issue.id, "in_progress");
        }

        // Close the detail panel and clean up after successful approve
        handlePanelClose();
      } catch (err) {
        // updateIssueStatus errors are handled by useOptimisticUpdate rollback
        // Only show toast for non-updateIssueStatus errors (e.g., closeIssue)
        if (!mountedRef.current) return;
        const reviewType = getReviewType(issue);
        if (reviewType === "code") {
          const message =
            err instanceof Error ? err.message : "Failed to approve";
          showToast(message, { type: "error" });
        }
      }
    },
    [updateIssueStatus, refetch, handlePanelClose, showToast],
  );

  // Handle reject button submission on review cards
  const handleReject = useCallback(
    async (issue: Issue, comment: string) => {
      try {
        const reviewType = getReviewType(issue);

        // Add feedback comment
        const prefix = reviewType === "code" ? "CODE REVIEW" : "FEEDBACK";
        await addComment(issue.id, `${prefix}: ${comment}`);

        // Add needs-revision label and set status to open
        await updateIssue(issue.id, {
          status: "open",
          add_labels: ["needs-revision"],
        });

        // Refetch to reflect label/status changes and close panel
        await refetch();
        handlePanelClose();
      } catch (err) {
        if (!mountedRef.current) return;
        const message = err instanceof Error ? err.message : "Failed to reject";
        showToast(message, { type: "error" });
      }
    },
    [refetch, handlePanelClose, showToast],
  );

  // Handle agent click from AgentsSidebar or MonitorDashboard
  const handleAgentClick = useCallback(
    (agentName: string) => {
      const hadIssuePanel = isOpen("issue");
      // Mutual exclusivity + no-op guard handled by usePanelManager
      openPanel({ type: "agent", name: agentName });
      // Clear stale issue data after close animation when swapping from issue panel
      if (hadIssuePanel) {
        setTimeout(() => {
          if (!mountedRef.current) return;
          clearIssue();
        }, 300);
      }
    },
    [openPanel, isOpen, clearIssue],
  );

  // Handle agent panel close
  const handleAgentPanelClose = useCallback(() => {
    closePanel();
  }, [closePanel]);

  // Derive activeRepoName: null = "All Workspaces", string = specific repo
  const activeRepoName = useMemo(
    () => (selectedRepoNames.size === 1 ? [...selectedRepoNames][0] : null),
    [selectedRepoNames],
  );

  // Close all panels synchronously (no animation) for workspace switch
  const closeAllPanels = useCallback(() => {
    closePanel();
    clearIssue();
  }, [closePanel, clearIssue]);

  // Workspace state preservation: save/restore per-workspace UI state on switch
  const { switchWorkspace } = useWorkspaceState({
    stateRef: latestStateRef,
    setView: setActiveView,
    filterActions,
    setSearchValue,
    closeAllPanels,
  });

  // Handle workspace/repo selection from WorkspaceTree
  const handleWorkspaceSelect = useCallback(
    (repoName: string | null) => {
      // Skip if same workspace
      if (repoName === activeRepoName) return;
      // Save/restore workspace state
      switchWorkspace(repoName);
      // Update repo filter
      if (repoName === null) {
        selectAll();
      } else {
        selectRepos([repoName]);
      }
      // Sync workspace URL param
      setWorkspaceParam(repoName);
    },
    [
      activeRepoName,
      switchWorkspace,
      selectAll,
      selectRepos,
      setWorkspaceParam,
    ],
  );

  // Handle Talk to Lead button click
  const handleTalkToLeadClick = useCallback(() => {
    setActiveView("terminal");
  }, [setActiveView]);

  // Handle "Open in Terminal" from issue detail view
  const handleOpenIssueInTerminal = useCallback(
    (issue: Issue | IssueDetails) => {
      const context: IssueContext = {
        issue_id: issue.id,
        title: issue.title,
      };
      if (issue.description) context.description = issue.description;
      if (issue.design) context.design = issue.design;
      setPendingIssueContext(context);
      setActiveView("terminal");
    },
    [setActiveView],
  );

  const handleIssueContextConsumed = useCallback(() => {
    setPendingIssueContext(undefined);
  }, []);

  // Handle task click from agent panel (opens issue panel overlay)
  const handleAgentTaskClick = useCallback(
    (taskId: string) => {
      // Mutual exclusivity handled by usePanelManager (closes agent panel first)
      openPanel({ type: "issue", id: taskId });
      fetchIssue(taskId);
    },
    [openPanel, fetchIssue],
  );

  const headerNavigation = (
    <div className={styles.headerControls}>
      <div className={styles.searchWrapper}>
        <SearchInput
          value={searchValue}
          onChange={setSearchValue}
          onClear={handleSearchClear}
          placeholder="Search tasks..."
          size="md"
        />
      </div>
      <div className={styles.filtersWrapper}>
        <FilterBar
          filters={filters}
          actions={filterActions}
          showPriority={true}
          showType={true}
          showLabels={false}
          showGroupBy={false}
          showRepos={availableRepoNames.length > 1}
          availableRepos={availableRepoNames}
          selectedRepos={selectedRepos}
          onRepoChange={setSelectedRepos}
          variant="header"
          showClear={true}
        />
        <MoreFiltersMenu
          groupBy={filters.groupBy ?? DEFAULT_GROUP_BY}
          onGroupByChange={filterActions.setGroupBy}
        />
      </div>
    </div>
  );

  const headerTitle = (
    <WorkspaceBreadcrumb
      workspaceName={isMultiRepo ? (workspace?.name ?? null) : null}
      activeView={activeView}
    />
  );

  const headerActions = (
    <div className={styles.headerActions}>
      <ThemeToggle theme={theme} onToggle={toggleTheme} />
      <ConnectionStatus
        state={connectionState}
        onRetry={retryConnection}
        reconnectAttempts={reconnectAttempts}
        showText={false}
        showRetryButton={false}
        connectionLost={sseConnectionLost}
        compact
      />
    </div>
  );

  // Sidebar: WorkspaceTree (multi-repo workspace view) or AgentsSidebar
  const sidebarContent =
    activeView === "workspace" && isMultiRepo ? (
      <WorkspaceTree
        activeRepoName={activeRepoName}
        onWorkspaceSelect={handleWorkspaceSelect}
      />
    ) : (
      <AgentsSidebar
        onAgentClick={handleAgentClick}
        defaultCollapsed={false}
        collapsible={false}
      />
    );

  // Loading state: show skeleton columns
  if (isLoading) {
    return (
      <>
        <AppLayout
          title={headerTitle}
          navigation={headerNavigation}
          actions={headerActions}
          navRail={
            <NavRail
              activeView={activeView}
              onChange={setActiveView}
              sessionCount={activeSessionCount}
              badges={{ terminal: hasTerminalUnread }}
            />
          }
          sidebar={sidebarContent}
        >
          {(showStaleBanner || isConnectionLost) &&
            staleBannerDisconnectedSince !== null && (
              <StaleDataBanner
                disconnectedSince={staleBannerDisconnectedSince}
                onRetry={staleBannerRetry}
                connectionLost={isConnectionLost}
              />
            )}
          <div
            className={styles.loadingContainer}
            data-testid="loading-container"
          >
            {activeView === "table" ? (
              <LoadingSkeleton.Table />
            ) : (
              <>
                <LoadingSkeleton.Column />
                <LoadingSkeleton.Column />
                <LoadingSkeleton.Column />
              </>
            )}
          </div>
        </AppLayout>
        {!isDaemonAvailable && (
          <DaemonUnavailableOverlay
            mode={connectionMode}
            retryCountdown={retryCountdown}
            lastError={lastError}
            onRetry={daemonRetryNow}
            onSettingsClick={() => setActiveView("settings")}
          />
        )}
      </>
    );
  }

  // Error state: show error display with retry
  if (error && !isLoading) {
    return (
      <>
        <AppLayout
          title={headerTitle}
          navigation={headerNavigation}
          actions={headerActions}
          navRail={
            <NavRail
              activeView={activeView}
              onChange={setActiveView}
              sessionCount={activeSessionCount}
              badges={{ terminal: hasTerminalUnread }}
            />
          }
          sidebar={sidebarContent}
        >
          {(showStaleBanner || isConnectionLost) &&
            staleBannerDisconnectedSince !== null && (
              <StaleDataBanner
                disconnectedSince={staleBannerDisconnectedSince}
                onRetry={staleBannerRetry}
                connectionLost={isConnectionLost}
              />
            )}
          <ErrorDisplay
            variant="fetch-error"
            error={new Error(error)}
            showDetails
            onRetry={refetch}
          />
        </AppLayout>
        {!isDaemonAvailable && (
          <DaemonUnavailableOverlay
            mode={connectionMode}
            retryCountdown={retryCountdown}
            lastError={lastError}
            onRetry={daemonRetryNow}
            onSettingsClick={() => setActiveView("settings")}
          />
        )}
      </>
    );
  }

  // Success state: show view based on activeView with filtered issues
  return (
    <SearchTermProvider value={debouncedSearch}>
      <AppLayout
        title={headerTitle}
        navigation={headerNavigation}
        actions={headerActions}
        navRail={
          <NavRail
            activeView={activeView}
            onChange={setActiveView}
            sessionCount={activeSessionCount}
            badges={{ terminal: hasTerminalUnread }}
          />
        }
        sidebar={sidebarContent}
      >
        {activeView === "kanban" && (
          <ErrorBoundary resetOnChange={[activeView]}>
            <div className={styles.kanbanShell}>
              <SwimLaneBoard
                issues={filteredIssues}
                groupBy={filters.groupBy ?? DEFAULT_GROUP_BY}
                onDragEnd={handleDragEnd}
                onIssueClick={handleIssueClick}
                {...(blockedIssuesMap !== undefined && {
                  blockedIssues: blockedIssuesMap,
                })}
                {...(filters.showBlocked !== undefined && {
                  showBlocked: filters.showBlocked,
                })}
                {...(pendingIds.size > 0 && { pendingIds })}
              />
            </div>
          </ErrorBoundary>
        )}
        {activeView === "table" && (
          <ErrorBoundary resetOnChange={[activeView]}>
            <IssueTable
              issues={filteredIssues}
              sortable
              showCheckbox
              selectedIds={selectedIds}
              onSelectionChange={toggleSelection}
              onRowClick={handleIssueClick}
              searchTerm={debouncedSearch}
              {...(selectedIssueId !== null && { selectedId: selectedIssueId })}
              {...(blockedIssuesMap !== undefined && {
                blockedIssues: blockedIssuesMap,
              })}
              {...(filters.showBlocked !== undefined && {
                showBlocked: filters.showBlocked,
              })}
            />
            <BulkActionToolbar
              selectedIds={selectedIds}
              onClearSelection={clearSelection}
            />
          </ErrorBoundary>
        )}
        {activeView === "graph" && (
          <ErrorBoundary resetOnChange={[activeView]}>
            <Suspense fallback={<LoadingSkeleton.Graph />}>
              <GraphView
                issues={filteredIssues}
                onNodeClick={handleIssueClick}
              />
            </Suspense>
          </ErrorBoundary>
        )}
        {activeView === "monitor" && (
          <ErrorBoundary resetOnChange={[activeView]}>
            <Suspense fallback={<LoadingSkeleton.Monitor />}>
              <MonitorDashboard
                onViewChange={setActiveView}
                onIssueClick={handleIssueClick}
                onAgentClick={handleAgentClick}
              />
            </Suspense>
          </ErrorBoundary>
        )}
        {activeView === "observability" && (
          <ErrorBoundary resetOnChange={[activeView]}>
            <Suspense fallback={<LoadingSkeleton.Observability />}>
              <ObservabilityDashboard />
            </Suspense>
          </ErrorBoundary>
        )}
        {activeView === "settings" && (
          <ErrorBoundary resetOnChange={[activeView]}>
            <Suspense fallback={<LoadingSkeleton.Column />}>
              <SettingsView />
            </Suspense>
          </ErrorBoundary>
        )}
        {activeView === "workspace" && isMultiRepo && (
          <ErrorBoundary resetOnChange={[activeView]}>
            <Suspense fallback={<LoadingSkeleton.Column />}>
              <WorkspaceView />
            </Suspense>
          </ErrorBoundary>
        )}
        {activeView === "files" && (
          <ErrorBoundary resetOnChange={[activeView]}>
            <Suspense fallback={<LoadingSkeleton.FileExplorer />}>
              <FileExplorer />
            </Suspense>
          </ErrorBoundary>
        )}
        {activeView === "issue-detail" && (
          <ErrorBoundary resetOnChange={[selectedIssueId]}>
            <IssueDetailView
              issue={issueDetails}
              isLoading={isLoadingDetails}
              error={detailError}
              previousView={previousView}
              onBack={handleBackFromDetail}
              onApprove={handleApprove}
              onReject={handleReject}
              onOpenInTerminal={handleOpenIssueInTerminal}
              onCopyLink={handleCopyLink}
              onNavigateToIssue={handleIssueClick}
            />
          </ErrorBoundary>
        )}
        {(showStaleBanner || isConnectionLost) &&
          staleBannerDisconnectedSince !== null && (
            <StaleDataBanner
              disconnectedSince={staleBannerDisconnectedSince}
              onRetry={staleBannerRetry}
              connectionLost={isConnectionLost}
            />
          )}
        <ToastContainer toasts={toasts} onDismiss={dismissToast} />
        <IssueDetailPanel
          isOpen={isPanelOpen}
          issue={issueDetails}
          isLoading={isLoadingDetails}
          error={detailError}
          onClose={handlePanelClose}
          onApprove={handleApprove}
          onReject={handleReject}
          onCopyLink={handleCopyLink}
          onNavigateToIssue={handleIssueClick}
        />
        <AgentDetailPanel
          isOpen={isAgentPanelOpen}
          agentName={selectedAgentName}
          agents={agents}
          agentTasks={agentTasks}
          onClose={handleAgentPanelClose}
          onTaskClick={handleAgentTaskClick}
        />
        <div
          style={{ display: activeView === "terminal" ? "contents" : "none" }}
        >
          <Suspense fallback={<LoadingSkeleton.Terminal />}>
            <TerminalView
              isActive={activeView === "terminal"}
              pendingIssueContext={pendingIssueContext}
              onIssueContextConsumed={handleIssueContextConsumed}
              onActiveSessionCountChange={setActiveSessionCount}
              onUnreadChange={setHasTerminalUnread}
              {...(selectedIssueId != null && { issueId: selectedIssueId })}
            />
          </Suspense>
        </div>
        <TalkToLeadButton
          onClick={handleTalkToLeadClick}
          isActive={activeView === "terminal"}
          sessionCount={activeSessionCount}
        />
        <AssigneePrompt
          isOpen={pendingDragData !== null}
          onConfirm={handleAssigneeConfirm}
          onSkip={handleAssigneeSkip}
          recentNames={recentAssignees}
        />
      </AppLayout>
      {!isDaemonAvailable && (
        <DaemonUnavailableOverlay
          mode={connectionMode}
          retryCountdown={retryCountdown}
          lastError={lastError}
          onRetry={daemonRetryNow}
          onSettingsClick={() => setActiveView("settings")}
        />
      )}
    </SearchTermProvider>
  );
}
export default App;
