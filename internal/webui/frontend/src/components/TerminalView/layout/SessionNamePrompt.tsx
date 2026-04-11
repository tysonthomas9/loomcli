/**
 * SessionNamePrompt component.
 * Modal that prompts for a session name when creating a new terminal tab.
 * Validates the name against the backend's validSessionName regex.
 */

import { useState, useEffect, useRef, useCallback } from "react";

import { useRegisterEscapeLayer, LAYER_MODAL } from "@/hooks";
import styles from "./SessionNamePrompt.module.css";

const VALID_SESSION_NAME = /^[a-zA-Z0-9_-]+$/;

export interface SessionNamePromptProps {
  isOpen: boolean;
  existingNames: string[];
  onConfirm: (name: string) => void;
  onCancel: () => void;
}

export function SessionNamePrompt({
  isOpen,
  existingNames,
  onConfirm,
  onCancel,
}: SessionNamePromptProps): JSX.Element {
  const [inputValue, setInputValue] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  const modalRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (isOpen) {
      setInputValue("");
      const timer = setTimeout(() => {
        inputRef.current?.focus();
      }, 100);
      return () => clearTimeout(timer);
    }
  }, [isOpen]);

  // Escape key via global shortcut layer system
  useRegisterEscapeLayer(LAYER_MODAL, onCancel, isOpen);

  const handleSubmit = useCallback(
    (event: React.FormEvent) => {
      event.preventDefault();
      const trimmed = inputValue.trim();
      if (
        trimmed &&
        VALID_SESSION_NAME.test(trimmed) &&
        !existingNames.includes(trimmed)
      ) {
        onConfirm(trimmed);
      }
    },
    [inputValue, onConfirm, existingNames],
  );

  const handleModalClick = useCallback((event: React.MouseEvent) => {
    event.stopPropagation();
  }, []);

  const trimmed = inputValue.trim();
  const isEmpty = !trimmed;
  const isInvalidChars = !isEmpty && !VALID_SESSION_NAME.test(trimmed);
  const isDuplicate =
    !isEmpty && !isInvalidChars && existingNames.includes(trimmed);
  const hasError = isInvalidChars || isDuplicate;
  const isDisabled = isEmpty || hasError;

  let errorMessage = "";
  if (isInvalidChars) {
    errorMessage =
      "Only letters, numbers, hyphens, and underscores are allowed";
  } else if (isDuplicate) {
    errorMessage = "Session already exists";
  }

  const overlayClassName = [styles.overlay, isOpen && styles.open]
    .filter(Boolean)
    .join(" ");

  const inputClassName = [styles.input, hasError && styles.inputError]
    .filter(Boolean)
    .join(" ");

  return (
    <div
      className={overlayClassName}
      aria-hidden={!isOpen}
      data-testid="session-name-prompt-overlay"
    >
      <div
        ref={modalRef}
        className={styles.modal}
        onClick={handleModalClick}
        role="dialog"
        aria-modal="true"
        aria-labelledby="session-name-prompt-title"
        tabIndex={-1}
        data-testid="session-name-prompt-modal"
      >
        <div className={styles.header}>
          <h2 id="session-name-prompt-title" className={styles.title}>
            New Terminal Session
          </h2>
          <p className={styles.subtitle}>Enter a name for the new session</p>
        </div>

        <form onSubmit={handleSubmit}>
          <div className={styles.content}>
            <div className={styles.inputGroup}>
              <label htmlFor="session-name" className={styles.label}>
                Session name
              </label>
              <input
                ref={inputRef}
                id="session-name"
                type="text"
                className={inputClassName}
                value={inputValue}
                onChange={(e) => setInputValue(e.target.value)}
                placeholder="e.g. auth-redesign"
                autoComplete="off"
                data-testid="session-name-input"
              />
              {hasError && (
                <p
                  className={styles.errorText}
                  data-testid="session-name-error"
                >
                  {errorMessage}
                </p>
              )}
            </div>
          </div>

          <div className={styles.footer}>
            <button
              type="button"
              className={styles.buttonSecondary}
              onClick={onCancel}
              data-testid="session-name-cancel-button"
            >
              Cancel
            </button>
            <button
              type="submit"
              className={styles.buttonPrimary}
              disabled={isDisabled}
              data-testid="session-name-confirm-button"
            >
              Create
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
