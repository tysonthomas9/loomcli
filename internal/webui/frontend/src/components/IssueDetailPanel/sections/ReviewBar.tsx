/**
 * ReviewBar — the one review bar of a task (P2.23), at the top of its Changes
 * tab. Before a decision it offers one filled primary that says what happens
 * next (Approve code & create PR / & merge / , merge after #N), an outlined
 * Reject that asks why, and a ⋯ menu with Override (needs a reason). After a
 * decision the buttons give way to one status line per repo with its next
 * step, read from the server's revisions list so it survives a reload. A task
 * that changes several repos is decided once, for every repo (D29).
 */

import { useEffect, useState } from "react";

import {
  applyRevision,
  approveRevisionMerge,
  cancelRevisionMerge,
  getTaskRevisions,
  submitRevisionVerdict,
  updateIssue,
  type ReviewRevision,
  type TaskDiff,
} from "@/hooks/api";
import { ApiError } from "@/types";

import styles from "./ReviewBar.module.css";

/** What a status line's button does. */
export type NextAction =
  | "apply"
  | "reapprove"
  | "retry"
  | "rerun"
  | "retry-task"
  | "merge"
  | "cancel-merge";

export interface StatusLine {
  tone: "ok" | "warn" | "bad" | "plain";
  text: string;
  /** The task's PR and its state ("is open", "was merged", "was closed"). */
  pr?: { number: number; url?: string | undefined; state: string } | undefined;
  /** Shown after the PR, such as where its merge stands. */
  after?: string | undefined;
  /** A second line that explains the state (feedback, reasons). */
  detail?: string | undefined;
  action?: NextAction | undefined;
  /** Button label of the action. */
  label?: string | undefined;
}

const approving = new Set([
  "approve",
  "override",
  "policy",
  "carried",
  "feedback",
]);

/** Whether a verdict approves the revision (a human, the lead, or carried). */
export function isApproval(verdict: string | undefined): boolean {
  return verdict !== undefined && approving.has(verdict);
}

type PRState = "open" | "merged" | "closed";

/** The PR's state from the server; an older server reports only open PRs. */
function prStateOf(revision: ReviewRevision): PRState {
  const state = revision.pr_state;
  return state === "merged" || state === "closed" ? state : "open";
}

const activeMerge = ["waiting", "blocked", "merging"];

/**
 * What a revision with an open PR offers (D29): Approve and merge on the
 * bottom PR, merge after #N on a higher one, and the state of an approval
 * already made. "verdict" approves a new version and its merge in one click;
 * "merge" approves the merge of the version already on the PR.
 */
export function mergeState(
  revision: ReviewRevision,
  decided: boolean,
): {
  action: "none" | "merge" | "verdict";
  label: string;
  status: string;
  canCancel: boolean;
} {
  const below = revision.merge_after ?? [];
  const label =
    below.length > 0
      ? `Approve code, merge after #${below[below.length - 1]}`
      : "Approve code & merge";
  const status = revision.merge_status ?? "";
  const reason = revision.merge_reason ?? "";
  const after = below.map((n) => `#${n}`).join(", ");
  const text: Record<string, string> = {
    waiting:
      reason.startsWith("merges after") && after
        ? `merges after ${after}`
        : `merge waiting: ${reason}`,
    blocked: `merge blocked: ${reason}. Retries when it passes.`,
    merging: "merging…",
    stale_subject: `not merged: ${reason}. Approve the new version to merge it.`,
    reapproval_required: `not merged: ${reason}`,
    cancelled: "auto-merge cancelled",
  };
  let action: "none" | "merge" | "verdict" = "none";
  const open = Boolean(revision.pr_number) && prStateOf(revision) === "open";
  if (open && !activeMerge.includes(status) && status !== "merged") {
    // After someone else pushed (stale_subject), or a rebuild that was not
    // patch-equivalent (reapproval_required), only a new version that the
    // human has not decided yet can be approved to merge.
    if (!decided) action = "verdict";
    else if (
      isApproval(revision.verdict) &&
      status !== "stale_subject" &&
      status !== "reapproval_required" &&
      revision.head_sha === revision.pr_head
    )
      action = "merge";
  }
  return {
    action,
    label,
    status: revision.pr_number ? (text[status] ?? "") : "",
    canCancel: open && (status === "waiting" || status === "blocked"),
  };
}

