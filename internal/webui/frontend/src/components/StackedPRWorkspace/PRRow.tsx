/**
 * One pull request row — a numbered step on a delivery-group path or a flat
 * standalone row. Selection is a button; the row never opens review on its
 * own (Enter/o or the summary's "Open review" do that).
 */

import type { KeyboardEvent as ReactKeyboardEvent, ReactNode } from "react";

import type { ReadinessDisplay } from "@/utils/pullRequest/readinessDisplay";
import { initialsFor } from "@/utils/pullRequest/stackedPrPresentation";
import { Icon } from "./Icon";
import { ReadinessBadge } from "./ReadinessBadge";
import styles from "./StackedPRWorkspace.module.css";

export type PathNodeState = "merged" | "next" | "pending";

export interface PRRowProps {
  testKey: string;
  numberLabel: string;
  title: string;
  repo: string;
  branch: string | null;
  display: ReadinessDisplay;
  authorLogin?: string | undefined;
  meta?: ReactNode;
  selected: boolean;
  dimmed?: boolean;
  /** Numbered path step; null renders a flat (standalone) row. */
  step: number | null;
  nodeState?: PathNodeState;
  crossNote?: string | undefined;
  onActivate: () => void;
}

export function PRRow({
  testKey,
  numberLabel,
  title,
  repo,
  branch,
  display,
  authorLogin,
  meta,
  selected,
  dimmed,
  step,
  nodeState = "pending",
  crossNote,
  onActivate,
}: PRRowProps): JSX.Element {
  const onKey = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      onActivate();
    }
  };
  return (
    <li
      className={step != null ? styles.prStep : styles.flatRow}
      data-node={step != null ? nodeState : undefined}
    >
      {step != null ? (
        <span className={styles.node} aria-hidden="true">
          {nodeState === "merged" ? <Icon name="check" /> : step}
        </span>
      ) : null}
      <button
        type="button"
        className={styles.row}
        data-current={selected || undefined}
        data-dimmed={dimmed || undefined}
        aria-current={selected ? "true" : undefined}
        aria-label={`Review ${title}`}
        onClick={onActivate}
        onKeyDown={onKey}
        data-testid={`pr-row-${testKey}`}
      >
        <span className={styles.rowTop}>
          <span className={styles.prNumber}>{numberLabel}</span>
          <span className={styles.prTitle}>{title}</span>
          <ReadinessBadge display={display} />
          {authorLogin ? (
            <span
              className={styles.rowAvatar}
              title={`@${authorLogin}`}
              aria-hidden="true"
            >
              {initialsFor(authorLogin)}
            </span>
          ) : null}
        </span>
        <span className={styles.rowBottom}>
          <Icon name="repo" />
          <span className={styles.repoName}>{repo}</span>
          {crossNote ? (
            <span className={styles.crossChip} title={crossNote}>
              <Icon name="link" />
              cross-repo
              <span className={styles.srOnly}>: {crossNote}</span>
            </span>
          ) : null}
          <span className={styles.sep} aria-hidden="true">
            /
          </span>
          <Icon name="branch" />
          <span className={styles.branchLabel}>
            {branch || "branch not reported"}
          </span>
          {meta ? <span className={styles.rowMeta}>{meta}</span> : null}
        </span>
      </button>
    </li>
  );
}
