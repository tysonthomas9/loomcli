/**
 * LabelEditor component.
 * Tag-style label editor with add/remove capabilities and optimistic updates.
 */

import {
  useState,
  useCallback,
  useRef,
  useEffect,
  type KeyboardEvent,
} from "react";

import styles from "./LabelEditor.module.css";

export interface LabelEditorProps {
  /** Current labels on the issue */
  labels: string[];
  /** Callback when a label is added - should call updateIssue with add_labels */
  onAddLabel: (label: string) => Promise<void>;
  /** Callback when a label is removed - should call updateIssue with remove_labels */
  onRemoveLabel: (label: string) => Promise<void>;
  /** Whether editing is disabled */
  disabled?: boolean;
}

export function LabelEditor({
  labels,
  onAddLabel,
  onRemoveLabel,
  disabled = false,
}: LabelEditorProps): JSX.Element {
  const [optimisticLabels, setOptimisticLabels] = useState<string[]>(labels);
  const [isAdding, setIsAdding] = useState(false);
  const [inputValue, setInputValue] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isBusy, setIsBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  // Sync optimistic state with prop changes (skip during in-flight operations)
  useEffect(() => {
    if (!isBusy) {
      setOptimisticLabels(labels);
    }
  }, [labels, isBusy]);

  // Focus input when add mode activates
  useEffect(() => {
    if (isAdding && inputRef.current) {
      inputRef.current.focus();
    }
  }, [isAdding]);

  const handleStartAdd = useCallback(() => {
    if (disabled) return;
    setIsAdding(true);
    setError(null);
  }, [disabled]);

  const handleCancelAdd = useCallback(() => {
    setIsAdding(false);
    setInputValue("");
    setError(null);
  }, []);

  const handleAdd = useCallback(async () => {
    const trimmed = inputValue.trim();

    if (!trimmed) {
      setError("Label cannot be empty");
      return;
    }

    // Case-insensitive duplicate check
    if (
      optimisticLabels.some((l) => l.toLowerCase() === trimmed.toLowerCase())
    ) {
      setError("Label already exists");
      return;
    }

    setError(null);
    setIsBusy(true);

    // Optimistic add
    const previousLabels = [...optimisticLabels];
    setOptimisticLabels([...optimisticLabels, trimmed]);
    setIsAdding(false);
    setInputValue("");

    try {
      await onAddLabel(trimmed);
    } catch (err) {
      // Rollback
      setOptimisticLabels(previousLabels);
      const message =
        err instanceof Error ? err.message : "Failed to add label";
      setError(message);
    } finally {
      setIsBusy(false);
    }
  }, [inputValue, optimisticLabels, onAddLabel]);

  const handleRemove = useCallback(
    async (label: string) => {
      if (disabled || isBusy) return;

      setError(null);
      setIsBusy(true);

      // Optimistic remove
      const previousLabels = [...optimisticLabels];
      setOptimisticLabels(optimisticLabels.filter((l) => l !== label));

      try {
        await onRemoveLabel(label);
      } catch (err) {
        // Rollback
        setOptimisticLabels(previousLabels);
        const message =
          err instanceof Error ? err.message : "Failed to remove label";
        setError(message);
      } finally {
        setIsBusy(false);
      }
    },
    [disabled, isBusy, optimisticLabels, onRemoveLabel],
  );

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLInputElement>) => {
      if (e.key === "Enter") {
        e.preventDefault();
        void handleAdd();
      } else if (e.key === "Escape") {
        e.preventDefault();
        handleCancelAdd();
      }
    },
    [handleAdd, handleCancelAdd],
  );

  return (
    <section className={styles.labelEditor} data-testid="label-editor">
      {/* Header */}
      <div className={styles.header}>
        <h3 className={styles.sectionTitle}>Labels</h3>
        {!disabled && !isAdding && (
          <button
            type="button"
            className={styles.addButton}
            onClick={handleStartAdd}
            disabled={isBusy}
            aria-label="Add label"
            data-testid="add-label-button"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 14 14"
              fill="none"
              aria-hidden="true"
            >
              <path
                d="M7 2V12M2 7H12"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
              />
            </svg>
            Add
          </button>
        )}
      </div>

      {/* Error display */}
      {error && (
        <div className={styles.error} role="alert" data-testid="label-error">
          {error}
        </div>
      )}

      {/* Add input */}
      {isAdding && (
        <div className={styles.addForm} data-testid="add-label-form">
          <input
            ref={inputRef}
            type="text"
            className={styles.addInput}
            value={inputValue}
            onChange={(e) => setInputValue(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Enter label name"
            disabled={isBusy}
            aria-label="Label name"
            data-testid="label-input"
          />
        </div>
      )}

      {/* Label pills */}
      {optimisticLabels.length > 0 ? (
        <div className={styles.labels} data-testid="label-list">
          {optimisticLabels.map((label) => (
            <span key={label} className={styles.label}>
              {label}
              {!disabled && (
                <button
                  type="button"
                  className={styles.removeButton}
                  onClick={() => handleRemove(label)}
                  disabled={isBusy}
                  aria-label={`Remove label ${label}`}
                  data-testid={`remove-label-${label}`}
                >
                  &times;
                </button>
              )}
            </span>
          ))}
        </div>
      ) : (
        !isAdding && (
          <p className={styles.emptyMessage} data-testid="no-labels">
            No labels
          </p>
        )
      )}
    </section>
  );
}
