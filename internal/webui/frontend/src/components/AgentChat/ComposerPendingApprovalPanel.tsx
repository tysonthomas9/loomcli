// Ported from T3 Code apps/web/src/components/chat/ComposerPendingApprovalPanel.tsx
// at commit 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: CSS modules in place of Tailwind, and the
// detail is Loom's ask.about (the command, file or diff the harness asks
// about), the same for every harness.

import { memo } from "react";
import type { Ask } from "@/api/agentsv1";
import styles from "./PendingAsk.module.css";

interface ComposerPendingApprovalPanelProps {
  approval: Ask;
  pendingCount: number;
}

export const ComposerPendingApprovalPanel = memo(
  function ComposerPendingApprovalPanel({
    approval,
    pendingCount,
  }: ComposerPendingApprovalPanelProps) {
    const fallbackLabel = "Approval";
    return (
      <div aria-label={fallbackLabel} className={styles.approval} role="group">
        <code
          aria-label="Approval request"
          className={styles.approvalDetail}
          data-approval-detail="complete"
          tabIndex={0}
        >
          {approval.about || fallbackLabel}
        </code>
        {pendingCount > 1 ? (
          <span className={styles.counter}>1/{pendingCount}</span>
        ) : null}
      </div>
    );
  },
);
