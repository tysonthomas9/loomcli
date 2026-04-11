/**
 * CreateWorkspaceModal component.
 * Modal dialog for creating a new workspace (Empty, Clone, or Template).
 * Renders via React portal above all other content.
 */

import { useState, useRef, useCallback, useEffect } from "react";
import { createPortal } from "react-dom";

import { createWorkspace } from "@/hooks/api";
import type { CreateWorkspaceRequest, WorkspaceData } from "@/api/workspace";
import { useRegisterEscapeLayer, LAYER_MODAL, useJobPolling } from "@/hooks";
import { useFocusTrap, useFocusReturn } from "@/hooks/ui";
import styles from "./CreateWorkspaceModal.module.css";

type WorkspaceType = "empty" | "clone" | "template";

const TYPE_CARDS: {
  type: WorkspaceType;
  title: string;
  desc: string;
}[] = [
  { type: "empty", title: "Empty", desc: "New git repository from scratch" },
  { type: "clone", title: "Clone", desc: "Clone from a remote URL" },
  {
    type: "template",
    title: "Template",
    desc: "Start from a project template",
  },
];

function EmptyIcon() {
  return (
    <svg
      className={styles.typeCardIcon}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
      <line x1="12" y1="11" x2="12" y2="17" />
      <line x1="9" y1="14" x2="15" y2="14" />
    </svg>
  );
}

function CloneIcon() {
  return (
    <svg
      className={styles.typeCardIcon}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <line x1="6" y1="3" x2="6" y2="15" />
      <circle cx="18" cy="6" r="3" />
      <circle cx="6" cy="18" r="3" />
      <path d="M18 9a9 9 0 0 1-9 9" />
    </svg>
  );
}

function TemplateIcon() {
  return (
    <svg
      className={styles.typeCardIcon}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
      <polyline points="14 2 14 8 20 8" />
      <line x1="16" y1="13" x2="8" y2="13" />
      <line x1="16" y1="17" x2="8" y2="17" />
      <polyline points="10 9 9 9 8 9" />
    </svg>
  );
}

function FolderBrowseIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
    </svg>
  );
}

const ICON_MAP: Record<WorkspaceType, () => JSX.Element> = {
  empty: EmptyIcon,
  clone: CloneIcon,
  template: TemplateIcon,
};

