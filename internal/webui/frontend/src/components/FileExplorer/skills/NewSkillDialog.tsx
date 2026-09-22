import { useState } from "react";

import type { SkillsScopeGroup } from "@/api/workspace";
import {
  validateSkillDescription,
  validateSkillName,
} from "@/utils/skillsPaths";

import styles from "../FileExplorer.module.css";

export function NewSkillDialog({
  group,
  onCancel,
  onConfirm,
}: {
  group: SkillsScopeGroup;
  onCancel: () => void;
  onConfirm: (input: { name: string; description: string }) => Promise<void>;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    const validation =
      validateSkillName(name.trim()) ??
      validateSkillDescription(description.trim());
    if (validation) {
      setError(validation);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await onConfirm({ name: name.trim(), description: description.trim() });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setSaving(false);
    }
  };

  return (
    <div className={styles.dialogOverlay}>
      <div
        className={styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-label="New skill"
      >
        <h2 className={styles.dialogTitle}>New skill</h2>
        <p className={styles.dialogMessage}>
          Scope:{" "}
          {group.kind === "workspace" ? "Workspace" : `Role: ${group.role}`}
        </p>
        <label className={styles.dialogField}>
          <span>Name</span>
          <input
            value={name}
            autoFocus
            placeholder="code-review"
            onChange={(event) => setName(event.target.value)}
          />
        </label>
        <label className={styles.dialogField}>
          <span>Description</span>
          <textarea
            value={description}
            rows={3}
            onChange={(event) => setDescription(event.target.value)}
          />
        </label>
        {error && <div className={styles.dialogError}>{error}</div>}
        <div className={styles.dialogActions}>
          <button
            type="button"
            className={styles.secondaryButton}
            disabled={saving}
            onClick={onCancel}
          >
            Cancel
          </button>
          <button
            type="button"
            className={styles.saveButton}
            disabled={saving}
            onClick={() => void submit()}
          >
            {saving ? "Creating..." : "Create"}
          </button>
        </div>
      </div>
    </div>
  );
}

export function DeleteSkillConfirmDialog({
  name,
  onCancel,
  onConfirm,
}: {
  name: string;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div className={styles.dialogOverlay}>
      <div className={styles.dialog} role="dialog" aria-modal="true">
        <p className={styles.dialogMessage}>
          Delete skill {name}? Agents already running keep their materialized
          copy until their next spawn.
        </p>
        <div className={styles.dialogActions}>
          <button
            type="button"
            className={styles.secondaryButton}
            onClick={onCancel}
          >
            Cancel
          </button>
          <button
            type="button"
            className={styles.dangerButton}
            onClick={onConfirm}
          >
            Delete
          </button>
        </div>
      </div>
    </div>
  );
}
