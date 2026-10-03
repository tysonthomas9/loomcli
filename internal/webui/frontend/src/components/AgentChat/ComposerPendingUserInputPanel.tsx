// Ported from T3 Code apps/web/src/components/chat/ComposerPendingUserInputPanel.tsx
// at commit 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Changes: CSS modules in place of Tailwind, a
// button and conditional body in place of T3's Collapsible, small inline
// glyphs in place of lucide icons, and Loom's ask (one prompt per card).

import { memo, useCallback, useEffect, useRef, useState } from "react";
import type { AskQuestion } from "@/api/agentsv1";
import {
  derivePendingUserInputProgress,
  type PendingUserInputDraftAnswer,
} from "./pendingUserInput";
import styles from "./PendingAsk.module.css";

interface PendingUserInputPanelProps {
  questions: ReadonlyArray<AskQuestion>;
  isResponding: boolean;
  answers: Record<string, PendingUserInputDraftAnswer>;
  questionIndex: number;
  onToggleOption: (questionId: string, optionLabel: string) => void;
  onAdvance: () => void;
}

export const ComposerPendingUserInputPanel = memo(
  function ComposerPendingUserInputPanel({
    questions,
    isResponding,
    answers,
    questionIndex,
    onToggleOption,
    onAdvance,
  }: PendingUserInputPanelProps) {
    const progress = derivePendingUserInputProgress(
      questions,
      answers,
      questionIndex,
    );
    const activeQuestion = progress.activeQuestion;
    const autoAdvanceTimerRef = useRef<number | null>(null);
    const onAdvanceRef = useRef(onAdvance);
    const [optimisticSingleSelect, setOptimisticSingleSelect] = useState<{
      questionId: string;
      optionLabel: string;
    } | null>(null);
    // Collapsing hides everything but the header so a tall prompt stops
    // covering the chat. Storing the collapsed question's id (rather than a
    // bare flag) reopens the card when the prompt advances to its next
    // question.
    const [collapsedQuestionId, setCollapsedQuestionId] = useState<
      string | null
    >(null);
    const isCollapsed =
      collapsedQuestionId !== null &&
      collapsedQuestionId === activeQuestion?.id;

    useEffect(() => {
      onAdvanceRef.current = onAdvance;
    }, [onAdvance]);

    useEffect(() => {
      if (
        !activeQuestion ||
        activeQuestion.multi_select ||
        !optimisticSingleSelect
      ) {
        return;
      }
      if (optimisticSingleSelect.questionId !== activeQuestion.id) {
        setOptimisticSingleSelect(null);
        return;
      }
      if (
        progress.customAnswer.trim().length === 0 &&
        progress.selectedOptionLabels.includes(
          optimisticSingleSelect.optionLabel,
        )
      ) {
        setOptimisticSingleSelect(null);
      }
    }, [
      activeQuestion,
      optimisticSingleSelect,
      progress.customAnswer,
      progress.selectedOptionLabels,
    ]);

    // Clear auto-advance timer on unmount
    useEffect(() => {
      return () => {
        if (autoAdvanceTimerRef.current !== null) {
          window.clearTimeout(autoAdvanceTimerRef.current);
        }
      };
    }, []);

    const handleOptionSelection = useCallback(
      (questionId: string, optionLabel: string) => {
        if (activeQuestion?.multi_select) {
          onToggleOption(questionId, optionLabel);
          return;
        }
        setOptimisticSingleSelect({ questionId, optionLabel });
        onToggleOption(questionId, optionLabel);
        if (autoAdvanceTimerRef.current !== null) {
          window.clearTimeout(autoAdvanceTimerRef.current);
        }
        autoAdvanceTimerRef.current = window.setTimeout(() => {
          autoAdvanceTimerRef.current = null;
          onAdvanceRef.current();
        }, 200);
      },
      [activeQuestion, onToggleOption],
    );

    // Keyboard shortcut: number keys 1-9 select corresponding options when
    // focus is outside editable fields. Multi-select prompts toggle options in
    // place; single-select prompts keep the auto-advance behavior. Collapsed
    // prompts opt out, since the numbers they refer to are not on screen.
    useEffect(() => {
      if (!activeQuestion || isResponding || isCollapsed) return;
      const handler = (event: globalThis.KeyboardEvent) => {
        if (event.metaKey || event.ctrlKey || event.altKey) return;
        const target = event.target;
        if (
          target instanceof HTMLInputElement ||
          target instanceof HTMLTextAreaElement
        ) {
          return;
        }
        if (
          target instanceof HTMLElement &&
          target.closest('[contenteditable]:not([contenteditable="false"])')
        ) {
          return;
        }
        const digit = Number.parseInt(event.key, 10);
        if (Number.isNaN(digit) || digit < 1 || digit > 9) return;
        const option = activeQuestion.options?.[digit - 1];
        if (!option) return;
        event.preventDefault();
        handleOptionSelection(activeQuestion.id, option.label);
      };
      document.addEventListener("keydown", handler);
      return () => document.removeEventListener("keydown", handler);
    }, [activeQuestion, handleOptionSelection, isCollapsed, isResponding]);

    if (!activeQuestion) {
      return null;
    }

    const customAnswerActive = progress.customAnswer.trim().length > 0;

    return (
      <div className={styles.userInput}>
        <button
          type="button"
          title={
            isCollapsed
              ? "Show the question and its options"
              : "Hide the question and its options"
          }
          aria-expanded={!isCollapsed}
          data-pending-user-input-toggle={
            isCollapsed ? "collapsed" : "expanded"
          }
          className={styles.userInputToggle}
          onClick={() =>
            setCollapsedQuestionId(isCollapsed ? null : activeQuestion.id)
          }
        >
          <span className={styles.userInputHeader}>
            {activeQuestion.header || "Question"}
          </span>
          {questions.length > 1 ? (
            <span className={styles.counter}>
              {progress.questionIndex + 1}/{questions.length}
            </span>
          ) : null}
          {/* Collapsed, the question itself is echoed as a one-line reminder. */}
          {isCollapsed ? (
            <span className={styles.userInputReminder}>
              {activeQuestion.question}
            </span>
          ) : null}
          <span
            aria-hidden="true"
            className={`${styles.chevron}${isCollapsed ? ` ${styles.chevronUp}` : ""}`}
          >
            ▾
          </span>
        </button>
        {!isCollapsed && (
          <div className={styles.userInputBody}>
            <p className={styles.userInputQuestion}>
              {activeQuestion.question}
            </p>
            {activeQuestion.multi_select ? (
              <p className={styles.userInputHint}>
                Select one or more options.
              </p>
            ) : null}
            <div className={styles.options}>
              {(activeQuestion.options ?? []).map((option, index) => {
                const isOptimisticallySelected =
                  optimisticSingleSelect?.questionId === activeQuestion.id &&
                  optimisticSingleSelect.optionLabel === option.label;
                const isSelected =
                  isOptimisticallySelected ||
                  (!customAnswerActive &&
                    progress.selectedOptionLabels.includes(option.label));
                const shortcutKey = index < 9 ? index + 1 : null;
                return (
                  <button
                    key={`${activeQuestion.id}:${option.label}`}
                    type="button"
                    disabled={isResponding}
                    aria-pressed={isSelected}
                    onClick={() => {
                      handleOptionSelection(activeQuestion.id, option.label);
                    }}
                    className={`${styles.option}${isSelected ? ` ${styles.optionSelected}` : ""}`}
                  >
                    <div className={styles.optionText}>
                      <span className={styles.optionLabel}>{option.label}</span>
                      {option.description &&
                      option.description !== option.label ? (
                        <span className={styles.optionDescription}>
                          {option.description}
                        </span>
                      ) : null}
                    </div>
                    {isSelected ? (
                      <span className={styles.check} aria-hidden="true">
                        ✓
                      </span>
                    ) : shortcutKey !== null ? (
                      <kbd className={styles.kbd}>{shortcutKey}</kbd>
                    ) : null}
                  </button>
                );
              })}
            </div>
          </div>
        )}
      </div>
    );
  },
);
