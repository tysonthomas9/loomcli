/**
 * Main App component.
 * Wires useIssues hook to KanbanBoard with loading states, error handling,
 * and optimistic drag-drop updates. Manages view switching between Kanban,
 * Table, and Graph views with URL synchronization. Supports filtering and
 * search across all views.
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
import { getReviewType } from "@/utils/issueCategory";
import {
  AppLayout,
  WorkspaceBreadcrumb,
  SwimLaneBoard,
  IssueTable,
  LoadingSkeleton,
  ErrorDisplay,
  ConnectionStatus,
  ToastContainer,
  FilterBar,
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
  // Theme state
  const { theme, toggleTheme } = useTheme();

  // Workspace context for breadcrumb
  const { workspace } = useWorkspaceContext();

  // View state must be read before useIssues to determine fetch mode
  const [activeView, setActiveView] = useViewState();

  // Repo filter for multi-repo workspaces
  const [selectedRepos] = useRepoFilter();

  const {
    issues,
    isLoading,
    error,
    connectionState,
    reconnectAttempts,
    refetch,
    updateIssueStatus,
    retryConnection,
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

  // Timeout refs for panel close animations (prevents race conditions)
  const issuePanelTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );
  const agentPanelTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );

  // Helper to clear a timeout ref safely
  const clearTimeoutRef = useCallback(
    (ref: React.MutableRefObject<ReturnType<typeof setTimeout> | null>) => {
      if (ref.current !== null) {
        clearTimeout(ref.current);
        ref.current = null;
      }
    },
    [],
  );

  // Bulk selection state for Table view
  const {
    selectedIds,
    toggleSelection,
    deselectAll: clearSelection,
  } = useSelection({ visibleItems: filteredIssues });

  // Issue detail panel state
  const [isPanelOpen, setIsPanelOpen] = useState(false);
  const [selectedIssueId, setSelectedIssueId] = useState<string | null>(null);
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

  // Agent data (shared via AgentProvider — single polling loop)
  const { agents, agentTasks } = useAgentContext();

  // Agent detail panel state
  const [isAgentPanelOpen, setIsAgentPanelOpen] = useState(false);
  const [selectedAgentName, setSelectedAgentName] = useState<string | null>(
    null,
  );

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
      // Clear any pending panel close timeouts
      clearTimeoutRef(issuePanelTimeoutRef);
      clearTimeoutRef(agentPanelTimeoutRef);
    };
  }, [clearTimeoutRef]);

  const handleDragEnd = useCallback(
    async (issueId: string, newStatus: Status, oldStatus: Status) => {
      // Check if dragging from Ready (open) to In Progress (in_progress)
      // If so, show the assignee prompt instead of updating immediately
      if (oldStatus === "open" && newStatus === "in_progress") {
        setPendingDragData({ issueId, newStatus, oldStatus });
        return;
      }

      // Normal drag - update status directly
      try {
        await updateIssueStatus(issueId, newStatus);
      } catch (err) {
        if (!mountedRef.current) return;
        const message =
          err instanceof Error ? err.message : "Failed to update status";
        showToast(message, { type: "error" });
      }
    },
    [updateIssueStatus, showToast],
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
    } catch (err) {
      if (!mountedRef.current) return;
      const message =
        err instanceof Error ? err.message : "Failed to update status";
      showToast(message, { type: "error" });
    }
  }, [pendingDragData, updateIssueStatus, showToast]);

  // Handle search clear to sync both local and filter state
  const handleSearchClear = useCallback(() => {
    setSearchValue("");
    filterActions.setSearch(undefined);
  }, [filterActions]);

  // Handle issue click from SwimLaneBoard/IssueTable
  const handleIssueClick = useCallback(
    (issue: Issue) => {
      // If already viewing this issue in detail view, no-op
      if (issue.id === selectedIssueId && activeView === "issue-detail") {
        return;
      }

      // Close agent panel if open
      if (isAgentPanelOpen) {
        clearTimeoutRef(agentPanelTimeoutRef);
        setIsAgentPanelOpen(false);
        agentPanelTimeoutRef.current = setTimeout(() => {
          if (!mountedRef.current) return;
          setSelectedAgentName(null);
        }, 300);
      }

      // Ensure issue panel overlay is closed
      setIsPanelOpen(false);

      // Store current view as previous (but not if already in issue-detail)
      if (activeView !== "issue-detail") {
        setPreviousView(activeView);
      }

      setSelectedIssueId(issue.id);
      fetchIssue(issue.id);
      setActiveView("issue-detail");
    },
    [
      selectedIssueId,
      activeView,
      isAgentPanelOpen,
      fetchIssue,
      setActiveView,
      clearTimeoutRef,
    ],
  );

  // Handle panel close
  const handlePanelClose = useCallback(() => {
    setIsPanelOpen(false);
    // Clear issue details after animation completes
    // Store timeout ID to allow cancellation if panel reopens quickly
    issuePanelTimeoutRef.current = setTimeout(() => {
      if (!mountedRef.current) return;
      clearIssue();
      setSelectedIssueId(null);
    }, 300); // Match CSS transition duration
  }, [clearIssue]);

  // Handle back from issue detail view
  const handleBackFromDetail = useCallback(() => {
    setActiveView(previousView);
    clearIssue();
    setSelectedIssueId(null);
  }, [previousView, clearIssue, setActiveView]);

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
        if (!mountedRef.current) return;
        const message =
          err instanceof Error ? err.message : "Failed to approve";
        showToast(message, { type: "error" });
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
      // Cancel any pending agent panel timeout (prevents wiping the new selection)
      clearTimeoutRef(agentPanelTimeoutRef);

      // Close issue panel if open (only one panel at a time)
      if (isPanelOpen) {
        // Cancel pending issue panel timeout before starting new one
        clearTimeoutRef(issuePanelTimeoutRef);
        setIsPanelOpen(false);
        issuePanelTimeoutRef.current = setTimeout(() => {
          if (!mountedRef.current) return;
          clearIssue();
          setSelectedIssueId(null);
        }, 300);
      }

      setSelectedAgentName(agentName);
      setIsAgentPanelOpen(true);
    },
    [isPanelOpen, clearIssue, clearTimeoutRef],
  );

  // Handle agent panel close
  const handleAgentPanelClose = useCallback(() => {
    setIsAgentPanelOpen(false);
    // Store timeout ID to allow cancellation if panel reopens quickly
    agentPanelTimeoutRef.current = setTimeout(() => {
      if (!mountedRef.current) return;
      setSelectedAgentName(null);
    }, 300);
  }, []);

  // Handle repo click from WorkspaceTree (no-op for now)
  const handleRepoClick = useCallback((_repoName: string) => {
    // Future: select repo, show repo detail
  }, []);

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

  // Handle task click from agent panel (navigates to issue-detail view)
  const handleAgentTaskClick = useCallback(
    (taskId: string) => {
      // Cancel any pending timeouts
      clearTimeoutRef(issuePanelTimeoutRef);
      clearTimeoutRef(agentPanelTimeoutRef);
      // Close agent panel
      setIsAgentPanelOpen(false);
      setSelectedAgentName(null);
      // Ensure issue panel overlay is closed
      setIsPanelOpen(false);

      // Store current view as previous (but not if already in issue-detail)
      if (activeView !== "issue-detail") {
        setPreviousView(activeView);
      }

      setSelectedIssueId(taskId);
      fetchIssue(taskId);
      setActiveView("issue-detail");
    },
    [activeView, fetchIssue, setActiveView, clearTimeoutRef],
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
          showPriority={false}
          showType={false}
          showLabels={false}
          showGroupBy={true}
          groupBy={filters.groupBy ?? DEFAULT_GROUP_BY}
          onGroupByChange={filterActions.setGroupBy}
          variant="header"
          showClear={false}
        />
      </div>
    </div>
  );

  const headerTitle = (
    <WorkspaceBreadcrumb
      workspaceName={workspace?.name ?? null}
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
        compact
      />
    </div>
  );

  // Loading state: show skeleton columns
  if (isLoading) {
    return (
      <AppLayout
        title={headerTitle}
        navigation={headerNavigation}
        actions={headerActions}
        navRail={<NavRail activeView={activeView} onChange={setActiveView} />}
        sidebar={
          activeView === "workspace" ? (
            <WorkspaceTree onRepoClick={handleRepoClick} />
          ) : (
            <AgentsSidebar
              onAgentClick={handleAgentClick}
              defaultCollapsed={false}
              collapsible={false}
            />
          )
        }
      >
        <div
          className={styles.loadingContainer}
          data-testid="loading-container"
        >
          <LoadingSkeleton.Column />
          <LoadingSkeleton.Column />
          <LoadingSkeleton.Column />
        </div>
      </AppLayout>
    );
  }

  // Error state: show error display with retry
  if (error && !isLoading) {
    return (
      <AppLayout
        title={headerTitle}
        navigation={headerNavigation}
        actions={headerActions}
        navRail={<NavRail activeView={activeView} onChange={setActiveView} />}
        sidebar={
          activeView === "workspace" ? (
            <WorkspaceTree onRepoClick={handleRepoClick} />
          ) : (
            <AgentsSidebar
              onAgentClick={handleAgentClick}
              defaultCollapsed={false}
              collapsible={false}
            />
          )
        }
      >
        <ErrorDisplay
          variant="fetch-error"
          error={new Error(error)}
          showDetails
          onRetry={refetch}
        />
      </AppLayout>
    );
  }

  // Success state: show view based on activeView with filtered issues
  return (
    <AppLayout
      title={headerTitle}
      navigation={headerNavigation}
      actions={headerActions}
      navRail={<NavRail activeView={activeView} onChange={setActiveView} />}
      sidebar={
        activeView === "workspace" ? (
          <WorkspaceTree onRepoClick={handleRepoClick} />
        ) : (
          <AgentsSidebar
            onAgentClick={handleAgentClick}
            defaultCollapsed={false}
            collapsible={false}
          />
        )
      }
    >
      {activeView === "kanban" && (
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
          />
        </div>
      )}
      {activeView === "table" && (
        <>
          <IssueTable
            issues={filteredIssues}
            sortable
            showCheckbox
            selectedIds={selectedIds}
            onSelectionChange={toggleSelection}
            onRowClick={handleIssueClick}
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
        </>
      )}
      {activeView === "graph" && (
        <Suspense fallback={<LoadingSkeleton.Graph />}>
          <GraphView issues={filteredIssues} onNodeClick={handleIssueClick} />
        </Suspense>
      )}
      {activeView === "monitor" && (
        <Suspense fallback={<LoadingSkeleton.Monitor />}>
          <MonitorDashboard
            onViewChange={setActiveView}
            onIssueClick={handleIssueClick}
            onAgentClick={handleAgentClick}
          />
        </Suspense>
      )}
      {activeView === "observability" && (
        <Suspense fallback={<LoadingSkeleton.Column />}>
          <ObservabilityDashboard />
        </Suspense>
      )}
      {activeView === "settings" && (
        <Suspense fallback={<LoadingSkeleton.Column />}>
          <SettingsView />
        </Suspense>
      )}
      {activeView === "workspace" && (
        <Suspense fallback={<LoadingSkeleton.Column />}>
          <WorkspaceView />
        </Suspense>
      )}
      {activeView === "files" && (
        <Suspense fallback={<LoadingSkeleton.Column />}>
          <FileExplorer />
        </Suspense>
      )}
      {activeView === "issue-detail" && (
        <IssueDetailView
          issue={issueDetails}
          isLoading={isLoadingDetails}
          error={detailError}
          previousView={previousView}
          onBack={handleBackFromDetail}
          onApprove={handleApprove}
          onReject={handleReject}
          onOpenInTerminal={handleOpenIssueInTerminal}
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
      />
      <AgentDetailPanel
        isOpen={isAgentPanelOpen}
        agentName={selectedAgentName}
        agents={agents}
        agentTasks={agentTasks}
        onClose={handleAgentPanelClose}
        onTaskClick={handleAgentTaskClick}
      />
      <div style={{ display: activeView === "terminal" ? "contents" : "none" }}>
        <Suspense fallback={null}>
          <TerminalView
            isActive={activeView === "terminal"}
            pendingIssueContext={pendingIssueContext}
            onIssueContextConsumed={handleIssueContextConsumed}
          />
        </Suspense>
      </div>
      <TalkToLeadButton
        onClick={handleTalkToLeadClick}
        isActive={activeView === "terminal"}
      />
      <AssigneePrompt
        isOpen={pendingDragData !== null}
        onConfirm={handleAssigneeConfirm}
        onSkip={handleAssigneeSkip}
        recentNames={recentAssignees}
      />
    </AppLayout>
  );
}

export default App;
