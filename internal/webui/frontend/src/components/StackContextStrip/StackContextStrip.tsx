/**
 * Compact delivery-group context strip for PR review and task detail
 * (STACKED-PRS-11). Additive chrome only — never replaces diff / discuss /
 * reviewer / agent / View PR controls.
 *
 * Presentational: async by-pr resolution lives in useStackContext.
 */

import { Link } from "react-router-dom";

import { ReadinessBadge } from "@/components/StackedPRWorkspace/ReadinessBadge";
import {
  isExternalMember,
  useStackContext,
} from "@/hooks/workspace/useStackContext";
import { parsePrKey } from "@/utils/issue";
import {
  deliveryAncestryCue,
  reviewPrDeepLink,
  stackGroupDeepLink,
} from "@/utils/pullRequest/deliveryAncestryCue";
import { shortPrKey } from "@/utils/pullRequest/readinessDisplay";

import styles from "./StackContextStrip.module.css";

/** Narrow PR row fields the strip may use when readiness snapshots are sparse. */
export interface StackContextStripPullRequest {
  base_ref_name?: string;
}

export interface StackContextStripProps {
  workspaceId: string;
  prKey: string;
  /** Optional GitHub list row for base/head refs when readiness is sparse. */
  pullRequest?: StackContextStripPullRequest | null;
  /** When set, renders a Back-to-queue control (review workspace). */
  onBack?: () => void;
}

function repoFromPrKey(prKey: string): string {
  const parsed = parsePrKey(prKey);
  return parsed ? `${parsed.owner}/${parsed.repo}` : "";
}

export function StackContextStrip({
  workspaceId,
  prKey,
  pullRequest,
  onBack,
}: StackContextStripProps): JSX.Element {
  const state = useStackContext(workspaceId, prKey);

  if (state.kind === "loading") {
    return (
      <div
        className={styles.strip}
        role="region"
        aria-label="Delivery group context"
        aria-busy="true"
        data-testid="stack-context-strip-loading"
      >
        <div className={styles.skeleton} />
      </div>
    );
  }

  if (state.kind === "unavailable") {
    return (
      <div
        className={styles.strip}
        role="region"
        aria-label="Delivery group context"
        data-testid="stack-context-strip"
        data-state="unavailable"
      >
        <span className={styles.muted}>Context unavailable</span>
        {onBack ? (
          <button type="button" className={styles.textBtn} onClick={onBack}>
            Back to queue
          </button>
        ) : null}
      </div>
    );
  }

  if (state.kind === "standalone") {
    return (
      <div
        className={styles.strip}
        role="region"
        aria-label="Delivery group context"
        data-testid="stack-context-strip"
        data-state="standalone"
      >
        <span>Standalone</span>
        <span className={styles.sep} aria-hidden="true">
          ·
        </span>
        <Link
          className={styles.link}
          to={`/ws/${encodeURIComponent(workspaceId)}/prs`}
        >
          Open in Pull Requests
        </Link>
        {onBack ? (
          <button type="button" className={styles.textBtn} onClick={onBack}>
            Back to queue
          </button>
        ) : null}
      </div>
    );
  }

  if (state.kind === "unverified") {
    return (
      <div
        className={styles.strip}
        role="region"
        aria-label="Delivery group context"
        data-testid="stack-context-strip"
        data-state="unverified"
      >
        <span>Membership unverified</span>
        {onBack ? (
          <button type="button" className={styles.textBtn} onClick={onBack}>
            Back to queue
          </button>
        ) : null}
      </div>
    );
  }

  const { group, index } = state;
  const member = group.members[index];
  const prev = index > 0 ? group.members[index - 1] : undefined;
  const next =
    index < group.members.length - 1 ? group.members[index + 1] : undefined;
  const prevHref = prev ? reviewPrDeepLink(workspaceId, prev.pr_key) : null;
  const nextHref = next ? reviewPrDeepLink(workspaceId, next.pr_key) : null;
  const openStackHref = stackGroupDeepLink(workspaceId, group.id, prKey);

  const currentBaseRef =
    member?.readiness?.snapshot?.base_ref || pullRequest?.base_ref_name || "";
  const prevHeadRef = prev?.readiness?.snapshot?.head_ref || "";
  const cue = deliveryAncestryCue({
    currentRepo: repoFromPrKey(prKey) || member?.repo_name || "",
    prevRepo: prev ? repoFromPrKey(prev.pr_key) || prev.repo_name : null,
    currentBaseRef,
    prevHeadRef,
  });

  const external = member ? isExternalMember(member) : false;
  const lineage = member?.lineage_stack_id?.trim();

  return (
    <div
      className={styles.strip}
      role="region"
      aria-label="Delivery group context"
      data-testid="stack-context-strip"
      data-state="grouped"
    >
      <div className={styles.row}>
        <span className={styles.title} title={group.title} tabIndex={0}>
          {group.title}
        </span>
        <span className={styles.position}>
          {index + 1} of {group.members.length}
        </span>
        {member?.readiness ? (
          <ReadinessBadge view={member.readiness} compact />
        ) : null}
        {external ? (
          <span className={styles.chip} data-testid="stack-context-external">
            External
          </span>
        ) : null}
        {group.inconsistent ? (
          <span
            className={styles.warn}
            data-testid="stack-context-inconsistent"
          >
            Group inconsistent
          </span>
        ) : null}
      </div>
      <div className={styles.row}>
        {prevHref ? (
          <Link className={styles.link} to={prevHref}>
            Prev {shortPrKey(prev!.pr_key)}
          </Link>
        ) : (
          <span className={styles.muted}>Prev —</span>
        )}
        {nextHref ? (
          <Link className={styles.link} to={nextHref}>
            Next {shortPrKey(next!.pr_key)}
          </Link>
        ) : (
          <span className={styles.muted}>Next —</span>
        )}
        {cue ? <span className={styles.cue}>{cue}</span> : null}
        {lineage ? (
          <span className={styles.muted} title={lineage}>
            lineage {lineage}
          </span>
        ) : null}
        <Link className={styles.link} to={openStackHref}>
          Open stack
        </Link>
        {onBack ? (
          <button type="button" className={styles.textBtn} onClick={onBack}>
            Back to queue
          </button>
        ) : null}
      </div>
    </div>
  );
}
