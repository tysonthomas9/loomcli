/**
 * ToolPill — a collapsed tool call: name + salient arg + caret, expanding to
 * the tool's input and result. Shared by the run transcript (SessionDetailView)
 * and the PR reviewer chat (PRDiscussionPanel), which render the same thing from
 * different sources.
 *
 * Expansion is controlled by the caller: the transcript lifts it into a per-turn
 * set, the chat keeps it per pill.
 */

import styles from "./ToolPill.module.css";

export interface ToolPillProps {
  /** Tool name shown in the pill's leading chip. */
  name: string;
  /** Short preview of the most salient input arg — see utils/toolPreview. */
  arg?: string | undefined;
  /** Full tool input, shown when expanded. */
  input?: string | undefined;
  /** Tool result text, shown when expanded. */
  result?: string | undefined;
  /** Rendered beside the Result label when the paired result carries a time. */
  resultTimestamp?: string | undefined;
  expanded: boolean;
  onToggle: () => void;
  /** Extra class on the outer block, for caller-specific layout. */
  className?: string | undefined;
  /** data-testid for the outer block; the toggle is always `tool-pill`. */
  testId?: string | undefined;
  /** data-type for the outer block (the transcript queries events by type). */
  dataType?: string | undefined;
}

export function ToolPill({
  name,
  arg,
  input,
  result,
  resultTimestamp,
  expanded,
  onToggle,
  className,
  testId,
  dataType,
}: ToolPillProps): JSX.Element {
  const body = expanded && (input || result);

  return (
    <div
      className={
        className ? `${styles.toolBlock} ${className}` : styles.toolBlock
      }
      data-testid={testId}
      data-type={dataType}
    >
      <button
        type="button"
        className={`${styles.toolPill} ${expanded ? styles.toolPillOpen : ""}`}
        onClick={onToggle}
        aria-expanded={expanded}
        data-testid="tool-pill"
      >
        <span className={styles.toolPillIcon}>{name}</span>
        {arg && <span className={styles.toolPillArg}>{arg}</span>}
        <span className={styles.toolPillCaret}>{expanded ? "▾" : "▸"}</span>
      </button>
      {body && (
        <div className={styles.toolBody}>
          {input && <pre className={styles.toolInput}>{input}</pre>}
          {result && (
            <>
              <div className={styles.toolResultLabel}>
                Result
                {resultTimestamp && (
                  <span className={styles.ts}>· {resultTimestamp}</span>
                )}
              </div>
              <pre className={styles.toolOutput}>{result}</pre>
            </>
          )}
        </div>
      )}
    </div>
  );
}
