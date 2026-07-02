/**
 * AgentConfigModal — Phase B agent-plane management surface.
 *
 * Opened from a custom-role agent, it loads that agent's Role (prompt + config)
 * via GET /roles/{name} and lets the user:
 *   - EDIT the prompt + description/model/read_only and Save via PATCH, and
 *   - CLONE the role under a new name, and
 *   - DELETE the role.
 *
 * HONESTY: a running agent reads its prompt once, at launch. After a Save we
 * tell the user the change takes effect on the agent's NEXT start/restart — we
 * never imply a live agent was hot-reloaded.
 */

import { useCallback, useEffect, useState } from "react";

import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { useRoleConfig } from "@/hooks/agents";
import { useToast } from "@/hooks/ui";
import type { WorkspaceRole } from "@/api/workspace";
import { ApiError, apiErrorMessage } from "@/types/common";

import styles from "./AgentConfigModal.module.css";

export interface AgentConfigModalProps {
  isOpen: boolean;
  workspaceId: string;
  /** Display name of the agent whose role is being edited. */
  agentName: string;
  /** Role name backing the agent (the roles API key). */
  roleName: string;
  onClose: () => void;
  /** Called after the role is deleted, so the opener can dismiss its surface. */
  onDeleted?: (roleName: string) => void;
  /** Called after a successful clone with the new role name. */
  onCloned?: (newRoleName: string) => void;
}

