/**
 * EpicTaskTree renders a hierarchical epic/task tree for a workspace.
 * Root component: TalkToLeadEntry, EpicRows with nested TaskRows, and orphan tasks.
 */

import { useState, useCallback } from "react";

import { useWorkspaceTree } from "@/hooks/useWorkspaceTree";

import { TalkToLeadEntry } from "./TalkToLeadEntry";
import { EpicRow } from "./EpicRow";
import { TaskRow } from "./TaskRow";
import styles from "./EpicTaskTree.module.css";

const COLLAPSE_STORAGE_PREFIX = "workspace-tree-epic-collapsed";

export interface EpicTaskTreeProps {
  workspaceName: string;
  activeFilter: "active" | "all";
  selectedId?: string | undefined;
  onSelect?: ((issueId: string) => void) | undefined;
  sourceRepos?: string[] | undefined;
  /** Backend name for TalkToLeadEntry display. */
  backend?: string | undefined;
  /** Callback when Talk to Lead is clicked. */
  onTalkToLead?: ((workspaceName: string) => void) | undefined;
}

/** Build storage key namespaced by workspace. */
function collapseKey(ws: string): string {
  return `${COLLAPSE_STORAGE_PREFIX}:${ws}`;
}

/** Load collapse state from localStorage. */
function loadCollapseState(ws: string): Record<string, boolean> {
  try {
    const stored = localStorage.getItem(collapseKey(ws));
    return stored ? JSON.parse(stored) : {};
  } catch {
    return {};
  }
}

/** Save collapse state to localStorage. */
function saveCollapseState(ws: string, state: Record<string, boolean>): void {
  try {
    localStorage.setItem(collapseKey(ws), JSON.stringify(state));
  } catch {
    // Ignore — private browsing or storage full
  }
}

export function EpicTaskTree({
  workspaceName,
  activeFilter,
  selectedId,
  onSelect,
  sourceRepos,
  backend,
  onTalkToLead,
}: EpicTaskTreeProps): JSX.Element {
  const { epics, orphanTasks, isLoading } = useWorkspaceTree(
    workspaceName,
    activeFilter,
    sourceRepos,
  );

  const [collapseState, setCollapseState] = useState<Record<string, boolean>>(
    () => loadCollapseState(workspaceName),
  );

  const handleToggle = useCallback(
    (epicId: string) => {
      setCollapseState((prev) => {
        const next = { ...prev, [epicId]: !prev[epicId] };
        saveCollapseState(workspaceName, next);
        return next;
      });
    },
    [workspaceName],
  );

  // Show orphan tasks in an "Ungrouped" collapsible section
  const [orphansCollapsed, setOrphansCollapsed] = useState(false);

  const hasContent = epics.length > 0 || orphanTasks.length > 0;

  if (isLoading) {
    return (
      <div className={styles.treeSection}>
        <TalkToLeadEntry
          workspaceName={workspaceName}
          backend={backend}
          onTalkToLead={onTalkToLead}
        />
        <div className={styles.loadingSkeleton}>
          <div className={styles.skeletonRow} />
          <div className={styles.skeletonRow} />
          <div className={styles.skeletonRow} />
        </div>
      </div>
    );
  }

  return (
    <div className={styles.treeSection}>
      <TalkToLeadEntry
        workspaceName={workspaceName}
        backend={backend}
        onTalkToLead={onTalkToLead}
      />

      {!hasContent && (
        <div className={styles.emptyTree}>
          {activeFilter === "active" ? "No active tasks" : "No epics or tasks"}
        </div>
      )}

      {epics.map(({ epic, tasks }) => (
        <EpicRow
          key={epic.id}
          epic={epic}
          tasks={tasks}
          isCollapsed={!!collapseState[epic.id]}
          onToggle={() => handleToggle(epic.id)}
          selectedId={selectedId}
          onSelect={onSelect}
        />
      ))}

      {orphanTasks.length > 0 && (
        <div className={styles.epicGroup}>
          <button
            type="button"
            className={styles.epicRow}
            onClick={() => setOrphansCollapsed((p) => !p)}
            title="Ungrouped tasks"
          >
            <span className={styles.epicIcon}>
              <svg
                width="16"
                height="16"
                viewBox="0 0 16 16"
                fill="none"
                xmlns="http://www.w3.org/2000/svg"
              >
                <rect
                  x="2"
                  y="3"
                  width="12"
                  height="10"
                  rx="1.5"
                  stroke="currentColor"
                  strokeWidth="1.3"
                  strokeDasharray="2 1.5"
                />
              </svg>
            </span>
            <span className={styles.titleText}>Ungrouped</span>
            <span
              className={styles.collapseChevron}
              data-expanded={!orphansCollapsed}
              role="img"
              aria-label={
                orphansCollapsed ? "Expand ungrouped" : "Collapse ungrouped"
              }
            >
              &rsaquo;
            </span>
          </button>
          {!orphansCollapsed && (
            <div className={styles.epicChildren}>
              {orphanTasks.map((task) => (
                <TaskRow
                  key={task.id}
                  task={task}
                  isSelected={selectedId === task.id}
                  onSelect={onSelect}
                />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
