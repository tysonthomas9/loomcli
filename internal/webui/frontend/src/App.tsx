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
  type CSSProperties,
  type RefObject,
} from "react";

import { useStore } from "zustand";

import { useParams, useNavigate, Outlet } from "react-router-dom";

import { updateIssue, addComment, closeIssue, startAgent } from "@/api";
import { fetchWorkspaceApi, type WorkspaceAgentInfo } from "@/api/workspace";
import type { IssueContext } from "@/api/terminal";
import { buildShareUrl } from "@/utils/buildShareUrl";
import { getReviewType } from "@/utils/issue";
import {
  isOnboardingRepo,
  ONBOARDING_AGENT_NAME,
  ONBOARDING_AGENT_ROLE,
  ONBOARDING_ISSUE_DESCRIPTION,
  ONBOARDING_ISSUE_TITLE,
  ONBOARDING_REPO_URL,
  ONBOARDING_WORKSPACE_NAME,
} from "@/utils/onboardingDefaults";
import {
  dismissOnboarding,
  isOnboardingDismissed,
  ONBOARDING_RESTART_EVENT,
  type OnboardingRestartDetail,
} from "@/utils/onboardingState";
import { buildWorkspaceSwitchUrl } from "@/utils/workspaceUrl";
import { requestCliSetup } from "@/utils/cliSetup";
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
import {
  OnboardingFlow,
  type OnboardingStep,
} from "@/components/OnboardingFlow";
import {
  AIBackendSetupList,
  type AIBackendSetupAction,
} from "@/components/AIBackendSetupList";
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
import { useBackends } from "@/hooks/workspace/useBackends";
import { useBackendConfig } from "@/hooks/workspace/useBackendConfig";
import type { BackendInfo } from "@/utils/workspace";
import type { Issue, Status } from "@/types";

import styles from "./App.module.css";

// Lazy load TerminalView; it stays in App.tsx because terminal
// is always-mounted in the shell to preserve WebSocket connections across views.
const TerminalView = lazy(() =>
  import("@/components/TerminalView/TerminalView").then((m) => ({
    default: m.TerminalView,
  })),
);

type CreateIssueMode = "manual" | "onboarding";

function isOnboardingPlannerAgent(agent: WorkspaceAgentInfo): boolean {
  const roleName = agent.role_name?.trim();
  if (roleName) {
    return roleName === ONBOARDING_AGENT_ROLE;
  }
  return agent.name === ONBOARDING_AGENT_NAME;
}

function getOnboardingPlannerName(
  agents: readonly WorkspaceAgentInfo[] | undefined,
): string | undefined {
  const agentList = agents ?? [];
  return (
    agentList.find(
      (agent) =>
        agent.name === ONBOARDING_AGENT_NAME &&
        (!agent.role_name || agent.role_name === ONBOARDING_AGENT_ROLE),
    )?.name ?? agentList.find(isOnboardingPlannerAgent)?.name
  );
}

