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

import { useStore } from "zustand";

import { useParams, useNavigate, Outlet } from "react-router-dom";

import { updateIssue, addComment, closeIssue } from "@/api";
import type { IssueContext } from "@/api/terminal";
import { buildShareUrl } from "@/utils/buildShareUrl";
import { getReviewType } from "@/utils/issue";
import { buildWorkspaceSwitchUrl } from "@/utils/workspaceUrl";
import { AppLayout } from "@/components/AppLayout/AppLayout";
import { WorkspaceBreadcrumb } from "@/components/WorkspaceBreadcrumb/WorkspaceBreadcrumb";
import { LoadingSkeleton } from "@/components/LoadingSkeleton/LoadingSkeleton";
import { ConnectionStatus } from "@/components/ConnectionStatus/ConnectionStatus";
import { StaleDataBanner } from "@/components/StaleDataBanner/StaleDataBanner";
import { WorkspaceStatusBadge } from "@/components/WorkspaceStatusBadge/WorkspaceStatusBadge";
import { ToastContainer } from "@/components/Toast/ToastContainer";
import { FilterBar } from "@/components/FilterBar/FilterBar";
import { MoreFiltersMenu } from "@/components/MoreFiltersMenu/MoreFiltersMenu";
import { SearchInput } from "@/components/search/SearchInput";
import { SearchScopeIndicator } from "@/components/search/SearchScopeIndicator";
import { IssueDetailPanel } from "@/components/IssueDetailPanel/IssueDetailPanel";
import { AgentDetailPanel } from "@/components/AgentDetailPanel/AgentDetailPanel";
import { WorkspaceTree } from "@/components/WorkspaceTree/WorkspaceTree";
import { TalkToLeadButton } from "@/components/TalkToLeadButton/TalkToLeadButton";
import { NavRail } from "@/components/NavRail/NavRail";
import { ViewSubSwitcher } from "@/components/ViewSubSwitcher/ViewSubSwitcher";
import { ThemeToggle } from "@/components/ThemeToggle/ThemeToggle";
import { KeyboardCheatsheet } from "@/components/KeyboardCheatsheet/KeyboardCheatsheet";
import { WorkspaceSwitcher } from "@/components/WorkspaceSwitcher/WorkspaceSwitcher";
import { CreateIssueModal } from "@/components/CreateIssueModal/CreateIssueModal";
import { CreateWorkspaceModal } from "@/components/CreateWorkspaceModal/CreateWorkspaceModal";
import { CreateAgentModal } from "@/components/CreateAgentModal/CreateAgentModal";
import { UserMenu } from "@/components/UserMenu/UserMenu";
import { SearchTermProvider } from "@/contexts/SearchTermContext";
import {
  WorkspaceViewProvider,
  type WorkspaceViewData,
  type WorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";
import type { BlockedInfo } from "@/types/issue";
import type { ViewMode } from "@/types";
import {
  useIssueStoreInstance,
  useAgentStoreInstance,
} from "@/hooks/common/useStoreContext";
import { useRouteView } from "@/hooks/common/useRouteView";
import { useDebounce } from "@/hooks/common/useDebounce";
import {
  useFilterState,
  DEFAULT_GROUP_BY,
} from "@/hooks/issues/useFilterState";
import { useIssueFilter } from "@/hooks/issues/useIssueFilter";
import { useBlockedIssues } from "@/hooks/issues/useBlockedIssues";
import { useIssueDetail } from "@/hooks/issues/useIssueDetail";
import { useSearchScope } from "@/hooks/issues/useSearchScope";
import { useToast } from "@/hooks/ui/useToast";
import { useTheme } from "@/hooks/ui/useTheme";
import { usePanelManager } from "@/hooks/ui/usePanelManager";
import { KeyboardShortcutProvider } from "@/hooks/ui/useKeyboardShortcuts";
import { useWorkspaceContext } from "@/hooks/workspace/useWorkspaceContext";
import { useWorkspaceState } from "@/hooks/workspace/useWorkspaceState";
import { useRepoFilterParam } from "@/hooks/workspace/useRepoFilterParam";
import { useWorkspaceHealth } from "@/hooks/workspace/useWorkspaceHealth";
import type { Issue, Status } from "@/types";

import styles from "./App.module.css";

// Lazy load TerminalView; it stays in App.tsx because terminal
// is always-mounted in the shell to preserve WebSocket connections across views.
const TerminalView = lazy(() =>
  import("@/components/TerminalView/TerminalView").then((m) => ({
    default: m.TerminalView,
  })),
);

function App() {
  // Route params: issueId present on /ws/:id/issues/:issueId
  const { issueId: routeIssueId } = useParams<{
    workspaceId: string;
    issueId: string;
  }>();
  const navigate = useNavigate();

  // Workspace service health monitoring
  const {
    isWorkspaceAvailable,
    connectionMode,
    retryCountdown,
    lastError,
    retryNow: workspaceRetryNow,
  } = useWorkspaceHealth();

  // Theme state
  const { theme, toggleTheme } = useTheme();

  // Workspace context for breadcrumb, single-repo guard, and workspace selection
  const {
    workspaceId,
    workspace,
    activeWorkspaceName,
    setActiveWorkspace,
    isMultiRepo,
    repos: workspaceRepos,
    selectedRepoNames,
    selectAll,
    selectRepos,
    sourceReposFilter,
    refetch: refetchWorkspace,
  } = useWorkspaceContext();

  // Repo filter URL param sync (deep linking for repo selection)
  const [repoFilterParam] = useRepoFilterParam();

  // Available repo names for repo selector
  const availableRepoNames = useMemo(
    () => workspaceRepos.map((r) => r.name),
    [workspaceRepos],
  );

  const agentDefaultBackend = useMemo(() => {
    const activeWorkspace = workspace?.workspaces?.find(
      (ws) => ws.id === workspaceId || ws.name === activeWorkspaceName,
    );
    return activeWorkspace?.backend?.trim() || "codex";
  }, [workspace?.workspaces, workspaceId, activeWorkspaceName]);
  const hasMultipleWorkspaces = (workspace?.workspaces?.length ?? 0) > 1;

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

  // View state derived from route path (e.g. /ws/:id/kanban → "kanban").
  // Issue-detail is detected via the issues/:issueId route segment.
  const {
    view: activeView,
    setView: setActiveView,
    navigateToView,
  } = useRouteView();

  // Alias for NavRail/keyboard — navigateToView uses push semantics (creates history entry)
  const handleNavChange = navigateToView;

  const selectedIssueId: string | null = routeIssueId ?? null;

  // Issue state from Zustand store (replaces useIssues hook)
  const issueStore = useIssueStoreInstance();

  const issuesMap = useStore(issueStore, (s) => s.issuesMap);
  const issues = useMemo(() => [...issuesMap.values()], [issuesMap]);
  const isLoading = useStore(issueStore, (s) => s.isLoading);
  const error = useStore(issueStore, (s) => s.error);
  const retryCount = useStore(issueStore, (s) => s.retryCount);
  const nextRetryAt = useStore(issueStore, (s) => s.nextRetryAt);
  const pendingIds = useStore(issueStore, (s) => s.pendingIds);

  const connectionState = useStore(issueStore, (s) => s.connectionState);
  const reconnectAttempts = useStore(issueStore, (s) => s.reconnectAttempts);
  const sseShowStaleBanner = useStore(issueStore, (s) => s.showStaleBanner);
  const sseConnectionLost = useStore(issueStore, (s) => s.connectionLost);
  const sseDisconnectedSince = useStore(issueStore, (s) => s.disconnectedSince);

  const refetch = useStore(issueStore, (s) => s.refetch);
  const storeUpdateIssueStatus = useStore(
    issueStore,
    (s) => s.updateIssueStatus,
  );
  const retryConnection = useStore(issueStore, (s) => s.retryConnection);
  const fetchIssues = useStore(issueStore, (s) => s.fetchIssues);

  // Wrap store's 3-arg updateIssueStatus to bind workspaceId (views expect 2-arg signature)
  const updateIssueStatus = useCallback(
    (issueId: string, newStatus: Status) =>
      storeUpdateIssueStatus(issueId, newStatus, workspaceId),
    [storeUpdateIssueStatus, workspaceId],
  );

  // Drive issue fetching based on active view mode, workspace, and source repos
  const issueModeByView: Record<string, "graph" | "kanban"> = {
    graph: "graph",
    kanban: "kanban",
    table: "kanban",
    "issue-detail": "kanban",
  };
  const issueMode = issueModeByView[activeView] ?? ("ready" as const);

  useEffect(() => {
    const controller = new AbortController();
    const params: Parameters<typeof fetchIssues>[0] = {
      workspaceId,
      mode: issueMode,
      signal: controller.signal,
    };
    if (sourceReposFilter) params.sourceRepos = sourceReposFilter;
    fetchIssues(params);
    return () => controller.abort();
  }, [fetchIssues, workspaceId, issueMode, sourceReposFilter]);

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

  const { filteredIssues, hasActiveFilters } = useIssueFilter(
    issues,
    filterOptions,
  );

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

  // Derive panel visibility booleans from the centralized panel state.
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

  const {
    issueDetails,
    isLoading: isLoadingDetails,
    error: detailError,
    fetchIssue,
    clearIssue,
    updateIssueDetails,
  } = useIssueDetail();

  // Previous view for issue-detail back navigation.
  // Tracks the last "content" view (excludes issue-detail, terminal, settings).
  const previousViewRef = useRef<ViewMode>("kanban");
  if (
    activeView !== "issue-detail" &&
    activeView !== "terminal" &&
    activeView !== "settings"
  ) {
    previousViewRef.current = activeView;
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

  // Agent data (shared via agentStore — single polling loop)
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const agentTasks = useStore(agentStore, (s) => s.agentTasks);
  const agentShowStaleBanner = useStore(agentStore, (s) => s.showStaleBanner);
  const agentConnectionLost = useStore(agentStore, (s) => s.connectionLost);
  const agentDisconnectedSince = useStore(
    agentStore,
    (s) => s.disconnectedSince,
  );
  const agentRetryNow = useStore(agentStore, (s) => s.retryNow);

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
  const [showCreateIssue, setShowCreateIssue] = useState(false);
  const [showCreateWorkspace, setShowCreateWorkspace] = useState(false);
  const [showCreateAgent, setShowCreateAgent] = useState(false);

  // Track mount state for async operations.
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

  // Deep-link: auto-fetch issue from URL; route changes are handled by useRouteView.
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

  // Refs for handleCopyLink stability — avoids recreating the actions context
  // on every activeView/selectedIssueId change (only IssueDetailPage uses it).
  const activeViewRef = useRef(activeView);
  activeViewRef.current = activeView;
  const selectedIssueIdRef = useRef(selectedIssueId);
  selectedIssueIdRef.current = selectedIssueId;

  // Copy link handler: copies a clean shareable URL to clipboard
  const handleCopyLink = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(
        buildShareUrl({
          view: activeViewRef.current,
          issue: selectedIssueIdRef.current,
        }),
      );
      showToast("Link copied to clipboard", { type: "success" });
    } catch {
      showToast("Failed to copy link", { type: "error" });
    }
  }, [showToast]);

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

  // Handle agent click from MonitorDashboard
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

  // Close all panels synchronously (no animation) for workspace switch
  const closeAllPanels = useCallback(() => {
    closePanel();
    clearIssue();
  }, [closePanel, clearIssue]);

  // Workspace state preservation: save/restore ephemeral per-workspace UI state on switch
  useWorkspaceState({
    scrollContainerRef: mainContentRef,
    activePanel,
    restorePanel: openPanel,
    closeAllPanels,
  });

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
    navigateToView("terminal");
  }, [navigateToView]);

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

  // Handle workspace switcher selection (receives workspace ID, navigates to it).
  // Preserves `view=` query param + uses flushSync to escape React Router v7's
  // startTransition starvation when switching from terminal view. See
  // useWorkspaceContext.setActiveWorkspace for the full rationale.
  const handleWorkspaceSwitcherSelect = useCallback(
    (wsId: string) => {
      if (wsId === workspaceId) return;
      closeAllPanels();
      navigate(buildWorkspaceSwitchUrl(wsId), { flushSync: true });
    },
    [workspaceId, closeAllPanels, navigate],
  );

  // Direct workspace switching via Cmd/Ctrl+Shift+1-9. Same semantics as the
  // explicit switcher above — preserve view, flushSync.
  const handleWorkspacePositionalSwitch = useCallback(
    (index: number) => {
      const workspaces = workspace?.workspaces ?? [];
      const ws = workspaces[index];
      if (ws && ws.id !== workspaceId) {
        closeAllPanels();
        navigate(buildWorkspaceSwitchUrl(ws.id), { flushSync: true });
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

  // -----------------------------------------------------------------------
  // WorkspaceViewContext: data + actions for view components
  // -----------------------------------------------------------------------

  const workspaceViewData: WorkspaceViewData = useMemo(
    () => ({
      issues,
      filteredIssues,
      hasActiveFilters,
      isLoading,
      error,
      retryCount,
      nextRetryAt,
      connectionState,
      reconnectAttempts,
      pendingIds,
      blockedIssuesMap,
      filters,
      groupBy: filters.groupBy ?? DEFAULT_GROUP_BY,
      debouncedSearch,
      activeView,
      selectedIssueId,
      workspaceId,
      isMultiRepo,
      agents,
      agentTasks,
      issueDetails,
      isLoadingDetails,
      detailError,
      previousView,
    }),
    [
      issues,
      filteredIssues,
      hasActiveFilters,
      isLoading,
      error,
      retryCount,
      nextRetryAt,
      connectionState,
      reconnectAttempts,
      pendingIds,
      blockedIssuesMap,
      filters,
      debouncedSearch,
      activeView,
      selectedIssueId,
      workspaceId,
      isMultiRepo,
      agents,
      agentTasks,
      issueDetails,
      isLoadingDetails,
      detailError,
      previousView,
    ],
  );

  const workspaceViewActions: WorkspaceViewActions = useMemo(
    () => ({
      refetch,
      updateIssueStatus,
      fetchIssue,
      clearIssue,
      updateIssueDetails,
      openPanel,
      closePanel,
      handleIssueClick,
      handlePanelClose,
      handleAgentClick,
      handleAgentPanelClose,
      handleAgentTaskClick,
      handleApprove,
      handleReject,
      handleCopyLink,
      navigateToView,
      showToast,
      setPendingIssueContext,
    }),
    [
      refetch,
      updateIssueStatus,
      fetchIssue,
      clearIssue,
      updateIssueDetails,
      openPanel,
      closePanel,
      handleIssueClick,
      handlePanelClose,
      handleAgentClick,
      handleAgentPanelClose,
      handleAgentTaskClick,
      handleApprove,
      handleReject,
      handleCopyLink,
      navigateToView,
      showToast,
      setPendingIssueContext,
    ],
  );

  // Whether the current view depends on issue data (for stale banner suppression)
  const isIssueBasedView =
    activeView === "kanban" ||
    activeView === "table" ||
    activeView === "graph" ||
    activeView === "issue-detail";

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
      </div>
      <button
        className={styles.newIssueButton}
        onClick={() => setShowCreateIssue(true)}
        data-testid="new-issue-button"
      >
        + New Issue
      </button>
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
      <WorkspaceStatusBadge
        isWorkspaceAvailable={isWorkspaceAvailable}
        mode={connectionMode}
        retryCountdown={retryCountdown}
        lastError={lastError}
        onRetry={workspaceRetryNow}
      />
      <UserMenu />
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
      onWorkspaceSwitch={handleWorkspaceSwitch}
      onAgentClick={handleAgentClick}
      agentTasks={agentTasks}
      onAddClick={() => setShowCreateAgent(true)}
      onAddWorkspaceClick={() => setShowCreateWorkspace(true)}
      connectionState={connectionState}
      connectionLost={isConnectionLost}
      disconnectedSince={staleBannerDisconnectedSince}
      onRetryConnection={staleBannerRetry}
      workQueueCounts={workQueueCounts}
      onTreeSelect={handleTreeIssueSelect}
    />
  );

  return (
    <KeyboardShortcutProvider
      onViewChange={handleNavChange}
      onSearchFocus={handleSearchFocus}
      {...(hasMultipleWorkspaces && {
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
              onChange={handleNavChange}
              sessionCount={activeSessionCount}
              badges={{ terminal: hasTerminalUnread }}
            />
          }
          sidebar={sidebarContent}
        >
          <ViewSubSwitcher activeView={activeView} onChange={navigateToView} />
          {(showStaleBanner || isConnectionLost) &&
            staleBannerDisconnectedSince !== null &&
            !(
              issues.length === 0 &&
              !isLoading &&
              !error &&
              isIssueBasedView
            ) && (
              <StaleDataBanner
                disconnectedSince={staleBannerDisconnectedSince}
                onRetry={staleBannerRetry}
                connectionLost={isConnectionLost}
              />
            )}
          <WorkspaceViewProvider
            data={workspaceViewData}
            actions={workspaceViewActions}
          >
            <Suspense fallback={<LoadingSkeleton.Column />}>
              <Outlet />
            </Suspense>
          </WorkspaceViewProvider>
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
          <CreateIssueModal
            isOpen={showCreateIssue}
            onClose={() => setShowCreateIssue(false)}
            onSuccess={async (issue) => {
              await refetch();
              openPanel({ type: "issue", id: issue.id });
              fetchIssue(issue.id);
            }}
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
                onTabLimitReached={(message) =>
                  showToast(message, { type: "error" })
                }
                onNavigateToSettings={() => navigateToView("settings")}
              />
            </Suspense>
          </div>
          <TalkToLeadButton
            onClick={handleTalkToLeadClick}
            isActive={activeView === "terminal"}
            sessionCount={activeSessionCount}
          />
        </AppLayout>
      </SearchTermProvider>
      <KeyboardCheatsheet />
      {hasMultipleWorkspaces && (
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
        onSuccess={(data, createdName, warnings) => {
          setShowCreateWorkspace(false);
          const newWs = data.workspaces?.find((ws) => ws.name === createdName);
          if (newWs) {
            navigate(buildWorkspaceSwitchUrl(newWs.id), { flushSync: true });
          }
          if (warnings && warnings.length > 0) {
            showToast(
              `Workspace "${createdName}" created, but: ${warnings.join("; ")}`,
              { type: "warning" },
            );
          }
        }}
      />
      <CreateAgentModal
        isOpen={showCreateAgent}
        workspaceId={workspaceId}
        repos={workspaceRepos}
        defaultBackend={agentDefaultBackend}
        onClose={() => setShowCreateAgent(false)}
        onSuccess={(agent) => {
          setShowCreateAgent(false);
          refetchWorkspace();
          showToast(`Agent "${agent.name}" created`, { type: "success" });
        }}
      />
    </KeyboardShortcutProvider>
  );
}
export default App;
