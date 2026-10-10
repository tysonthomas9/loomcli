// primaryActionLabel is ported from T3 Code
// apps/web/src/components/chat/ComposerPrimaryActions.tsx at commit 2daff8c25.
// Copyright (c) 2026 T3 Tools Inc. MIT License; see THIRD_PARTY_NOTICES.md.
import { useCallback, useState } from "react";
import type { Ask, AskQuestion, RespondBody } from "@/api/agentsv1";
import {
  ComposerPendingApprovalActions,
  type ApprovalDecision,
} from "./ComposerPendingApprovalActions";
import { ComposerPendingApprovalPanel } from "./ComposerPendingApprovalPanel";
import { ComposerPendingUserInputPanel } from "./ComposerPendingUserInputPanel";
import {
  buildPendingUserInputAnswers,
  derivePendingUserInputProgress,
  setPendingUserInputCustomAnswer,
  togglePendingUserInputOptionSelection,
  type PendingUserInputDraftAnswer,
} from "./pendingUserInput";
import styles from "./PendingAsk.module.css";

export interface AskCardProps {
  ask: Ask;
  /** How many asks are open; the card shows the first (T3's "1/N"). */
  pendingCount: number;
  /** Resolves when the server took the answer; rejects to re-enable the card. */
  onRespond: (body: RespondBody) => Promise<void>;
  /** Stops the running turn, after a Cancel declined the approval. */
  onStop: () => Promise<void>;
}

/** Loom's answer for each of T3's approval decisions. */
const DECISIONS: Record<
  ApprovalDecision,
  NonNullable<RespondBody["decision"]>
> = {
  accept: "allow_once",
  acceptForSession: "allow_always",
  decline: "deny",
  cancel: "deny",
};

/**
 * One card for every harness's approval or question (design v2 §9.3), with
 * T3 Code's approval panel and actions and its multi-question panel. A
 * question its harness did not detail is one free-text question.
 */
export function AskCard({
  ask,
  pendingCount,
  onRespond,
  onStop,
}: AskCardProps) {
  const [pending, setPending] = useState(false);
  const respond = useCallback(
    (body: RespondBody, then?: () => Promise<void>) => {
      setPending(true);
      onRespond(body)
        .then(() => then?.())
        .catch(() => setPending(false));
    },
    [onRespond],
  );
  return (
    <div className={styles.card} data-testid="ask-card">
      {ask.type === "question" ? (
        <QuestionAsk ask={ask} isResponding={pending} respond={respond} />
      ) : (
        <>
          <div className={styles.title}>The agent asks for approval</div>
          <ComposerPendingApprovalPanel
            approval={ask}
            pendingCount={pendingCount}
          />
          <div className={styles.actions}>
            <ComposerPendingApprovalActions
              isResponding={pending}
              onRespondToApproval={(d) =>
                respond(
                  { decision: DECISIONS[d] },
                  d === "cancel" ? onStop : undefined,
                )
              }
            />
          </div>
        </>
      )}
    </div>
  );
}

/** The label of T3's pending primary action (ComposerPrimaryActions.tsx). */
function primaryActionLabel(input: {
  isLastQuestion: boolean;
  isResponding: boolean;
  questionIndex: number;
}) {
  if (input.isResponding) return "Submitting...";
  if (!input.isLastQuestion) return "Next question";
  return input.questionIndex > 0 ? "Submit answers" : "Submit answer";
}

function QuestionAsk({
  ask,
  isResponding,
  respond,
}: {
  ask: Ask;
  isResponding: boolean;
  respond: (body: RespondBody) => void;
}) {
  const detailed = (ask.questions?.length ?? 0) > 0;
  const questions: AskQuestion[] = detailed
    ? (ask.questions ?? [])
    : [{ id: "answer", question: ask.about || "The agent asks" }];
  const [answers, setAnswers] = useState<
    Record<string, PendingUserInputDraftAnswer>
  >({});
  const [questionIndex, setQuestionIndex] = useState(0);
  const progress = derivePendingUserInputProgress(
    questions,
    answers,
    questionIndex,
  );
  const active = progress.activeQuestion;

  // The last question submits every answer; an earlier one moves on.
  const advance = () => {
    if (!progress.isLastQuestion) {
      setQuestionIndex(progress.questionIndex + 1);
      return;
    }
    const resolved = buildPendingUserInputAnswers(questions, answers);
    if (!resolved) return;
    respond(
      detailed
        ? { answers: resolved }
        : { answer: (resolved.answer ?? []).join("\n") },
    );
  };

  return (
    <form
      className={styles.questionForm}
      onSubmit={(e) => {
        e.preventDefault();
        if (progress.isLastQuestion ? progress.isComplete : progress.canAdvance)
          advance();
      }}
    >
      <ComposerPendingUserInputPanel
        questions={questions}
        isResponding={isResponding}
        answers={answers}
        questionIndex={progress.questionIndex}
        onToggleOption={(questionId, label) => {
          const question = questions.find((q) => q.id === questionId);
          if (!question) return;
          setAnswers((all) => ({
            ...all,
            [questionId]: togglePendingUserInputOptionSelection(
              question,
              all[questionId],
              label,
            ),
          }));
        }}
        onAdvance={advance}
      />
      {active && (
        <textarea
          className={styles.customAnswer}
          aria-label="Write custom answer"
          placeholder={
            active.options?.length ? "Write custom answer" : "Write your answer"
          }
          value={progress.customAnswer}
          disabled={isResponding}
          onChange={(e) =>
            setAnswers((all) => ({
              ...all,
              [active.id]: setPendingUserInputCustomAnswer(
                all[active.id],
                e.target.value,
              ),
            }))
          }
        />
      )}
      <div className={styles.pendingActions}>
        {progress.questionIndex > 0 && (
          <button
            type="button"
            className={styles.pill}
            disabled={isResponding}
            onClick={() => setQuestionIndex(progress.questionIndex - 1)}
          >
            Previous
          </button>
        )}
        <button
          type="submit"
          className={`${styles.pill} ${styles.pillPrimary}`}
          disabled={
            isResponding ||
            (progress.isLastQuestion
              ? !progress.isComplete
              : !progress.canAdvance)
          }
        >
          {primaryActionLabel({
            isLastQuestion: progress.isLastQuestion,
            isResponding,
            questionIndex: progress.questionIndex,
          })}
        </button>
      </div>
    </form>
  );
}
