/**
 * TargetBranchSelector - Inline editor for the target/integration branch.
 * Read-only in legacy mode, editable in workspace mode.
 */

import { useState, useCallback } from "react";

import styles from "./GitActionBar.module.css";

interface TargetBranchSelectorProps {
  currentTarget: string;
  isWorkspace: boolean;
  onUpdate: (branch: string) => Promise<void>;
  loading: boolean;
}

export function TargetBranchSelector({
  currentTarget,
  isWorkspace,
  onUpdate,
  loading,
}: TargetBranchSelectorProps): JSX.Element {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(currentTarget);

  const handleSubmit = useCallback(async () => {
    const trimmed = value.trim();
    if (trimmed && trimmed !== currentTarget) {
      await onUpdate(trimmed);
    }
    setEditing(false);
  }, [value, currentTarget, onUpdate]);

  const handleCancel = useCallback(() => {
    setValue(currentTarget);
    setEditing(false);
  }, [currentTarget]);

  if (!isWorkspace || !editing) {
    return (
      <span className={styles.targetSelector}>
        <span>{currentTarget}</span>
        {isWorkspace && (
          <button
            type="button"
            className={styles.targetBtnSmall}
            onClick={() => {
              setValue(currentTarget);
              setEditing(true);
            }}
            disabled={loading}
          >
            Change
          </button>
        )}
      </span>
    );
  }

  return (
    <span className={styles.targetSelector}>
      <input
        type="text"
        className={styles.targetInput}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        autoFocus
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            void handleSubmit();
          }
          if (e.key === "Escape") handleCancel();
        }}
        disabled={loading}
      />
      <button
        type="button"
        className={styles.targetBtnSmall}
        onClick={() => void handleSubmit()}
        disabled={loading || !value.trim()}
      >
        {loading ? "..." : "Save"}
      </button>
      <button
        type="button"
        className={styles.targetBtnSmall}
        onClick={handleCancel}
        disabled={loading}
      >
        Cancel
      </button>
    </span>
  );
}
