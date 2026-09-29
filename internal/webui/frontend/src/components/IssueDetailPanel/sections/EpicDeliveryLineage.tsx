/**
 * EpicDeliveryLineage — read-only "Delivery & lineage" rollup on epic detail
 * (STACKED-PRS-7).
 *
 * Shows the durable delivery groups FleetDB links to this epic: the numbered
 * delivery path (which may cross repositories), same-repository branch-base
 * footnotes observed from GitHub refs, readiness with the same labels as
 * /prs, and deep links into the Pull Requests workspace. It is a contextual
 * rollup — no membership edits, ordering, or merge controls live here — and it
 * never infers a group from child-ticket PR URLs.
 */
import { useId, type ReactNode } from "react";
import { Link, useInRouterContext } from "react-router-dom";

import type { DeliveryGroupView } from "@/api/workspace/deliveryGroups";
import type { PullRequestReadinessView } from "@/api/workspace/pullRequests";
import { ReadinessBadge } from "@/components/StackedPRWorkspace";
import {
  useEpicDeliveryGroups,
  type EpicDeliveryGroupsError,
} from "@/hooks/workspace/useEpicDeliveryGroups";
import { useWorkspaceContext } from "@/hooks/workspace/useWorkspaceContext";
import {
  branchBaseLinks,
  crossRepoFrom,
  isExternalMember,
  memberReadiness,
  prsEpicHref,
  prsGroupHref,
  worstGroupReadiness,
} from "@/utils/pullRequest/epicDeliveryLineage";
import {
  readinessDisplay,
  shortPrKey,
} from "@/utils/pullRequest/readinessDisplay";
import { memberRepo } from "@/utils/pullRequest/stackedPrModel";

import styles from "./EpicDeliveryLineage.module.css";

/** Client-side link when mounted under the app router; plain anchor otherwise. */
function PrsLink({
  to,
  className,
  children,
  testId,
}: {
  to: string;
  className?: string | undefined;
  children: ReactNode;
  testId?: string | undefined;
}): JSX.Element {
  const inRouter = useInRouterContext();
  const common = { className, "data-testid": testId };
  return inRouter ? (
    <Link to={to} {...common}>
      {children}
    </Link>
  ) : (
    <a href={to} {...common}>
      {children}
    </a>
  );
}

export interface EpicDeliveryLineageViewProps {
  workspaceId: string;
  epicId: string;
  groups: DeliveryGroupView[];
  extraReadiness?: ReadonlyMap<string, PullRequestReadinessView>;
  warnings?: string[];
  hasMore?: boolean;
  loading?: boolean;
  loadingMore?: boolean;
  error?: EpicDeliveryGroupsError | null;
  loadMoreError?: EpicDeliveryGroupsError | null;
  onLoadMore?: () => void;
}

const NO_READINESS: ReadonlyMap<string, PullRequestReadinessView> = new Map();

