/**
 * Main App component.
 * Wires useIssues hook to KanbanBoard with loading states, error handling,
 * and optimistic drag-drop updates. Manages view switching between Kanban,
 * Table, and Graph views with URL synchronization. Supports filtering and
 * search across all views.
 */

import { useState, useCallback, useEffect, useRef, useMemo, lazy, Suspense } from 'react';

import { updateIssue, addComment } from '@/api';
import {
  AppLayout,
  SwimLaneBoard,
  IssueTable,
  LoadingSkeleton,
  ErrorDisplay,
  ConnectionStatus,
  ToastContainer,
  FilterBar,
  SearchInput,
  IssueDetailPanel,
  AgentDetailPanel,
  AgentsSidebar,
  AssigneePrompt,
  BulkActionToolbar,
  TalkToLeadButton,
  NavRail,
} from '@/components';
import type { BlockedInfo } from '@/components/KanbanBoard';
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
  useRecentAssignees,
  useSelection,
  useAgents,
} from '@/hooks';
import type { Issue, Status } from '@/types';

import styles from './App.module.css';

// Lazy load GraphView (React Flow ~100KB)
const GraphView = lazy(() =>
  import('@/components/GraphView').then((m) => ({ default: m.GraphView }))
);

// Lazy load MonitorDashboard (multi-agent operator view)
const MonitorDashboard = lazy(() =>
  import('@/components/MonitorDashboard').then((m) => ({ default: m.MonitorDashboard }))
);

