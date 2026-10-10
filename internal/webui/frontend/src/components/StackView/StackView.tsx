/**
 * StackView — the Pull Requests page's stacks (D37): one card per stack, its
 * PRs bottom first with one state each, merged rows folded into a history
 * line. "Merge up to here" queues the row's PR and the approved PRs below it
 * into the one merge queue the lead's `loom merge` also uses (D38).
 */
import { useState } from "react";
import { Link } from "react-router-dom";

import {
  mergeRunning,
  mergeUpTo,
  useStacks,
  type StackCard,
  type StackCardLayer,
} from "@/hooks/workspace/useStacks";
import type { Issue } from "@/types";

import styles from "./StackView.module.css";

const STATE_LABELS: Record<StackCardLayer["state"], string> = {
  draft: "Draft",
  needs_review: "Needs review",
  approved: "Approved",
  checks_failing: "Checks failing",
  ready: "Ready to merge",
  merging: "Merging",
  merged: "Merged",
  diverged: "Diverged",
};

const MERGEABLE = new Set<StackCardLayer["state"]>(["approved", "ready"]);

/** Whether Merge up to here can be offered on layers[index]: no merge is
 * running and it and every unmerged PR below it are approved. */
export function canMergeUpTo(card: StackCard, index: number): boolean {
  if (mergeRunning(card)) return false;
  return card.layers
    .slice(0, index + 1)
    .every((layer) => layer.state === "merged" || MERGEABLE.has(layer.state));
}

/** The card's plain-words status line: a blocker, or who is merging. */
export function stackNote(card: StackCard): string {
  if (card.note) return card.note;
  const merge = card.merge;
  if (!merge) return "";
  const by = merge.queued_by === "lead" ? " (queued by the lead)" : "";
  const target = merge.pr_number ? ` up to #${merge.pr_number}` : "";
  return `Merging${target}${by}`;
}

function repoShortName(repo: string): string {
  return repo.split("/").pop() || repo;
}

function StackRow({
  workspaceId,
  card,
  layer,
  index,
  title,
  onMerge,
  busy,
}: {
  workspaceId: string;
  card: StackCard;
  layer: StackCardLayer;
  index: number;
  title: string;
  onMerge: (change: string) => void;
  busy: boolean;
}): JSX.Element {
  const merged = layer.state === "merged";
  return (
    <li
      className={styles.row}
      data-testid="stack-row"
      data-change={layer.change}
      data-task={layer.task}
      data-state={layer.state}
    >
      <span className={styles.position} data-merged={merged || undefined}>
        {index + 1}
      </span>
      <div className={styles.rowMain}>
        <Link
          className={styles.rowTitle}
          to={`/ws/${workspaceId}/issues/${encodeURIComponent(layer.task || layer.change)}?tab=changes`}
          data-testid="stack-row-changes"
        >
          {title}
        </Link>
        <a
          className={styles.prLink}
          href={layer.pr_url}
          target="_blank"
          rel="noreferrer"
          data-testid="stack-row-pr"
        >
          {repoShortName(card.repo)}#{layer.pr_number}
        </a>
      </div>
      {!merged && canMergeUpTo(card, index) && (
        <button
          type="button"
          className={styles.mergeButton}
          disabled={busy}
          onClick={() => onMerge(layer.change)}
          data-testid="merge-up-to-here"
        >
          Merge up to here
        </button>
      )}
      <span
        className={styles.pill}
        data-state={layer.state}
        data-testid="stack-row-state"
      >
        {STATE_LABELS[layer.state] ?? layer.state}
      </span>
    </li>
  );
}

function StackCardView({
  workspaceId,
  card,
  titles,
  epic,
  onMerged,
}: {
  workspaceId: string;
  card: StackCard;
  titles: Map<string, string>;
  epic: string;
  onMerged: () => Promise<void>;
}): JSX.Element {
  const [showHistory, setShowHistory] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const merged = card.layers.filter((layer) => layer.state === "merged");
  const note = stackNote(card);

  const merge = async (change: string) => {
    setBusy(true);
    setError("");
    try {
      await mergeUpTo(workspaceId, change);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      await onMerged();
      setBusy(false);
    }
  };

  return (
    <section
      className={styles.card}
      data-testid="stack-card"
      data-repo={card.repo}
      data-publisher={card.backend}
      data-merging={card.merge ? card.merge.phase : undefined}
    >
      <header className={styles.cardHeader}>
        <div>
          <h2 className={styles.cardTitle}>
            {epic || repoShortName(card.repo)}
          </h2>
          <p className={styles.cardSubtitle}>
            {card.repo}
            <span className={styles.publisher}>
              {card.backend === "native" ? "GitHub stack" : "Loom stack"}
            </span>
          </p>
        </div>
        <span className={styles.count}>
          {card.layers.length} change{card.layers.length === 1 ? "" : "s"}
        </span>
      </header>
      {note && (
        <p className={styles.note} data-testid="stack-card-note">
          {note}
        </p>
      )}
      {error && (
        <p
          className={styles.error}
          role="alert"
          data-testid="stack-merge-error"
        >
          {error}
        </p>
      )}
      {merged.length > 0 && (
        <button
          type="button"
          className={styles.history}
          aria-expanded={showHistory}
          onClick={() => setShowHistory(!showHistory)}
          data-testid="stack-merged-summary"
        >
          {merged.length} merged change{merged.length === 1 ? "" : "s"} ·{" "}
          {showHistory ? "Hide history" : "Show history"}
        </button>
      )}
      <ol className={styles.rows}>
        {card.layers.map((layer, index) =>
          layer.state === "merged" && !showHistory ? null : (
            <StackRow
              key={layer.change}
              workspaceId={workspaceId}
              card={card}
              layer={layer}
              index={index}
              title={titles.get(layer.task || layer.change) ?? layer.change}
              onMerge={(change) => void merge(change)}
              busy={busy}
            />
          ),
        )}
      </ol>
    </section>
  );
}

/** The workspace's stacks; renders nothing when there are none. */
export function StackView({
  workspaceId,
  issues,
}: {
  workspaceId: string;
  issues: Issue[];
}): JSX.Element | null {
  const { stacks, refresh } = useStacks(workspaceId);
  if (stacks.length === 0) return null;
  const byId = new Map(issues.map((issue) => [issue.id, issue]));
  const titles = new Map(issues.map((issue) => [issue.id, issue.title]));
  return (
    <div className={styles.stacks} data-testid="stack-view">
      {stacks.map((card) => {
        const epic =
          card.layers
            .map((layer) => byId.get(layer.task || layer.change)?.parent_title)
            .find(Boolean) ?? "";
        return (
          <StackCardView
            key={card.stack_id}
            workspaceId={workspaceId}
            card={card}
            titles={titles}
            epic={epic}
            onMerged={refresh}
          />
        );
      })}
    </div>
  );
}
