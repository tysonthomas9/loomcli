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
  type RefObject,
} from "react";
import { useParams, useNavigate } from "react-router-dom";

import { updateIssue, addComment, closeIssue } from "@/api";
import type { IssueContext } from "@/api/terminal";
import { getAgentTerminalInfo } from "@/api/logs";
import { buildShareUrl } from "@/utils/buildShareUrl";
import { getReviewType } from "@/utils/issueCategory";
import {
  AppLayout,
  WorkspaceBreadcrumb,
  SwimLaneBoard,
  IssueTable,
  LoadingSkeleton,
  EmptyWorkspaceBoard,
  ErrorDisplay,
  ErrorBoundary,
  ConnectionStatus,
  StaleDataBanner,
  DaemonUnavailableOverlay,
  ToastContainer,
  FilterBar,
  MoreFiltersMenu,
  SearchInput,
  SearchScopeIndicator,
  IssueDetailPanel,
  IssueDetailView,
  AgentDetailPanel,
  WorkspaceTree,
  AssigneePrompt,
  BulkActionToolbar,
  TalkToLeadButton,
  NavRail,
  ViewSubSwitcher,
  ThemeToggle,
  KeyboardCheatsheet,
  WorkspaceSwitcher,
  CreateIssueModal,
  CreateWorkspaceModal,
} from "@/components";
import { SearchTermProvider } from "@/contexts/SearchTermContext";
import type { BlockedInfo } from "@/components/KanbanBoard";
import type { ViewMode } from "@/components/ViewSwitcher";
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
  useAgentContext,
  useTheme,
  useWorkspaceContext,
  useWorkspaceState,
  useRepoFilterParam,
  useSearchScope,
  useDaemonHealth,
  usePanelManager,
  KeyboardShortcutProvider,
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
  // Route params: workspaceId always present, issueId present on /ws/:id/issues/:issueId
  const { workspaceId = "", issueId: routeIssueId } = useParams<{
    workspaceId: string;
    issueId: string;
  }>();
  const navigate = useNavigate();

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
    activeWorkspaceName,
    setActiveWorkspace,
    isMultiRepo,
    repos: workspaceRepos,
    selectedRepoNames,
    selectAll,
    selectRepos,
    sourceReposFilter,
  } = useWorkspaceContext();

  // Repo filter URL param sync (deep linking for repo selection)
  const [repoFilterParam, setRepoFilterParam] = useRepoFilterParam();

  // Available repo names for repo selector
  const availableRepoNames = useMemo(
    () => workspaceRepos.map((r) => r.name),
    [workspaceRepos],
  );

  // Convert Set<string> to string[] for components that expect arrays
  const selectedRepoNamesArray = useMemo(
    () => [...selectedRepoNames],
    [selectedRepoNames],
  );

  // Search scope (workspace-scoped search indicator)
  const { scopeName: searchScopeName, clearScope: handleScopeClear } =
    useSearchScope();

  // Scroll position cache for restoring scroll on back navigation
  const scrollPositionCache = useRef<Map<string, number>>(new Map());

  // Search input ref for Cmd/Ctrl+K shortcut
  const searchInputRef = useRef<HTMLInputElement>(null);

  // View state must be read before useIssues to determine fetch mode.
  // Issue-detail is now detected via route params (routeIssueId).
  const { view: activeViewFromParams, setView: setActiveView } = useViewState();

  // If on the issue-detail route, override the view mode
  const activeView: ViewMode = routeIssueId
    ? "issue-detail"
    : activeViewFromParams;
  const selectedIssueId: string | null = routeIssueId ?? null;

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
    sourceRepos: sourceReposFilter,
    workspaceId,
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
  if (sourceReposFilter !== undefined) filterOptions.repos = sourceReposFilter;

  const { filteredIssues } = useIssueFilter(issues, filterOptions);

  // Only fetch blocked issues separately when NOT in kanban mode (kanban mode includes it inline)
  const { data: blockedIssuesData } = useBlockedIssues({
    workspaceId,
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

  // Compute Work Queue counts from workspace-scoped issues for sidebar display
  const workQueueCounts = useMemo(() => {
    let backlog = 0;
    let open = 0;
    let blocked = 0;
    let inProgress = 0;
    let needsReview = 0;
    let done = 0;
    for (const issue of issues) {
      switch (issue.status) {
        case "deferred":
          backlog++;
          break;
        case "open":
        case undefined:
          if (issue.is_blocked) {
            blocked++;
          } else {
            open++;
          }
          break;
        case "blocked":
          blocked++;
          break;
        case "in_progress":
          inProgress++;
          break;
        case "review":
          needsReview++;
          break;
        case "closed":
          done++;
          break;
        default:
          break;
      }
    }
    return { backlog, open, blocked, inProgress, needsReview, done };
  }, [issues]);

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

  // Ref to main scrollable container for workspace state snapshot.
  // NOTE: Must be declared BEFORE useWorkspaceState below — React runs effects
  // top-to-bottom, and the hook's mount effect needs this ref populated first.
  const mainContentRef = useRef<HTMLElement | null>(null);
  useEffect(() => {
    mainContentRef.current = document.getElementById("main-content");
  }, []);

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
    updateIssueDetails,
  } = useIssueDetail(workspaceId);

  // Previous view state for back/escape navigation.
  // Tracks the last "content" view (excludes issue-detail, terminal, settings)
  // so that escape from terminal and back from issue-detail return to the right place.
  const previousViewRef = useRef<ViewMode>("kanban");
  if (
    !routeIssueId &&
    activeViewFromParams !== "issue-detail" &&
    activeViewFromParams !== "terminal" &&
    activeViewFromParams !== "settings"
  ) {
    previousViewRef.current = activeViewFromParams;
  }
  const previousView = previousViewRef.current;

  // Pending issue context for terminal seeding
  const [pendingIssueContext, setPendingIssueContext] = useState<
    IssueContext | undefined
  >(undefined);

  // Pending agent name for opening agent terminal from workspace tree
  const [pendingAgentName, setPendingAgentName] = useState<string | undefined>(
    undefined,
  );

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

  // Workspace quick-switcher state (Cmd/Ctrl+K in multi-repo mode)
  const [isWorkspaceSwitcherOpen, setIsWorkspaceSwitcherOpen] = useState(false);

  // Create workspace modal state
  const [showCreateWorkspace, setShowCreateWorkspace] = useState(false);

  // Create issue modal state
  const [showCreateIssue, setShowCreateIssue] = useState(false);

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
  // Note: In multi-repo mode, the sidebar always shows the workspace tree,
  // so this only guards against stale URL bookmarks in single-repo setups.
  useEffect(() => {
    if (!isMultiRepo && activeView === "workspace") {
      setActiveView("kanban");
    }
  }, [isMultiRepo, activeView, setActiveView]);

  // Sync workspace URL param → repo selection (mount deep-link + popstate back/forward)
  useEffect(() => {
    if (repoFilterParam === null) {
      // null means "all repos" — only call selectAll if currently filtered
      if (
        selectedRepoNames.size !== workspaceRepos.length &&
        workspaceRepos.length > 0
      ) {
        selectAll();
      }
      return;
    }
    if (
      !(selectedRepoNames.size === 1 && selectedRepoNames.has(repoFilterParam))
    ) {
      selectRepos([repoFilterParam]);
    }
  }, [repoFilterParam]); // eslint-disable-line react-hooks/exhaustive-deps

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
        await updateIssue(workspaceId, issueId, {
          status: newStatus,
          assignee,
        });
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
        navigate(`/ws/${workspaceId}/issues/${issue.id}`);
        fetchIssue(issue.id);
        return;
      }

      // From list/graph/monitor views — open panel overlay
      // (mutual exclusivity + no-op guard handled by usePanelManager)
      openPanel({ type: "issue", id: issue.id });
      fetchIssue(issue.id);
    },
    [activeView, selectedIssueId, workspaceId, navigate, fetchIssue, openPanel],
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

  // Handle back from issue detail view — navigate back to workspace root.
  // React Router handles browser history traversal.
  const handleBackFromDetail = useCallback(() => {
    navigate(`/ws/${workspaceId}/`);
  }, [navigate, workspaceId]);

  // Handle approve button click on review cards
  const handleApprove = useCallback(
    async (issue: Issue) => {
      try {
        const reviewType = getReviewType(issue);

        if (reviewType === "code") {
          // Code review: Close the issue (PR was reviewed and approved)
          await closeIssue(
            workspaceId,
            issue.id,
            "PR approved after code review",
          );
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
        await addComment(workspaceId, issue.id, `${prefix}: ${comment}`);

        // Add needs-revision label and set status to open
        await updateIssue(workspaceId, issue.id, {
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

  // Workspace state preservation: save/restore ephemeral per-workspace UI state on switch
  useWorkspaceState({
    workspaceId,
    scrollContainerRef: mainContentRef,
    activePanel,
    restorePanel: openPanel,
    closeAllPanels,
  });

  // Handle workspace/repo selection from WorkspaceTree
  const handleWorkspaceSelect = useCallback(
    (repoName: string | null) => {
      // Skip if same workspace
      if (repoName === activeRepoName) return;
      // Update repo filter
      if (repoName === null) {
        selectAll();
      } else {
        selectRepos([repoName]);
      }
      // Sync workspace URL param
      setRepoFilterParam(repoName);
    },
    [activeRepoName, selectAll, selectRepos, setRepoFilterParam],
  );

  // Handle workspace entry click to switch to a different workspace.
  // SPA navigation via React Router — no page reload.
  const handleWorkspaceSwitch = useCallback(
    (workspaceName: string) => {
      if (workspaceName === activeWorkspaceName) return;
      closeAllPanels();
      setActiveWorkspace(workspaceName);
    },
    [activeWorkspaceName, closeAllPanels, setActiveWorkspace],
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

  // Handle tree issue select (wraps handleIssueClick with minimal Issue shape)
  const handleTreeIssueSelect = useCallback(
    (issueId: string) => {
      openPanel({ type: "issue", id: issueId });
      fetchIssue(issueId);
    },
    [openPanel, fetchIssue],
  );

  // Handle Talk to Lead from workspace tree
  const handleTreeTalkToLead = useCallback(
    (_workspaceName: string) => {
      setActiveView("terminal");
    },
    [setActiveView],
  );

  // Handle task terminal open from workspace tree (task with active agent)
  const handleTreeTaskTerminalOpen = useCallback(
    async (_issueId: string, agentName: string) => {
      try {
        const mode = await getAgentTerminalInfo(workspaceId, agentName);
        if (mode === "tmux") {
          setPendingAgentName(agentName);
          setActiveView("terminal");
        } else {
          // Archive mode — open agent detail panel instead
          openPanel({ type: "agent", name: agentName });
        }
      } catch {
        // Network error — fall back to agent detail panel
        openPanel({ type: "agent", name: agentName });
      }
    },
    [workspaceId, setActiveView, openPanel],
  );

  const handleAgentNameConsumed = useCallback(() => {
    setPendingAgentName(undefined);
  }, []);

  // Focus search input (for Cmd/Ctrl+K shortcut in single-repo mode)
  const handleSearchFocus = useCallback(() => {
    searchInputRef.current?.focus();
  }, []);

  // Open workspace quick-switcher (Cmd/Ctrl+K in multi-repo mode)
  const handleWorkspaceSwitcherToggle = useCallback(() => {
    setIsWorkspaceSwitcherOpen((prev) => !prev);
  }, []);

  // Handle workspace switcher selection (receives workspace ID, navigates to it)
  const handleWorkspaceSwitcherSelect = useCallback(
    (wsId: string) => {
      if (wsId === workspaceId) return;
      closeAllPanels();
      navigate(`/ws/${wsId}/`);
    },
    [workspaceId, closeAllPanels, navigate],
  );

  // Direct workspace switching via Cmd/Ctrl+Shift+1-9
  const handleWorkspacePositionalSwitch = useCallback(
    (index: number) => {
      const workspaces = workspace?.workspaces ?? [];
      const ws = workspaces[index];
      if (ws && ws.id !== workspaceId) {
        closeAllPanels();
        navigate(`/ws/${ws.id}/`);
      }
    },
    [workspace, workspaceId, closeAllPanels, navigate],
  );

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
        {searchScopeName && (
          <SearchScopeIndicator
            scopeName={searchScopeName}
            onClear={handleScopeClear}
          />
        )}
        <SearchInput
          ref={searchInputRef as RefObject<HTMLInputElement>}
          value={searchValue}
          onChange={setSearchValue}
          onClear={handleSearchClear}
          placeholder={
            searchScopeName
              ? `Search in ${searchScopeName}...`
              : "Search tasks..."
          }
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
          selectedRepos={selectedRepoNamesArray}
          onRepoChange={selectRepos}
          variant="header"
          showClear={true}
        />
        <MoreFiltersMenu
          groupBy={filters.groupBy ?? DEFAULT_GROUP_BY}
          onGroupByChange={filterActions.setGroupBy}
        />
        <button
          className={styles.newIssueButton}
          onClick={() => setShowCreateIssue(true)}
          data-testid="new-issue-button"
        >
          + New Issue
        </button>
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

  // Sidebar: always show WorkspaceTree with agents nested inside.
  // The tree includes agent list per workspace plus "+ New Workspace" button.
  const sidebarContent = (
    <WorkspaceTree
      activeRepoName={activeRepoName}
      onWorkspaceSelect={handleWorkspaceSelect}
      onWorkspaceSwitch={handleWorkspaceSwitch}
      onAgentClick={handleAgentClick}
      agentTasks={agentTasks}
      onAddClick={() => setShowCreateWorkspace(true)}
      connectionState={connectionState}
      connectionLost={isConnectionLost}
      disconnectedSince={staleBannerDisconnectedSince}
      onRetryConnection={staleBannerRetry}
      workQueueCounts={workQueueCounts}
      onTalkToLead={handleTreeTalkToLead}
      onTreeSelect={handleTreeIssueSelect}
      onTaskTerminalOpen={handleTreeTaskTerminalOpen}
    />
  );

  // Loading state: show skeleton columns (only for issue-based views;
  // terminal and other independent views render without waiting for issues)
  if (isLoading && activeView !== "terminal" && activeView !== "settings") {
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
          <ViewSubSwitcher activeView={activeView} onChange={setActiveView} />
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

  // Error state: show error display with retry (only for issue-based views;
  // terminal, settings, and other independent views should still render)
  const issueBasedViews = new Set(["kanban", "table", "graph", "issue-detail"]);
  if (error && !isLoading && issueBasedViews.has(activeView)) {
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
          <ViewSubSwitcher activeView={activeView} onChange={setActiveView} />
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

  // Empty state: no issues across issue-based views
  if (
    issues.length === 0 &&
    (activeView === "kanban" ||
      activeView === "table" ||
      activeView === "graph")
  ) {
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
          <ViewSubSwitcher activeView={activeView} onChange={setActiveView} />
          <EmptyWorkspaceBoard isMultiRepo={isMultiRepo} />
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
    <KeyboardShortcutProvider
      onViewChange={setActiveView}
      onSearchFocus={handleSearchFocus}
      {...(isMultiRepo && {
        onWorkspaceSwitcher: handleWorkspaceSwitcherToggle,
        onWorkspacePositionalSwitch: handleWorkspacePositionalSwitch,
      })}
    >
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
          <ViewSubSwitcher activeView={activeView} onChange={setActiveView} />
          {activeView === "kanban" && (
            <ErrorBoundary resetOnChange={[activeView]}>
              <div className={styles.kanbanShell}>
                <SwimLaneBoard
                  issues={filteredIssues}
                  groupBy={filters.groupBy ?? DEFAULT_GROUP_BY}
                  onDragEnd={handleDragEnd}
                  onIssueClick={handleIssueClick}
                  isMultiRepo={isMultiRepo}
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
                {...(selectedIssueId !== null && {
                  selectedId: selectedIssueId,
                })}
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
                <SettingsView onNavigate={setActiveView} />
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
                onIssueUpdate={updateIssueDetails}
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
            onIssueUpdate={updateIssueDetails}
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
                pendingAgentName={pendingAgentName}
                onAgentNameConsumed={handleAgentNameConsumed}
                onActiveSessionCountChange={setActiveSessionCount}
                onUnreadChange={setHasTerminalUnread}
                onEscape={() => setActiveView(previousView || "kanban")}
                onNavigateToSettings={() => setActiveView("settings")}
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
      <KeyboardCheatsheet />
      {isMultiRepo && (
        <WorkspaceSwitcher
          isOpen={isWorkspaceSwitcherOpen}
          workspaces={workspace?.workspaces ?? []}
          activeWorkspaceId={workspaceId}
          onSelect={handleWorkspaceSwitcherSelect}
          onClose={() => setIsWorkspaceSwitcherOpen(false)}
        />
      )}
      <CreateWorkspaceModal
        isOpen={showCreateWorkspace}
        onClose={() => setShowCreateWorkspace(false)}
        onSuccess={(data, createdName) => {
          setShowCreateWorkspace(false);
          // Navigate to the new workspace via SPA navigation.
          // Find the new workspace ID from the refreshed workspace list.
          const newWs = data.workspaces?.find((ws) => ws.name === createdName);
          if (newWs) {
            navigate(`/ws/${newWs.id}/`);
          }
        }}
      />
      <CreateIssueModal
        isOpen={showCreateIssue}
        onClose={() => setShowCreateIssue(false)}
        onSuccess={() => {
          refetch();
        }}
      />
    </KeyboardShortcutProvider>
  );
}
export default App;