// Lazy load TerminalPanel (xterm.js ~100KB)
const TerminalPanel = lazy(() =>
  import('@/components/TerminalPanel').then((m) => ({ default: m.TerminalPanel }))
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
  const mountedRef = useRef(true);
  const profileMenuRef = useRef<HTMLDivElement | null>(null);

  // Timeout refs for panel close animations (prevents race conditions)
  const issuePanelTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const agentPanelTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Helper to clear a timeout ref safely
  const clearTimeoutRef = useCallback(
    (ref: React.MutableRefObject<ReturnType<typeof setTimeout> | null>) => {
      if (ref.current !== null) {
        clearTimeout(ref.current);
        ref.current = null;
      }
    },
    []
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

  // Agent data (shared between AgentsSidebar, MonitorDashboard, and AgentDetailPanel)
  const { agents, agentTasks } = useAgents({ pollInterval: 5000 });

  // Agent detail panel state
  const [isAgentPanelOpen, setIsAgentPanelOpen] = useState(false);
  const [selectedAgentName, setSelectedAgentName] = useState<string | null>(null);

  // Terminal panel state
  const [isTerminalOpen, setIsTerminalOpen] = useState(false);
  const [isProfileMenuOpen, setIsProfileMenuOpen] = useState(false);

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

  // Close profile menu on outside click
  useEffect(() => {
    if (!isProfileMenuOpen) return;
    const handleClickOutside = (event: MouseEvent) => {
      if (!profileMenuRef.current?.contains(event.target as Node)) {
        setIsProfileMenuOpen(false);
      }
    };
    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [isProfileMenuOpen]);

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

      // Cancel any pending issue panel timeout (prevents wiping the new selection)
      clearTimeoutRef(issuePanelTimeoutRef);

      // Close agent panel if open (only one panel at a time)
      if (isAgentPanelOpen) {
        // Cancel pending agent panel timeout before starting new one
        clearTimeoutRef(agentPanelTimeoutRef);
        setIsAgentPanelOpen(false);
        agentPanelTimeoutRef.current = setTimeout(() => {
          if (!mountedRef.current) return;
          setSelectedAgentName(null);
        }, 300);
      }

      // Close terminal if open
      if (isTerminalOpen) {
        setIsTerminalOpen(false);
      }

      setSelectedIssueId(issue.id);
      setIsPanelOpen(true);
      fetchIssue(issue.id);
    },
    [selectedIssueId, isPanelOpen, isAgentPanelOpen, isTerminalOpen, fetchIssue, clearTimeoutRef]
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

      // Close terminal if open
      if (isTerminalOpen) {
        setIsTerminalOpen(false);
      }

      setSelectedAgentName(agentName);
      setIsAgentPanelOpen(true);
    },
    [isPanelOpen, isTerminalOpen, clearIssue, clearTimeoutRef]
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

  // Handle Talk to Lead button click
  const handleTalkToLeadClick = useCallback(() => {
    if (isTerminalOpen) {
      // Close terminal
      setIsTerminalOpen(false);
    } else {
      // Close other panels first (single-panel policy)
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
      if (isAgentPanelOpen) {
        // Cancel pending agent panel timeout before starting new one
        clearTimeoutRef(agentPanelTimeoutRef);
        setIsAgentPanelOpen(false);
        agentPanelTimeoutRef.current = setTimeout(() => {
          if (!mountedRef.current) return;
          setSelectedAgentName(null);
        }, 300);
      }
      setIsTerminalOpen(true);
    }
  }, [isTerminalOpen, isPanelOpen, isAgentPanelOpen, clearIssue, clearTimeoutRef]);

  // Handle terminal panel close
  const handleTerminalClose = useCallback(() => {
    setIsTerminalOpen(false);
  }, []);

  // Handle task click from agent panel (opens IssueDetailPanel for that task)
  const handleAgentTaskClick = useCallback(
    (taskId: string) => {
      // Cancel any pending issue panel timeout to prevent it from wiping the new selection
      clearTimeoutRef(issuePanelTimeoutRef);
      // Cancel any pending agent panel timeout before the transition
      clearTimeoutRef(agentPanelTimeoutRef);
      // Close agent panel first
      setIsAgentPanelOpen(false);
      agentPanelTimeoutRef.current = setTimeout(() => {
        if (!mountedRef.current) return;
        setSelectedAgentName(null);
        // Open issue panel for the task
        setSelectedIssueId(taskId);
        setIsPanelOpen(true);
        fetchIssue(taskId);
      }, 300);
    },
    [fetchIssue, clearTimeoutRef]
  );

  const toggleProfileMenu = useCallback(() => {
    setIsProfileMenuOpen((prev) => !prev);
  }, []);

  const closeProfileMenu = useCallback(() => {
    setIsProfileMenuOpen(false);
  }, []);

  const headerNavigation = (
    <div className={styles.headerControls}>
      <div className={styles.searchWrapper}>
        <SearchInput
          value={searchValue}
          onChange={setSearchValue}
          onClear={handleSearchClear}
          placeholder="Search issues..."
          size="md"
        />
      </div>
      <div className={styles.filtersWrapper}>
        <FilterBar
          filters={filters}
          actions={filterActions}
          groupBy={filters.groupBy ?? DEFAULT_GROUP_BY}
          onGroupByChange={filterActions.setGroupBy}
          showClear={false}
        />
      </div>
    </div>
  );

  const headerActions = (
    <div className={styles.headerActions}>
      <ConnectionStatus
        state={connectionState}
        onRetry={retryConnection}
        reconnectAttempts={reconnectAttempts}
        showText={false}
        showRetryButton={false}
        compact
      />
      <div className={styles.profileMenu} ref={profileMenuRef}>
        <button
          type="button"
          className={styles.profileButton}
          onClick={toggleProfileMenu}
          aria-haspopup="true"
          aria-expanded={isProfileMenuOpen}
          aria-label="Open profile menu"
        >
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path
              d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </button>
        {isProfileMenuOpen && (
          <div className={styles.profileDropdown} role="menu">
            <button type="button" className={styles.profileItem} onClick={closeProfileMenu}>
              Settings
            </button>
            <button type="button" className={styles.profileItem} onClick={closeProfileMenu}>
              Logout
            </button>
          </div>
        )}
      </div>
    </div>
  );

  // Loading state: show skeleton columns
  if (isLoading) {
    return (
      <AppLayout
        title="Cortex"
        navigation={headerNavigation}
        actions={headerActions}
        navRail={<NavRail activeView={activeView} onChange={setActiveView} />}
        sidebar={
          <AgentsSidebar
            onAgentClick={handleAgentClick}
            defaultCollapsed={false}
            collapsible={false}
          />
        }
      >
        <div className={styles.loadingContainer} data-testid="loading-container">
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
        title="Cortex"
        navigation={headerNavigation}
        actions={headerActions}
        navRail={<NavRail activeView={activeView} onChange={setActiveView} />}
        sidebar={
          <AgentsSidebar
            onAgentClick={handleAgentClick}
            defaultCollapsed={false}
            collapsible={false}
          />
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
      title="Cortex"
      navigation={headerNavigation}
      actions={headerActions}
      navRail={<NavRail activeView={activeView} onChange={setActiveView} />}
      sidebar={
        <AgentsSidebar
          onAgentClick={handleAgentClick}
          defaultCollapsed={false}
          collapsible={false}
        />
      }
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
          <MonitorDashboard
            onViewChange={setActiveView}
            onIssueClick={handleIssueClick}
            onAgentClick={handleAgentClick}
          />
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
      <AgentDetailPanel
        isOpen={isAgentPanelOpen}
        agentName={selectedAgentName}
        agents={agents}
        agentTasks={agentTasks}
        onClose={handleAgentPanelClose}
        onTaskClick={handleAgentTaskClick}
      />
      <TalkToLeadButton onClick={handleTalkToLeadClick} isActive={isTerminalOpen} />
      <AssigneePrompt
        isOpen={pendingDragData !== null}
        onConfirm={handleAssigneeConfirm}
        onSkip={handleAssigneeSkip}
        recentNames={recentAssignees}
      />
      <Suspense fallback={null}>
        <TerminalPanel isOpen={isTerminalOpen} onClose={handleTerminalClose} />
      </Suspense>
    </AppLayout>
  );
}

export default App;
