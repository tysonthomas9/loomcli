/**
 * InlineAddInput - Shared inline input for creating new tasks/epics in the sidebar tree.
 * Auto-focuses on mount, submits on Enter, cancels on Escape or empty blur.
 */

import { useRef, useEffect, useState } from "react";

import styles from "./EpicTaskTree.module.css";

export interface InlineAddInputProps {
  placeholder: string;
  onSubmit: (title: string) => Promise<void>;
  onCancel: () => void;
  isSubmitting: boolean;
  error: string | null;
  className?: string | undefined;
}

export function InlineAddInput({
  placeholder,
  onSubmit,
  onCancel,
  isSubmitting,
  error,
  className,
}: InlineAddInputProps): JSX.Element {
  const inputRef = useRef<HTMLInputElement>(null);
  const [value, setValue] = useState("");

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Enter") {
      e.preventDefault();
      const trimmed = value.trim();
      if (trimmed) {
        onSubmit(trimmed);
      }
    } else if (e.key === "Escape") {
      e.preventDefault();
      onCancel();
    }
  };

  const handleBlur = () => {
    if (isSubmitting) return;
    const trimmed = value.trim();
    if (trimmed) {
      onSubmit(trimmed);
    } else {
      onCancel();
    }
  };

  return (
    <div className={`${styles.inlineAddInput} ${className ?? ""}`}>
      <input
        ref={inputRef}
        type="text"
        placeholder={placeholder}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={handleKeyDown}
        onBlur={handleBlur}
        disabled={isSubmitting}
        data-testid="inline-add-input"
      />
      {isSubmitting && (
        <span className={styles.inlineAddSaving}>Creating...</span>
      )}
      {error && (
        <span className={styles.inlineAddError} role="alert">
          {error}
        </span>
      )}
    </div>
  );
}
