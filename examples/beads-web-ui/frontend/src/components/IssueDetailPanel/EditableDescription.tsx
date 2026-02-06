/**
 * EditableDescription component.
 * Editable description field with markdown preview.
 */

import { useState, useRef, useEffect, useCallback, type KeyboardEvent } from 'react';

import styles from './EditableDescription.module.css';
import { MarkdownRenderer } from './MarkdownRenderer';

export interface EditableDescriptionProps {
  /** Current description value */
  description: string | undefined;
  /** Whether editing is enabled */
  isEditable?: boolean;
  /** Callback when description is saved - receives new description, should throw on error */
  onSave: (newDescription: string) => Promise<void>;
  /** Additional CSS class name */
  className?: string;
}

/**
 * EditableDescription displays a description with markdown rendering.
 * When editable, allows switching to edit mode with a live preview.
 *
 * State machine:
 * VIEW -> (click edit) -> EDITING
 * EDITING -> (click save) -> SAVING -> VIEW (success) or EDITING (failure with error)
 * EDITING -> (click cancel/Escape) -> VIEW
 */
export function EditableDescription({
  description,
  isEditable = true,
  onSave,
  className,
}: EditableDescriptionProps): JSX.Element {
  const [isEditing, setIsEditing] = useState(false);
  const [draftDescription, setDraftDescription] = useState(description ?? '');
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const containerRef = useRef<HTMLDivElement>(null);

  // Sync draft description when prop changes (e.g., from server response)
  useEffect(() => {
    if (!isEditing) {
      setDraftDescription(description ?? '');
    }
  }, [description, isEditing]);

  // Focus textarea when entering edit mode
  useEffect(() => {
    if (isEditing && textareaRef.current) {
      textareaRef.current.focus();
      // Move cursor to end
      const len = textareaRef.current.value.length;
      textareaRef.current.setSelectionRange(len, len);
    }
  }, [isEditing]);

  // Auto-resize textarea based on content
  useEffect(() => {
    if (isEditing && textareaRef.current) {
      const textarea = textareaRef.current;
      textarea.style.height = 'auto';
      textarea.style.height = `${Math.max(150, textarea.scrollHeight)}px`;
    }
  }, [isEditing, draftDescription]);

  const enterEditMode = useCallback(() => {
    if (!isEditable || isSaving) return;
    setIsEditing(true);
    setDraftDescription(description ?? '');
    setError(null);
  }, [isEditable, isSaving, description]);

  const cancelEdit = useCallback(() => {
    setIsEditing(false);
    setDraftDescription(description ?? '');
    setError(null);
  }, [description]);

  const saveDescription = useCallback(async () => {
    const trimmedDescription = draftDescription.trim();

    // Skip save if unchanged (treat empty and undefined as equivalent)
    const currentDescription = description ?? '';
    if (trimmedDescription === currentDescription.trim()) {
      setIsEditing(false);
      return;
    }

    setError(null);
    setIsSaving(true);

    try {
      await onSave(trimmedDescription);
      setIsEditing(false);
    } catch (err) {
      // Show error to user and stay in edit mode
      const message = err instanceof Error ? err.message : 'Failed to save description';
      setError(message);
      // Use setTimeout to avoid focus/blur race condition
      setTimeout(() => textareaRef.current?.focus(), 0);
    } finally {
      setIsSaving(false);
    }
  }, [draftDescription, description, onSave]);

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLTextAreaElement>) => {
      // Cmd/Ctrl+Enter to save
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        saveDescription();
      } else if (e.key === 'Escape') {
        e.preventDefault();
        cancelEdit();
      }
    },
    [saveDescription, cancelEdit]
  );

  const rootClassName = [styles.editableDescription, className].filter(Boolean).join(' ');

  // Edit mode: textarea with preview
  if (isEditing) {
    return (
      <div className={rootClassName} ref={containerRef} data-testid="editable-description">
        <div className={styles.editContainer}>
          <textarea
            ref={textareaRef}
            className={styles.textarea}
            value={draftDescription}
            onChange={(e) => setDraftDescription(e.target.value)}
            onKeyDown={handleKeyDown}
            disabled={isSaving}
            placeholder="Enter description (supports markdown)"
            aria-label="Edit description"
            data-testid="description-textarea"
          />
          <div className={styles.previewContainer}>
            <span className={styles.previewLabel}>Preview</span>
            <MarkdownRenderer content={draftDescription || undefined} />
          </div>
        </div>
        {error && (
          <div className={styles.error} role="alert" data-testid="description-error">
            {error}
          </div>
        )}
        <div className={styles.actions}>
          <button
            type="button"
            className={styles.cancelButton}
            onClick={cancelEdit}
            disabled={isSaving}
            data-testid="description-cancel"
          >
            Cancel
          </button>
          <button
            type="button"
            className={styles.saveButton}
            onClick={saveDescription}
            disabled={isSaving}
            data-testid="description-save"
          >
            {isSaving ? 'Saving...' : 'Save'}
          </button>
        </div>
      </div>
    );
  }

  // View mode: rendered markdown with edit button on hover
  return (
    <div className={rootClassName} ref={containerRef} data-testid="editable-description">
      <div className={styles.viewContainer}>
        {description ? (
          <MarkdownRenderer content={description} />
        ) : (
          <p className={styles.placeholder}>No description. Click to add one.</p>
        )}
        {isEditable && (
          <button
            type="button"
            className={styles.editButton}
            onClick={enterEditMode}
            aria-label="Edit description"
            data-testid="description-edit-button"
          >
            <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
              <path
                d="M10.5 1.5L12.5 3.5L4 12H2V10L10.5 1.5Z"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            Edit
          </button>
        )}
      </div>
    </div>
  );
}
