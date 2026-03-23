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
  onSuccess: (data: WorkspaceData, createdName: string) => void;
}

export function CreateWorkspaceModal({
  isOpen,
  onClose,
  onSuccess,
}: CreateWorkspaceModalProps): JSX.Element | null {
  const [name, setName] = useState("");
  const [type, setType] = useState<WorkspaceType>("clone");
  const [path, setPath] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState("");

  // Multi-URL input for clone type
  const [cloneUrls, setCloneUrls] = useState<string[]>([]);
  const [urlInput, setUrlInput] = useState("");

  // Multi-path input for empty type
  const [repos, setRepos] = useState<string[]>([]);
  const [repoInput, setRepoInput] = useState("");

  const dialogRef = useRef<HTMLDivElement>(null);
  const nameRef = useRef<HTMLInputElement>(null);

  // Reset form state when modal opens
  useEffect(() => {
    if (isOpen) {
      setName("");
      setType("clone");
      setPath("");
      setIsSubmitting(false);
      setError("");
      setCloneUrls([]);
      setUrlInput("");
      setRepos([]);
      setRepoInput("");
    }
  }, [isOpen]);

  // Auto-focus name input on open
  useEffect(() => {
    if (isOpen && nameRef.current) {
      nameRef.current.focus();
    }
  }, [isOpen]);

  const guardedClose = useCallback(() => {
    if (!isSubmitting) onClose();
  }, [isSubmitting, onClose]);
  useRegisterEscapeLayer(LAYER_MODAL, guardedClose, isOpen);
  useFocusTrap(dialogRef, isOpen, { initialFocus: nameRef });
  useFocusReturn(isOpen);

  const handleTypeChange = (newType: WorkspaceType) => {
    setType(newType);
    setError("");
  };

  const addCloneUrl = () => {
    const trimmed = urlInput.trim();
    if (trimmed && !cloneUrls.includes(trimmed)) {
      setCloneUrls((prev) => [...prev, trimmed]);
      setUrlInput("");
    }
  };

  const removeCloneUrl = (url: string) => {
    setCloneUrls((prev) => prev.filter((u) => u !== url));
  };

  const addRepo = () => {
    const trimmed = repoInput.trim();
    if (trimmed && !repos.includes(trimmed)) {
      setRepos((prev) => [...prev, trimmed]);
      setRepoInput("");
    }
  };

  const removeRepo = (repo: string) => {
    setRepos((prev) => prev.filter((r) => r !== repo));
  };

  // Include pending input text in submit eligibility check
  const hasPendingUrl = type === "clone" && urlInput.trim() !== "";
  const hasPendingRepo = type === "empty" && repoInput.trim() !== "";
  const canSubmit =
    name.trim() !== "" &&
    !isSubmitting &&
    (type !== "clone" || cloneUrls.length > 0 || hasPendingUrl) &&
    (type !== "empty" || repos.length > 0 || hasPendingRepo);

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      if (!canSubmit) return;

      setIsSubmitting(true);
      setError("");

      // Auto-add any pending URL/repo input before submitting
      let finalCloneUrls = cloneUrls;
      if (type === "clone" && urlInput.trim()) {
        const trimmed = urlInput.trim();
        if (!cloneUrls.includes(trimmed)) {
          finalCloneUrls = [...cloneUrls, trimmed];
        }
        setUrlInput("");
      }
      let finalRepos = repos;
      if (type === "empty" && repoInput.trim()) {
        const trimmed = repoInput.trim();
        if (!repos.includes(trimmed)) {
          finalRepos = [...repos, trimmed];
        }
        setRepoInput("");
      }

      const req: CreateWorkspaceRequest = {
        name: name.trim(),
        type,
      };

      if (type === "clone") {
        req.clone_urls = finalCloneUrls;
      }

      if (type === "empty") {
        req.repos = finalRepos;
      }

      if (path.trim()) {
        req.path = path.trim();
      }

      try {
        const data = await createWorkspace(req);
        try {
          onSuccess(data, req.name);
        } catch {
          // onSuccess side effects (navigation) may fail — still close
        }
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
    [
      canSubmit,
      name,
      type,
      cloneUrls,
      urlInput,
      repos,
      repoInput,
      path,
      onSuccess,
      onClose,
    ],
  );

  if (!isOpen) return null;

  return createPortal(
    <div
      className={styles.overlay}
      onClick={() => { if (!isSubmitting) onClose(); }}
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
                  value="clone"
                  checked={type === "clone"}
                  onChange={() => handleTypeChange("clone")}
                  disabled={isSubmitting}
                />
                Clone
              </label>
              <label className={styles.typeOption}>
                <input
                  type="radio"
                  name="ws-type"
                  value="empty"
                  checked={type === "empty"}
                  onChange={() => handleTypeChange("empty")}
                  disabled={isSubmitting}
                />
                Local Repos
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
                Repository URLs
              </label>
              <div className={styles.addRow}>
                <input
                  id="ws-clone-url"
                  className={styles.input}
                  type="text"
                  value={urlInput}
                  onChange={(e) => setUrlInput(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      addCloneUrl();
                    }
                  }}
                  placeholder="https://github.com/... or git@..."
                  disabled={isSubmitting}
                  data-testid="create-workspace-clone-url"
                />
                <button
                  type="button"
                  className={styles.addButton}
                  onClick={addCloneUrl}
                  disabled={isSubmitting || !urlInput.trim()}
                >
                  Add
                </button>
              </div>
              {cloneUrls.length > 0 && (
                <div className={styles.chipList}>
                  {cloneUrls.map((url) => (
                    <span key={url} className={styles.chip}>
                      <span className={styles.chipText}>{url}</span>
                      <button
                        type="button"
                        className={styles.chipRemove}
                        onClick={() => removeCloneUrl(url)}
                        aria-label={`Remove ${url}`}
                      >
                        &times;
                      </button>
                    </span>
                  ))}
                </div>
              )}
            </div>
          )}

          {type === "empty" && (
            <div className={styles.fieldGroup}>
              <label className={styles.label} htmlFor="ws-repo-path">
                Repository Paths
              </label>
              <div className={styles.addRow}>
                <input
                  id="ws-repo-path"
                  className={styles.input}
                  type="text"
                  value={repoInput}
                  onChange={(e) => setRepoInput(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      addRepo();
                    }
                  }}
                  placeholder="/path/to/existing/repo"
                  disabled={isSubmitting}
                  data-testid="create-workspace-repo-path"
                />
                <button
                  type="button"
                  className={styles.addButton}
                  onClick={addRepo}
                  disabled={isSubmitting || !repoInput.trim()}
                >
                  Add
                </button>
              </div>
              {repos.length > 0 && (
                <div className={styles.chipList}>
                  {repos.map((repo) => (
                    <span key={repo} className={styles.chip}>
                      <span className={styles.chipText}>{repo}</span>
                      <button
                        type="button"
                        className={styles.chipRemove}
                        onClick={() => removeRepo(repo)}
                        aria-label={`Remove ${repo}`}
                      >
                        &times;
                      </button>
                    </span>
                  ))}
                </div>
              )}
            </div>
          )}

          {isSubmitting && (
            <p className={styles.statusMessage} data-testid="create-workspace-status">
              {type === "clone" ? "Cloning repository\u2026" : "Setting up workspace\u2026"}
            </p>
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
              {isSubmitting ? (
                <>
                  <span className={styles.spinner} />
                  Creating...
                </>
              ) : (
                "Create Workspace"
              )}
            </button>
          </div>
        </form>
      </div>
    </div>,
    document.body,
  );
}