/**
 * How a review fix-up's automatic update of its open PR stands (D29 (6)):
 * Loom pushes it with no Approve, unless it is held or must not be pushed.
 */
export function feedbackText(revision: ReviewRevision): string {
  const pr = revision.pr_number ? `PR #${revision.pr_number}` : "its PR";
  const reason = revision.feedback_reason ?? "";
  switch (revision.feedback_status) {
    case "pushing":
      return reason
        ? `Fixing review comments: not pushed to ${pr} yet (${reason})`
        : `Fixing review comments: pushing to ${pr} automatically`;
    case "pushed":
      return `Pushed to ${pr} automatically`;
    case "held":
      return `Held, not pushed to ${pr}: ${reason}`;
    case "not_pushed":
      return `Not pushed to ${pr}: ${reason}`;
    case "superseded":
      return "Replaced by a newer fix-up";
    default:
      return "";
  }
}

/**
 * The status line of a decided revision, or null while it awaits review. The
 * line always carries the next step: a button, or what happens on its own.
 */
export function statusLine(
  revision: ReviewRevision,
  taskStatus?: string,
): StatusLine | null {
  if (revision.no_changes) return { tone: "plain", text: "No changes" };
  const verdict = revision.verdict;
  if (verdict === "reject") {
    const text = revision.verdict_reason
      ? `✗ Rejected: ${revision.verdict_reason}`
      : "✗ Rejected";
    // Reject sends the task back to open for another attempt; a task that
    // was closed since is reopened by Retry task.
    return taskStatus === "closed"
      ? { tone: "bad", text, action: "retry-task", label: "Retry task" }
      : {
          tone: "bad",
          text,
          detail: "Sent back to the agent for another attempt.",
        };
  }
  if (revision.pr_number) {
    // A PR that merged or closed ends the review, decided or not.
    const state = prStateOf(revision);
    if (state === "merged" || revision.merge_status === "merged")
      return {
        tone: "ok",
        text: "✅ Merged",
        pr: prOf(revision, "was merged"),
      };
    if (state === "closed")
      return {
        tone: "warn",
        text: isApproval(verdict) ? "Approved" : "",
        pr: prOf(revision, "was closed"),
      };
  }
  if (!isApproval(verdict)) return null;
  const feedback = revision.feedback_status ? feedbackText(revision) : "";
  if (revision.pr_number) {
    const merge = mergeState(revision, true);
    const line: StatusLine = {
      tone: "ok",
      text: revision.applied ? "✅ Approved · Applied to lead" : "✅ Approved",
      pr: prOf(revision, "is open"),
      after: merge.status || undefined,
      detail: revision.feedback_merge_cancelled
        ? "Auto-merge cancelled because the code changed. Approve again to merge."
        : feedback || undefined,
    };
    if (merge.action === "merge")
      return { ...line, action: "merge", label: merge.label };
    if (merge.canCancel)
      return { ...line, action: "cancel-merge", label: "Cancel auto-merge" };
    return line;
  }
  switch (revision.follow_status) {
    case "conflict":
      return {
        tone: "warn",
        text: "⚠️ Approved · Couldn't apply: it conflicts with the lead's current code",
        detail:
          "Rerun the task from the latest code to get an attempt that applies.",
        action: "rerun",
        label: "Rerun from latest",
      };
    case "apply_pending":
      return {
        tone: "warn",
        text: "⚠️ Approved · Not applied yet: the lead's working area has unsaved edits to the same files",
        detail:
          "Save or resolve those edits; it applies and opens its PR then.",
      };
  }
  if (revision.applied) {
    const status = revision.publish_status;
    if (status === "pending" || status === "waiting")
      return {
        tone: "ok",
        text: "✅ Approved · Applied to lead · PR not opened yet",
        detail: revision.publish_reason
          ? `${revision.publish_reason}. Loom retries on its own.`
          : "Opening the PR…",
      };
    if (status === "published") {
      return { tone: "ok", text: "✅ Approved · Applied to lead" };
    }
    // No PR: no provider, a skip, or an approval from before Approve always
    // opened its PR (D40). Approving again retries the PR.
    return {
      tone: "ok",
      text: `✅ Approved · Applied · ${revision.publish_reason || "no PR yet"}`,
      detail: feedback || undefined,
      action: "retry",
      label: "Retry",
    };
  }
  if (revision.follow_status === "spent")
    return {
      tone: "plain",
      text: "Approved · not applied",
      detail: revision.follow_reason,
      action: "reapprove",
      label: "Apply",
    };
  return {
    tone: "plain",
    text: "Approved · not applied",
    action: "apply",
    label: "Apply",
  };
}

