/**
 * CreatePRAction - Publish an approved change as a PR.
 */

import { useState, useCallback } from "react";

import type { ParsedLoomStatus } from "@/types";
import type { UseGitActionsReturn } from "@/hooks/workspace";

import actionStyles from "./GitActionBar.module.css";
import styles from "./CreatePRAction.module.css";

interface CreatePRActionProps {
  agentStatus: ParsedLoomStatus;
  actions: UseGitActionsReturn;
}

interface CreatePRActionResult {
  button: JSX.Element;
  form: JSX.Element | null;
}

export function useCreatePRAction({
  agentStatus,
  actions,
}: CreatePRActionProps): CreatePRActionResult {
  const [showPRForm, setShowPRForm] = useState(false);
  const [changeId, setChangeId] = useState("");

  const agentBusy =
    agentStatus.type === "working" || agentStatus.type === "planning";
  const disabled = actions.anyLoading || agentBusy;
  const disabledTitle = agentBusy
    ? "Agent is actively working"
    : actions.anyLoading
      ? "Operation in progress"
      : undefined;

  const handlePRFormOpen = useCallback(() => {
    setShowPRForm(true);
  }, []);

  const handlePRSubmit = useCallback(async () => {
    if (!changeId.trim()) return;
    await actions.createPR(changeId.trim());
    setShowPRForm(false);
    setChangeId("");
  }, [actions, changeId]);

  const handlePRCancel = useCallback(() => {
    setShowPRForm(false);
    setChangeId("");
  }, []);

  const button = (
    <button
      type="button"
      className={styles.createPrBtn}
      disabled={disabled}
      title={disabledTitle}
      onClick={handlePRFormOpen}
    >
      {actions.prState.isLoading && <span className={actionStyles.spinner} />}
      Create PR
    </button>
  );

  const form = showPRForm ? (
    <div className={styles.prForm}>
      <label className={actionStyles.inlineLabel}>
        Approved change ID
        <input
          type="text"
          className={actionStyles.inlineInput}
          value={changeId}
          onChange={(e) => setChangeId(e.target.value)}
          placeholder="Change ID"
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
          disabled={actions.prState.isLoading || !changeId.trim()}
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
