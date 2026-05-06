/**
 * IssueDetailPanel component.
 * Slide-out side panel that displays detailed information about a selected issue.
 * Features improved information hierarchy with sticky header, collapsible sections,
 * and markdown rendering for design field.
 */

import { useEffect, useRef, useState, useCallback, useMemo } from "react";

import {
  updateIssue,
  addDependency,
  removeDependency,
  getIssueEvents,
  moveIssue,
  deleteTabMetadata,
  getTaskLogPhases,
  startAgent,
} from "@/hooks/api";
import type { IssueTab } from "@/api/issues";
import {
  useFocusReturn,
  useFocusTrap,
  useRegisterEscapeLayer,
  LAYER_ISSUE_PANEL,
} from "@/hooks";
import { useStore } from "zustand";

import { useAgentStoreInstance } from "@/hooks/common";
import { useWorkspaceContext } from "@/hooks/workspace";
import { useIssueTabPersistence } from "@/hooks/issues";
import type {
  Issue,
  IssueDetails,
  IssueWithDependencyMetadata,
  Priority,
  IssueType,
  DependencyType,
  Comment,
  Event,
} from "@/types";
import type { Status } from "@/types/issue";
import { getReviewType, isPRUrl } from "@/utils/issue";

import {
  getBackendFromSessionName,
  type ConnectionState,
} from "@/components/TerminalView";
import { useTaskLogPolling } from "@/hooks/terminal";

import {
  ActivityLog,
  CommentForm,
  DependencySection,
  EditableDescription,
  DesignPanel,
  MarkdownRenderer,
  RejectCommentForm,
} from "./sections";
import { AgentStatusBadge, IssueHeader } from "./header";
import {
  AssigneeDropdown,
  OwnerDropdown,
  PriorityDropdown,
  RepoDropdown,
  TypeDropdown,
  LabelEditor,
} from "./fields";
import { StartWorkButton } from "./actions";
import { ConfirmDialog } from "../ConfirmDialog";
import { MoveIssueDialog } from "./actions";
import { SplitDetailSummary } from "./SplitDetailSummary";
import { EmbeddedTerminal } from "../EmbeddedTerminal";
import { ResizeDivider } from "./actions";
import { ErrorToast } from "../ErrorToast";
import { useSplitRatio } from "@/hooks/ui";
import { CollapsibleSection } from "./CollapsibleSection";
import { SessionHistorySection, SessionsTab } from "./sessions";
import styles from "./IssueDetailPanel.module.css";
import { formatDate, formatIssueType, isIssueDetails } from "./utils";

/**
 * Blocking banner component - shows when issue is in blocked state with open dependencies.
 * Displays as a visual indicator (non-interactive).
 */
interface BlockingBannerProps {
  openBlockerCount: number;
  status: Status | undefined;
}

function BlockingBanner({
  openBlockerCount,
  status,
}: BlockingBannerProps): JSX.Element | null {
  // Only show banner when status is 'blocked' AND there are open blockers
  if (status !== "blocked" || openBlockerCount === 0) return null;

  return (
    <div
      className={styles.blockingBanner}
      role="alert"
      data-testid="blocking-banner"
    >
      <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <path
          d="M8 1L1 15h14L8 1z"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <path
          d="M8 6v3M8 11.5v.5"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
        />
      </svg>
      Blocked by {openBlockerCount}{" "}
      {openBlockerCount === 1 ? "issue" : "issues"}
    </div>
  );
}

/**
 * Props for the IssueDetailPanel component.
 */
export interface IssueDetailPanelProps {
  /** Whether the panel is open */
  isOpen: boolean;
  /** The issue to display (null when closed or loading) */
  issue: Issue | IssueDetails | null;
  /** Callback when panel should close */
  onClose: () => void;
  /** Whether the issue details are loading */
  isLoading?: boolean;
  /** Error message if loading failed */
  error?: string | null;
  /** Additional CSS class name */
  className?: string;
  /** Children to render in the panel content area (overrides default content) */
  children?: React.ReactNode;
  /** Callback when approve button is clicked (only for review items) */
  onApprove?: (issue: Issue) => void | Promise<void>;
  /** Callback when reject is submitted with comment (only for review items) */
  onReject?: (issue: Issue, comment: string) => void | Promise<void>;
  /** Callback when issue is updated (e.g., status, title, priority changed) */
  onIssueUpdate?: (issue: Issue) => void;
  /** Callback when copy-link button is clicked */
  onCopyLink?: () => void;
  /** Callback when a dependency/dependent issue is clicked for navigation */
  onNavigateToIssue?: (issue: Issue) => void;
}

/**
 * Get the CSS class for a dependency status dot.
 */
function getDependentStatusDotClass(status: string | undefined): string {
  switch (status) {
    case "closed":
      return styles.statusDotClosed ?? "";
    case "in_progress":
      return styles.statusDotInProgress ?? "";
    case "blocked":
      return styles.statusDotBlocked ?? "";
    default:
      return styles.statusDotOpen ?? "";
  }
}

/**
 * Render a dependency/dependent issue as a clickable chip.
 */
function renderDependencyChip(
  dep: IssueWithDependencyMetadata,
  onNavigateToIssue?: (issue: Issue) => void,
): JSX.Element {
  const statusClass = dep.status === "closed" ? styles.dependencyClosed : "";
  const isClickable = !!onNavigateToIssue;
  return (
    <li
      key={dep.id}
      className={`${styles.dependencyChip} ${statusClass} ${isClickable ? styles.clickableChip : ""}`}
      onClick={isClickable ? () => onNavigateToIssue(dep) : undefined}
      role={isClickable ? "button" : undefined}
      tabIndex={isClickable ? 0 : undefined}
      onKeyDown={
        isClickable
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onNavigateToIssue(dep);
              }
            }
          : undefined
      }
    >
      <span
        className={`${styles.statusDot} ${getDependentStatusDotClass(dep.status)}`}
        aria-label={dep.status ?? "open"}
      />
      <span className={styles.dependencyId}>{dep.id}</span>
      <span className={styles.dependencyTitle}>{dep.title}</span>
      {dep.dependency_type && (
        <span className={styles.dependencyType}>{dep.dependency_type}</span>
      )}
    </li>
  );
}

