/**
 * MoveIssueDialog component.
 * Modal dialog for moving an issue to a different workspace.
 * Shows workspace selector, warnings about dependencies and agents,
 * and confirm/cancel actions.
 */

import { useState, useEffect, useRef, useCallback, useMemo } from "react";
import { createPortal } from "react-dom";

import { useRegisterEscapeLayer, LAYER_CONFIRM_DIALOG } from "@/hooks";
import type { Issue, IssueDetails, IssueWithDependencyMetadata } from "@/types";
import type { WorkspaceSummary } from "@/api/workspace";

import styles from "./MoveIssueDialog.module.css";

export interface MoveIssueDialogProps {
  isOpen: boolean;
  issue: Issue | IssueDetails;
  workspaces: WorkspaceSummary[];
  currentWorkspace: string;
  dependencies?: IssueWithDependencyMetadata[] | undefined;
  error?: string | null | undefined;
  onConfirm: (targetWorkspace: string) => Promise<void>;
  onCancel: () => void;
}

export function MoveIssueDialog({
  isOpen,
  issue,
  workspaces,
  currentWorkspace,
  dependencies,
  error,
  onConfirm,
  onCancel,
}: MoveIssueDialogProps): JSX.Element | null {
  const [selectedWorkspace, setSelectedWorkspace] = useState("");
  const [isMoving, setIsMoving] = useState(false);
  const cancelRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);

  // Filter out current workspace (memoized for stable reference)
  const availableWorkspaces = useMemo(
    () => workspaces.filter((ws) => ws.name !== currentWorkspace),
    [workspaces, currentWorkspace],
  );

  // Auto-select first workspace if only one option
  useEffect(() => {
    if (isOpen) {
      const first = availableWorkspaces[0];
      if (availableWorkspaces.length === 1 && first) {
        setSelectedWorkspace(first.name);
      } else {
        setSelectedWorkspace("");
      }
    }
  }, [isOpen, availableWorkspaces]);

  // Focus cancel button on open
  useEffect(() => {
    if (isOpen && cancelRef.current) {
      cancelRef.current.focus();
    }
  }, [isOpen]);

  // Escape to cancel
  useRegisterEscapeLayer(LAYER_CONFIRM_DIALOG, onCancel, isOpen);

  // Focus trap
  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key !== "Tab" || !dialogRef.current) return;

    const focusable = dialogRef.current.querySelectorAll<HTMLElement>(
      'button:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
    );
    if (focusable.length === 0) return;

    const first = focusable[0] as HTMLElement | undefined;
    const last = focusable[focusable.length - 1] as HTMLElement | undefined;
    if (!first || !last) return;

    if (e.shiftKey) {
      if (document.activeElement === first) {
        e.preventDefault();
        last.focus();
      }
    } else {
      if (document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  }, []);

  const handleConfirm = async () => {
    if (!selectedWorkspace || isMoving) return;
    setIsMoving(true);
    try {
      await onConfirm(selectedWorkspace);
    } finally {
      setIsMoving(false);
    }
  };

  if (!isOpen) return null;

  // Compute warnings
  const hasAssignee = !!issue.assignee;
  const openDeps = dependencies?.filter((d) => d.status !== "closed") ?? [];

  return createPortal(
    <div
      className={styles.overlay}
      onClick={onCancel}
      data-testid="move-dialog-overlay"
    >
      <div
        ref={dialogRef}
        className={styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-labelledby="move-dialog-title"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleKeyDown}
      >
        <h2 id="move-dialog-title" className={styles.title}>
          Move to workspace
        </h2>
        <p className={styles.message}>
          Move <strong>{issue.id}</strong> to a different workspace. The issue
          will be copied to the target workspace and closed in the current one.
        </p>

        {/* Workspace selector */}
        <div className={styles.selectGroup}>
          <label htmlFor="move-workspace-select" className={styles.label}>
            Target workspace
          </label>
          <select
            id="move-workspace-select"
            className={styles.select}
            value={selectedWorkspace}
            onChange={(e) => setSelectedWorkspace(e.target.value)}
            disabled={isMoving}
            data-testid="move-workspace-select"
          >
            <option value="">Select a workspace...</option>
            {availableWorkspaces.map((ws) => (
              <option key={ws.name} value={ws.name}>
                {ws.name}
              </option>
            ))}
          </select>
        </div>

        {/* Warnings */}
        {(hasAssignee || openDeps.length > 0) && (
          <div className={styles.warnings} data-testid="move-warnings">
            {hasAssignee && (
              <div className={styles.warning}>
                <svg
                  width="14"
                  height="14"
                  viewBox="0 0 16 16"
                  fill="none"
                  aria-hidden="true"
                >
                  <path
                    d="M8 1.5l6.5 13H1.5L8 1.5z"
                    stroke="currentColor"
                    strokeWidth="1.2"
                    fill="none"
                  />
                  <path
                    d="M8 6v3"
                    stroke="currentColor"
                    strokeWidth="1.2"
                    strokeLinecap="round"
                  />
                  <circle cx="8" cy="11.5" r="0.75" fill="currentColor" />
                </svg>
                This issue has an active agent ({issue.assignee}). Moving it
                will not stop the agent.
              </div>
            )}
            {openDeps.length > 0 && (
              <div className={styles.warning}>
                <svg
                  width="14"
                  height="14"
                  viewBox="0 0 16 16"
                  fill="none"
                  aria-hidden="true"
                >
                  <path
                    d="M8 1.5l6.5 13H1.5L8 1.5z"
                    stroke="currentColor"
                    strokeWidth="1.2"
                    fill="none"
                  />
                  <path
                    d="M8 6v3"
                    stroke="currentColor"
                    strokeWidth="1.2"
                    strokeLinecap="round"
                  />
                  <circle cx="8" cy="11.5" r="0.75" fill="currentColor" />
                </svg>
                {openDeps.length}{" "}
                {openDeps.length === 1 ? "dependency" : "dependencies"} will be
                broken by this move.
                <ul className={styles.depList}>
                  {openDeps.slice(0, 5).map((dep) => (
                    <li key={dep.id}>
                      {dep.id}: {dep.title}
                    </li>
                  ))}
                  {openDeps.length > 5 && (
                    <li>...and {openDeps.length - 5} more</li>
                  )}
                </ul>
              </div>
            )}
          </div>
        )}

        {/* Error message */}
        {error && (
          <p className={styles.error} data-testid="move-dialog-error">
            {error}
          </p>
        )}

        {/* Actions */}
        <div className={styles.actions}>
          <button
            ref={cancelRef}
            type="button"
            className={styles.cancelButton}
            onClick={onCancel}
            disabled={isMoving}
            data-testid="move-dialog-cancel"
          >
            Cancel
          </button>
          <button
            type="button"
            className={styles.confirmButton}
            onClick={handleConfirm}
            disabled={!selectedWorkspace || isMoving}
            data-testid="move-dialog-confirm"
          >
            {isMoving ? "Moving..." : "Move"}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