function GroupCard({
  workspaceId,
  group,
  extraReadiness,
}: {
  workspaceId: string;
  group: DeliveryGroupView;
  extraReadiness: ReadonlyMap<string, PullRequestReadinessView>;
}): JSX.Element {
  const titleId = useId();
  const members = group.members;
  const total = members.length;
  const worst = worstGroupReadiness(group, extraReadiness);
  const ancestry = branchBaseLinks(group, extraReadiness);
  const lineageStacks = [
    ...new Set(
      members
        .map((m) => m.lineage_stack_id?.trim())
        .filter((id): id is string => Boolean(id)),
    ),
  ];
  const integrity = group.integrity?.trim();
  const integrityUnverified = Boolean(integrity) && integrity !== "verified";

  return (
    <article
      className={styles.group}
      aria-labelledby={titleId}
      data-testid={`epic-delivery-group-${group.id}`}
    >
      <header className={styles.groupHead}>
        <h4 id={titleId} className={styles.groupTitle}>
          {group.title || group.id}
        </h4>
        <span className={styles.groupMeta}>
          <code className={styles.groupId}>{group.id}</code>
          {group.owner ? (
            <span className={styles.chip}>
              <span className={styles.srOnly}>Owner </span>
              {group.owner}
            </span>
          ) : null}
          {worst ? (
            <span className={styles.worst}>
              <span aria-hidden="true">Worst:</span>
              <span className={styles.srOnly}>Worst member readiness:</span>
              <ReadinessBadge display={worst} />
            </span>
          ) : null}
        </span>
      </header>

      {group.inconsistent ? (
        <p className={styles.warnBanner} role="status">
          Group state is inconsistent in FleetDB. Showing the committed members
          only; manage it from Pull Requests.
        </p>
      ) : null}
      {integrityUnverified ? (
        <p className={styles.note}>
          Integrity unverified ({integrity}). Order and membership may not be
          current.
        </p>
      ) : null}
      {lineageStacks.length > 0 ? (
        <p className={styles.note}>
          Loom lineage:{" "}
          {lineageStacks.map((id) => (
            <code key={id} className={styles.lineageChip}>
              {id}
            </code>
          ))}
        </p>
      ) : null}

      {total === 0 ? (
        <p className={styles.empty}>This delivery group has no members.</p>
      ) : (
        <ol
          className={styles.path}
          aria-label={`Delivery order for ${group.title || group.id}`}
        >
          {members.map((member, index) => {
            const view = memberReadiness(member, extraReadiness);
            const display = readinessDisplay(view);
            const external = isExternalMember(member);
            const cross = crossRepoFrom(members, index);
            const repo = memberRepo(member);
            const snap = view?.snapshot;
            const step = index + 1;
            return (
              <li
                key={member.pr_key}
                className={styles.step}
                data-testid={`epic-delivery-step-${shortPrKey(member.pr_key)}`}
              >
                {cross ? (
                  <p
                    className={styles.cross}
                    data-testid="epic-delivery-cross-repo"
                  >
                    <span className={styles.srOnly}>
                      Cross-repository delivery dependency:{" "}
                    </span>
                    <span aria-hidden="true">↳ </span>
                    Delivery order crosses {cross} → {repo} (not a branch link)
                  </p>
                ) : null}
                <PrsLink
                  to={prsGroupHref(workspaceId, group.id, member.pr_key)}
                  className={styles.stepLink}
                  testId={`epic-delivery-link-${shortPrKey(member.pr_key)}`}
                >
                  <span
                    className={styles.stepNum}
                    data-status={display.key}
                    aria-hidden="true"
                  >
                    {step}
                  </span>
                  <span className={styles.stepMain}>
                    <span className={styles.stepTitle}>
                      <span className={styles.srOnly}>
                        Delivery step {step} of {total}:{" "}
                      </span>
                      <span className={styles.repoChip}>{repo}</span>
                      <span className={styles.prNum}>#{member.pr_number}</span>
                      {external ? (
                        <span
                          className={styles.externalChip}
                          title="Not produced by a Loom task"
                        >
                          External
                        </span>
                      ) : member.task_id ? (
                        <code className={styles.taskId}>{member.task_id}</code>
                      ) : null}
                    </span>
                    {snap?.head_ref ? (
                      <span className={styles.refs}>
                        {snap.head_ref} → {snap.base_ref}
                        {view?.freshness !== "fresh" ? " (last observed)" : ""}
                      </span>
                    ) : null}
                    {member.warnings?.length ? (
                      <span className={styles.memberWarn}>
                        {member.warnings.join("; ")}
                      </span>
                    ) : null}
                  </span>
                  <ReadinessBadge display={display} />
                  <span className={styles.srOnly}>
                    {" "}
                    — open in Pull Requests
                  </span>
                </PrsLink>
              </li>
            );
          })}
        </ol>
      )}

      {ancestry.length > 0 ? (
        <div className={styles.ancestry}>
          <p className={styles.ancestryTitle}>
            Branch base (same repository) — does not change delivery order
          </p>
          <ul className={styles.ancestryList}>
            {ancestry.map((link) => {
              const child = members[link.childIndex]!;
              const base = members[link.baseIndex]!;
              return (
                <li
                  key={`${child.pr_key}->${base.pr_key}`}
                  data-testid="epic-delivery-branch-base"
                >
                  <span className={styles.srOnly}>
                    Same-repository branch base:{" "}
                  </span>
                  Step {link.childIndex + 1} (#{child.pr_number}) is based on
                  step {link.baseIndex + 1} (#{base.pr_number}) branch{" "}
                  <code className={styles.branch}>{link.branch}</code> in{" "}
                  {link.repo}
                  {link.lastKnown ? " · last observed, may be outdated" : ""}
                </li>
              );
            })}
          </ul>
        </div>
      ) : null}

      <div className={styles.groupActions}>
        <PrsLink
          to={prsGroupHref(workspaceId, group.id, members[0]?.pr_key)}
          className={styles.openLink}
          testId={`epic-delivery-open-${group.id}`}
        >
          Open in Pull Requests
        </PrsLink>
      </div>
    </article>
  );
}

/** Presentational rollup; data comes from props so it renders deterministically. */
export function EpicDeliveryLineageView({
  workspaceId,
  epicId,
  groups,
  extraReadiness = NO_READINESS,
  warnings = [],
  hasMore = false,
  loading = false,
  loadingMore = false,
  error = null,
  loadMoreError = null,
  onLoadMore,
}: EpicDeliveryLineageViewProps): JSX.Element {
  const headingId = useId();
  const epicLink = (
    <PrsLink to={prsEpicHref(workspaceId, epicId)} className={styles.openLink}>
      Open this epic in Pull Requests
    </PrsLink>
  );

  let body: ReactNode;
  if (loading) {
    body = (
      <div
        className={styles.skeleton}
        aria-busy="true"
        data-testid="epic-delivery-loading"
      >
        <span className={styles.srOnly}>Loading delivery groups…</span>
        <span className={styles.skeletonBar} />
        <span className={styles.skeletonBar} />
      </div>
    );
  } else if (error) {
    body = (
      <div className={styles.errorBox} role="alert">
        <p className={styles.errorTitle}>
          {error.kind === "unavailable"
            ? "Delivery groups unavailable."
            : "Couldn't load delivery groups."}
        </p>
        {error.message ? (
          <p className={styles.errorDetail}>{error.message}</p>
        ) : null}
        <p className={styles.errorDetail}>
          Ticket progress above is unaffected. {epicLink}
        </p>
      </div>
    );
  } else if (groups.length === 0) {
    body = (
      <div className={styles.empty} data-testid="epic-delivery-empty">
        <p>No delivery groups linked to this epic.</p>
        <p className={styles.emptyHint}>
          Child-ticket PRs are not grouped automatically. {epicLink}
        </p>
      </div>
    );
  } else {
    body = (
      <>
        {groups.length > 1 ? (
          <p className={styles.note} data-testid="epic-delivery-ambiguous">
            {groups.length} delivery groups{hasMore ? " (so far)" : ""} are
            linked to this epic. Each is shown separately; their orders are not
            combined.
          </p>
        ) : null}
        {groups.map((group) => (
          <GroupCard
            key={group.id}
            workspaceId={workspaceId}
            group={group}
            extraReadiness={extraReadiness}
          />
        ))}
        <div className={styles.pager}>
          <span className={styles.pagerText} aria-live="polite">
            Showing {groups.length} group{groups.length === 1 ? "" : "s"}
            {hasMore ? " · more available" : ""}
          </span>
          {hasMore && onLoadMore ? (
            <button
              type="button"
              className={styles.loadMore}
              onClick={onLoadMore}
              disabled={loadingMore}
            >
              {loadingMore ? "Loading…" : "Load more"}
            </button>
          ) : null}
        </div>
        {loadMoreError ? (
          <p className={styles.warnBanner} role="alert">
            Couldn&apos;t load more groups: {loadMoreError.message}
          </p>
        ) : null}
      </>
    );
  }

  return (
    <section
      className={styles.section}
      aria-labelledby={headingId}
      data-testid="epic-delivery-lineage"
    >
      <div className={styles.head}>
        <h3 id={headingId} className={styles.title}>
          Delivery &amp; lineage
        </h3>
        <span className={styles.caption}>
          Read-only · manage in Pull Requests
        </span>
      </div>
      {warnings.length > 0 ? (
        <ul className={styles.warnBanner} role="status" aria-label="Warnings">
          {warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
      ) : null}
      {body}
    </section>
  );
}

/** Data-backed rollup for one epic. Renders nothing outside a workspace. */
export function EpicDeliveryLineage({
  epicId,
}: {
  epicId: string;
}): JSX.Element | null {
  const { workspaceId } = useWorkspaceContext();
  const state = useEpicDeliveryGroups(epicId);
  if (!workspaceId) return null;
  return (
    <EpicDeliveryLineageView
      workspaceId={workspaceId}
      epicId={epicId}
      groups={state.groups}
      extraReadiness={state.extraReadiness}
      warnings={state.warnings}
      hasMore={state.hasMore}
      loading={state.loading}
      loadingMore={state.loadingMore}
      error={state.error}
      loadMoreError={state.loadMoreError}
      onLoadMore={state.loadMore}
    />
  );
}