/**
 * Props for the DefaultContent component.
 */
interface DefaultContentProps {
  issue: Issue | IssueDetails | null;
  isLoading: boolean;
  error: string | null;
  onClose: () => void;
  onRetry?: () => void;
  /** Callback when issue is updated (e.g., title changed) */
  onIssueUpdate?: (issue: Issue) => void;
  /** Callback when approve button is clicked */
  onApprove?: (issue: Issue) => void | Promise<void>;
  /** Callback when reject is submitted with comment */
  onReject?: (issue: Issue, comment: string) => void | Promise<void>;
  /** Callback when copy-link button is clicked */
  onCopyLink?: () => void;
  /** Callback when a dependency/dependent issue is clicked for navigation */
  onNavigateToIssue?: (issue: Issue) => void;
}

/**
 * Tab model for dynamic tab management.
 */
interface DetailTabMetadata {
  sessionName?: string | undefined;
  backend?: string | undefined;
  agentName?: string | null | undefined;
  worktreePath?: string | undefined;
}

interface DetailTab {
  id: string;
  type: "details" | "terminal" | "sessions" | "task-log";
  label: string;
  closable: boolean;
  metadata?: DetailTabMetadata | undefined;
  connectionState?: ConnectionState | undefined;
}

const DETAILS_TAB: DetailTab = {
  id: "details",
  type: "details",
  label: "Details",
  closable: false,
};

const SESSIONS_TAB: DetailTab = {
  id: "sessions",
  type: "sessions",
  label: "Sessions",
  closable: false,
};

function formatPhaseLabel(phase: string): string {
  return phase.charAt(0).toUpperCase() + phase.slice(1);
}

function canRenderDetailTab(tab: DetailTab | undefined): boolean {
  if (!tab) return false;
  switch (tab.type) {
    case "details":
    case "sessions":
    case "task-log":
      return true;
    case "terminal":
      return Boolean(tab.metadata?.sessionName && tab.metadata.backend);
    default:
      return false;
  }
}

function TaskPhaseLogPanel({
  issueId,
  phase,
}: {
  issueId: string;
  phase: "planning" | "implementation";
}): JSX.Element {
  const { chunks, state } = useTaskLogPolling({
    taskId: issueId,
    phase,
    enabled: true,
    pollIntervalMs: 500,
  });
  const text = useMemo(() => {
    const decoder = new TextDecoder();
    return chunks.map((chunk) => decoder.decode(chunk.chunk)).join("");
  }, [chunks]);

  return (
    <div
      className={styles.scrollableContent}
      role="tabpanel"
      id={`issue-panel-tabpanel-task-log-${phase}`}
      aria-labelledby={`issue-panel-tab-task-log-${phase}`}
    >
      <div className={styles.section}>
        <h3 className={styles.sectionTitle}>{formatPhaseLabel(phase)}</h3>
        <div data-testid="log-viewer">
          <span data-state={state}>{state}</span>
          <pre data-testid="terminal-container">{text}</pre>
        </div>
      </div>
    </div>
  );
}

/**
 * Default content renderer for issue details.
 */
