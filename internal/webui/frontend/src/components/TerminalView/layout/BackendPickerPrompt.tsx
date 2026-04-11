/**
 * BackendPickerPrompt component.
 * Modal that prompts users to select a backend when creating a new terminal tab.
 * Replaces SessionNamePrompt with a dropdown of available backends.
 */

import { useState, useEffect, useRef, useCallback } from "react";

import { KNOWN_BACKEND_DEFAULTS } from "@/utils/backendDefaults";
import { useRegisterEscapeLayer, LAYER_MODAL } from "@/hooks";
import styles from "./BackendPickerPrompt.module.css";

export interface BackendPickerPromptProps {
  isOpen: boolean;
  availableBackends: string[];
  isLoading: boolean;
  onSelect: (backend: string) => void;
  onCancel: () => void;
}

export function BackendPickerPrompt({
  isOpen,
  availableBackends,
  isLoading,
  onSelect,
  onCancel,
}: BackendPickerPromptProps): JSX.Element {
  const [selected, setSelected] = useState("");
  const selectRef = useRef<HTMLSelectElement>(null);
  const modalRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (isOpen) {
      // Default to the first available backend
      setSelected(availableBackends[0] ?? "");
      const timer = setTimeout(() => {
        selectRef.current?.focus();
      }, 100);
      return () => clearTimeout(timer);
    }
  }, [isOpen, availableBackends]);

  // Escape key via global shortcut layer system
  useRegisterEscapeLayer(LAYER_MODAL, onCancel, isOpen);

  const handleSubmit = useCallback(
    (event: React.FormEvent) => {
      event.preventDefault();
      if (selected) {
        onSelect(selected);
      }
    },
    [selected, onSelect],
  );

  const handleModalClick = useCallback((event: React.MouseEvent) => {
    event.stopPropagation();
  }, []);

  const isEmpty = availableBackends.length === 0;
  const isDisabled = isLoading || isEmpty || !selected;

  const overlayClassName = [styles.overlay, isOpen && styles.open]
    .filter(Boolean)
    .join(" ");

  return (
    <div
      className={overlayClassName}
      aria-hidden={!isOpen}
      data-testid="backend-picker-prompt-overlay"
    >
      <div
        ref={modalRef}
        className={styles.modal}
        onClick={handleModalClick}
        role="dialog"
        aria-modal="true"
        aria-labelledby="backend-picker-prompt-title"
        tabIndex={-1}
        data-testid="backend-picker-prompt-modal"
      >
        <div className={styles.header}>
          <h2 id="backend-picker-prompt-title" className={styles.title}>
            New Terminal Session
          </h2>
          <p className={styles.subtitle}>
            Select a backend for the new session
          </p>
        </div>

        <form onSubmit={handleSubmit}>
          <div className={styles.content}>
            {isLoading ? (
              <p
                className={styles.loadingText}
                data-testid="backend-picker-loading"
              >
                Loading backends...
              </p>
            ) : isEmpty ? (
              <p
                className={styles.emptyText}
                data-testid="backend-picker-empty"
              >
                No backends available
              </p>
            ) : (
              <div className={styles.selectGroup}>
                <label htmlFor="backend-select" className={styles.label}>
                  Backend
                </label>
                <select
                  ref={selectRef}
                  id="backend-select"
                  className={styles.select}
                  value={selected}
                  onChange={(e) => setSelected(e.target.value)}
                  data-testid="backend-picker-select"
                >
                  {availableBackends.map((b) => (
                    <option key={b} value={b}>
                      {KNOWN_BACKEND_DEFAULTS[b]?.displayName ?? b}
                    </option>
                  ))}
                </select>
              </div>
            )}
          </div>

          <div className={styles.footer}>
            <button
              type="button"
              className={styles.buttonSecondary}
              onClick={onCancel}
              data-testid="backend-picker-cancel-button"
            >
              Cancel
            </button>
            <button
              type="submit"
              className={styles.buttonPrimary}
              disabled={isDisabled}
              data-testid="backend-picker-create-button"
            >
              Create
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
