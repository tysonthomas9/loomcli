// Ported from T3 Code apps/web/src/components/chat/ComposerPendingApprovalActions.tsx
// at commit 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: CSS modules in place of Tailwind and
// plain buttons in place of T3's Button.

import { memo } from "react";
import styles from "./PendingAsk.module.css";

/**
 * T3's approval decisions. AskCard maps them to Loom's: accept is
 * allow_once, acceptForSession allow_always (a grant for this harness
 * session only), decline deny, and cancel deny plus stopping the turn.
 */
export type ApprovalDecision =
  | "accept"
  | "acceptForSession"
  | "decline"
  | "cancel";

interface ApprovalOption {
  decision: ApprovalDecision;
  label: string;
}

interface ComposerPendingApprovalActionsProps {
  isResponding: boolean;
  options?: ReadonlyArray<ApprovalOption>;
  onRespondToApproval: (decision: ApprovalDecision) => void;
}

const DEFAULT_APPROVAL_OPTIONS: ReadonlyArray<ApprovalOption> = [
  { decision: "cancel", label: "Cancel" },
  { decision: "decline", label: "Decline" },
  { decision: "acceptForSession", label: "Always allow this session" },
  { decision: "accept", label: "Approve" },
];

export const ComposerPendingApprovalActions = memo(
  function ComposerPendingApprovalActions({
    isResponding,
    options = DEFAULT_APPROVAL_OPTIONS,
    onRespondToApproval,
  }: ComposerPendingApprovalActionsProps) {
    return (
      <>
        {options.map((option) => (
          <button
            key={option.decision}
            type="button"
            className={`${styles.action}${
              option.decision === "decline"
                ? ` ${styles.actionDecline}`
                : option.decision === "accept"
                  ? ` ${styles.actionAccept}`
                  : ""
            }`}
            disabled={isResponding}
            onClick={() => onRespondToApproval(option.decision)}
          >
            <span className={styles.actionLabel}>{option.label}</span>
          </button>
        ))}
      </>
    );
  },
);