export function AgentConfigModal({
  isOpen,
  workspaceId,
  agentName,
  roleName,
  onClose,
  onDeleted,
  onCloned,
}: AgentConfigModalProps): JSX.Element | null {
  const { getRole, updateRole, cloneRole, deleteRole } =
    useRoleConfig(workspaceId);
  const { showToast } = useToast();

  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [role, setRole] = useState<WorkspaceRole | null>(null);

  // Editable fields.
  const [prompt, setPrompt] = useState("");
  const [description, setDescription] = useState("");
  const [model, setModel] = useState("");
  const [readOnly, setReadOnly] = useState(false);

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [savedNote, setSavedNote] = useState<string | null>(null);

  const [cloneName, setCloneName] = useState("");
  const [cloning, setCloning] = useState(false);
  const [cloneError, setCloneError] = useState<string | null>(null);

  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);

  // Load the role each time the modal opens (or the target role changes).
  useEffect(() => {
    if (!isOpen || !roleName) return;
    let cancelled = false;
    setLoading(true);
    setLoadError(null);
    setSaveError(null);
    setSavedNote(null);
    setCloneError(null);
    setDeleteError(null);
    setCloneName("");
    getRole(roleName)
      .then((res) => {
        if (cancelled) return;
        setRole(res.role);
        setPrompt(res.prompt ?? "");
        setDescription(res.role.description ?? "");
        setModel(res.role.model ?? "");
        setReadOnly(res.role.read_only ?? false);
      })
      .catch((err) => {
        if (cancelled) return;
        setRole(null);
        setLoadError(apiErrorMessage(err, "Failed to load role"));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [isOpen, roleName, getRole]);

  const handleSave = useCallback(async () => {
    setSaving(true);
    setSaveError(null);
    setSavedNote(null);
    try {
      const res = await updateRole(roleName, {
        prompt,
        description,
        model,
        read_only: readOnly,
      });
      setRole(res.role);
      setPrompt(res.prompt ?? prompt);
      // HONESTY: prompt/config is read at launch, not hot-reloaded.
      setSavedNote(
        `Saved. Prompt and configuration changes take effect on ${agentName}'s next start/restart — a running agent keeps the prompt it read at launch.`,
      );
      showToast(`Role "${roleName}" saved`, { type: "success" });
    } catch (err) {
      setSaveError(apiErrorMessage(err, "Failed to save role"));
    } finally {
      setSaving(false);
    }
  }, [
    updateRole,
    roleName,
    prompt,
    description,
    model,
    readOnly,
    agentName,
    showToast,
  ]);

  const handleClone = useCallback(async () => {
    const target = cloneName.trim();
    setCloneError(null);
    if (!target) {
      setCloneError("Enter a name for the clone.");
      return;
    }
    setCloning(true);
    try {
      const cloned = await cloneRole(roleName, { target_name: target });
      showToast(`Cloned to "${cloned.name}"`, { type: "success" });
      setCloneName("");
      onCloned?.(cloned.name);
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        setCloneError(`The name "${target}" is already taken.`);
      } else {
        setCloneError(apiErrorMessage(err, "Failed to clone role"));
      }
    } finally {
      setCloning(false);
    }
  }, [cloneName, cloneRole, roleName, showToast, onCloned]);

  const handleDelete = useCallback(async () => {
    setShowDeleteConfirm(false);
    setDeleting(true);
    setDeleteError(null);
    try {
      await deleteRole(roleName);
      showToast(`Role "${roleName}" deleted`, { type: "success" });
      onDeleted?.(roleName);
      onClose();
    } catch (err) {
      setDeleteError(apiErrorMessage(err, "Failed to delete role"));
    } finally {
      setDeleting(false);
    }
  }, [deleteRole, roleName, showToast, onDeleted, onClose]);

  const busy = saving || cloning || deleting;

  return (
    <AetherModal
      isOpen={isOpen}
      title={`Edit agent · ${roleName}`}
      ariaLabel={`Edit configuration for ${agentName}`}
      onClose={onClose}
      overlayTestId="agent-config-overlay"
      closeTestId="agent-config-close"
      dialogClassName={aetherModalStyles.dialogWide}
      footer={
        <button
          type="button"
          className={aetherModalStyles.linkButton}
          onClick={onClose}
          disabled={busy}
        >
          Close
        </button>
      }
    >
      <div className={styles.form}>
        {loading ? (
          <div className={styles.loading}>Loading role…</div>
        ) : loadError ? (
          <div
            className={styles.error}
            role="alert"
            data-testid="agent-config-load-error"
          >
            {loadError}
          </div>
        ) : role ? (
          <>
            {/* Edit prompt + config */}
            <section className={styles.section}>
              <h3 className={styles.sectionHeader}>Prompt &amp; configuration</h3>
              <p className={styles.sectionHint}>
                Editing role <strong>{roleName}</strong> used by agent{" "}
                <strong>{agentName}</strong>.
              </p>
              <div className={styles.row}>
                <div className={styles.field}>
                  <label
                    className={styles.label}
                    htmlFor="agent-config-description"
                  >
                    Description
                  </label>
                  <input
                    id="agent-config-description"
                    className={styles.input}
                    value={description}
                    onChange={(e) => setDescription(e.target.value)}
                    disabled={busy}
                    data-testid="agent-config-description"
                  />
                </div>
                <div className={styles.field}>
                  <label className={styles.label} htmlFor="agent-config-model">
                    Model
                  </label>
                  <input
                    id="agent-config-model"
                    className={styles.input}
                    value={model}
                    onChange={(e) => setModel(e.target.value)}
                    placeholder="backend default"
                    disabled={busy}
                    data-testid="agent-config-model"
                  />
                </div>
              </div>
              <div className={styles.field}>
                <label className={styles.checkboxRow}>
                  <input
                    type="checkbox"
                    checked={readOnly}
                    onChange={(e) => setReadOnly(e.target.checked)}
                    disabled={busy}
                    data-testid="agent-config-readonly"
                  />
                  Read-only (agent may inspect but not modify the repo)
                </label>
              </div>
              <div className={styles.field}>
                <label className={styles.label} htmlFor="agent-config-prompt">
                  Prompt
                </label>
                <textarea
                  id="agent-config-prompt"
                  className={styles.textarea}
                  value={prompt}
                  onChange={(e) => setPrompt(e.target.value)}
                  placeholder={
                    role.prompt_file
                      ? ""
                      : "This role has no prompt file yet — saving a prompt creates one."
                  }
                  disabled={busy}
                  spellCheck={false}
                  data-testid="agent-config-prompt"
                />
              </div>
              <div className={styles.actionRow}>
                <button
                  type="button"
                  className={styles.button}
                  onClick={handleSave}
                  disabled={busy}
                  data-testid="agent-config-save"
                >
                  {saving ? "Saving…" : "Save changes"}
                </button>
              </div>
              {savedNote && (
                <p
                  className={styles.note}
                  role="status"
                  data-testid="agent-config-saved-note"
                >
                  {savedNote}
                </p>
              )}
              {saveError && (
                <div
                  className={styles.error}
                  role="alert"
                  data-testid="agent-config-save-error"
                >
                  {saveError}
                </div>
              )}
            </section>

            {/* Clone */}
            <section className={styles.section}>
              <h3 className={styles.sectionHeader}>Clone</h3>
              <p className={styles.sectionHint}>
                Duplicate this role (config + prompt) under a new name. Edits to
                one do not affect the other.
              </p>
              <div className={styles.field}>
                <label
                  className={styles.label}
                  htmlFor="agent-config-clone-name"
                >
                  New role name
                </label>
                <input
                  id="agent-config-clone-name"
                  className={styles.input}
                  value={cloneName}
                  onChange={(e) => setCloneName(e.target.value)}
                  placeholder={`${roleName}-copy`}
                  disabled={busy}
                  data-testid="agent-config-clone-name"
                />
              </div>
              <div className={styles.actionRow}>
                <button
                  type="button"
                  className={styles.secondaryButton}
                  onClick={handleClone}
                  disabled={busy || cloneName.trim() === ""}
                  data-testid="agent-config-clone"
                >
                  {cloning ? "Cloning…" : "Clone role"}
                </button>
              </div>
              {cloneError && (
                <div
                  className={styles.error}
                  role="alert"
                  data-testid="agent-config-clone-error"
                >
                  {cloneError}
                </div>
              )}
            </section>

            {/* Delete */}
            <section className={`${styles.section} ${styles.dangerSection}`}>
              <h3 className={styles.sectionHeader}>Delete</h3>
              <p className={styles.sectionHint}>
                Permanently remove the <strong>{roleName}</strong> role. Agents
                still assigned to it will fail to start until reassigned.
              </p>
              <div className={styles.actionRow}>
                <button
                  type="button"
                  className={styles.dangerButton}
                  onClick={() => setShowDeleteConfirm(true)}
                  disabled={busy}
                  data-testid="agent-config-delete"
                >
                  {deleting ? "Deleting…" : "Delete role"}
                </button>
              </div>
              {deleteError && (
                <div
                  className={styles.error}
                  role="alert"
                  data-testid="agent-config-delete-error"
                >
                  {deleteError}
                </div>
              )}
            </section>
          </>
        ) : null}
      </div>

      <ConfirmDialog
        isOpen={showDeleteConfirm}
        title={`Delete role "${roleName}"?`}
        message={`This permanently removes the "${roleName}" role. This cannot be undone.`}
        confirmLabel="Delete"
        variant="danger"
        onConfirm={handleDelete}
        onCancel={() => setShowDeleteConfirm(false)}
      />
    </AetherModal>
  );
}
