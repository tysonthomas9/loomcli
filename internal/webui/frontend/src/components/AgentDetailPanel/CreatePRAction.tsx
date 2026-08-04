/**
 * CreatePRAction - Create PR button and inline target-branch form.
 */

import { useState, useCallback } from "react";

import type { ParsedLoomStatus } from "@/types";
import type { UseGitActionsReturn } from "@/hooks/workspace";

import actionStyles from "./GitActionBar.module.css";
import styles from "./CreatePRAction.module.css";

interface CreatePRActionProps {
  targetBranch: string;
  ahead: number;
  agentStatus: ParsedLoomStatus;
  actions: UseGitActionsReturn;
}

interface CreatePRActionResult {
  button: JSX.Element;
  form: JSX.Element | null;
}

export function useCreatePRAction({
  targetBranch,
  ahead,
  agentStatus,
  actions,
}: CreatePRActionProps): CreatePRActionResult {
  const [showPRForm, setShowPRForm] = useState(false);
  const [prTarget, setPrTarget] = useState("");

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

  const button = (
    <button
      type="button"
      className={styles.createPrBtn}
      disabled={disabled || ahead === 0}
      title={
        disabledTitle ??
        (ahead === 0 ? "No commits to create PR for" : undefined)
      }
      onClick={handlePRFormOpen}
    >
      {actions.prState.isLoading && <span className={actionStyles.spinner} />}
      Create PR
    </button>
  );

  const form = showPRForm ? (
    <div className={styles.prForm}>
      <label className={actionStyles.inlineLabel}>
        Target branch
        <input
          type="text"
          className={actionStyles.inlineInput}
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
      <div className={actionStyles.inlineFormActions}>
        <button
          type="button"
          className={actionStyles.actionBtn}
          disabled={actions.prState.isLoading}
          onClick={() => void handlePRSubmit()}
        >
          {actions.prState.isLoading && (
            <span className={actionStyles.spinner} />
          )}
          Create
        </button>
        <button
          type="button"
          className={actionStyles.actionBtnSecondary}
          onClick={handlePRCancel}
          disabled={actions.prState.isLoading}
        >
          Cancel
        </button>
      </div>
    </div>
  ) : null;

  return { button, form };
}
