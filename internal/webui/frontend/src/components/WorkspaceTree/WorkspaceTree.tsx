/**
 * WorkspaceTree component displays a collapsible sidebar with repo navigation.
 * Shows all repos in the workspace with per-repo agent counts and status.
 * Includes workspace entries with context menu rename support.
 */

import { useState, useCallback, useEffect, useMemo, useRef } from "react";

import {
  DndContext,
  closestCenter,
  PointerSensor,
  KeyboardSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  verticalListSortingStrategy,
  arrayMove,
} from "@dnd-kit/sortable";

import {
  renameWorkspace,
  deleteWorkspace,
  reorderWorkspaces,
} from "@/hooks/api";
import type { WorkspaceSummary } from "@/api/workspace";
import type { ConnectionState } from "@/api/sse";
import type { LoomAgentStatus } from "@/types";
import { useStore } from "zustand";

import {
  useWorkspaceRepos,
  useAgentStoreInstance,
  useToast,
  useWorkspaceContext,
  useDebouncedCallback,
} from "@/hooks";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import {
  computeRepoHealth,
  worstHealthColor,
  type WorkspaceHealthSummary,
} from "@/utils/workspaceHealth";
import { AgentCard } from "@/components/AgentCard";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ErrorDisplay } from "@/components/ErrorDisplay";

import { WorkspaceContextMenu } from "./menus";
import {
  ActiveAllToggle,
  type ActiveFilter,
  RepoGroupList,
  SidebarStatusBar,
  SortableWorkspaceEntry,
  WorkQueueSection,
  type WorkQueueCounts,
} from "./nav";
import { EpicTaskTree } from "./tree";
import styles from "./WorkspaceTree.module.css";

/**
 * Props for the WorkspaceTree component.
 */
export interface WorkspaceTreeProps {
  /** Additional CSS class name */
  className?: string;
  /** Default collapsed state */
  defaultCollapsed?: boolean;
  /** Currently active repo name, or null/undefined for "All Workspaces" */
  activeRepoName?: string | null | undefined;
  /** Callback when a workspace/repo is selected. null = "All Workspaces" */
  onWorkspaceSelect?: (repoName: string | null) => void;
  /** Callback when a workspace entry is clicked to switch to it */
  onWorkspaceSwitch?: (workspaceName: string) => void;
  /** Callback when an agent card is clicked */
  onAgentClick?: (agentName: string) => void;
  /** Map of agent name to task info for display in AgentCards */
  agentTasks?: Record<string, { title: string }>;
  /** Callback when the "+" button is clicked */
  onAddClick?: () => void;
  /** SSE connection state */
  connectionState?: ConnectionState;
  /** True when reconnection failed after max attempts */
  connectionLost?: boolean;
  /** Timestamp (ms) when disconnection started, null if connected */
  disconnectedSince?: number | null;
  /** Callback for retry button in daemon prompt */
  onRetryConnection?: () => void;
  /** Callback when the active/all filter changes (for downstream consumers) */
  onFilterChange?: (filter: ActiveFilter) => void;
  /** Work Queue counts derived from workspace-scoped issues */
  workQueueCounts?: WorkQueueCounts;
  /** Callback when Talk to Lead is clicked in the tree */
  onTalkToLead?: (workspaceName: string) => void;
  /** Callback when a task is selected in the tree */
  onTreeSelect?: (issueId: string) => void;
  /** Callback when a task with an active agent is clicked for terminal */
  onTaskTerminalOpen?: (issueId: string, agentName: string) => void;
}

// Scoped key suffixes for workspace-specific tree state
const SK_COLLAPSED = "tree-collapsed";
const SK_ACTIVE_FILTER = "tree-active-filter";
const SK_REPO_COLLAPSED = "tree-repo-collapsed";