async function assignAndStartAgent(
  workspaceId: string,
  issueId: string,
  agentName: string,
): Promise<void> {
  let assigned = false;
  try {
    await updateIssue(workspaceId, issueId, { assignee: agentName });
    assigned = true;
    await startAgent(workspaceId, agentName, { taskId: issueId });
  } catch (err) {
    if (assigned) {
      try {
        await updateIssue(workspaceId, issueId, { assignee: "" });
      } catch {
        // Preserve the original start error; the user can still unassign manually.
      }
    }
    throw err;
  }
}

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
  const {
    backends: aiBackends,
    isLoading: aiBackendsLoading,
    error: aiBackendsError,
    refetch: refetchAiBackends,
  } = useBackends();
  const {
    config: onboardingBackendConfig,
    isLoading: onboardingBackendConfigLoading,
    isSaving: isSavingOnboardingBackend,
    updateBackend: updateOnboardingBackend,
  } = useBackendConfig(workspaceId, { enabled: Boolean(workspaceId) });

  // Repo filter URL param sync (deep linking for repo selection)
  const [repoFilterParam] = useRepoFilterParam();

  // Available repo names for repo selector
  const availableRepoNames = useMemo(
    () => workspaceRepos.map((r) => r.name),
    [workspaceRepos],
  );

  const agentDefaultBackend = useMemo(() => {
    const configuredBackend = onboardingBackendConfig?.backend?.trim();
    if (configuredBackend) return configuredBackend;

    const activeWorkspace = workspace?.workspaces?.find(
      (ws) => ws.id === workspaceId || ws.name === activeWorkspaceName,
    );
    const workspaceBackend = activeWorkspace?.backend?.trim();
    if (workspaceBackend) return workspaceBackend;

    const firstReadyBackend = aiBackends.find(
      (backend) => backend.available,
    )?.name;
    return firstReadyBackend?.trim() || "codex";
  }, [
    onboardingBackendConfig?.backend,
    workspace?.workspaces,
    workspaceId,
    activeWorkspaceName,
    aiBackends,
  ]);
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
  const hasOnboardingRepo = useMemo(
    () => workspaceRepos.some((repo) => isOnboardingRepo(repo)),
    [workspaceRepos],
  );
  const shouldPrefillOnboardingIssue = hasOnboardingRepo && issues.length === 0;
  const shouldPrefillOnboardingAgent =
    hasOnboardingRepo && !getOnboardingPlannerName(workspace?.agents);
  const onboardingWorkspaceInitialValues = useMemo(
    () => ({
      name: ONBOARDING_WORKSPACE_NAME,
      type: "clone" as const,
      urlInput: ONBOARDING_REPO_URL,
    }),
    [],
  );
  const onboardingIssueInitialValues = useMemo(
    () =>
      shouldPrefillOnboardingIssue
        ? {
            title: ONBOARDING_ISSUE_TITLE,
            description: ONBOARDING_ISSUE_DESCRIPTION,
            issueType: "task" as const,
            priority: 2 as const,
          }
        : undefined,
    [shouldPrefillOnboardingIssue],
  );
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
  const [searchValue, setSearchValueState] = useState(filters.search ?? "");
  const hasPendingSearchEditRef = useRef(false);
  const pendingSearchCommitRef = useRef<{ value: string | undefined } | null>(
    null,
  );
  const setSearchValue = useCallback((value: string) => {
    pendingSearchCommitRef.current = null;
    hasPendingSearchEditRef.current = true;
    setSearchValueState(value);
  }, []);
  const debouncedSearch = useDebounce(searchValue, 300);

  // Sync debounced search to filter state
  useEffect(() => {
    if (!hasPendingSearchEditRef.current) return;
    if (debouncedSearch !== searchValue) return;
    const nextSearch = debouncedSearch || undefined;
    if (filters.search === nextSearch) {
      hasPendingSearchEditRef.current = false;
      pendingSearchCommitRef.current = null;
      return;
    }
    pendingSearchCommitRef.current = { value: nextSearch };
    filterActions.setSearch(nextSearch);
  }, [debouncedSearch, searchValue, filters.search, filterActions]);

  // Sync search value from filter state (e.g., when Clear filters is clicked)
  useEffect(() => {
    const pendingCommit = pendingSearchCommitRef.current;
    if (pendingCommit) {
      if (filters.search === pendingCommit.value) {
        pendingSearchCommitRef.current = null;
        hasPendingSearchEditRef.current = false;
      } else {
        return;
      }
    }

    if (hasPendingSearchEditRef.current) return;

    const filterSearch = filters.search ?? "";
    if (filterSearch !== searchValue) {
      setSearchValueState(filterSearch);
    }
  }, [filters.search, searchValue]);
  const activeSearchTerm = filters.search ?? "";

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
  const [createIssueMode, setCreateIssueMode] =
    useState<CreateIssueMode>("manual");
  const [showCreateWorkspace, setShowCreateWorkspace] = useState(false);
  const [showCreateAgent, setShowCreateAgent] = useState(false);
  const [onboardingDismissed, setOnboardingDismissed] = useState(false);

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

  // Keep the open detail panel in sync with live issue-list mutations.
  // The panel fetches full issue details, while SSE updates land in issuesMap.
  useEffect(() => {
    if (!issueDetails) return;
    const latestIssue = issuesMap.get(issueDetails.id);
    if (!latestIssue) return;

    const latestUpdatedAt = latestIssue.updated_at
      ? Date.parse(latestIssue.updated_at)
      : NaN;
    const detailUpdatedAt = issueDetails.updated_at
      ? Date.parse(issueDetails.updated_at)
      : NaN;
    if (
      !Number.isNaN(latestUpdatedAt) &&
      !Number.isNaN(detailUpdatedAt) &&
      latestUpdatedAt < detailUpdatedAt
    ) {
      return;
    }

    if (
      latestIssue.title !== issueDetails.title ||
      latestIssue.status !== issueDetails.status ||
      latestIssue.priority !== issueDetails.priority ||
      latestIssue.issue_type !== issueDetails.issue_type ||
      latestIssue.assignee !== issueDetails.assignee ||
      latestIssue.owner !== issueDetails.owner ||
      latestIssue.updated_at !== issueDetails.updated_at
    ) {
      updateIssueDetails(latestIssue);
    }
  }, [issueDetails, issuesMap, updateIssueDetails]);

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
    hasPendingSearchEditRef.current = false;
    pendingSearchCommitRef.current = { value: undefined };
    setSearchValueState("");
    filterActions.setSearch(undefined);
  }, [filterActions]);

  const syncedFilterActions = useMemo(
    () => ({
      ...filterActions,
      setSearch: (search: string | undefined) => {
        const nextSearch = search || undefined;
        hasPendingSearchEditRef.current = false;
        pendingSearchCommitRef.current = { value: nextSearch };
        setSearchValueState(nextSearch ?? "");
        filterActions.setSearch(nextSearch);
      },
      clearFilter: (key: keyof typeof filters) => {
        if (key === "search") {
          hasPendingSearchEditRef.current = false;
          pendingSearchCommitRef.current = { value: undefined };
          setSearchValueState("");
        }
        filterActions.clearFilter(key);
      },
      clearAll: () => {
        hasPendingSearchEditRef.current = false;
        pendingSearchCommitRef.current = { value: undefined };
        setSearchValueState("");
        filterActions.clearAll();
      },
    }),
    [filterActions],
  );

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
    [workspaceId, updateIssueStatus, refetch, handlePanelClose, showToast],
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
    [workspaceId, refetch, handlePanelClose, showToast],
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

  useEffect(() => {
    if (activeView === "terminal") {
      closeAllPanels();
    }
  }, [activeView, closeAllPanels]);

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
    closeAllPanels();
    navigateToView("terminal");
  }, [closeAllPanels, navigateToView]);

  const hasWorkspaceRepo = workspaceRepos.length > 0;
  const hasWorkspaceAgent = Boolean(
    getOnboardingPlannerName(workspace?.agents),
  );
  const hasWorkspaceIssue = issues.length > 0;
  const defaultBackend = onboardingBackendConfig?.backend;
  const defaultBackendStatus = aiBackends.find(
    (backend) => backend.name === defaultBackend,
  );
  const isDefaultBackendReady = defaultBackendStatus?.available === true;
  const isWorkspaceOnboardingComplete =
    hasWorkspaceRepo &&
    hasWorkspaceAgent &&
    hasWorkspaceIssue &&
    isDefaultBackendReady;
  const shouldShowWorkspaceOnboarding =
    !onboardingDismissed &&
    !isWorkspaceOnboardingComplete &&
    (workspaceRepos.length === 0 || hasOnboardingRepo) &&
    (!hasWorkspaceRepo || !hasWorkspaceAgent || !hasWorkspaceIssue);
  const handleOnboardingDismiss = useCallback(() => {
    dismissOnboarding(workspaceId);
    setOnboardingDismissed(true);
  }, [workspaceId]);
  const handleBackendSetupAction = useCallback(
    async (backend: BackendInfo, action: AIBackendSetupAction) => {
      if (action === "set-default") {
        const ok = await updateOnboardingBackend(backend.name);
        if (ok) {
          showToast(`${backend.displayName} set as default`, {
            type: "success",
          });
          refetchAiBackends();
        } else {
          showToast(`Failed to set ${backend.displayName} as default`, {
            type: "error",
          });
        }
        return;
      }
      requestCliSetup(backend, action);
      navigateToView("terminal");
    },
    [navigateToView, refetchAiBackends, showToast, updateOnboardingBackend],
  );
  const handleCreateIssueSuccess = useCallback(
    async (issue: Issue) => {
      await refetch();

      if (
        createIssueMode === "onboarding" &&
        shouldShowWorkspaceOnboarding &&
        shouldPrefillOnboardingIssue
      ) {
        closeAllPanels();
        navigateToView("kanban");
        let latestAgents = workspace?.agents ?? [];
        try {
          latestAgents = (await fetchWorkspaceApi(workspaceId)).agents ?? [];
        } catch {
          // Fall back to the context snapshot; the toast below covers missing agents.
        }
        const onboardingAgent = getOnboardingPlannerName(latestAgents);

        if (onboardingAgent) {
          try {
            await assignAndStartAgent(workspaceId, issue.id, onboardingAgent);
            showToast(`Started ${onboardingAgent} on ${issue.id}`, {
              type: "success",
            });
          } catch (err) {
            const message =
              err instanceof Error ? err.message : "failed to start agent";
            showToast(`Task created, but agent did not start: ${message}`, {
              type: "error",
            });
          }
        } else {
          showToast(
            "Task created, but no planner agent is available. Create a plan agent to start it.",
            { type: "warning" },
          );
        }
        return;
      }

      openPanel({ type: "issue", id: issue.id });
      fetchIssue(issue.id);
    },
    [
      closeAllPanels,
      createIssueMode,
      fetchIssue,
      navigateToView,
      openPanel,
      refetch,
      shouldPrefillOnboardingIssue,
      shouldShowWorkspaceOnboarding,
      showToast,
      workspace?.agents,
      workspaceId,
    ],
  );
  const workspaceOnboardingSteps: OnboardingStep[] = useMemo(
    () => [
      {
        id: "workspace-repo",
        title: "Create workspace with repo",
        description: hasWorkspaceRepo
          ? "The sample repo is attached to this workspace."
          : "Add the sample repo from the workspace tree; the URL is prefilled for first-run setup.",
        status: hasWorkspaceRepo ? "complete" : "current",
      },
      {
        id: "verify-repo",
        title: "Verify repository",
        description: hasWorkspaceRepo
          ? "The repo is visible to Loom and ready for the next setup step."
          : "Repository checks run after a repo has been attached.",
        status: hasWorkspaceRepo ? "complete" : "blocked",
      },
      {
        id: "setup-backend",
        title: "Set up AI CLIs",
        description: isDefaultBackendReady
          ? `${defaultBackendStatus?.displayName ?? "The default CLI"} is ready.`
          : "Install, login, or choose a ready CLI.",
        status: !hasWorkspaceRepo
          ? "blocked"
          : isDefaultBackendReady
            ? "complete"
            : "actionable",
        detail: hasWorkspaceRepo ? (
          <AIBackendSetupList
            backends={aiBackends}
            defaultBackend={defaultBackend}
            isLoading={aiBackendsLoading || onboardingBackendConfigLoading}
            error={aiBackendsError}
            isSavingDefault={isSavingOnboardingBackend}
            onAction={handleBackendSetupAction}
          />
        ) : undefined,
      },
      {
        id: "create-agent",
        title: "Create agent",
        description: hasWorkspaceAgent
          ? "The first agent definition exists for this workspace."
          : "Create a prefilled planner agent for the sample repo.",
        status: hasWorkspaceAgent
          ? "complete"
          : hasWorkspaceRepo && isDefaultBackendReady
            ? "current"
            : "blocked",
        actionLabel: "Create Agent",
        onAction: () => setShowCreateAgent(true),
      },
      {
        id: "create-issue",
        title: "Create first issue",
        description: hasWorkspaceIssue
          ? "The first issue is ready for agent work."
          : "Create the prefilled sample task for the first agent run.",
        status: hasWorkspaceIssue
          ? "complete"
          : hasWorkspaceAgent && isDefaultBackendReady
            ? "current"
            : "blocked",
        actionLabel: "Create Issue",
        onAction: () => {
          setCreateIssueMode("onboarding");
          setShowCreateIssue(true);
        },
      },
    ],
    [
      hasWorkspaceAgent,
      hasWorkspaceIssue,
      hasWorkspaceRepo,
      isDefaultBackendReady,
      defaultBackendStatus,
      aiBackends,
      defaultBackend,
      aiBackendsLoading,
      onboardingBackendConfigLoading,
      aiBackendsError,
      isSavingOnboardingBackend,
      handleBackendSetupAction,
    ],
  );

  useEffect(() => {
    setOnboardingDismissed(isOnboardingDismissed(workspaceId));
  }, [workspaceId]);

  useEffect(() => {
    const handleRestart = (event: Event) => {
      const detail = (event as CustomEvent<OnboardingRestartDetail>).detail;
      if (!detail?.workspaceId || detail.workspaceId === workspaceId) {
        setOnboardingDismissed(false);
      }
    };
    window.addEventListener(ONBOARDING_RESTART_EVENT, handleRestart);
    return () => {
      window.removeEventListener(ONBOARDING_RESTART_EVENT, handleRestart);
    };
  }, [workspaceId]);

  useEffect(() => {
    if (isWorkspaceOnboardingComplete) {
      setOnboardingDismissed(false);
    }
  }, [isWorkspaceOnboardingComplete]);

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

  const refetchWorkspaceAfterAgentCreate = useCallback(() => {
    refetchWorkspace();
    window.setTimeout(refetchWorkspace, 750);
  }, [refetchWorkspace]);

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
      debouncedSearch: activeSearchTerm,
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
      activeSearchTerm,
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
          actions={syncedFilterActions}
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
          onGroupByChange={syncedFilterActions.setGroupBy}
        />
      </div>
      <button
        className={styles.newIssueButton}
        onClick={() => {
          setCreateIssueMode("manual");
          setShowCreateIssue(true);
        }}
        aria-label="New Issue"
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

  const terminalContainerClassName =
    activeView === "terminal"
      ? styles.terminalRouteContainer
      : styles.terminalHidden;
  const terminalContainerStyle: CSSProperties =
    activeView === "terminal" ? { display: "contents" } : { display: "none" };
  const isTerminalActive = activeView === "terminal";

  return (
    <KeyboardShortcutProvider
      onViewChange={handleNavChange}
      onSearchFocus={handleSearchFocus}
      {...(hasMultipleWorkspaces && {
        onWorkspaceSwitcher: handleWorkspaceSwitcherToggle,
        onWorkspacePositionalSwitch: handleWorkspacePositionalSwitch,
      })}
    >
      <SearchTermProvider value={activeSearchTerm}>
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
          <div
            className={
              shouldShowWorkspaceOnboarding
                ? styles.workspaceContentWithOnboarding
                : styles.workspaceContent
            }
          >
            <div className={styles.workspaceMainContent}>
              <ViewSubSwitcher
                activeView={activeView}
                onChange={navigateToView}
              />
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
              <div
                className={terminalContainerClassName}
                style={terminalContainerStyle}
              >
                <Suspense fallback={<LoadingSkeleton.Terminal />}>
                  <TerminalView
                    isActive={isTerminalActive}
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
            </div>
            {shouldShowWorkspaceOnboarding && (
              <aside
                className={styles.onboardingSidePanel}
                aria-label="Onboarding checklist"
              >
                <OnboardingFlow
                  className={styles.workspaceOnboarding ?? ""}
                  variant="panel"
                  title="Finish onboarding"
                  subtitle="Keep this checklist open while you move through Loom. Setup actions switch the main view without losing progress."
                  repoUrl={ONBOARDING_REPO_URL}
                  steps={workspaceOnboardingSteps}
                  onDismiss={handleOnboardingDismiss}
                />
              </aside>
            )}
          </div>
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
            onClose={() => {
              setShowCreateIssue(false);
              setCreateIssueMode("manual");
            }}
            onSuccess={handleCreateIssueSuccess}
            {...(createIssueMode === "onboarding" &&
            onboardingIssueInitialValues
              ? { initialValues: onboardingIssueInitialValues }
              : {})}
          />
          <TalkToLeadButton
            onClick={handleTalkToLeadClick}
            isActive={activeView === "terminal"}
            sessionCount={activeSessionCount}
            avoidSidePanel={shouldShowWorkspaceOnboarding}
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
        initialValues={onboardingWorkspaceInitialValues}
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
        {...(shouldPrefillOnboardingAgent
          ? {
              defaultName: ONBOARDING_AGENT_NAME,
              defaultRoleName: ONBOARDING_AGENT_ROLE,
            }
          : {})}
        onClose={() => setShowCreateAgent(false)}
        onSuccess={(agent) => {
          setShowCreateAgent(false);
          refetchWorkspaceAfterAgentCreate();
          showToast(`Agent "${agent.name}" created`, { type: "success" });
        }}
      />
    </KeyboardShortcutProvider>
  );
}
export default App;