function DefaultContent({
  issue,
  isLoading,
  error,
  onClose,
  onRetry,
  onIssueUpdate,
  onApprove,
  onReject,
  onCopyLink,
  onNavigateToIssue,
}: DefaultContentProps): JSX.Element {
  const { workspaceId, workspace, repos } = useWorkspaceContext();
  const [isSavingTitle, setIsSavingTitle] = useState(false);
  const [isSavingStatus, setIsSavingStatus] = useState(false);
  const [isSavingPriority, setIsSavingPriority] = useState(false);
  const [isSavingType, setIsSavingType] = useState(false);
  const [isSavingAssignee, setIsSavingAssignee] = useState(false);
  const [isSavingOwner, setIsSavingOwner] = useState(false);
  const [isSavingRepo, setIsSavingRepo] = useState(false);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [titleError, setTitleError] = useState<string | null>(null);
  const [showRejectForm, setShowRejectForm] = useState(false);
  const [isApproving, setIsApproving] = useState(false);
  const [isRejecting, setIsRejecting] = useState(false);
  const [rejectError, setRejectError] = useState<string | null>(null);
  const [showMoveDialog, setShowMoveDialog] = useState(false);
  const [moveError, setMoveError] = useState<string | null>(null);

  // Split view state for terminal tabs
  const splitContainerRef = useRef<HTMLDivElement>(null);
  const { ratio, applyDelta, resetRatio, isMaximized, toggleMaximize } =
    useSplitRatio();

  // Workspace data for move dialog
  const workspaces = workspace?.workspaces ?? [];
  const currentWorkspace = workspace?.name ?? "";
  const canMove = workspaces.length > 1 && issue?.status !== "closed";

  const currentRepo = useMemo(() => {
    if (issue?.repo) return issue.repo;
    if (issue?.source_repo) return issue.source_repo;
    const repoLabel = issue?.labels?.find((l) => l.startsWith("repo:"));
    return repoLabel ? repoLabel.slice(5) : null;
  }, [issue?.labels, issue?.repo, issue?.source_repo]);
  const [taskLogPhases, setTaskLogPhases] = useState<
    ("planning" | "implementation")[]
  >([]);

  // Tab persistence hook - loads/saves tab state to Redis
  const issueId = issue?.id ?? "";
  const {
    savedState: persistedTabState,
    isLoading: isLoadingPersistedTabs,
    saveTabs: persistTabs,
  } = useIssueTabPersistence(issueId);

  // Tab state - managed tab array with dynamic add/remove
  const [tabs, setTabs] = useState<DetailTab[]>([DETAILS_TAB, SESSIONS_TAB]);
  const [activeTabId, setActiveTabId] = useState("details");
  // Track whether we've already restored tabs from persistence for this issue
  const restoredIssueIdRef = useRef<string | null>(null);
  // Ref mirroring tabs for cleanup effects (avoids adding tabs as dependency)
  const tabsRef = useRef<DetailTab[]>(tabs);
  useEffect(() => {
    tabsRef.current = tabs;
  }, [tabs]);

  useEffect(() => {
    let cancelled = false;
    setTaskLogPhases([]);
    if (!issue?.id || issue.issue_type !== "task") return;

    getTaskLogPhases(workspaceId, issue.id)
      .then((phases) => {
        if (cancelled) return;
        setTaskLogPhases(
          phases.filter(
            (phase): phase is "planning" | "implementation" =>
              phase === "planning" || phase === "implementation",
          ),
        );
      })
      .catch(() => {
        if (!cancelled) setTaskLogPhases([]);
      });

    return () => {
      cancelled = true;
    };
  }, [issue?.id, issue?.issue_type, workspaceId]);

  // Pending close confirmation state
  const [pendingCloseTabId, setPendingCloseTabId] = useState<string | null>(
    null,
  );

  // Fire-and-forget cleanup for terminal tabs discarded implicitly (issue change, unmount)
  const cleanupTerminalTabs = useCallback(
    (tabList: DetailTab[]) => {
      for (const tab of tabList) {
        if (tab.type !== "terminal" || !tab.metadata?.sessionName) continue;
        const sn = tab.metadata.sessionName;
        // Only tab metadata cleanup remains — PTYs die with their WS so no
        // server-side kill call is needed.
        if (workspace?.id) deleteTabMetadata(workspace.id, sn).catch(() => {});
      }
    },
    [workspace?.id],
  );

  // Close a tab: remove from state, clean up backend resources
  const closeTab = useCallback(
    (id: string) => {
      const tab = tabs.find((t) => t.id === id);
      if (tab) cleanupTerminalTabs([tab]);
      setTabs((prev) => prev.filter((t) => t.id !== id));
      setActiveTabId((prev) => (prev === id ? "details" : prev));
    },
    [tabs, cleanupTerminalTabs],
  );

  // Handle tab close: confirm if terminal has active connection
  const handleTabClose = useCallback(
    (tabId: string) => {
      const tab = tabs.find((t) => t.id === tabId);
      if (!tab || !tab.closable) return;

      if (tab.type === "terminal" && tab.connectionState === "connected") {
        setPendingCloseTabId(tabId);
        return;
      }

      closeTab(tabId);
    },
    [tabs, closeTab],
  );

  // Track connectionState changes from EmbeddedTerminal
  const handleConnectionStateChange = useCallback(
    (tabId: string, state: ConnectionState) => {
      setTabs((prev) =>
        prev.map((t) =>
          t.id === tabId ? { ...t, connectionState: state } : t,
        ),
      );
    },
    [],
  );

  // Agent data for StartWorkButton (shared store, no duplicate polling)
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const agentTasks = useStore(agentStore, (s) => s.agentTasks);
  const isLoomConnected = useStore(agentStore, (s) => s.isConnected);
  const refetchAgents = useStore(agentStore, (s) => s.fetchData);

  // Reset tabs when issue changes — clean up orphaned terminal sessions first
  useEffect(() => {
    cleanupTerminalTabs(tabsRef.current);
    // Sync ref immediately so unmount cleanup won't re-clean the same tabs
    tabsRef.current = [DETAILS_TAB, SESSIONS_TAB];
    restoredIssueIdRef.current = null;
    setTabs([DETAILS_TAB, SESSIONS_TAB]);
    setActiveTabId("details");
  }, [issue?.id, cleanupTerminalTabs]);

  // Restore tabs from persisted state once loaded
  useEffect(() => {
    if (
      !persistedTabState ||
      !persistedTabState.tabs ||
      isLoadingPersistedTabs ||
      !issue?.id ||
      restoredIssueIdRef.current === issue.id
    ) {
      return;
    }
    restoredIssueIdRef.current = issue.id;

    const validTypes = new Set(["details", "terminal", "sessions"]);
    const restoredTabs: DetailTab[] = persistedTabState.tabs
      .filter((t) => validTypes.has(t.type))
      .map((t) => ({
        id: t.id,
        type: t.type as DetailTab["type"],
        label: t.label,
        closable: t.type !== "details" && t.type !== "sessions",
        metadata:
          t.type === "terminal" && t.session_name
            ? (() => {
                const derived = getBackendFromSessionName(t.session_name);
                return {
                  sessionName: t.session_name,
                  backend:
                    t.backend || (derived !== "unknown" ? derived : undefined),
                };
              })()
            : undefined,
        connectionState:
          t.type === "terminal"
            ? ("disconnected" as ConnectionState)
            : undefined,
      }));

    // Ensure details tab is always present
    if (!restoredTabs.some((t) => t.id === "details")) {
      restoredTabs.unshift(DETAILS_TAB);
    }
    // Ensure sessions tab is always present
    if (!restoredTabs.some((t) => t.id === "sessions")) {
      // Insert after details tab
      const detailsIdx = restoredTabs.findIndex((t) => t.id === "details");
      restoredTabs.splice(detailsIdx + 1, 0, SESSIONS_TAB);
    }

    if (restoredTabs.length > 0) {
      setTabs(restoredTabs);
      const activeId = persistedTabState.active_tab_id;
      if (activeId && restoredTabs.some((t) => t.id === activeId)) {
        setActiveTabId(activeId);
      }
    }
  }, [persistedTabState, isLoadingPersistedTabs, issue?.id]);

  // Persist tab state on changes (debounced via hook)
  useEffect(() => {
    // Don't persist while still loading persisted state or before restoration
    if (
      isLoadingPersistedTabs ||
      !issue?.id ||
      restoredIssueIdRef.current !== issue.id
    ) {
      return;
    }
    // Only persist if there's something beyond the default tabs (details + sessions)
    const isDefault =
      tabs.length === 2 &&
      tabs[0]?.id === "details" &&
      tabs[1]?.id === "sessions" &&
      activeTabId === "details";
    if (isDefault) return;

    const persistableTabs = tabs.filter((t) => t.type !== "task-log");
    const tabsToSave: IssueTab[] = persistableTabs.map((t, i) => {
      const tab: IssueTab = {
        id: t.id,
        type: t.type as IssueTab["type"],
        label: t.label,
        sort_order: i,
      };
      if (t.metadata?.sessionName) {
        tab.session_name = t.metadata.sessionName;
      }
      if (t.metadata?.backend) {
        tab.backend = t.metadata.backend;
      }
      return tab;
    });
    const activeTabIsPersistable = persistableTabs.some(
      (t) => t.id === activeTabId && canRenderDetailTab(t),
    );
    persistTabs(tabsToSave, activeTabIsPersistable ? activeTabId : "details");
  }, [tabs, activeTabId, issue?.id, isLoadingPersistedTabs, persistTabs]);

  const visibleTabs = useMemo(() => {
    const phaseTabs: DetailTab[] = taskLogPhases.map((phase) => ({
      id: `task-log-${phase}`,
      type: "task-log",
      label: formatPhaseLabel(phase),
      closable: false,
    }));
    const detailsIndex = tabs.findIndex((tab) => tab.id === "details");
    if (detailsIndex === -1) return [...phaseTabs, ...tabs];
    return [
      ...tabs.slice(0, detailsIndex + 1),
      ...phaseTabs,
      ...tabs.slice(detailsIndex + 1),
    ];
  }, [tabs, taskLogPhases]);
  const activeTab = useMemo(
    () => visibleTabs.find((tab) => tab.id === activeTabId),
    [activeTabId, visibleTabs],
  );
  const renderedActiveTabId = canRenderDetailTab(activeTab)
    ? activeTabId
    : "details";

  useEffect(() => {
    if (activeTabId !== renderedActiveTabId) {
      setActiveTabId(renderedActiveTabId);
    }
  }, [activeTabId, renderedActiveTabId]);

  // Ref to cleanupTerminalTabs so unmount cleanup uses latest without re-registering
  const cleanupRef = useRef(cleanupTerminalTabs);
  useEffect(() => {
    cleanupRef.current = cleanupTerminalTabs;
  }, [cleanupTerminalTabs]);
  useEffect(
    () => () => {
      cleanupRef.current(tabsRef.current);
    },
    [],
  );

  // Local state for comments to enable optimistic updates
  const hasDetails = issue && isIssueDetails(issue);
  const initialComments = hasDetails ? issue.comments : undefined;
  const [localComments, setLocalComments] = useState<Comment[] | undefined>(
    initialComments,
  );

  // Local state for events (activity log)
  const [events, setEvents] = useState<Event[]>([]);

  // Sync local comments when issue changes (e.g., different issue selected)
  useEffect(() => {
    if (issue && isIssueDetails(issue)) {
      setLocalComments(issue.comments);
    } else {
      setLocalComments(undefined);
    }
  }, [issue]);

  // Fetch events when issue changes
  const eventIssueId = issue?.id;
  useEffect(() => {
    if (!eventIssueId) {
      setEvents([]);
      return;
    }
    let cancelled = false;
    getIssueEvents(workspaceId, eventIssueId).then(
      (data) => {
        if (!cancelled) setEvents(data ?? []);
      },
      () => {
        if (!cancelled) setEvents([]);
      },
    );
    return () => {
      cancelled = true;
    };
  }, [eventIssueId]);

  // Handler for when a new comment is added
  const handleCommentAdded = useCallback((newComment: Comment) => {
    setLocalComments((prev) => {
      if (!prev) return [newComment];
      return [...prev, newComment];
    });
  }, []);

  const handleTitleSave = useCallback(
    async (newTitle: string) => {
      if (!issue) return;

      setTitleError(null);
      setIsSavingTitle(true);
      try {
        const updatedIssue = await updateIssue(workspaceId, issue.id, {
          title: newTitle,
        });
        onIssueUpdate?.(updatedIssue);
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to update title";
        setTitleError(message);
        throw err; // Re-throw so EditableTitle's internal error handling activates
      } finally {
        setIsSavingTitle(false);
      }
    },
    [issue, onIssueUpdate],
  );

  const handleStatusChange = useCallback(
    async (newStatus: Status) => {
      if (!issue) return;

      setIsSavingStatus(true);
      setStatusError(null);
      try {
        const updatedIssue = await updateIssue(workspaceId, issue.id, {
          status: newStatus,
        });
        onIssueUpdate?.(updatedIssue);
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to update status";
        setStatusError(message);
      } finally {
        setIsSavingStatus(false);
      }
    },
    [issue, onIssueUpdate],
  );

  const handlePrioritySave = useCallback(
    async (newPriority: Priority) => {
      if (!issue) return;

      setIsSavingPriority(true);
      try {
        const updatedIssue = await updateIssue(workspaceId, issue.id, {
          priority: newPriority,
        });
        onIssueUpdate?.(updatedIssue);
      } finally {
        setIsSavingPriority(false);
      }
    },
    [issue, onIssueUpdate],
  );

  const handleTypeSave = useCallback(
    async (newType: IssueType) => {
      if (!issue) return;

      setIsSavingType(true);
      try {
        const updatedIssue = await updateIssue(workspaceId, issue.id, {
          issue_type: newType,
        });
        onIssueUpdate?.(updatedIssue);
      } finally {
        setIsSavingType(false);
      }
    },
    [issue, onIssueUpdate],
  );

  const handleAssigneeSave = useCallback(
    async (newAssignee: string) => {
      if (!issue) return;

      setIsSavingAssignee(true);
      try {
        const updatedIssue = await updateIssue(workspaceId, issue.id, {
          assignee: newAssignee,
        });
        onIssueUpdate?.(updatedIssue);
      } finally {
        setIsSavingAssignee(false);
      }
    },
    [issue, onIssueUpdate],
  );

  const handleOwnerSave = useCallback(
    async (newOwner: string) => {
      if (!issue) return;

      setIsSavingOwner(true);
      try {
        const updatedIssue = await updateIssue(workspaceId, issue.id, {
          owner: newOwner,
        });
        onIssueUpdate?.(updatedIssue);
      } finally {
        setIsSavingOwner(false);
      }
    },
    [issue, onIssueUpdate],
  );

  const handleRepoSave = useCallback(
    async (newRepo: string | null) => {
      if (!issue) return;

      setIsSavingRepo(true);
      try {
        const currentLabels = issue.labels ?? [];
        const repoLabelsToRemove = currentLabels.filter((l) =>
          l.startsWith("repo:"),
        );
        const updatedIssue = await updateIssue(workspaceId, issue.id, {
          ...(repoLabelsToRemove.length > 0
            ? { remove_labels: repoLabelsToRemove }
            : {}),
          ...(newRepo ? { add_labels: [`repo:${newRepo}`] } : {}),
        });
        onIssueUpdate?.(updatedIssue);
      } finally {
        setIsSavingRepo(false);
      }
    },
    [issue, onIssueUpdate],
  );

  const handleStartWork = useCallback(
    async (agentName: string) => {
      if (!issue) return;

      const updatedIssue = await updateIssue(workspaceId, issue.id, {
        assignee: agentName,
      });
      onIssueUpdate?.(updatedIssue);
      await startAgent(workspaceId, agentName, { taskId: issue.id });
      void refetchAgents();
    },
    [issue, onIssueUpdate, refetchAgents, workspaceId],
  );

  const handleAddDependency = useCallback(
    async (dependsOnId: string, type: DependencyType) => {
      if (!issue) return;
      await addDependency(workspaceId, issue.id, dependsOnId, type);
      // The parent component should refresh issue details via SSE or manual refetch
    },
    [issue],
  );

  const handleRemoveDependency = useCallback(
    async (dependsOnId: string) => {
      if (!issue) return;
      await removeDependency(workspaceId, issue.id, dependsOnId);
      // The parent component should refresh issue details via SSE or manual refetch
    },
    [issue],
  );

  const handleAddLabel = useCallback(
    async (label: string) => {
      if (!issue) return;
      const updatedIssue = await updateIssue(workspaceId, issue.id, {
        add_labels: [label],
      });
      const labels = updatedIssue.labels?.includes(label)
        ? updatedIssue.labels
        : [...(updatedIssue.labels ?? issue.labels ?? []), label];
      onIssueUpdate?.({ ...updatedIssue, labels });
    },
    [issue, onIssueUpdate],
  );

  const handleRemoveLabel = useCallback(
    async (label: string) => {
      if (!issue) return;
      const updatedIssue = await updateIssue(workspaceId, issue.id, {
        remove_labels: [label],
      });
      const labels = (updatedIssue.labels ?? issue.labels ?? []).filter(
        (current) => current !== label,
      );
      onIssueUpdate?.({ ...updatedIssue, labels });
    },
    [issue, onIssueUpdate],
  );

  // Approve handler
  const handleApprove = useCallback(async () => {
    if (!issue || !onApprove || isApproving) return;
    setIsApproving(true);
    try {
      await onApprove(issue as Issue);
    } catch {
      setIsApproving(false);
    }
  }, [issue, onApprove, isApproving]);

  // Reject button click - show form
  const handleRejectClick = useCallback(() => {
    setShowRejectForm(true);
    setRejectError(null);
  }, []);

  // Reject form cancel
  const handleRejectCancel = useCallback(() => {
    if (isRejecting) return;
    setShowRejectForm(false);
    setRejectError(null);
  }, [isRejecting]);

  // Reject form submit
  const handleRejectSubmit = useCallback(
    async (comment: string) => {
      if (!issue || !onReject || isRejecting) return;
      setIsRejecting(true);
      setRejectError(null);
      try {
        await onReject(issue as Issue, comment);
        // On success, panel will update via status change
      } catch (err) {
        setIsRejecting(false);
        const message = err instanceof Error ? err.message : "Failed to reject";
        setRejectError(message);
      }
    },
    [issue, onReject, isRejecting],
  );

  // Move issue handler
  const handleMoveConfirm = useCallback(
    async (targetWorkspace: string) => {
      if (!issue) return;
      setMoveError(null);
      try {
        await moveIssue(workspaceId, issue.id, targetWorkspace);
        setShowMoveDialog(false);
        onClose();
      } catch (err) {
        setMoveError(
          err instanceof Error ? err.message : "Failed to move issue",
        );
      }
    },
    [issue, onClose],
  );

  // Reset reject form state when issue changes
  useEffect(() => {
    setShowRejectForm(false);
    setIsApproving(false);
    setIsRejecting(false);
    setRejectError(null);
    setShowMoveDialog(false);
    setMoveError(null);
  }, [issue?.id]);

  // Loading state
  if (isLoading) {
    return (
      <div className={styles.loadingContainer} data-testid="panel-loading">
        <div className={styles.spinner} />
        <p>Loading issue details...</p>
      </div>
    );
  }

  // Error state
  if (error) {
    return (
      <div className={styles.errorContainer} data-testid="panel-error">
        <p className={styles.errorMessage}>{error}</p>
        {onRetry && (
          <button className={styles.retryButton} onClick={onRetry}>
            Retry
          </button>
        )}
      </div>
    );
  }

  // No issue
  if (!issue) {
    return (
      <div className={styles.emptyContainer}>
        <p>No issue selected</p>
      </div>
    );
  }

  const issueHasDetails = isIssueDetails(issue);
  const dependencies = issueHasDetails ? issue.dependencies : undefined;
  const dependents = issueHasDetails ? issue.dependents : undefined;

  // Determine if this is a review item
  const reviewType = getReviewType(issue);
  const isReviewItem = reviewType !== null;

  // Calculate open blocker count for banner
  const openBlockerCount =
    dependencies?.filter((d) => d.status !== "closed").length ?? 0;

  // Extract PR URL and number from external_ref for header links
  const prNumber =
    issue.external_ref && isPRUrl(issue.external_ref)
      ? issue.external_ref.match(/\/pulls?\/(\d+)/)?.[1]
      : undefined;
  const prProps = prNumber ? { prUrl: issue.external_ref!, prNumber } : {};

  // Auto-collapse logic for Notes (collapse if long, but keep expanded for review items)
  const shouldCollapseNotes =
    issue.notes &&
    (issue.notes.length > 200 || issue.notes.split("\n").length > 5);

  return (
    <>
      {/* Sticky Header Wrapper */}
      <div className={styles.stickyHeaderWrapper}>
        {/* Header with ID, status dropdown, priority badge, close button, and title */}
        <IssueHeader
          issue={issue}
          onClose={onClose}
          onTitleSave={handleTitleSave}
          isSavingTitle={isSavingTitle}
          onStatusChange={handleStatusChange}
          isSavingStatus={isSavingStatus}
          showPriority={true}
          {...(onCopyLink !== undefined && { onCopyLink })}
          {...(canMove && { onMove: () => setShowMoveDialog(true) })}
          {...prProps}
          sticky={true}
        />

        {/* Metadata Bar */}
        <div className={styles.metadataBar}>
          <span className={styles.metadataItem} data-testid="metadata-type">
            <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <path
                d="M2 4h12M2 8h12M2 12h8"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
              />
            </svg>
            {formatIssueType(issue.issue_type)}
          </span>
          <OwnerDropdown
            owner={issue.owner}
            onSave={handleOwnerSave}
            isSaving={isSavingOwner}
          />
          <AssigneeDropdown
            assignee={issue.assignee}
            onSave={handleAssigneeSave}
            isSaving={isSavingAssignee}
            agents={agents}
            agentTasks={agentTasks}
          />
          {issue.created_at && (
            <span
              className={styles.metadataItem}
              data-testid="metadata-created"
            >
              Created: {formatDate(issue.created_at)}
            </span>
          )}
        </div>
      </div>

      {/* Review Action Bar (shown for review items when reject form is not open) */}
      {isReviewItem && !showRejectForm && onApprove && onReject && (
        <div className={styles.reviewActionBar} data-testid="review-action-bar">
          <button
            type="button"
            className={styles.reviewApproveButton}
            onClick={handleApprove}
            disabled={isApproving}
            aria-label="Approve"
            data-testid="panel-approve-button"
          >
            {isApproving ? "..." : "\u2713"} Approve
          </button>
          <button
            type="button"
            className={styles.reviewRejectButton}
            onClick={handleRejectClick}
            aria-label="Reject"
            data-testid="panel-reject-button"
          >
            {"\u2717"} Reject
          </button>
        </div>
      )}

      {/* Reject Comment Form (shown below action bar when rejecting) */}
      {showRejectForm && onReject && (
        <RejectCommentForm
          issueId={issue.id}
          onSubmit={handleRejectSubmit}
          onCancel={handleRejectCancel}
          isSubmitting={isRejecting}
          error={rejectError}
        />
      )}

      {/* Blocking Banner */}
      <BlockingBanner
        openBlockerCount={openBlockerCount}
        status={issue.status}
      />

      {/* Tab Bar */}
      <div className={styles.tabBarWrapper}>
        <div
          className={styles.tabBar}
          role="tablist"
          aria-label="Issue detail tabs"
        >
          {visibleTabs.map((tab) => (
            <div
              key={tab.id}
              role="tab"
              className={`${styles.tab} ${renderedActiveTabId === tab.id ? styles.activeTab : ""}`}
              aria-selected={renderedActiveTabId === tab.id}
              aria-controls={`issue-panel-tabpanel-${tab.id}`}
              id={`issue-panel-tab-${tab.id}`}
              onClick={() => setActiveTabId(tab.id)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  setActiveTabId(tab.id);
                }
              }}
              tabIndex={renderedActiveTabId === tab.id ? 0 : -1}
            >
              {tab.type === "terminal" && tab.connectionState && (
                <span
                  className={styles.tabConnectionDot}
                  data-state={tab.connectionState}
                  data-testid={`tab-connection-dot-${tab.id}`}
                />
              )}
              <span className={styles.tabLabel}>{tab.label}</span>
              {tab.closable && (
                <button
                  type="button"
                  className={styles.tabCloseButton}
                  onClick={(e) => {
                    e.stopPropagation();
                    handleTabClose(tab.id);
                  }}
                  aria-label={`Close ${tab.label} tab`}
                  data-testid={`close-tab-${tab.id}`}
                >
                  <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
                    <path
                      d="M4 4l8 8M12 4l-8 8"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                    />
                  </svg>
                </button>
              )}
            </div>
          ))}
        </div>
      </div>

      {/* Terminal tab content — split view: details on top, terminal on bottom */}
      {renderedActiveTabId.startsWith("terminal-") &&
        (() => {
          const activeTab = tabs.find((t) => t.id === renderedActiveTabId);
          if (!activeTab?.metadata?.sessionName || !activeTab.metadata.backend)
            return null;
          return (
            <div
              ref={splitContainerRef}
              className={styles.splitContainer}
              role="tabpanel"
              id={`issue-panel-tabpanel-${renderedActiveTabId}`}
              aria-labelledby={`issue-panel-tab-${renderedActiveTabId}`}
            >
              <div
                className={styles.splitTop}
                style={{ flex: `0 0 ${ratio * 100}%` }}
              >
                <SplitDetailSummary
                  issue={issue}
                  isSavingPriority={isSavingPriority}
                  isSavingType={isSavingType}
                  isSavingAssignee={isSavingAssignee}
                  agents={agents}
                  agentTasks={agentTasks}
                  onPrioritySave={handlePrioritySave}
                  onTypeSave={handleTypeSave}
                  onAssigneeSave={handleAssigneeSave}
                  onIssueUpdate={onIssueUpdate}
                />
              </div>
              <ResizeDivider
                onDragDelta={(deltaY) =>
                  applyDelta(
                    splitContainerRef.current?.clientHeight ?? 600,
                    deltaY,
                  )
                }
                onDoubleClick={resetRatio}
                ratio={ratio}
              />
              <div className={styles.splitBottom}>
                <EmbeddedTerminal
                  sessionName={activeTab.metadata.sessionName}
                  backend={activeTab.metadata.backend}
                  agentName={activeTab.metadata.agentName ?? null}
                  worktreePath={activeTab.metadata.worktreePath}
                  isActive={true}
                  onConnectionStateChange={(state) =>
                    handleConnectionStateChange(activeTab.id, state)
                  }
                  onMaximize={toggleMaximize}
                  isMaximized={isMaximized}
                />
              </div>
            </div>
          );
        })()}

      {renderedActiveTabId === "details" && (
        <div
          className={styles.scrollableContent}
          role="tabpanel"
          id="issue-panel-tabpanel-details"
          aria-labelledby="issue-panel-tab-details"
        >
          <div className={styles.detailContent}>
            {/* Two-column layout: left=metadata+description, right=design */}
            <div className={issue.design ? styles.detailColumns : undefined}>
              <div
                className={
                  issue.design
                    ? styles.detailColumnLeft
                    : styles.detailColumnFull
                }
              >
                {/* Priority/Type dropdowns for editing */}
                <div className={styles.statusRow}>
                  <PriorityDropdown
                    priority={issue.priority as Priority}
                    onSave={handlePrioritySave}
                    isSaving={isSavingPriority}
                  />
                  <TypeDropdown
                    type={issue.issue_type}
                    onSave={handleTypeSave}
                    isSaving={isSavingType}
                  />
                  <AssigneeDropdown
                    assignee={issue.assignee}
                    onSave={handleAssigneeSave}
                    isSaving={isSavingAssignee}
                    agents={agents}
                    agentTasks={agentTasks}
                  />
                  {(repos.length > 0 || currentRepo !== null) && (
                    <RepoDropdown
                      currentRepo={currentRepo}
                      repos={repos.map((r) => r.name)}
                      onSave={handleRepoSave}
                      isSaving={isSavingRepo}
                    />
                  )}
                  {issue.assignee && !issue.assignee.startsWith("[H]") && (
                    <AgentStatusBadge
                      agentName={issue.assignee}
                      onOpenTerminal={() => setActiveTabId("sessions")}
                    />
                  )}
                  <StartWorkButton
                    issueId={issue.id}
                    issueStatus={issue.status}
                    currentAssignee={issue.assignee}
                    agents={agents}
                    agentTasks={agentTasks}
                    isConnected={isLoomConnected}
                    onAssign={handleStartWork}
                  />
                </div>

                {/* Description */}
                <section className={styles.section}>
                  <h3 className={styles.sectionTitle}>Description</h3>
                  <EditableDescription
                    description={issue.description}
                    isEditable={true}
                    onSave={async (newDescription) => {
                      const updatedIssue = await updateIssue(
                        workspaceId,
                        issue.id,
                        {
                          description: newDescription,
                        },
                      );
                      onIssueUpdate?.(updatedIssue);
                    }}
                  />
                </section>
              </div>

              {/* Design in right column */}
              {issue.design && (
                <div
                  className={styles.detailColumnRight}
                  data-testid="design-section"
                >
                  <DesignPanel content={issue.design} />
                </div>
              )}
            </div>

            {/* Full-width sections below the columns */}

            {/* Notes (collapsible) */}
            {issue.notes && (
              <CollapsibleSection
                title="Notes"
                defaultExpanded={!shouldCollapseNotes}
                testId="notes-section"
              >
                <MarkdownRenderer content={issue.notes} />
              </CollapsibleSection>
            )}

            {/* Dependencies (blocking this issue) - editable */}
            {hasDetails && (
              <DependencySection
                issueId={issue.id}
                dependencies={dependencies ?? []}
                onAddDependency={handleAddDependency}
                onRemoveDependency={handleRemoveDependency}
                {...(onNavigateToIssue !== undefined && { onNavigateToIssue })}
                disabled={isLoading}
              />
            )}

            {/* Dependents (this issue blocks) */}
            {dependents && dependents.length > 0 && (
              <section className={styles.section}>
                <h3 className={styles.sectionTitle}>
                  Blocks ({dependents.length})
                </h3>
                <ul className={styles.dependencyList}>
                  {dependents.map((dep) =>
                    renderDependencyChip(dep, onNavigateToIssue),
                  )}
                </ul>
              </section>
            )}

            {/* Session History */}
            <CollapsibleSection
              title="Session History"
              defaultExpanded={false}
              testId="session-history-section"
            >
              <SessionHistorySection
                issueId={issue.id}
                onJumpToSession={(sessionName) => {
                  const tabId = `terminal-${sessionName}`;
                  const tab = tabs.find((t) => t.id === tabId);
                  if (tab) {
                    setActiveTabId(tabId);
                  }
                }}
              />
            </CollapsibleSection>

            {/* Activity Log (comments + events) */}
            <ActivityLog
              comments={localComments ?? []}
              events={events}
              issueId={issue.id}
            />
            <CommentForm
              issueId={issue.id}
              onCommentAdded={handleCommentAdded}
            />

            {/* Labels */}
            <LabelEditor
              labels={issue.labels ?? []}
              onAddLabel={handleAddLabel}
              onRemoveLabel={handleRemoveLabel}
              disabled={isLoading}
            />
          </div>
        </div>
      )}

      {renderedActiveTabId === "task-log-planning" && (
        <TaskPhaseLogPanel issueId={issue.id} phase="planning" />
      )}

      {renderedActiveTabId === "task-log-implementation" && (
        <TaskPhaseLogPanel issueId={issue.id} phase="implementation" />
      )}

      {renderedActiveTabId === "sessions" && (
        <div
          className={styles.logsContainer}
          role="tabpanel"
          id="issue-panel-tabpanel-sessions"
          aria-labelledby="issue-panel-tab-sessions"
        >
          <SessionsTab taskId={issue.id} />
        </div>
      )}

      {/* Error toast for status change failures */}
      {statusError && (
        <ErrorToast
          message={statusError}
          onDismiss={() => setStatusError(null)}
          testId="status-error-toast"
        />
      )}

      {/* Error toast for title save failures */}
      {titleError && (
        <ErrorToast
          message={titleError}
          onDismiss={() => setTitleError(null)}
          testId="title-error-toast"
        />
      )}

      {/* Close confirmation dialog for terminal tabs with active sessions */}
      <ConfirmDialog
        isOpen={pendingCloseTabId !== null}
        title="Close terminal?"
        message="This terminal has an active session. Closing it will terminate the session after a brief grace period."
        confirmLabel="Close"
        variant="danger"
        onConfirm={() => {
          if (pendingCloseTabId) closeTab(pendingCloseTabId);
          setPendingCloseTabId(null);
        }}
        onCancel={() => setPendingCloseTabId(null)}
      />

      {/* Move issue dialog */}
      <MoveIssueDialog
        isOpen={showMoveDialog}
        issue={issue}
        workspaces={workspaces}
        currentWorkspace={currentWorkspace}
        dependencies={dependencies}
        error={moveError}
        onConfirm={handleMoveConfirm}
        onCancel={() => setShowMoveDialog(false)}
      />
    </>
  );
}

