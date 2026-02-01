/**
 * AssigneePrompt component.
 * Modal that prompts for a name when dragging an issue to In Progress.
 * Remembers recent names for quick selection.
 */

import { useState, useEffect, useRef, useCallback } from 'react';
import styles from './AssigneePrompt.module.css';

/**
 * Props for the AssigneePrompt component.
 */
export interface AssigneePromptProps {
  /** Whether the prompt is open */
  isOpen: boolean;
  /** Callback when user confirms with a name (receives name with [H] prefix added) */
  onConfirm: (name: string) => void;
  /** Callback when user skips (no assignee) */
  onSkip: () => void;
  /** List of recent names to show for quick selection */
  recentNames: string[];
}

/**
 * Modal prompt for entering assignee name when dragging to In Progress.
 *
 * Features:
 * - Input field for entering name
 * - Quick selection from recent names
 * - Skip button to proceed without assignee
 * - Escape key to skip
 * - Focus trap within modal
 * - Adds [H] prefix to distinguish human assignees
 */
export function AssigneePrompt({
  isOpen,
  onConfirm,
  onSkip,
  recentNames,
}: AssigneePromptProps): JSX.Element {
  const [inputValue, setInputValue] = useState('');
  const inputRef = useRef<HTMLInputElement>(null);
  const modalRef = useRef<HTMLDivElement>(null);

  // Reset input and focus when opening
  useEffect(() => {
    if (isOpen) {
      setInputValue('');
      // Focus input after modal transition
      const timer = setTimeout(() => {
        inputRef.current?.focus();
      }, 100);
      return () => clearTimeout(timer);
    }
  }, [isOpen]);

  // Handle Escape key to skip
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onSkip();
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onSkip]);

  // Handle form submission
  const handleSubmit = useCallback(
    (event: React.FormEvent) => {
      event.preventDefault();
      const trimmed = inputValue.trim();
      if (trimmed) {
        // Add [H] prefix to distinguish human assignees
        onConfirm(`[H] ${trimmed}`);
      }
    },
    [inputValue, onConfirm]
  );

  // Handle recent name click
  const handleRecentClick = useCallback(
    (name: string) => {
      // Add [H] prefix to distinguish human assignees
      onConfirm(`[H] ${name}`);
    },
    [onConfirm]
  );

  // Prevent clicks inside modal from closing it
  const handleModalClick = useCallback((event: React.MouseEvent) => {
    event.stopPropagation();
  }, []);

  const isInputEmpty = !inputValue.trim();

  const overlayClassName = [styles.overlay, isOpen && styles.open]
    .filter(Boolean)
    .join(' ');

  return (
    <div
      className={overlayClassName}
      aria-hidden={!isOpen}
      data-testid="assignee-prompt-overlay"
    >
      <div
        ref={modalRef}
        className={styles.modal}
        onClick={handleModalClick}
        role="dialog"
        aria-modal="true"
        aria-labelledby="assignee-prompt-title"
        tabIndex={-1}
        data-testid="assignee-prompt-modal"
      >
        <div className={styles.header}>
          <h2 id="assignee-prompt-title" className={styles.title}>
            Who is working on this?
          </h2>
          <p className={styles.subtitle}>
            Enter your name to claim this task
          </p>
        </div>

        <form onSubmit={handleSubmit}>
          <div className={styles.content}>
            {/* Recent names quick select */}
            {recentNames.length > 0 && (
              <div className={styles.recentSection}>
                <span className={styles.recentLabel}>Recent</span>
                <div className={styles.recentList}>
                  {recentNames.map((name) => (
                    <button
                      key={name}
                      type="button"
                      className={styles.recentButton}
                      onClick={() => handleRecentClick(name)}
                      data-testid={`recent-name-${name}`}
                    >
                      {name}
                    </button>
                  ))}
                </div>
              </div>
            )}

            {/* Name input */}
            <div className={styles.inputGroup}>
              <label htmlFor="assignee-name" className={styles.label}>
                {recentNames.length > 0 ? 'Or enter a new name' : 'Your name'}
              </label>
              <input
                ref={inputRef}
                id="assignee-name"
                type="text"
                className={styles.input}
                value={inputValue}
                onChange={(e) => setInputValue(e.target.value)}
                placeholder="e.g., Tyson"
                autoComplete="off"
                data-testid="assignee-name-input"
              />
            </div>
          </div>

          <div className={styles.footer}>
            <button
              type="button"
              className={styles.buttonSecondary}
              onClick={onSkip}
              data-testid="assignee-skip-button"
            >
              Skip
            </button>
            <button
              type="submit"
              className={styles.buttonPrimary}
              disabled={isInputEmpty}
              data-testid="assignee-confirm-button"
            >
              Assign
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
