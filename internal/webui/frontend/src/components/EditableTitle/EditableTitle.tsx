/**
 * EditableTitle component.
 * Inline editable heading that switches between display and edit modes.
 */

import { useState, useRef, useEffect, useCallback, type KeyboardEvent } from 'react';
import styles from './EditableTitle.module.css';

export interface EditableTitleProps {
  /** Current title value */
  title: string;
  /** Callback when title is saved - receives new title, should throw on error */
  onSave: (newTitle: string) => Promise<void>;
  /** Whether saving is in progress */
  isSaving?: boolean;
  /** Whether editing is disabled */
  disabled?: boolean;
  /** Additional CSS class name */
  className?: string;
}

export function EditableTitle({
  title,
  onSave,
  isSaving = false,
  disabled = false,
  className,
}: EditableTitleProps): JSX.Element {
  const [isEditing, setIsEditing] = useState(false);
  const [draftTitle, setDraftTitle] = useState(title);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  // Sync draft title when prop changes (e.g., from server response)
  useEffect(() => {
    if (!isEditing) {
      setDraftTitle(title);
    }
  }, [title, isEditing]);

  // Focus input when entering edit mode
  useEffect(() => {
    if (isEditing && inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, [isEditing]);

  const enterEditMode = useCallback(() => {
    if (disabled || isSaving) return;
    setIsEditing(true);
    setDraftTitle(title);
    setError(null);
  }, [disabled, isSaving, title]);

  const cancelEdit = useCallback(() => {
    setIsEditing(false);
    setDraftTitle(title);
    setError(null);
  }, [title]);

  const saveTitle = useCallback(async () => {
    const trimmedTitle = draftTitle.trim();

    // Validate: non-empty
    if (!trimmedTitle) {
      setError('Title cannot be empty');
      // Use setTimeout to avoid focus/blur race condition
      setTimeout(() => inputRef.current?.focus(), 0);
      return;
    }

    // Skip save if unchanged
    if (trimmedTitle === title) {
      setIsEditing(false);
      return;
    }

    setError(null);

    try {
      await onSave(trimmedTitle);
      setIsEditing(false);
    } catch (err) {
      // Show error to user and stay in edit mode
      const message = err instanceof Error ? err.message : 'Failed to save title';
      setError(message);
      // Use setTimeout to avoid focus/blur race condition
      setTimeout(() => inputRef.current?.focus(), 0);
    }
  }, [draftTitle, title, onSave]);

  const handleKeyDown = useCallback((e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      saveTitle();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      cancelEdit();
    }
  }, [saveTitle, cancelEdit]);

  const handleDisplayKeyDown = useCallback((e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      enterEditMode();
    }
  }, [enterEditMode]);

  const rootClassName = [styles.editableTitle, className].filter(Boolean).join(' ');

  if (isEditing) {
    return (
      <div className={rootClassName} ref={containerRef}>
        <input
          ref={inputRef}
          type="text"
          className={styles.input}
          value={draftTitle}
          onChange={(e) => setDraftTitle(e.target.value)}
          onBlur={saveTitle}
          onKeyDown={handleKeyDown}
          disabled={isSaving}
          aria-label="Edit issue title"
          data-testid="editable-title-input"
        />
        {error && (
          <span className={styles.error} role="alert" data-testid="title-error">
            {error}
          </span>
        )}
        {isSaving && (
          <span className={styles.saving} data-testid="title-saving">
            Saving...
          </span>
        )}
      </div>
    );
  }

  return (
    <div
      className={rootClassName}
      ref={containerRef}
      onClick={enterEditMode}
      onKeyDown={handleDisplayKeyDown}
      role="button"
      tabIndex={disabled ? -1 : 0}
      aria-label={`${title}. Click to edit.`}
      data-testid="editable-title-display"
    >
      <h2 className={styles.title}>
        {title}
        {!disabled && (
          <span className={styles.editHint} aria-hidden="true">
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
              <path
                d="M10.5 1.5L12.5 3.5L4 12H2V10L10.5 1.5Z"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
          </span>
        )}
      </h2>
    </div>
  );
}