function prOf(revision: ReviewRevision, state: string): StatusLine["pr"] {
  return { number: revision.pr_number ?? 0, url: revision.pr_url, state };
}

/** A status line as one string (the Details tab, tests). */
export function lineText(line: StatusLine): string {
  return [
    line.text,
    line.pr && `PR #${line.pr.number} ${line.pr.state}`,
    line.after,
  ]
    .filter(Boolean)
    .join(" · ");
}

/** A status line's text, with its PR as a link. */
function LineText({ line }: { line: StatusLine }): JSX.Element {
  const parts: (string | JSX.Element)[] = [];
  if (line.text) parts.push(line.text);
  if (line.pr)
    parts.push(
      <span key="pr" data-testid="revision-pr">
        {line.pr.url ? (
          <a href={line.pr.url} target="_blank" rel="noreferrer">
            PR #{line.pr.number}
          </a>
        ) : (
          `PR #${line.pr.number}`
        )}{" "}
        {line.pr.state}
      </span>,
    );
  if (line.after) parts.push(line.after);
  return (
    <>
      {parts.map((part, i) => (
        <span key={i}>
          {i > 0 && " · "}
          {part}
        </span>
      ))}
    </>
  );
}

/** The newest revision of each change, in list order. */
export function newestRevisions(revisions: ReviewRevision[]): ReviewRevision[] {
  const newest = new Map<string, number>();
  for (const r of revisions)
    newest.set(r.change_id, Math.max(newest.get(r.change_id) ?? 0, r.number));
  return revisions.filter((r) => newest.get(r.change_id) === r.number);
}

/** One line for the Details tab: what the task's review stands at. */
export function reviewSummary(
  current: ReviewRevision[],
  taskStatus?: string,
): string {
  if (current.length === 0) return "";
  const lines = current.map((r) => ({ r, line: statusLine(r, taskStatus) }));
  if (lines.some(({ line }) => line === null)) return "Code awaiting review";
  const multi = current.length > 1;
  return lines
    .map(({ r, line }) =>
      multi ? `${r.repo}: ${lineText(line!)}` : lineText(line!),
    )
    .join("; ");
}

/**
 * Why a verdict request failed. A held approval's response carries a readable
 * message (with the overlapping paths) next to its error code; prefer it.
 */
export function verdictErrorText(err: unknown): string {
  if (err instanceof ApiError && err.body && typeof err.body === "object") {
    const message = (err.body as { message?: unknown }).message;
    if (typeof message === "string" && message) return message;
  }
  return err instanceof Error ? err.message : "Could not record verdict";
}

/**
 * The verdict a failed request still recorded, when the server says so (its
 * error carries a status): the recorded kind, else the one submitted.
 */
