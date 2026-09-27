/**
 * Selected PR summary — the reference right-hand panel: status, author,
 * branch relationship, delivery dependency, merge requirements, and changes.
 * Requirements come from readiness facts and are never shown as met when the
 * evidence is not current. Merge preview stays read-only.
 */

import type {
  GitPullRequest,
  PullRequestReadinessView,
} from "@/api/workspace/pullRequests";
import type { DeliveryGroupView } from "@/api/workspace/deliveryGroups";
import {
  formatObservedAtLine,
  shortPrKey,
  type ReadinessDisplay,
} from "@/utils/pullRequest/readinessDisplay";
import {
  changeSummary,
  initialsFor,
  relativeAge,
  repoBasename,
  requirementLines,
  type RequirementState,
} from "@/utils/pullRequest/stackedPrPresentation";
import { Icon, type IconName } from "./Icon";
import { ReadinessBadge } from "./ReadinessBadge";
import styles from "./StackedPRWorkspace.module.css";

export interface GroupContext {
  group: DeliveryGroupView;
  index: number;
  prevKey?: string | undefined;
  prevMerged: boolean;
  prevCrossRepo: boolean;
  nextKey?: string | undefined;
  nextRepo?: string | undefined;
}

export interface SummaryPanelProps {
  prKey: string;
  pr: GitPullRequest | null;
  title: string;
  repo: string;
  display: ReadinessDisplay;
  view: PullRequestReadinessView | undefined;
  issueId?: string | undefined;
  isLocalOnly: boolean;
  groupContext: GroupContext | null;
  membershipUnverified: boolean;
  addableGroups: DeliveryGroupView[];
  saving: boolean;
  mobileOpen: boolean;
  onClose: () => void;
  onOpenReview: () => void;
  onRefresh: () => void;
  onPreview: (groupId: string) => void;
  onRemove: (group: DeliveryGroupView) => void;
  onAdd: (groupId: string) => void;
}

const REQUIREMENT_ICON: Record<RequirementState, IconName> = {
  met: "checkCircle",
  pending: "clock",
  failing: "warning",
  unknown: "help",
};

