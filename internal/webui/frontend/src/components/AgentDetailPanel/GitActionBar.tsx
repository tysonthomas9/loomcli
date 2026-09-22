/**
 * GitActionBar - Action buttons for git operations in the Git tab.
 * Provides pull, pull-request, and reset actions with inline forms.
 */

import { useState, useCallback } from "react";

import type { GitStatus } from "@/api/workspace";
import type { ParsedLoomStatus } from "@/types";
import type { UseGitActionsReturn } from "@/hooks/workspace";

import styles from "./GitActionBar.module.css";

interface GitActionBarProps {
  agentName: string;
  gitStatus: GitStatus | null;
  agentStatus: ParsedLoomStatus;
  actions: UseGitActionsReturn;
}

export function GitActionBar({
  gitStatus,
  agentStatus,
  actions,
}: GitActionBarProps): JSX.Element {
  const [showPRForm, setShowPRForm] = useState(false);
  const [prTarget, setPrTarget] = useState("");
  const [showResetConfirm, setShowResetConfirm] = useState(false);

  const ahead = gitStatus?.ahead ?? 0;
  const behind = gitStatus?.behind ?? 0;
  const targetBranch = gitStatus?.target_branch ?? "main";

  const agentBusy =
    agentStatus.type === "working" || agentStatus.type === "planning";
  const disabled = actions.anyLoading || agentBusy;
  const disabledTitle = agentBusy
    ? "Agent is actively working"
    : actions.anyLoading
      ? "Operation in progress"
      : undefined;

  const handlePRFormOpen = useCallback(() => {
    setPrTarget(targetBranch);
    setShowPRForm(true);
  }, [targetBranch]);

  const handlePRSubmit = useCallback(async () => {
    await actions.createPR(prTarget || undefined);
    setShowPRForm(false);
    setPrTarget("");
  }, [actions, prTarget]);

  const handlePRCancel = useCallback(() => {
    setShowPRForm(false);
    setPrTarget("");
  }, []);

  const handleResetConfirm = useCallback(async () => {
    await actions.reset();
    setShowResetConfirm(false);
  }, [actions]);

  const handleResetCancel = useCallback(() => {
    setShowResetConfirm(false);
  }, []);

  return (
    <div className={styles.actionBarWrapper}>
      <div className={styles.actionBar}>
        {/* Pull */}
        <button
          type="button"
          className={styles.actionBtn}
          disabled={disabled || behind === 0}
          title={
            disabledTitle ?? (behind === 0 ? "Nothing to pull" : undefined)
          }
          onClick={() => actions.pull()}
        >
          {actions.pullState.isLoading && <span className={styles.spinner} />}
          Pull{behind > 0 ? ` (${behind})` : ""}
        </button>

        {/* Create PR */}
        <button
          type="button"
          className={styles.actionBtn}
          disabled={disabled || ahead === 0}
          title={
            disabledTitle ??
            (ahead === 0 ? "No commits to create PR for" : undefined)
          }
          onClick={handlePRFormOpen}
        >
          {actions.prState.isLoading && <span className={styles.spinner} />}
          Create PR
        </button>

        {/* Reset */}
        <button
          type="button"
          className={styles.actionBtn}
          data-variant="danger"
          disabled={disabled}
          title={disabledTitle}
          onClick={() => setShowResetConfirm(true)}
        >
          {actions.resetState.isLoading && <span className={styles.spinner} />}
          Reset
        </button>
      </div>

      {/* Inline PR Form */}
      {showPRForm && (
        <div className={styles.inlineForm}>
          <label className={styles.inlineLabel}>
            Target branch
            <input
              type="text"
              className={styles.inlineInput}
              value={prTarget}
              onChange={(e) => setPrTarget(e.target.value)}
              placeholder="main"
              autoFocus
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  void handlePRSubmit();
                }
                if (e.key === "Escape") handlePRCancel();
              }}
            />
          </label>
          <div className={styles.inlineFormActions}>
            <button
              type="button"
              className={styles.actionBtn}
              disabled={actions.prState.isLoading}
              onClick={() => void handlePRSubmit()}
            >
              {actions.prState.isLoading && <span className={styles.spinner} />}
              Create
            </button>
            <button
              type="button"
              className={styles.actionBtnSecondary}
              onClick={handlePRCancel}
              disabled={actions.prState.isLoading}
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Reset Confirmation */}
      {showResetConfirm && (
        <div className={styles.inlineForm}>
          <p className={styles.warningText}>
            This will discard all local changes and reset to {targetBranch}.
            Continue?
          </p>
          <div className={styles.inlineFormActions}>
            <button
              type="button"
              className={styles.actionBtn}
              data-variant="danger"
              disabled={actions.resetState.isLoading}
              onClick={() => void handleResetConfirm()}
            >
              {actions.resetState.isLoading && (
                <span className={styles.spinner} />
              )}
              Confirm Reset
            </button>
            <button
              type="button"
              className={styles.actionBtnSecondary}
              onClick={handleResetCancel}
              disabled={actions.resetState.isLoading}
            >
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