function recordedVerdict(err: unknown, submitted: string): string {
  if (!(err instanceof ApiError)) return "";
  const body = err.body as
    | { status?: unknown; data?: { Kind?: unknown } }
    | undefined;
  if (typeof body?.status !== "string") return "";
  const kind = body.data?.Kind;
  return typeof kind === "string" && kind ? kind : submitted;
}

function keyOf(r: ReviewRevision): string {
  return `${r.change_id}:${r.number}`;
}

function lineStats(files: TaskDiff["files"]): {
  additions: number;
  deletions: number;
} {
  let additions = 0;
  let deletions = 0;
  for (const file of files)
    for (const line of (file.patch ?? "").split("\n")) {
      if (line.startsWith("+") && !line.startsWith("+++")) additions++;
      else if (line.startsWith("-") && !line.startsWith("---")) deletions++;
    }
  return { additions, deletions };
}

/** "Code changes · by coder · 2 files, +10 −3 · <time>"; SHAs in a tooltip. */
function Header({
  current,
  diffs,
}: {
  current: ReviewRevision[];
  diffs: TaskDiff[] | null;
}): JSX.Element {
  const authors = [
    ...new Set(current.map((r) => r.author).filter(Boolean)),
  ] as string[];
  const shown = (diffs ?? []).filter((d) =>
    current.some((r) => r.change_id === d.change),
  );
  const files = shown.reduce((n, d) => n + d.files.length, 0);
  const { additions, deletions } = lineStats(shown.flatMap((d) => d.files));
  const dates = current
    .map((r) => r.date)
    .filter(Boolean)
    .sort() as string[];
  const newest = dates[dates.length - 1];
  const parts = ["Code changes"];
  if (authors.length) parts.push(`by ${authors.join(", ")}`);
  if (diffs)
    parts.push(
      `${files} ${files === 1 ? "file" : "files"}, +${additions} −${deletions}`,
    );
  if (newest) parts.push(new Date(newest).toLocaleString());
  return (
    <div
      className={styles.header}
      data-testid="review-header"
      title={current
        .map(
          (r) =>
            `${current.length > 1 ? `${r.repo} ` : ""}commit ${r.head_sha}`,
        )
        .join("\n")}
    >
      {parts.join(" · ")}
    </div>
  );
}