export interface CreateWorkspaceModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess: (
    data: WorkspaceData,
    createdName: string,
    warnings?: string[],
  ) => void;
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

  // Async clone job polling
  const job = useJobPolling(name, {
    onSuccess,
    onClose,
    onFinish: () => setIsSubmitting(false),
  });

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
      job.reset();
    }
  }, [isOpen]); // eslint-disable-line react-hooks/exhaustive-deps -- job.reset is stable

  // Auto-focus name input on open
  useEffect(() => {
    if (isOpen && nameRef.current) {
      nameRef.current.focus();
    }
  }, [isOpen]);

  useRegisterEscapeLayer(LAYER_MODAL, onClose, isOpen && !job.isPolling);
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
  const canSubmit =
    name.trim() !== "" &&
    !isSubmitting &&
    type !== "template" &&
    (type !== "clone" || cloneUrls.length > 0 || hasPendingUrl);

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      if (!canSubmit) return;

      setIsSubmitting(true);
      setError("");

      // Auto-add any pending URL input before submitting
      let finalCloneUrls = cloneUrls;
      if (type === "clone" && urlInput.trim()) {
        const trimmed = urlInput.trim();
        if (!cloneUrls.includes(trimmed)) {
          finalCloneUrls = [...cloneUrls, trimmed];
        }
        setUrlInput("");
      }

      const req: CreateWorkspaceRequest = {
        name: name.trim(),
        type,
      };

      if (type === "clone") {
        req.clone_urls = finalCloneUrls;
      }

      if (path.trim()) {
        req.path = path.trim();
      }

      try {
        const result = await createWorkspace(req);
        if (result.kind === "async") {
          // Clone job started: switch to progress state and begin polling.
          // Keep isSubmitting true so the form stays disabled during polling.
          job.startJob(result.jobId);
          return;
        }
        // Sync path: workspace created immediately
        const warnings = result.warnings;
        setIsSubmitting(false);
        try {
          onSuccess(result.data, req.name, warnings);
        } catch {
          // onSuccess errors (e.g., navigation failure) must not block close
        }
        onClose();
      } catch (err: unknown) {
        setIsSubmitting(false);
        const message =
          err instanceof Error ? err.message : "Failed to create workspace";
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
      job.startJob,
    ],
  );

  const handleCardKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    const cards = TYPE_CARDS.map((c) => c.type);
    const currentIndex = cards.indexOf(type);
    let newIndex = currentIndex;

    if (e.key === "ArrowRight" || e.key === "ArrowDown") {
      e.preventDefault();
      newIndex = (currentIndex + 1) % cards.length;
    } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
      e.preventDefault();
      newIndex = (currentIndex - 1 + cards.length) % cards.length;
    } else if (e.key === " " || e.key === "Enter") {
      e.preventDefault();
      // Already selected by focus movement, but ensure it's set
      return;
    } else {
      return;
    }

    handleTypeChange(cards[newIndex]!);
    // Focus the new card
    const group = (e.currentTarget as HTMLElement).closest(
      '[role="radiogroup"]',
    );
    if (group) {
      const cardElements = group.querySelectorAll('[role="radio"]');
      (cardElements[newIndex] as HTMLElement)?.focus();
    }
  };

  if (!isOpen) return null;

  return createPortal(
    <div
      className={styles.overlay}
      onClick={job.isPolling ? undefined : onClose}
      data-testid="create-workspace-overlay"
    >
      <div
        ref={dialogRef}
        className={styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-label="New Workspace"
        onClick={(e) => e.stopPropagation()}
      >
        <div className={styles.headerRow}>
          <h2 className={styles.title}>
            {job.isPolling ? "Creating Workspace" : "New Workspace"}
          </h2>
          {!job.isPolling && (
            <button
              type="button"
              className={styles.closeButton}
              onClick={onClose}
              aria-label="Close"
              data-testid="create-workspace-close"
            >
              &times;
            </button>
          )}
        </div>

        {job.isPolling ? (
          <div
            className={styles.progressContainer}
            data-testid="create-workspace-progress"
          >
            <div className={styles.progressSpinner} aria-hidden="true" />
            <p className={styles.progressMessage}>{job.progress}</p>
            <p className={styles.progressElapsed}>{job.elapsed}</p>
          </div>
        ) : (
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
              <div className={styles.locationRow}>
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
                <button
                  type="button"
                  className={styles.browseButton}
                  disabled
                  title="Filesystem browsing is not available in the browser"
                  aria-label="Browse for folder"
                  data-testid="create-workspace-browse"
                >
                  <FolderBrowseIcon />
                </button>
              </div>
            </div>

            <div className={styles.fieldGroup}>
              <span className={styles.label}>Type</span>
              <div
                className={styles.typeSelector}
                role="radiogroup"
                aria-label="Workspace type"
              >
                {TYPE_CARDS.map((card) => {
                  const Icon = ICON_MAP[card.type];
                  const isSelected = type === card.type;
                  return (
                    <div
                      key={card.type}
                      className={`${styles.typeCard}${isSelected ? ` ${styles.typeCardSelected}` : ""}`}
                      role="radio"
                      aria-checked={isSelected}
                      tabIndex={isSelected ? 0 : -1}
                      onClick={() => handleTypeChange(card.type)}
                      onKeyDown={handleCardKeyDown}
                      data-testid={`create-workspace-type-${card.type}`}
                    >
                      <Icon />
                      <span className={styles.typeCardTitle}>{card.title}</span>
                      <span className={styles.typeCardDesc}>{card.desc}</span>
                    </div>
                  );
                })}
              </div>
            </div>

            {type === "clone" && (
              <div className={styles.fieldGroup}>
                <label className={styles.label} htmlFor="ws-clone-url">
                  Repository URL
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

            {type === "template" && (
              <div
                className={styles.comingSoon}
                data-testid="create-workspace-template-placeholder"
              >
                Coming soon — template registry is not yet available
              </div>
            )}

            {(error || job.error) && (
              <p className={styles.error} data-testid="create-workspace-error">
                {error || job.error}
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
                className={`${styles.submitButton}${isSubmitting ? ` ${styles.submitting}` : ""}`}
                disabled={!canSubmit}
                data-testid="create-workspace-submit"
              >
                {isSubmitting && (
                  <span
                    className={styles.spinner}
                    aria-hidden="true"
                    data-testid="create-workspace-spinner"
                  />
                )}
                {isSubmitting ? "Creating..." : "Create Workspace"}
              </button>
            </div>
          </form>
        )}
      </div>
    </div>,
    document.body,
  );
}