/**
 * IssueDetailPanel displays issue details in a slide-out panel from the right edge.
 * Features:
 * - Smooth slide-in/out animation with CSS transforms
 * - Backdrop overlay that dims the background
 * - Closes on backdrop click or Escape key
 * - Locks body scroll when open
 * - Accessible with proper ARIA attributes
 * - Default content rendering with loading/error states
 */
export function IssueDetailPanel({
  isOpen,
  issue,
  onClose,
  isLoading,
  error,
  className,
  children,
  onApprove,
  onReject,
  onIssueUpdate,
  onCopyLink,
  onNavigateToIssue,
}: IssueDetailPanelProps): JSX.Element {
  const panelRef = useRef<HTMLElement>(null);

  // Handle Escape key to close panel via global shortcut layer system
  useRegisterEscapeLayer(LAYER_ISSUE_PANEL, onClose, isOpen);

  // Lock body scroll when open, restoring previous value on close.
  // Note: Only ONE panel should be open at a time. Multiple concurrent panels
  // would require a scroll lock manager to handle overflow restoration properly.
  useEffect(() => {
    if (isOpen) {
      const previousOverflow = document.body.style.overflow;
      document.body.style.overflow = "hidden";
      return () => {
        document.body.style.overflow = previousOverflow;
      };
    }
  }, [isOpen]);

  // Focus management: focus the panel when opened, restore focus on close
  useFocusReturn(isOpen, { focusTarget: panelRef });
  useFocusTrap(panelRef, isOpen);

  // Build root class name
  const rootClassName = [styles.overlay, isOpen && styles.open, className]
    .filter(Boolean)
    .join(" ");

  // Determine content: children override default, otherwise render default content
  const content = children ?? (
    <DefaultContent
      issue={issue}
      isLoading={isLoading ?? false}
      error={error ?? null}
      onClose={onClose}
      {...(onApprove !== undefined && { onApprove })}
      {...(onReject !== undefined && { onReject })}
      {...(onIssueUpdate !== undefined && { onIssueUpdate })}
      {...(onCopyLink !== undefined && { onCopyLink })}
      {...(onNavigateToIssue !== undefined && { onNavigateToIssue })}
    />
  );

  return (
    <div
      className={rootClassName}
      onClick={onClose}
      data-testid="issue-detail-overlay"
      aria-hidden={!isOpen}
    >
      <aside
        ref={panelRef}
        className={styles.panel}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={issue ? `Details for ${issue.title}` : "Issue details"}
        tabIndex={-1}
        data-testid="issue-detail-panel"
        data-state={isOpen ? "open" : "closed"}
        data-loading={isLoading ? "true" : "false"}
        data-error={error ? "true" : "false"}
      >
        <div className={styles.content}>{content}</div>
      </aside>
    </div>
  );
}