export function ReviewBar({
  workspaceId,
  taskId,
  lead,
  taskStatus,
  current,
  diffs,
  onChanged,
}: {
  workspaceId: string;
  taskId: string;
  lead?: string | undefined;
  taskStatus?: string | undefined;
  /** The newest revision of each repo, ordered by repo name. */
  current: ReviewRevision[];
  /**
   * The diffs on screen. The buttons enable only once every repo's diff for
   * its newest revision has loaded, so a verdict is always on what was shown.
   */
  diffs: TaskDiff[] | null;
  /** Reload the revisions and diffs after a decision. */
  onChanged: () => void;
}): JSX.Element {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [form, setForm] = useState<"" | "reject" | "override">("");
  const [reason, setReason] = useState("");
  const [menu, setMenu] = useState(false);
  // Verdicts the server recorded although the request failed (held apply or
  // PR), by revision: shown until the reloaded list carries them (P2.21).
  const [recorded, setRecorded] = useState<Record<string, string>>({});

  const shown = current.map((r): ReviewRevision => {
    const kept = recorded[keyOf(r)];
    return r.verdict || !kept ? r : { ...r, verdict: kept };
  });
  const multi = shown.length > 1;
  const undecided = shown.filter((r) => statusLine(r, taskStatus) === null);
  const ready =
    diffs !== null &&
    undecided.every((r) =>
      diffs.some((d) => d.change === r.change_id && d.revision === r.number),
    );
  const incomplete = undecided.some((r) => r.incomplete);
  const disabled = busy || !ready || incomplete;
  // An open PR makes the primary approve its merge too (D29); while a merge
  // of the PR is already under way there is nothing to approve here.
  const onPR = undecided.filter((r) => r.pr_number);
  const withPR = onPR.find((r) => mergeState(r, false).action === "verdict");
  const merging = onPR.some((r) => mergeState(r, false).action === "none");
  const primary = withPR
    ? mergeState(withPR, false).label
    : "Approve code & create PR";

  async function run(work: () => Promise<void>) {
    setBusy(true);
    setMenu(false);
    setError("");
    try {
      await work();
    } catch (err) {
      setError(verdictErrorText(err));
    } finally {
      setBusy(false);
      onChanged();
    }
  }

  // One decision for the task: every repo's newest revision, in repo order.
  // A failure stops it and names the repo.
  function decide(verdict: "approve" | "reject" | "override", why = "") {
    void run(async () => {
      for (const r of undecided) {
        const merge =
          verdict === "approve" && mergeState(r, false).action === "verdict";
        try {
          await submitRevisionVerdict(
            workspaceId,
            r,
            verdict,
            why,
            lead,
            ...(merge ? [true] : []),
          );
        } catch (err) {
          const kept = recordedVerdict(err, verdict);
          if (kept) setRecorded((prev) => ({ ...prev, [keyOf(r)]: kept }));
          throw multi ? new Error(`${r.repo}: ${verdictErrorText(err)}`) : err;
        }
      }
      setForm("");
      setReason("");
    });
  }

  function act(r: ReviewRevision, action: NextAction) {
    void run(async () => {
      switch (action) {
        case "apply":
          if (lead) await applyRevision(workspaceId, r, lead);
          return;
        case "reapprove":
        case "retry":
          await submitRevisionVerdict(workspaceId, r, "approve", "", lead);
          return;
        case "rerun":
          // A rejection naming the conflict sends the task back; the rerun
          // starts from the lead's current code (P1.28).
          await submitRevisionVerdict(
            workspaceId,
            r,
            "reject",
            "Couldn't apply: it conflicts with the lead's current code. Rerun from the latest code.",
            lead,
          );
          return;
        case "retry-task":
          await updateIssue(workspaceId, taskId, { status: "open" });
          return;
        case "merge":
          if (lead) await approveRevisionMerge(workspaceId, r, lead);
          return;
        case "cancel-merge":
          await cancelRevisionMerge(workspaceId, r.change_id);
          return;
      }
    });
  }

  return (
    <section
      className={styles.bar}
      aria-label="Review"
      data-testid="revisions-section"
    >
      <Header current={shown} diffs={diffs} />
      {shown.map((r) => {
        const line = statusLine(r, taskStatus);
        if (!line) return null;
        return (
          <div
            key={keyOf(r)}
            className={styles.line}
            data-tone={line.tone}
            data-testid={r.no_changes ? "revision-no-changes" : "review-status"}
            role="status"
          >
            <span>
              {multi && <strong>{r.repo}: </strong>}
              <LineText line={line} />
            </span>
            {line.action && (
              <button
                type="button"
                className={
                  line.action === "cancel-merge"
                    ? styles.outline
                    : styles.primary
                }
                data-testid={
                  line.action === "merge"
                    ? "approve-merge"
                    : line.action === "cancel-merge"
                      ? "cancel-auto-merge"
                      : "review-next-action"
                }
                disabled={
                  busy ||
                  ((line.action === "apply" || line.action === "merge") &&
                    !lead)
                }
                onClick={() => act(r, line.action!)}
              >
                {line.label}
              </button>
            )}
            {line.detail && (
              <div className={styles.detail} data-testid="review-status-detail">
                {line.detail}
              </div>
            )}
          </div>
        );
      })}
      {onPR.map((r) => (
        // A new version of a task whose PR is open: say where its PR stands.
        <div
          key={keyOf(r)}
          className={styles.line}
          data-tone="plain"
          data-testid="merge-status"
        >
          {multi && <strong>{r.repo}: </strong>}
          <LineText
            line={{
              tone: "plain",
              text: "",
              pr: prOf(r, "is open"),
              after: mergeState(r, false).status || undefined,
            }}
          />
          {r.feedback_merge_cancelled &&
            " · Auto-merge cancelled because the code changed. Approve again to merge."}
        </div>
      ))}
      {undecided.length > 0 && (
        <div
          className={styles.line}
          data-tone="plain"
          data-testid="review-awaiting"
        >
          {incomplete
            ? "Capture incomplete: this attempt can't be approved."
            : "Code awaiting review"}
        </div>
      )}
      {undecided.length > 0 && (
        <div className={styles.actions}>
          {!merging && (
            <button
              type="button"
              className={styles.primary}
              data-testid={withPR ? "approve-merge" : "approve-create-pr"}
              disabled={disabled}
              onClick={() => decide("approve")}
            >
              {primary}
            </button>
          )}
          <button
            type="button"
            className={styles.outline}
            data-testid="review-reject"
            disabled={disabled}
            onClick={() => {
              setForm("reject");
              setReason("");
            }}
          >
            Reject
          </button>
          <span className={styles.more}>
            <button
              type="button"
              className={styles.outline}
              aria-label="More review options"
              aria-haspopup="menu"
              aria-expanded={menu}
              data-testid="review-more"
              disabled={disabled}
              onClick={() => setMenu((open) => !open)}
            >
              ⋯
            </button>
            {menu && (
              <span role="menu" className={styles.menu}>
                <button
                  type="button"
                  role="menuitem"
                  data-testid="review-override"
                  onClick={() => {
                    setMenu(false);
                    setForm("override");
                    setReason("");
                  }}
                >
                  Override (needs a reason)
                </button>
              </span>
            )}
          </span>
        </div>
      )}
      {form && undecided.length > 0 && (
        <div className={styles.form}>
          <label htmlFor={`review-reason-${taskId}`}>
            {form === "reject"
              ? "Why? The agent sees this on its next attempt."
              : "Override reason"}
          </label>
          <textarea
            id={`review-reason-${taskId}`}
            data-testid="review-reason"
            rows={2}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
          <div className={styles.formButtons}>
            <button
              type="button"
              className={form === "reject" ? styles.danger : styles.primary}
              data-testid={
                form === "reject" ? "reject-confirm" : "override-confirm"
              }
              disabled={disabled || !reason.trim()}
              onClick={() => decide(form, reason.trim())}
            >
              {form === "reject" ? "Reject and rerun" : "Record override"}
            </button>
            <button
              type="button"
              className={styles.outline}
              onClick={() => setForm("")}
            >
              Cancel
            </button>
          </div>
        </div>
      )}
      {error && (
        <p role="alert" className={styles.error}>
          {error}
        </p>
      )}
    </section>
  );
}

/**
 * The Details tab's one-line review status, linking to the review bar on
 * Changes (P2.23): e.g. "Code awaiting review → Changes".
 */
export function ReviewStatusLink({
  workspaceId,
  taskId,
  lead,
  taskStatus,
  onOpen,
}: {
  workspaceId: string;
  taskId: string;
  lead?: string | undefined;
  taskStatus?: string | undefined;
  onOpen: () => void;
}): JSX.Element | null {
  const [summary, setSummary] = useState("");
  useEffect(() => {
    let active = true;
    getTaskRevisions(workspaceId, taskId, lead)
      .then((items) => {
        const current = newestRevisions(items).sort((a, b) =>
          a.repo.localeCompare(b.repo),
        );
        if (active) setSummary(reviewSummary(current, taskStatus));
      })
      .catch(() => {
        // The Changes tab shows the error; Details stays quiet.
      });
    return () => {
      active = false;
    };
  }, [workspaceId, taskId, lead, taskStatus]);
  if (!summary) return null;
  return (
    <p className={styles.link} data-testid="review-status-link">
      {summary}{" "}
      <button type="button" onClick={onOpen}>
        → Changes
      </button>
    </p>
  );
}
