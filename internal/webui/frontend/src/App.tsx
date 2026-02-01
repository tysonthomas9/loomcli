/**
 * Main App component.
 * Wires useIssues hook to KanbanBoard with loading states, error handling,
 * and optimistic drag-drop updates. Manages view switching between Kanban,
 * Table, and Graph views with URL synchronization. Supports filtering and
 * search across all views.
 */

import { useState, useCallback, useEffect, useRef, useMemo, lazy, Suspense } from 'react';
import type { Issue, Status } from '@/types';
import {
  useIssues,
  useViewState,
  useFilterState,
  DEFAULT_GROUP_BY,
  useIssueFilter,
  useDebounce,
  useBlockedIssues,
  useIssueDetail,
  useToast,
  useStats,
  useRecentAssignees,
  useSelection,
} from '@/hooks';
import { updateIssue, addComment } from '@/api';
import type { BlockedInfo } from '@/components/KanbanBoard';
import styles from './App.module.css';
import {
  AppLayout,
  SwimLaneBoard,
  IssueTable,
  ViewSwitcher,
  LoadingSkeleton,
  ErrorDisplay,
  ConnectionStatus,
  BlockedSummary,
  ToastContainer,
  FilterBar,
  SearchInput,
  IssueDetailPanel,
  AgentsSidebar,
  StatsHeader,
  AssigneePrompt,
  BulkActionToolbar,
  TalkToLeadButton,
} from '@/components';

// Lazy load GraphView (React Flow ~100KB)
const GraphView = lazy(() =>
  import('@/components/GraphView').then((m) => ({ default: m.GraphView }))
);

// Lazy load MonitorDashboard (multi-agent operator view)
const MonitorDashboard = lazy(() =>
  import('@/components/MonitorDashboard').then((m) => ({ default: m.MonitorDashboard }))
);

