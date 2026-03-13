/**
 * Collapsible notes bar for a single terminal tab.
 * Collapsed: one-line summary (or "Add notes..." placeholder).
 * Expanded: textarea with auto-save on debounce/blur.
 */

import { useState, useRef, useEffect, useCallback } from "react";

import styles from "./NotesBar.module.css";

export interface NotesBarProps {
  notes: string;
  onSave: (text: string) => Promise<void>;
  isLoading?: boolean;
}

const DEBOUNCE_MS = 1000;

export function NotesBar({
  notes,
  onSave,
  isLoading,
}: NotesBarProps): JSX.Element {
  const [isExpanded, setIsExpanded] = useState(false);
  const [draft, setDraft] = useState(notes);
  const [isSaving, setIsSaving] = useState(false);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const isExpandedRef = useRef(isExpanded);
  const lastSavedRef = useRef(notes);
  const mountedRef = useRef(true);
  isExpandedRef.current = isExpanded;

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Sync prop → draft when collapsed (not editing)
  useEffect(() => {
    if (!isExpandedRef.current) {
      setDraft(notes);
    }
    lastSavedRef.current = notes;
  }, [notes]);

  const save = useCallback(
    async (text: string) => {
      const trimmed = text.trim();
      if (trimmed === lastSavedRef.current) return;
      lastSavedRef.current = trimmed;
      setIsSaving(true);
      try {
        await onSave(trimmed);
      } catch {
        // onSave (useTerminalMetadata.updateNotes) handles rollback
      } finally {
        if (mountedRef.current) {
          setIsSaving(false);
        }
      }
    },
    [onSave],
  );

  // Debounced auto-save on draft change while expanded
  useEffect(() => {
    if (!isExpanded) return;
    if (draft.trim() === lastSavedRef.current) return;

    debounceRef.current = setTimeout(() => {
      void save(draft);
    }, DEBOUNCE_MS);

    return () => {
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
        debounceRef.current = null;
      }
    };
  }, [draft, isExpanded, save]);

  const handleExpand = useCallback(() => {
    setDraft(notes);
    setIsExpanded(true);
  }, [notes]);

  // Auto-focus textarea on expand
  useEffect(() => {
    if (isExpanded && textareaRef.current) {
      textareaRef.current.focus();
    }
  }, [isExpanded]);

  const handleBlur = useCallback(() => {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current);
      debounceRef.current = null;
    }
    if (draft.trim() !== lastSavedRef.current) {
      void save(draft);
    }
  }, [draft, save]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === "Escape") {
        e.preventDefault();
        setDraft(notes);
        setIsExpanded(false);
        return;
      }
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        if (debounceRef.current) {
          clearTimeout(debounceRef.current);
          debounceRef.current = null;
        }
        void save(draft);
        setIsExpanded(false);
      }
    },
    [draft, notes, save],
  );

  if (isExpanded) {
    return (
      <div className={styles.notesBar} data-testid="notes-bar">
        <div className={styles.expanded}>
          <textarea
            ref={textareaRef}
            className={styles.textarea}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onBlur={handleBlur}
            onKeyDown={handleKeyDown}
            placeholder="Session notes..."
            rows={3}
            data-testid="notes-bar-textarea"
          />
          <div className={styles.hint}>
            {isSaving ? (
              "Saving..."
            ) : (
              <>
                Esc to cancel &middot;{" "}
                {/(Mac|iPhone|iPad)/i.test(navigator.userAgent)
                  ? "\u2318"
                  : "Ctrl"}
                +Enter to save
              </>
            )}
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className={styles.notesBar} data-testid="notes-bar">
      <div
        className={styles.collapsed}
        onClick={handleExpand}
        role="button"
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            handleExpand();
          }
        }}
      >
        <svg
          className={styles.noteIcon}
          viewBox="0 0 16 16"
          fill="currentColor"
          aria-hidden="true"
        >
          <path d="M3 2h10a1 1 0 011 1v10a1 1 0 01-1 1H3a1 1 0 01-1-1V3a1 1 0 011-1zm1 2v1h8V4H4zm0 2.5v1h8v-1H4zm0 2.5v1h5V9H4z" />
        </svg>
        <span
          className={`${styles.summaryText} ${!notes ? styles.placeholder : ""}`}
          data-testid="notes-bar-summary"
        >
          {notes || "Add notes..."}
        </span>
        {isSaving && <span className={styles.savingIndicator}>Saving...</span>}
        {isLoading && !isSaving && (
          <span className={styles.savingIndicator}>Loading...</span>
        )}
      </div>
    </div>
  );
}