export function SummaryPanel({
  prKey,
  pr,
  title,
  repo,
  display,
  view,
  issueId,
  isLocalOnly,
  groupContext,
  membershipUnverified,
  addableGroups,
  saving,
  mobileOpen,
  onClose,
  onOpenReview,
  onRefresh,
  onPreview,
  onRemove,
  onAdd,
}: SummaryPanelProps): JSX.Element {
  const changes = changeSummary(pr);
  const requirements = requirementLines(view);
  const current = view?.freshness === "fresh";
  const opened = relativeAge(pr?.created_at);
  const head = pr?.head_ref_name || view?.snapshot?.head_ref || "";
  const base = pr?.base_ref_name || view?.snapshot?.base_ref || "";
  const repoShort = repoBasename(repo);
  const g = groupContext;

  return (
    <aside
      className={styles.detail}
      data-mobile-open={mobileOpen || undefined}
      aria-label="Selected PR summary"
      data-testid="selected-pr-detail"
    >
      <header className={styles.detailLabel}>
        <Icon name="pr" />
        <span>Summary</span>
        <span className={styles.detailId}>{shortPrKey(prKey)}</span>
        <button
          type="button"
          className={styles.iconBtn}
          aria-label="Close details"
          onClick={onClose}
        >
          <Icon name="panel" />
        </button>
      </header>

      <section className={styles.detailSection}>
        <div className={styles.detailTopline}>
          <ReadinessBadge display={display} />
          <span className={styles.detailStep}>
            {g
              ? `${g.index + 1} of ${g.group.members.length} in group`
              : isLocalOnly
                ? "Local task"
                : "Standalone"}
          </span>
        </div>
        <h2 className={styles.detailTitle}>{title}</h2>
        {pr?.author_login ? (
          <p className={styles.authorLine}>
            <span className={styles.rowAvatar} aria-hidden="true">
              {initialsFor(pr.author_login)}
            </span>
            <span className={styles.authorName}>@{pr.author_login}</span>
            opened this PR
            {opened ? <span className={styles.authorAge}>{opened}</span> : null}
          </p>
        ) : null}
        <p className={styles.evidenceLine}>
          {isLocalOnly
            ? "Local Loom task — no GitHub pull request is linked yet."
            : formatObservedAtLine(display)}
        </p>
        {issueId ? (
          <p className={styles.taskLine}>
            Loom task <code>{issueId}</code>
          </p>
        ) : null}

        <div className={styles.divider} />

        <div className={styles.kvRow}>
          <span className={styles.sectionTitle}>
            <Icon name="repo" /> Repository
          </span>
          <code className={styles.kvValue}>{repoShort}</code>
        </div>
        <p className={styles.sectionTitle}>
          <Icon name="branch" /> Branch relationship
        </p>
        {head ? (
          <div className={styles.branchRoute}>
            <div className={styles.routeRow}>
              <Icon name="branch" />
              <code>
                {repoShort} / {head}
              </code>
              <span className={styles.routeCaption}>Source</span>
            </div>
            <div className={styles.routeArrow}>targets</div>
            <div className={styles.routeRow} data-target>
              <Icon name="branch" />
              <code>
                {repoShort} / {base || "unknown"}
              </code>
              <span className={styles.routeCaption}>Base</span>
            </div>
          </div>
        ) : (
          <p className={styles.muted}>
            {isLocalOnly
              ? "No branch yet — this task has no GitHub PR."
              : "Branches not reported by GitHub."}
          </p>
        )}

        {g ? (
          <>
            <p className={styles.relation}>
              <Icon name="checkCircle" />
              {g.prevKey ? (
                <span>
                  Delivers after <strong>{shortPrKey(g.prevKey)}</strong>
                  {g.prevMerged ? " · merged" : " · not merged yet"}
                </span>
              ) : (
                <span>First step in “{g.group.title}”.</span>
              )}
            </p>
            {g.prevKey && g.prevCrossRepo ? (
              <p className={styles.muted}>
                Cross-repo delivery dependency, not branch ancestry. This PR
                stays in <strong>{repoShort}</strong>.
              </p>
            ) : null}
          </>
        ) : (
          <div className={styles.callout}>
            <Icon name="info" />
            <span>
              Not in a delivery group. Loom never infers membership from
              branches, epics, labels, or GitHub stacks.
            </span>
          </div>
        )}
        {membershipUnverified ? (
          <p className={styles.calloutWarn} role="status">
            Membership unverified — not confirmed standalone.
          </p>
        ) : null}
      </section>

      <section className={styles.detailMiddle}>
        <p className={styles.sectionTitle}>Merge requirements</p>
        {!current && !isLocalOnly ? (
          <p className={styles.reqNote}>
            Evidence is not current — nothing below is shown as met.
          </p>
        ) : null}
        <ul className={styles.readiness} data-testid="merge-requirements">
          {requirements.map((r) => (
            <li key={r.id} className={styles.checkLine} data-state={r.state}>
              <Icon name={REQUIREMENT_ICON[r.state]} />
              <span>{r.label}</span>
              {r.detail ? (
                <span className={styles.checkRight}>{r.detail}</span>
              ) : null}
            </li>
          ))}
        </ul>
        {g?.nextKey ? (
          <div className={styles.callout}>
            <Icon name="link" />
            <span>
              Merging this PR clears the delivery dependency for{" "}
              <strong>{shortPrKey(g.nextKey)}</strong>.
              {g.nextRepo ? (
                <>
                  {" "}
                  Its base stays in <strong>{repoBasename(g.nextRepo)}</strong>.
                </>
              ) : null}
            </span>
          </div>
        ) : null}

        <div className={styles.divider} />

        <div className={styles.kvRow}>
          <span className={styles.sectionTitle}>
            <Icon name="file" /> Changes
          </span>
          {changes ? (
            <span className={styles.filesSummary}>
              <span data-testid="selected-pr-changes-summary">
                {changes.files}
                {changes.additions != null || changes.deletions != null
                  ? ` · +${changes.additions ?? "?"} / −${changes.deletions ?? "?"}`
                  : ""}
              </span>
              <span className={styles.diffBars} aria-hidden="true">
                <i />
                <i />
                <i />
                <i />
                <i />
              </span>
            </span>
          ) : (
            <span className={styles.muted}>Not reported</span>
          )}
        </div>

        {g ? (
          <div className={styles.btns}>
            <button
              type="button"
              className={styles.btn}
              onClick={() => onPreview(g.group.id)}
            >
              <Icon name="merge" /> Ordered preview
            </button>
            <button
              type="button"
              className={styles.btn}
              disabled={saving}
              onClick={() => onRemove(g.group)}
            >
              Remove from group
            </button>
          </div>
        ) : addableGroups.length > 0 && !isLocalOnly ? (
          <label className={styles.field}>
            Add to delivery group
            <select
              aria-label="Add to delivery group"
              defaultValue=""
              onChange={(e) => {
                const id = e.target.value;
                e.target.value = "";
                if (id) onAdd(id);
              }}
            >
              <option value="" disabled>
                Choose group…
              </option>
              {addableGroups.map((grp) => (
                <option key={grp.id} value={grp.id}>
                  {grp.title}
                </option>
              ))}
            </select>
          </label>
        ) : null}
      </section>

      <footer className={styles.detailAction}>
        <button
          type="button"
          className={styles.btnPrimary}
          onClick={onOpenReview}
        >
          <Icon name="pr" /> Open review <Icon name="arrowRight" />
        </button>
        <button type="button" className={styles.btnGhost} onClick={onRefresh}>
          <Icon name="refresh" /> Refresh evidence
        </button>
        <p>
          {base && !isLocalOnly
            ? `Destination: ${repoShort}/${base}`
            : "Destination not reported"}
        </p>
      </footer>
    </aside>
  );
}
