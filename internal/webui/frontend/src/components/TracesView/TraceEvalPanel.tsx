import { useEffect, useState } from "react";

import { formatTokens } from "@/components/TranscriptView";
import { useToast } from "@/hooks/ui";
import type {
  EvalRejudgeResult,
  EvalScoreKey,
  SessionEvalRecord,
  SessionEvalState,
} from "@/types";

import styles from "./TracesView.module.css";

const SCORE_DIMENSIONS: Array<{ key: EvalScoreKey; label: string }> = [
  { key: "outcome_success", label: "Outcome success" },
  { key: "instruction_adherence", label: "Instruction adherence" },
  { key: "efficiency", label: "Efficiency" },
  { key: "tool_use_quality", label: "Tool use quality" },
];

const INSIGHT_GROUPS: Array<{
  key: keyof SessionEvalRecord["improvement_categories"];
  label: string;
}> = [
  { key: "harness", label: "Harness" },
  { key: "linter", label: "Linter" },
  { key: "prompt", label: "Prompt" },
  { key: "skill", label: "Skill" },
];

function scorePercent(value: number): number {
  if (!Number.isFinite(value)) return 0;
  return Math.max(0, Math.min(100, value));
}

function RequestedBadge({
  requested,
}: {
  requested: boolean;
}): JSX.Element | null {
  if (!requested) return null;
  return <span className={styles.evalRequestedBadge}>Re-judge requested</span>;
}

function RejudgeButton({
  evalState,
  isRejudging,
  onClick,
  requestedOverride,
}: {
  evalState: SessionEvalState | null;
  isRejudging: boolean;
  onClick: () => void;
  requestedOverride: boolean;
}): JSX.Element {
  const requested = Boolean(evalState?.eval_requested || requestedOverride);
  return (
    <button
      type="button"
      className={styles.evalRejudgeButton}
      disabled={isRejudging || requested}
      onClick={onClick}
      data-testid="trace-eval-rejudge"
    >
      {isRejudging
        ? "Requesting..."
        : requested
          ? "Re-judge requested"
          : "Re-judge"}
    </button>
  );
}

function ScoreBars({
  evalRecord,
}: {
  evalRecord: SessionEvalRecord;
}): JSX.Element {
  return (
    <div className={styles.evalScoreGrid}>
      {SCORE_DIMENSIONS.map((dimension) => {
        const value = evalRecord.scores[dimension.key] ?? 0;
        return (
          <div key={dimension.key} className={styles.evalScoreRow}>
            <div className={styles.evalScoreLabel}>{dimension.label}</div>
            <div className={styles.evalScoreTrack}>
              <div
                className={styles.evalScoreFill}
                style={{ width: `${scorePercent(value)}%` }}
              />
            </div>
            <div className={styles.evalScoreValue}>{value.toFixed(0)}</div>
          </div>
        );
      })}
    </div>
  );
}

function EvalCostLine({
  evalRecord,
}: {
  evalRecord: SessionEvalRecord;
}): JSX.Element {
  const cost = evalRecord.eval_cost;
  return (
    <div className={styles.evalMetaLine}>
      <span>{evalRecord.judge_model}</span>
      <span>Prompt {evalRecord.judge_prompt_version}</span>
      <span>
        {formatTokens(cost.total_tokens)} tokens (
        {formatTokens(cost.input_tokens)} in, {formatTokens(cost.output_tokens)}{" "}
        out)
      </span>
    </div>
  );
}