export function WorkspaceTree({
  className,
  defaultCollapsed = false,
  activeRepoName,
  onWorkspaceSelect,
  onWorkspaceSwitch,
  onAgentClick,
  agentTasks,
  onAddClick,
  connectionState,
  connectionLost,
  disconnectedSince,
  onRetryConnection,
  onFilterChange,
  workQueueCounts,
  onTalkToLead,
  onTreeSelect,
  onTaskTerminalOpen,
}: WorkspaceTreeProps): JSX.Element {
  const {
    workspaceId,
    activeWorkspaceName,
    defaultWorkspaceName,
    setDefaultWorkspace,
    agents: workspaceConfigAgents,
    refetch: contextRefetch,
  } = useWorkspaceContext();

  // Load initial collapsed state from scoped localStorage
  const [isCollapsed, setIsCollapsed] = useState(() => {
    if (!workspaceId) return defaultCollapsed;
    const stored = wsGet(workspaceId, SK_COLLAPSED);
    return stored !== null ? stored === "true" : defaultCollapsed;
  });

  // Active/All filter state persisted to scoped localStorage
  const [activeFilter, setActiveFilter] = useState<ActiveFilter>(() => {
    if (!workspaceId) return "active";
    const stored = wsGet(workspaceId, SK_ACTIVE_FILTER);
    return stored === "all" ? "all" : "active";
  });

  const handleFilterChange = useCallback(
    (filter: ActiveFilter) => {
      setActiveFilter(filter);
      onFilterChange?.(filter);
    },
    [onFilterChange],
  );

  const {
    workspace,
    repos,
    isLoading,
    error,
    refetch,
    connectionState: wsConnectionState,
    retryCountdown,
    retryNow,
  } = useWorkspaceRepos();
  const agentStore = useAgentStoreInstance();
  const fleetAgents = useStore(agentStore, (s) => s.agents);
  const contextAgentTasks = useStore(agentStore, (s) => s.agentTasks);
  const { showToast } = useToast();

  // Re-read scoped state when workspace changes (SPA navigation)
  useEffect(() => {
    if (!workspaceId) return;
    const storedCollapsed = wsGet(workspaceId, SK_COLLAPSED);
    setIsCollapsed(
      storedCollapsed !== null ? storedCollapsed === "true" : defaultCollapsed,
    );
    const storedFilter = wsGet(workspaceId, SK_ACTIVE_FILTER);
    setActiveFilter(storedFilter === "all" ? "all" : "active");
  }, [workspaceId, defaultCollapsed]);

  // Persist activeFilter state to scoped storage
  useEffect(() => {
    if (workspaceId) wsSet(workspaceId, SK_ACTIVE_FILTER, activeFilter);
  }, [activeFilter, workspaceId]);

  // Merge fleet agents with workspace config agents.
  // Config agents that aren't yet running appear as "configured" placeholders.
  const agents = useMemo(() => {
    if (workspaceConfigAgents.length === 0) return fleetAgents;
    const fleetNames = new Set(fleetAgents.map((a) => a.name));
    const configPlaceholders: typeof fleetAgents = workspaceConfigAgents
      .filter((ca) => !fleetNames.has(ca.name))
      .map((ca) => {
        const entry: (typeof fleetAgents)[number] = {
          name: ca.name,
          branch: "",
          status: "configured",
          ahead: 0,
          behind: 0,
          workspace: workspace?.name ?? "",
          cross_repo: ca.cross_repo,
        };
        if (ca.repos?.[0]) entry.repo = ca.repos[0];
        return entry;
      });
    return [...fleetAgents, ...configPlaceholders];
  }, [fleetAgents, workspaceConfigAgents, workspace?.name]);

  // Workspace delete state
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
  const [pendingDeleteName, setPendingDeleteName] = useState<string | null>(
    null,
  );
  const deleteTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const deletionPendingRef = useRef(false);

  // Workspace rename state
  const [editingWorkspace, setEditingWorkspace] = useState<string | null>(null);
  const [draftName, setDraftName] = useState("");
  const [renameError, setRenameError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  const renameInputRef = useRef<HTMLInputElement>(null);

  // Context menu state
  const [contextMenu, setContextMenu] = useState<{
    workspaceName: string;
    x: number;
    y: number;
  } | null>(null);

  // Workspace ordering state for drag-and-drop reorder
  const [workspaceOrder, setWorkspaceOrder] = useState<string[]>([]);

  // Initialize/sync order from API data
  useEffect(() => {
    if (workspace?.workspaces) {
      setWorkspaceOrder(workspace.workspaces.map((ws) => ws.name));
    }
  }, [workspace?.workspaces]);

  // DnD sensors with 5px activation distance to prevent accidental drags on click
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor),
  );

  // Re-fetch order from server on error (triggers context refetch which updates workspace data)
  const rollbackOrder = useCallback(() => {
    contextRefetch();
  }, [contextRefetch]);

  // Debounced server persist — coalesces rapid reorder operations into a single API call
  const debouncedPersistOrder = useDebouncedCallback((order: string[]) => {
    reorderWorkspaces(order).catch(rollbackOrder);
  }, 300);

  // Drag-end handler: reorder and persist
  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event;
      if (!over || active.id === over.id) return;

      let pendingOrder: string[] | null = null;
      setWorkspaceOrder((prev) => {
        const oldIndex = prev.indexOf(active.id as string);
        const newIndex = prev.indexOf(over.id as string);
        if (oldIndex < 0 || newIndex < 0) return prev;
        pendingOrder = arrayMove(prev, oldIndex, newIndex);
        return pendingOrder;
      });
      if (pendingOrder) debouncedPersistOrder(pendingOrder);
    },
    [debouncedPersistOrder],
  );

  // Alt+Up keyboard reorder
  const handleMoveUp = useCallback(
    (name: string) => {
      let pendingOrder: string[] | null = null;
      setWorkspaceOrder((prev) => {
        const idx = prev.indexOf(name);
        if (idx <= 0) return prev;
        pendingOrder = arrayMove(prev, idx, idx - 1);
        return pendingOrder;
      });
      if (pendingOrder) debouncedPersistOrder(pendingOrder);
    },
    [debouncedPersistOrder],
  );

  // Alt+Down keyboard reorder
  const handleMoveDown = useCallback(
    (name: string) => {
      let pendingOrder: string[] | null = null;
      setWorkspaceOrder((prev) => {
        const idx = prev.indexOf(name);
        if (idx < 0 || idx >= prev.length - 1) return prev;
        pendingOrder = arrayMove(prev, idx, idx + 1);
        return pendingOrder;
      });
      if (pendingOrder) debouncedPersistOrder(pendingOrder);
    },
    [debouncedPersistOrder],
  );

  // Persist collapsed state to scoped storage
  useEffect(() => {
    if (workspaceId) wsSet(workspaceId, SK_COLLAPSED, String(isCollapsed));
  }, [isCollapsed, workspaceId]);

  // Focus rename input when entering edit mode
  useEffect(() => {
    if (editingWorkspace && renameInputRef.current) {
      renameInputRef.current.focus();
      renameInputRef.current.select();
    }
  }, [editingWorkspace]);

  const handleToggle = useCallback(() => {
    setIsCollapsed((prev) => !prev);
  }, []);

  // Context menu handlers
  // Workspace summaries for resolving name→ID
  const wsSummaries = workspace?.workspaces;
  const wsIdByName = useCallback(
    (name: string) => wsSummaries?.find((w) => w.name === name)?.id ?? "",
    [wsSummaries],
  );

  const handleOverflowClick = useCallback(
    (e: React.MouseEvent, wsName: string) => {
      e.stopPropagation();
      setContextMenu({ workspaceName: wsName, x: e.clientX, y: e.clientY });
    },
    [],
  );

  const handleContextMenu = useCallback(
    (e: React.MouseEvent, wsName: string) => {
      e.preventDefault();
      e.stopPropagation();
      setContextMenu({ workspaceName: wsName, x: e.clientX, y: e.clientY });
    },
    [],
  );

  const handleCloseContextMenu = useCallback(() => {
    setContextMenu(null);
  }, []);

  // Rename handlers
  const handleStartRename = useCallback(() => {
    if (!contextMenu) return;
    setEditingWorkspace(contextMenu.workspaceName);
    setDraftName(contextMenu.workspaceName);
    setRenameError(null);
  }, [contextMenu]);

  const handleCancelRename = useCallback(() => {
    setEditingWorkspace(null);
    setDraftName("");
    setRenameError(null);
  }, []);

  const handleSaveRename = useCallback(async () => {
    if (!editingWorkspace) return;

    const trimmed = draftName.trim();

    // Client-side pre-validation: empty name
    if (!trimmed) {
      setRenameError("Name cannot be empty");
      setTimeout(() => renameInputRef.current?.focus(), 0);
      return;
    }

    // No-op if name unchanged
    if (trimmed === editingWorkspace) {
      setEditingWorkspace(null);
      return;
    }

    setIsSaving(true);
    setRenameError(null);

    try {
      await renameWorkspace(wsIdByName(editingWorkspace), trimmed);
      setEditingWorkspace(null);
      refetch();
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to rename workspace";
      setRenameError(message);
      setTimeout(() => renameInputRef.current?.focus(), 0);
    } finally {
      setIsSaving(false);
    }
  }, [editingWorkspace, draftName, refetch, wsIdByName]);

  const handleRenameKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === "Enter") {
        e.preventDefault();
        handleSaveRename();
      } else if (e.key === "Escape") {
        e.preventDefault();
        handleCancelRename();
      }
    },
    [handleSaveRename, handleCancelRename],
  );

  // Delete handlers
  const handleStartRemove = useCallback(() => {
    if (!contextMenu) return;
    setPendingDeleteName(contextMenu.workspaceName);
    setConfirmDeleteOpen(true);
  }, [contextMenu]);

  const handleCancelDelete = useCallback(() => {
    setConfirmDeleteOpen(false);
    setPendingDeleteName(null);
  }, []);

  const handleConfirmDelete = useCallback(() => {
    if (!pendingDeleteName) return;
    if (deletionPendingRef.current) return;
    const nameToDelete = pendingDeleteName;
    const idToDelete = wsIdByName(pendingDeleteName);
    setConfirmDeleteOpen(false);
    setPendingDeleteName(null);

    // Mark deletion as pending (undo window open)
    deletionPendingRef.current = true;

    // Start delayed deletion
    deleteTimerRef.current = setTimeout(async () => {
      deleteTimerRef.current = null;
      try {
        await deleteWorkspace(idToDelete);
        refetch();
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to remove workspace";
        showToast(message, { type: "error" });
        refetch();
      } finally {
        deletionPendingRef.current = false;
      }
    }, 5000);

    showToast(`Workspace "${nameToDelete}" removed`, {
      type: "success",
      duration: 5000,
      onUndo: () => {
        if (deleteTimerRef.current) {
          // Timer hasn't fired yet — cancel it
          clearTimeout(deleteTimerRef.current);
          deleteTimerRef.current = null;
          deletionPendingRef.current = false;
          showToast(`Workspace "${nameToDelete}" restored`, { type: "info" });
          refetch();
          return;
        }
        // Timer already fired — deletion in progress or done
        showToast("Deletion already in progress", { type: "info" });
      },
    });
  }, [pendingDeleteName, refetch, showToast, wsIdByName]);

  // Cleanup delete timer on unmount
  useEffect(() => {
    return () => {
      if (deleteTimerRef.current) {
        clearTimeout(deleteTimerRef.current);
      }
    };
  }, []);

  // Default workspace handlers
  const handleSetDefault = useCallback(() => {
    if (contextMenu) {
      setDefaultWorkspace(contextMenu.workspaceName).catch((err: unknown) => {
        const message =
          err instanceof Error ? err.message : "Failed to set default";
        showToast(message, { type: "error" });
      });
    }
  }, [contextMenu, setDefaultWorkspace, showToast]);

  const handleClearDefault = useCallback(() => {
    setDefaultWorkspace(null).catch((err: unknown) => {
      const message =
        err instanceof Error ? err.message : "Failed to clear default";
      showToast(message, { type: "error" });
    });
  }, [setDefaultWorkspace, showToast]);

  // Workspace summaries from the data
  const workspaces: WorkspaceSummary[] = workspace?.workspaces ?? [];

  // Per-repo collapse state (persisted to scoped localStorage)
  const [repoCollapseState, setRepoCollapseState] = useState<
    Record<string, boolean>
  >(() => {
    if (!workspaceId) return {};
    const stored = wsGet(workspaceId, SK_REPO_COLLAPSED);
    if (!stored) return {};
    try {
      return JSON.parse(stored);
    } catch {
      return {};
    }
  });

  // Re-read repo collapse state when workspace changes (SPA navigation)
  useEffect(() => {
    if (!workspaceId) return;
    const stored = wsGet(workspaceId, SK_REPO_COLLAPSED);
    if (!stored) {
      setRepoCollapseState({});
      return;
    }
    try {
      setRepoCollapseState(JSON.parse(stored));
    } catch {
      setRepoCollapseState({});
    }
  }, [workspaceId]);

  const handleRepoToggle = useCallback(
    (repoName: string) => {
      setRepoCollapseState((prev) => {
        const next = { ...prev, [repoName]: !prev[repoName] };
        if (workspaceId)
          wsSet(workspaceId, SK_REPO_COLLAPSED, JSON.stringify(next));
        return next;
      });
    },
    [workspaceId],
  );

  // Group agents by repo, collect unassigned
  const { repoAgents, unassignedAgents } = useMemo(() => {
    const grouped = new Map<string, LoomAgentStatus[]>();
    for (const repo of repos) {
      grouped.set(repo.name, []);
    }
    const unassigned: LoomAgentStatus[] = [];
    for (const agent of agents) {
      let matched = false;
      if (agent.repo) {
        for (const repo of repos) {
          if (agent.repo === repo.name || agent.repo === repo.path) {
            grouped.get(repo.name)!.push(agent);
            matched = true;
            break;
          }
        }
      }
      if (!matched) {
        unassigned.push(agent);
      }
    }
    return { repoAgents: grouped, unassignedAgents: unassigned };
  }, [repos, agents]);

  // Compute health summary per repo
  const repoHealthMap = useMemo(() => {
    const map = new Map<string, WorkspaceHealthSummary>();
    for (const repo of repos) {
      const agentList = repoAgents.get(repo.name) ?? [];
      map.set(repo.name, computeRepoHealth(agentList));
    }
    return map;
  }, [repos, repoAgents]);

  // Derive total active count and worst health color across all repos
  const { totalActiveCount, worstHealth } = useMemo(() => {
    const colors: Array<"green" | "yellow" | "red"> = [];
    for (const [, health] of repoHealthMap) {
      colors.push(health.healthColor);
    }
    // Include unassigned agents in totals
    if (unassignedAgents.length > 0) {
      const unassignedHealth = computeRepoHealth(unassignedAgents);
      colors.push(unassignedHealth.healthColor);
    }
    const totalCount =
      Array.from(repoHealthMap.values()).reduce(
        (sum, h) => sum + h.totalAgents,
        0,
      ) + unassignedAgents.length;
    return {
      totalActiveCount: totalCount,
      worstHealth: worstHealthColor(colors),
    };
  }, [repoHealthMap, unassignedAgents]);

  const isDisconnected =
    connectionState !== undefined &&
    connectionState !== "connected" &&
    connectionState !== "connecting" &&
    disconnectedSince != null;

  const rootClassName = [
    styles.sidebar,
    isCollapsed && styles.collapsed,
    className,
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <aside className={rootClassName} data-collapsed={isCollapsed}>
      <button
        type="button"
        className={styles.toggleButton}
        onClick={handleToggle}
        aria-expanded={!isCollapsed}
        aria-label={
          isCollapsed ? "Expand workspace tree" : "Collapse workspace tree"
        }
      >
        {!isCollapsed && (
          <>
            <span className={styles.toggleText}>
              Workspaces
              {defaultWorkspaceName && (
                <span
                  className={styles.defaultStar}
                  title={`Default: ${defaultWorkspaceName}`}
                >
                  &#9733;
                </span>
              )}
            </span>
            <span className={styles.sectionCount}>{workspaces.length}</span>
            {onAddClick && (
              <span
                role="button"
                tabIndex={0}
                className={styles.addButton}
                onClick={(e) => {
                  e.stopPropagation();
                  onAddClick();
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    e.stopPropagation();
                    onAddClick();
                  }
                }}
                aria-label="Add workspace"
                title="Add workspace"
              >
                + Add
              </span>
            )}
            <span
              className={styles.headerToggleWrapper}
              onClick={(e) => e.stopPropagation()}
              onKeyDown={(e) => e.stopPropagation()}
            >
              <ActiveAllToggle
                value={activeFilter}
                onChange={handleFilterChange}
              />
            </span>
          </>
        )}
        <span className={styles.toggleIcon}>{isCollapsed ? ">" : "<"}</span>
      </button>

      {!isCollapsed && (
        <div className={styles.content}>
          {isLoading && repos.length === 0 && (
            <div className={styles.loading}>
              <div className={styles.skeletonRow} />
              <div className={styles.skeletonRow} />
              <div className={styles.skeletonRow} />
            </div>
          )}

          {wsConnectionState === "error_never_connected" && (
            <ErrorDisplay
              variant="connection-error"
              title="Workspace unavailable"
              description="Could not connect to workspace. The server may be starting up."
              onRetry={retryNow}
              isRetrying={isLoading}
              retryLabel={
                retryCountdown != null
                  ? `Retry in ${retryCountdown}s`
                  : "Retry now"
              }
            />
          )}

          {wsConnectionState === "error_lost_connection" && (
            <div className={styles.staleBanner}>
              <span>Connection lost — showing last known state</span>
              <button
                type="button"
                onClick={retryNow}
                className={styles.retryButton}
              >
                {retryCountdown != null
                  ? `Retry in ${retryCountdown}s`
                  : "Retry now"}
              </button>
            </div>
          )}

          {!isLoading &&
            !error &&
            repos.length === 0 &&
            agents.length === 0 && (
              <div className={styles.emptyState}>No repos in workspace</div>
            )}

          {/* Agent list section — always visible when agents exist */}
          {agents.length > 0 && (
            <div className={styles.agentSection}>
              <div className={styles.agentSectionHeader}>
                Agents
                <span className={styles.sectionCount}>{agents.length}</span>
              </div>
              <div className={styles.agentSectionList}>
                {agents.map((agent) => (
                  <AgentCard
                    key={agent.name}
                    agent={agent}
                    taskTitle={
                      agentTasks?.[agent.name]?.title ??
                      contextAgentTasks?.[agent.name]?.title
                    }
                    {...(onAgentClick
                      ? { onClick: () => onAgentClick(agent.name) }
                      : {})}
                  />
                ))}
              </div>
            </div>
          )}

          {workspaces.length > 1 && (
            <div className={styles.workspaceSection}>
              <div className={styles.workspaceSectionHeader}>Workspaces</div>
              <DndContext
                sensors={sensors}
                collisionDetection={closestCenter}
                onDragEnd={handleDragEnd}
              >
                <SortableContext
                  items={workspaceOrder}
                  strategy={verticalListSortingStrategy}
                >
                  {workspaceOrder.map((name, idx) => {
                    const ws = workspaces.find((w) => w.name === name);
                    if (!ws) return null;
                    return (
                      <SortableWorkspaceEntry
                        key={ws.name}
                        ws={ws}
                        isActive={ws.name === activeWorkspaceName}
                        isDefault={ws.name === defaultWorkspaceName}
                        isEditing={editingWorkspace === ws.name}
                        draftName={draftName}
                        isSaving={isSaving}
                        renameError={renameError}
                        renameInputRef={renameInputRef}
                        {...(onWorkspaceSwitch
                          ? { onClick: onWorkspaceSwitch }
                          : {})}
                        onDraftChange={setDraftName}
                        onSaveRename={handleSaveRename}
                        onRenameKeyDown={handleRenameKeyDown}
                        onContextMenu={handleContextMenu}
                        onOverflowClick={handleOverflowClick}
                        onMoveUp={
                          idx > 0 ? () => handleMoveUp(name) : undefined
                        }
                        onMoveDown={
                          idx < workspaceOrder.length - 1
                            ? () => handleMoveDown(name)
                            : undefined
                        }
                      />
                    );
                  })}
                </SortableContext>
              </DndContext>
            </div>
          )}

          {repos.length > 0 && (
            <div
              className={`${styles.repoList}${wsConnectionState === "error_lost_connection" ? ` ${styles.staleOverlay}` : ""}`}
              role="radiogroup"
              aria-label="Workspace selection"
            >
              {/* All Workspaces option */}
              <button
                type="button"
                className={styles.repoItem}
                onClick={() => onWorkspaceSelect?.(null)}
                role="radio"
                aria-checked={
                  activeRepoName === null || activeRepoName === undefined
                }
              >
                <span
                  className={styles.radioIndicator}
                  data-active={
                    activeRepoName === null || activeRepoName === undefined
                  }
                />
                <svg
                  className={styles.allWorkspacesIcon}
                  viewBox="0 0 16 16"
                  width="14"
                  height="14"
                >
                  <rect
                    x="1"
                    y="4"
                    width="10"
                    height="8"
                    rx="1.5"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.3"
                  />
                  <rect
                    x="5"
                    y="1"
                    width="10"
                    height="8"
                    rx="1.5"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.3"
                  />
                </svg>
                <span className={styles.allWorkspacesLabel}>
                  All Workspaces
                </span>
                <span
                  className={styles.agentCount}
                  data-has-agents={totalActiveCount > 0 ? "true" : undefined}
                  data-health={totalActiveCount > 0 ? worstHealth : undefined}
                >
                  {totalActiveCount}
                </span>
              </button>

              <RepoGroupList
                repos={repos}
                repoAgents={repoAgents}
                unassignedAgents={unassignedAgents}
                activeRepoName={activeRepoName}
                repoCollapseState={repoCollapseState}
                onWorkspaceSelect={onWorkspaceSelect}
                onAgentClick={onAgentClick}
                onRepoToggle={handleRepoToggle}
                agentTasks={agentTasks}
                connectionState={connectionState}
                disconnectedSince={disconnectedSince}
                repoHealthMap={repoHealthMap}
              />
              {workspace?.name && (
                <EpicTaskTree
                  workspaceName={workspace.name}
                  activeFilter={activeFilter}
                  sourceRepos={repos.map((r) => r.name)}
                  onTalkToLead={onTalkToLead}
                  onSelect={onTreeSelect}
                  onTaskTerminalOpen={onTaskTerminalOpen}
                />
              )}
            </div>
          )}

          {/* Work Queue section — shows issue counts scoped to active workspace */}
          {workQueueCounts && <WorkQueueSection counts={workQueueCounts} />}

          {/* + New Workspace button at the bottom of the tree */}
          {onAddClick && (
            <button
              type="button"
              className={styles.newWorkspaceButton}
              onClick={onAddClick}
            >
              + New Workspace
            </button>
          )}
        </div>
      )}

      {!isCollapsed && <SidebarStatusBar agents={agents} />}

      {!isCollapsed && connectionLost && (
        <div className={styles.daemonPrompt} role="alert">
          <span className={styles.daemonPromptIcon}>&#9888;</span>
          <div className={styles.daemonPromptText}>
            <span className={styles.daemonPromptTitle}>Connection lost</span>
            <span className={styles.daemonPromptDesc}>
              Check that the daemon is running.
            </span>
          </div>
          {onRetryConnection && (
            <button
              type="button"
              className={styles.retryButton}
              onClick={onRetryConnection}
            >
              Retry
            </button>
          )}
        </div>
      )}

      {isCollapsed && (totalActiveCount > 0 || isDisconnected) && (
        <div
          className={styles.collapsedBadge}
          data-disconnected={isDisconnected}
          data-health={worstHealth}
          title={
            connectionLost
              ? "Connection lost"
              : connectionState === "reconnecting"
                ? "Reconnecting..."
                : isDisconnected
                  ? "Disconnected"
                  : `${totalActiveCount} agent(s)`
          }
        >
          {isDisconnected ? "!" : totalActiveCount}
        </div>
      )}

      {isCollapsed &&
        (wsConnectionState === "error_never_connected" ||
          wsConnectionState === "error_lost_connection") && (
          <div className={styles.errorBadge} title="Workspace connection error">
            !
          </div>
        )}

      <WorkspaceContextMenu
        isOpen={contextMenu !== null}
        position={contextMenu ?? { x: 0, y: 0 }}
        onRename={handleStartRename}
        onRemove={handleStartRemove}
        onClose={handleCloseContextMenu}
        isDefault={
          contextMenu != null &&
          contextMenu.workspaceName === defaultWorkspaceName
        }
        onSetDefault={handleSetDefault}
        onClearDefault={handleClearDefault}
      />

      <ConfirmDialog
        isOpen={confirmDeleteOpen}
        title="Remove workspace"
        message={`Are you sure you want to remove "${pendingDeleteName}"? Git worktrees will be kept on disk.`}
        confirmLabel="Remove"
        variant="danger"
        onConfirm={handleConfirmDelete}
        onCancel={handleCancelDelete}
      />
    </aside>
  );
}