function App() {
  // View state must be read before useIssues to determine fetch mode
  const [activeView, setActiveView] = useViewState();

  const {
    issues,
    isLoading,
    error,
    connectionState,
    reconnectAttempts,
    refetch,
    updateIssueStatus,
    retryConnection,
  } = useIssues({ mode: activeView === 'graph' ? 'graph' : 'ready' });

  // Filter state with URL synchronization
  const [filters, filterActions] = useFilterState();

  // Local search state with debouncing
  const [searchValue, setSearchValue] = useState(filters.search ?? '');
  const debouncedSearch = useDebounce(searchValue, 300);

  // Sync debounced search to filter state
  useEffect(() => {
    filterActions.setSearch(debouncedSearch || undefined);
  }, [debouncedSearch, filterActions]);

  // Sync search value from filter state (e.g., when Clear filters is clicked)
  useEffect(() => {
    const filterSearch = filters.search ?? '';
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

  // Fetch blocked issues for display
  const { data: blockedIssuesData } = useBlockedIssues();

  // Convert BlockedIssue[] to Map<string, BlockedInfo> for efficient lookup
  const blockedIssuesMap = useMemo(() => {
    if (!blockedIssuesData) return undefined;
    const map = new Map<string, BlockedInfo>();
    for (const issue of blockedIssuesData) {
      map.set(issue.id, {
        blockedByCount: issue.blocked_by_count,
        blockedBy: issue.blocked_by,
      });
    }
    return map;
  }, [blockedIssuesData]);

  const { toasts, showToast, dismissToast } = useToast();
  const {
    data: stats,
    loading: statsLoading,
    error: statsError,
    refetch: refetchStats,
  } = useStats({
    pollInterval: 30000,
  });
  const mountedRef = useRef(true);

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

  const handleDragEnd = useCallback(
    async (issueId: string, newStatus: Status, oldStatus: Status) => {
      // Check if dragging from Ready (open) to In Progress (in_progress)
      // If so, show the assignee prompt instead of updating immediately
      if (oldStatus === 'open' && newStatus === 'in_progress') {
        setPendingDragData({ issueId, newStatus, oldStatus });
        return;
      }

      // Normal drag - update status directly
      try {
        await updateIssueStatus(issueId, newStatus);
      } catch (err) {
        if (!mountedRef.current) return;
        const message = err instanceof Error ? err.message : 'Failed to update status';
        showToast(message, { type: 'error' });
      }
    },
    [updateIssueStatus, showToast]
  );

  // Handle assignee prompt confirmation
  const handleAssigneeConfirm = useCallback(
    async (assignee: string) => {
      if (!pendingDragData) return;

      const { issueId, newStatus } = pendingDragData;
      setPendingDragData(null);

      // Extract the name without [H] prefix for storing in recent (we add it back when selecting)
      const nameWithoutPrefix = assignee.replace(/^\[H\]\s*/, '');
      addRecentAssignee(nameWithoutPrefix);

      try {
        // Update both status and assignee
        await updateIssue(issueId, { status: newStatus, assignee });
      } catch (err) {
        if (!mountedRef.current) return;
        const message = err instanceof Error ? err.message : 'Failed to update status';
        showToast(message, { type: 'error' });
      }
    },
    [pendingDragData, addRecentAssignee, showToast]
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
      const message = err instanceof Error ? err.message : 'Failed to update status';
      showToast(message, { type: 'error' });
    }
  }, [pendingDragData, updateIssueStatus, showToast]);

  // Handle approve button click on review cards
  const handleApprove = useCallback(
    async (issue: Issue) => {
      try {
        const hasNeedReview = issue.title?.includes('[Need Review]') ?? false;
        const isReviewStatus = issue.status === 'review';
        const isBlockedWithNotes = issue.status === 'blocked' && !!issue.notes;

        if (hasNeedReview) {
          // Plan review: Remove [Need Review] prefix and set to open (Ready column)
          const newTitle = issue.title.replace(/\[Need Review\]\s*/g, '').trim();
          await updateIssue(issue.id, { title: newTitle, status: 'open' });
        } else if (isReviewStatus) {
          // Code review: Move to closed (Done)
          await updateIssue(issue.id, { status: 'closed' });
        } else if (isBlockedWithNotes) {
          // Needs help: Move to in_progress (unblock)
          await updateIssue(issue.id, { status: 'in_progress' });
        }
      } catch (err) {
        if (!mountedRef.current) return;
        const message = err instanceof Error ? err.message : 'Failed to approve';
        showToast(message, { type: 'error' });
      }
    },
    [showToast]
  );

  // Handle reject button submission on review cards
  const handleReject = useCallback(
    async (issue: Issue, comment: string) => {
      try {
        // First add the comment
        await addComment(issue.id, comment);

        // Then update status and remove [Need Review] prefix if present
        const hasNeedReview = issue.title?.includes('[Need Review]') ?? false;
        if (hasNeedReview) {
          const newTitle = issue.title.replace(/\[Need Review\]\s*/g, '').trim();
          await updateIssue(issue.id, { title: newTitle, status: 'open' });
        } else {
          await updateIssue(issue.id, { status: 'open' });
        }
      } catch (err) {
        if (!mountedRef.current) return;
        const message = err instanceof Error ? err.message : 'Failed to reject';
        showToast(message, { type: 'error' });
      }
    },
    [showToast]
  );

  // Handle search clear to sync both local and filter state
  const handleSearchClear = useCallback(() => {
    setSearchValue('');
    filterActions.setSearch(undefined);
  }, [filterActions]);

  // Handle issue click from SwimLaneBoard/IssueTable
  const handleIssueClick = useCallback(
    (issue: Issue) => {
      // If clicking the same issue that's already selected, just ensure panel is open
      if (issue.id === selectedIssueId && isPanelOpen) {
        return;
      }

      setSelectedIssueId(issue.id);
      setIsPanelOpen(true);
      fetchIssue(issue.id);
    },
    [selectedIssueId, isPanelOpen, fetchIssue]
  );

  // Handle panel close
  const handlePanelClose = useCallback(() => {
    setIsPanelOpen(false);
    // Clear issue details after animation completes
    setTimeout(() => {
      if (!mountedRef.current) return;
      clearIssue();
      setSelectedIssueId(null);
    }, 300); // Match CSS transition duration
  }, [clearIssue]);

  // Handle blocked issue click from BlockedSummary dropdown
  const handleBlockedIssueClick = useCallback(
    (issueId: string) => {
      if (issueId === '__show_all_blocked__') {
        // Toggle showBlocked filter to true
        filterActions.setShowBlocked(true);
      }
      // Individual issue clicks could navigate to issue detail in the future
      // For now, just show all blocked issues
    },
    [filterActions]
  );

  // Loading state: show skeleton columns (ViewSwitcher disabled, no filters)
  if (isLoading) {
    return (
      <AppLayout
        navigation={<ViewSwitcher activeView={activeView} onChange={setActiveView} disabled />}
        actions={
          <div className={styles.actionsContainer}>
            <StatsHeader
              stats={stats}
              loading={statsLoading}
              error={statsError}
              onRetry={refetchStats}
            />
            <BlockedSummary onIssueClick={handleBlockedIssueClick} />
            <ConnectionStatus state={connectionState} />
          </div>
        }
        sidebar={<AgentsSidebar />}
      >
        <div className={styles.loadingContainer} data-testid="loading-container">
          <LoadingSkeleton.Column />
          <LoadingSkeleton.Column />
          <LoadingSkeleton.Column />
        </div>
      </AppLayout>
    );
  }

  // Error state: show error display with retry (ViewSwitcher disabled, no filters)
  if (error && !isLoading) {
    return (
      <AppLayout
        navigation={<ViewSwitcher activeView={activeView} onChange={setActiveView} disabled />}
        actions={
          <div className={styles.actionsContainer}>
            <StatsHeader
              stats={stats}
              loading={statsLoading}
              error={statsError}
              onRetry={refetchStats}
            />
            <BlockedSummary onIssueClick={handleBlockedIssueClick} />
            <ConnectionStatus
              state={connectionState}
              onRetry={retryConnection}
              reconnectAttempts={reconnectAttempts}
            />
          </div>
        }
        sidebar={<AgentsSidebar />}
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

  // Navigation element with view switcher, search, and filters (success state only)
  const navigation = (
    <div className={styles.navigationBar} data-testid="navigation-bar">
      <ViewSwitcher activeView={activeView} onChange={setActiveView} />
      <SearchInput
        value={searchValue}
        onChange={setSearchValue}
        onClear={handleSearchClear}
        placeholder="Search issues..."
        size="sm"
      />
      <FilterBar
        filters={filters}
        actions={filterActions}
        groupBy={filters.groupBy ?? DEFAULT_GROUP_BY}
        onGroupByChange={filterActions.setGroupBy}
      />
    </div>
  );

  // Success state: show view based on activeView with filtered issues
  return (
    <AppLayout
      navigation={navigation}
      actions={
        <div className={styles.actionsContainer}>
          <StatsHeader
            stats={stats}
            loading={statsLoading}
            error={statsError}
            onRetry={refetchStats}
          />
          <BlockedSummary onIssueClick={handleBlockedIssueClick} />
          <ConnectionStatus
            state={connectionState}
            onRetry={retryConnection}
            reconnectAttempts={reconnectAttempts}
          />
        </div>
      }
      sidebar={<AgentsSidebar />}
    >
      {activeView === 'kanban' && (
        <SwimLaneBoard
          issues={filteredIssues}
          groupBy={filters.groupBy ?? DEFAULT_GROUP_BY}
          onDragEnd={handleDragEnd}
          onIssueClick={handleIssueClick}
          onApprove={handleApprove}
          onReject={handleReject}
          {...(blockedIssuesMap !== undefined && { blockedIssues: blockedIssuesMap })}
          {...(filters.showBlocked !== undefined && { showBlocked: filters.showBlocked })}
        />
      )}
      {activeView === 'table' && (
        <>
          <IssueTable
            issues={filteredIssues}
            sortable
            showCheckbox
            selectedIds={selectedIds}
            onSelectionChange={toggleSelection}
            onRowClick={handleIssueClick}
            {...(selectedIssueId !== null && { selectedId: selectedIssueId })}
            {...(blockedIssuesMap !== undefined && { blockedIssues: blockedIssuesMap })}
            {...(filters.showBlocked !== undefined && { showBlocked: filters.showBlocked })}
          />
          <BulkActionToolbar selectedIds={selectedIds} onClearSelection={clearSelection} />
        </>
      )}
      {activeView === 'graph' && (
        <Suspense fallback={<LoadingSkeleton.Graph />}>
          <GraphView issues={filteredIssues} onNodeClick={handleIssueClick} />
        </Suspense>
      )}
      {activeView === 'monitor' && (
        <Suspense fallback={<LoadingSkeleton.Monitor />}>
          <MonitorDashboard onViewChange={setActiveView} />
        </Suspense>
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
      <TalkToLeadButton />
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
