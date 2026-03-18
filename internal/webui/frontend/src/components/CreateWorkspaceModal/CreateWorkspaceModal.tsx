/**
 * CreateWorkspaceModal component.
 * Modal dialog for creating a new workspace (Empty or Clone).
 * Renders via React portal above all other content.
 */

import { useState, useRef, useCallback, useEffect } from "react";
import { createPortal } from "react-dom";

import { createWorkspace } from "@/api/workspace";
import type { CreateWorkspaceRequest, WorkspaceData } from "@/api/workspace";
import { useRegisterEscapeLayer, LAYER_MODAL } from "@/hooks";
import { useFocusTrap } from "@/hooks/useFocusTrap";
import { useFocusReturn } from "@/hooks/useFocusReturn";
import styles from "./CreateWorkspaceModal.module.css";

type WorkspaceType = "empty" | "clone" | "template";

export interface CreateWorkspaceModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess: (data: WorkspaceData) => void;
}

export function CreateWorkspaceModal({
  isOpen,
  onClose,
  onSuccess,
}: CreateWorkspaceModalProps): JSX.Element | null {
  const [name, setName] = useState("");
  const [type, setType] = useState<WorkspaceType>("empty");
  const [cloneUrl, setCloneUrl] = useState("");
  const [path, setPath] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState("");

  const dialogRef = useRef<HTMLDivElement>(null);
  const nameRef = useRef<HTMLInputElement>(null);

  // Reset form state when modal opens
  useEffect(() => {
    if (isOpen) {
      setName("");
      setType("empty");
      setCloneUrl("");
      setPath("");
      setIsSubmitting(false);
      setError("");
    }
  }, [isOpen]);

  // Auto-focus name input on open
  useEffect(() => {
    if (isOpen && nameRef.current) {
      nameRef.current.focus();
    }
  }, [isOpen]);

  useRegisterEscapeLayer(LAYER_MODAL, onClose, isOpen);
  useFocusTrap(dialogRef, isOpen, { initialFocus: nameRef });
  useFocusReturn(isOpen);

  const canSubmit =
    name.trim() !== "" &&
    !isSubmitting &&
    (type !== "clone" || cloneUrl.trim() !== "");

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      if (!canSubmit) return;

      setIsSubmitting(true);
      setError("");

      const req: CreateWorkspaceRequest = {
        name: name.trim(),
        type,
      };

      if (type === "clone") {
        req.clone_url = cloneUrl.trim();
      }

      if (path.trim()) {
        req.path = path.trim();
      }

      try {
        const data = await createWorkspace(req);
        onSuccess(data);
        onClose();
      } catch (err: unknown) {
        const message =
          err instanceof Error ? err.message : "Failed to create workspace";
        // Extract server error message from ApiError body
        if (
          typeof err === "object" &&
          err !== null &&
          "body" in err &&
          typeof (err as { body: unknown }).body === "object" &&
          (err as { body: { error?: string } }).body?.error
        ) {
          setError((err as { body: { error: string } }).body.error);
        } else {
          setError(message);
        }
      } finally {
        setIsSubmitting(false);
      }
    },
    [canSubmit, name, type, cloneUrl, path, onSuccess, onClose],
  );

  if (!isOpen) return null;

  return createPortal(
    <div
      className={styles.overlay}
      onClick={onClose}
      data-testid="create-workspace-overlay"
    >
      <div
        ref={dialogRef}
        className={styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-label="Create Workspace"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className={styles.title}>Create Workspace</h2>

        <form onSubmit={handleSubmit}>
          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="ws-name">
              Name
            </label>
            <input
              ref={nameRef}
              id="ws-name"
              className={styles.input}
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-workspace"
              disabled={isSubmitting}
              data-testid="create-workspace-name"
            />
          </div>

          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="ws-path">
              Location
            </label>
            <input
              id="ws-path"
              className={styles.input}
              type="text"
              value={path}
              onChange={(e) => setPath(e.target.value)}
              placeholder={
                name.trim()
                  ? `~/.loom/workspaces/${name.trim()}`
                  : "~/.loom/workspaces/<name>"
              }
              disabled={isSubmitting}
              data-testid="create-workspace-path"
            />
          </div>

          <div className={styles.fieldGroup}>
            <span className={styles.label}>Type</span>
            <div className={styles.typeSelector}>
              <label className={styles.typeOption}>
                <input
                  type="radio"
                  name="ws-type"
                  value="empty"
                  checked={type === "empty"}
                  onChange={() => setType("empty")}
                  disabled={isSubmitting}
                />
                Empty
              </label>
              <label className={styles.typeOption}>
                <input
                  type="radio"
                  name="ws-type"
                  value="clone"
                  checked={type === "clone"}
                  onChange={() => setType("clone")}
                  disabled={isSubmitting}
                />
                Clone
              </label>
              <label
                className={`${styles.typeOption} ${styles.typeOptionDisabled}`}
              >
                <input
                  type="radio"
                  name="ws-type"
                  value="template"
                  disabled
                  data-testid="create-workspace-template-radio"
                />
                Template
              </label>
            </div>
          </div>

          {type === "clone" && (
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="ws-clone-url">
                Repository URL
              </label>
              <input
                id="ws-clone-url"
                className={styles.input}
                type="text"
                value={cloneUrl}
                onChange={(e) => setCloneUrl(e.target.value)}
                placeholder="https://github.com/... or git@..."
                disabled={isSubmitting}
                data-testid="create-workspace-clone-url"
              />
            </div>
          )}

          {error && (
            <p className={styles.error} data-testid="create-workspace-error">
              {error}
            </p>
          )}

          <div className={styles.actions}>
            <button
              type="button"
              className={styles.cancelButton}
              onClick={onClose}
              disabled={isSubmitting}
              data-testid="create-workspace-cancel"
            >
              Cancel
            </button>
            <button
              type="submit"
              className={styles.submitButton}
              disabled={!canSubmit}
              data-testid="create-workspace-submit"
            >
              {isSubmitting ? "Creating..." : "Create Workspace"}
            </button>
          </div>
        </form>
      </div>
    </div>,
    document.body,
  );
}