function DoneEval({
  evalRecord,
  onOpenJudge,
}: {
  evalRecord: SessionEvalRecord;
  onOpenJudge?: (judgeSessionId: string) => void;
}): JSX.Element {
  return (
    <div className={styles.evalDone}>
      <ScoreBars evalRecord={evalRecord} />
      {evalRecord.error_taxonomy_tags.length > 0 && (
        <div className={styles.evalTags}>
          {evalRecord.error_taxonomy_tags.map((tag) => (
            <span key={tag} className={styles.evalTag}>
              {tag}
            </span>
          ))}
        </div>
      )}
      <p className={styles.evalSummary}>{evalRecord.judge_summary}</p>
      <details className={styles.evalDetails}>
        <summary>Score rationales</summary>
        <div className={styles.evalRationaleList}>
          {SCORE_DIMENSIONS.map((dimension) => (
            <div key={dimension.key} className={styles.evalRationale}>
              <span>{dimension.label}</span>
              <p>{evalRecord.score_rationales[dimension.key]}</p>
            </div>
          ))}
        </div>
      </details>
      <div className={styles.evalInsightGroups}>
        {INSIGHT_GROUPS.map((group) => {
          const items = evalRecord.improvement_categories[group.key] ?? [];
          return (
            <section key={group.key} className={styles.evalInsightGroup}>
              <h4>{group.label}</h4>
              {items.length === 0 ? (
                <p className={styles.evalEmptyText}>No insights</p>
              ) : (
                <ul>
                  {items.map((text) => (
                    <li key={text}>{text}</li>
                  ))}
                </ul>
              )}
            </section>
          );
        })}
      </div>
      <EvalCostLine evalRecord={evalRecord} />
      {evalRecord.judge_session_id && onOpenJudge && (
        <button
          type="button"
          className={styles.evalJudgeLink}
          onClick={() => onOpenJudge(evalRecord.judge_session_id!)}
        >
          View judge transcript
        </button>
      )}
    </div>
  );
}

export function TraceEvalPanel({
  sessionId,
  kind,
  evalState,
  isLoading,
  isRejudging,
  error,
  requestRejudge,
  onOpenJudge,
}: {
  sessionId: string | null;
  kind?: string;
  evalState: SessionEvalState | null;
  isLoading: boolean;
  isRejudging: boolean;
  error: Error | null;
  requestRejudge: () => Promise<EvalRejudgeResult | null>;
  onOpenJudge?: (judgeSessionId: string) => void;
}): JSX.Element {
  const { showToast } = useToast();
  const [pausedNote, setPausedNote] = useState(false);
  const [requestedOverride, setRequestedOverride] = useState(false);

  useEffect(() => {
    setPausedNote(false);
    setRequestedOverride(false);
  }, [sessionId]);

  const handleRejudge = async () => {
    let result: Awaited<ReturnType<typeof requestRejudge>>;
    try {
      result = await requestRejudge();
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      showToast(`Re-judge rejected: ${message}`, { type: "error" });
      return;
    }
    if (!result) return;
    if (!result.requested) {
      showToast("Failed to request re-judge", { type: "error" });
      return;
    }
    setRequestedOverride(true);
    if (!result.binding_enabled) {
      setPausedNote(true);
      showToast("evals are paused — the request queues until re-enabled", {
        type: "warning",
      });
      return;
    }
    showToast("Re-judge requested", { type: "success" });
  };

  if (!sessionId) {
    return (
      <div className={styles.detailEmpty}>Select a trace to inspect it.</div>
    );
  }

  return (
    <div className={styles.evalPanel} data-testid="trace-eval-panel">
      <div className={styles.evalPanelHeader}>
        <div>
          <h3 className={styles.evalPanelTitle}>Eval</h3>
          {evalState && <RequestedBadge requested={evalState.eval_requested} />}
        </div>
        {kind !== "judge" && (
          <RejudgeButton
            evalState={evalState}
            isRejudging={isRejudging}
            requestedOverride={requestedOverride}
            onClick={() => void handleRejudge()}
          />
        )}
      </div>

      {pausedNote && (
        <div className={styles.evalPausedNote}>
          evals are paused — the request queues until re-enabled
        </div>
      )}

      {isLoading && !evalState && (
        <div className={styles.listStatus}>Loading eval...</div>
      )}
      {error && (
        <div className={styles.listError}>
          Failed to load eval: {error.message}
        </div>
      )}
      {!isLoading && !error && evalState?.eval_status === "none" && (
        <div className={styles.evalStateEmpty}>
          Not evaluated yet
          <RequestedBadge requested={evalState.eval_requested} />
        </div>
      )}
      {!isLoading && !error && evalState?.eval_status === "failed" && (
        <div className={styles.evalFailed}>
          <span className={styles.evalErrorChip}>
            {evalState.eval_error_class ?? "unknown"}
          </span>
          {evalState.eval_prompt_version && (
            <span className={styles.evalPromptVersion}>
              Prompt {evalState.eval_prompt_version}
            </span>
          )}
        </div>
      )}
      {!isLoading &&
        !error &&
        evalState?.eval_status === "done" &&
        evalState.eval && (
          <DoneEval
            evalRecord={evalState.eval}
            {...(onOpenJudge ? { onOpenJudge } : {})}
          />
        )}
    </div>
  );
}
