/**
 * AddRepoModal — "Add Repo" dialog.
 *
 * Replaces the old inline sidebar form with a focused two-field modal:
 * Repository URL + optional Default branch. Both are wired to the real
 * addWorkspaceRepos API (which accepts an optional `branch`). When omitted,
 * cloned repositories use their advertised remote HEAD. A clone-style
 * URL (https:// or git@) is sent as `clone_urls`; anything else is treated as
 * a local `repos` path, preserving loom's existing behaviour.
 */
import { useEffect, useRef, useState, type FormEvent } from "react";

import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import { useJobPolling } from "@/hooks";
import { addWorkspaceRepos } from "@/hooks/api";

import styles from "./AddRepoModal.module.css";

/** Matches the detection used by the legacy inline add-repo form. */
const CLONE_URL_RE = /^(https:\/\/|git@)/;

const ADD_REPO_JOB_MESSAGES = {
  initialProgress: "Cloning repository...",
  loadError:
    "Repository was added but the workspace failed to reload. Please refresh the page.",
  connectionError:
    "Lost connection while adding the repository. The clone may still be running.",
  terminalError: "Repository attachment failed",
};

export interface AddRepoModalProps {
  isOpen: boolean;
  workspaceId: string;
  onClose: () => void;
  /** Called after a repo is added so the caller can refetch the repo list. */
  onSuccess: () => void;
  /** Optional URL/path to seed the field with (e.g. onboarding sample repo). */
  initialUrl?: string;
}

export function AddRepoModal({
  isOpen,
  workspaceId,
  onClose,
  onSuccess,
  initialUrl = "",
}: AddRepoModalProps): JSX.Element | null {
  const [url, setUrl] = useState(initialUrl);
  const [branch, setBranch] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const urlRef = useRef<HTMLInputElement>(null);
  const wasOpenRef = useRef(false);
  const {
    isPolling,
    progress,
    elapsed,
    error: jobError,
    startJob,
    reset: resetJob,
  } = useJobPolling(
    workspaceId,
    {
      onSuccess: () => onSuccess(),
      onClose,
      onFinish: () => setIsSubmitting(false),
    },
    ADD_REPO_JOB_MESSAGES,
  );

  // Reset + focus whenever the dialog opens.
  useEffect(() => {
    if (!isOpen) {
      wasOpenRef.current = false;
      return;
    }
    if (wasOpenRef.current) return;
    wasOpenRef.current = true;

    setUrl(initialUrl);
    setBranch("");
    setError(null);
    setIsSubmitting(false);
    resetJob();
    const id = window.setTimeout(() => urlRef.current?.focus(), 0);
    return () => window.clearTimeout(id);
  }, [isOpen, initialUrl, resetJob]);

  const trimmedUrl = url.trim();
  const valid = trimmedUrl.length > 0 && workspaceId.length > 0;

  const handleSubmit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (!valid || isSubmitting) return;
    setIsSubmitting(true);
    setError(null);
    const trimmedBranch = branch.trim();
    try {
      const result = await addWorkspaceRepos(workspaceId, {
        ...(CLONE_URL_RE.test(trimmedUrl)
          ? { clone_urls: [trimmedUrl] }
          : { repos: [trimmedUrl] }),
        ...(trimmedBranch ? { branch: trimmedBranch } : {}),
      });
      if (result.kind === "async") {
        startJob(result.jobId);
        return;
      }
      setIsSubmitting(false);
      onSuccess();
      onClose();
    } catch (err) {
      setIsSubmitting(false);
      setError(err instanceof Error ? err.message : "Failed to add repository");
    }
  };

  const modalFooter = isPolling ? undefined : (
    <>
      <button
        type="button"
        className={aetherModalStyles.linkButton}
        onClick={onClose}
        disabled={isSubmitting}
      >
        Cancel
      </button>
      <button
        type="submit"
        form="add-repo-form"
        className={aetherModalStyles.primaryButton}
        disabled={isSubmitting || !valid}
      >
        {isSubmitting ? "Adding..." : "Add Repository"}
      </button>
    </>
  );

  return (
    <AetherModal
      isOpen={isOpen}
      title={isPolling ? "Adding Repository" : "Add Repo"}
      onClose={onClose}
      disableOverlayDismiss={isPolling}
      overlayTestId="add-repo-overlay"
      dialogClassName={aetherModalStyles.dialogWide}
      showCloseButton={!isPolling}
      footer={modalFooter}
    >
      {isPolling ? (
        <div
          className={styles.progressContainer}
          data-testid="add-repo-progress"
        >
          <div className={styles.progressSpinner} aria-hidden="true" />
          <p className={styles.progressMessage}>{progress}</p>
          <p className={styles.progressElapsed}>{elapsed}</p>
        </div>
      ) : (
        <form id="add-repo-form" onSubmit={handleSubmit}>
          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="repo-url">
              Repository URL
            </label>
            <input
              id="repo-url"
              ref={urlRef}
              className={`${styles.input} ${styles.mono}`}
              value={url}
              onChange={(event) => setUrl(event.target.value)}
              placeholder="https://github.com/org/repo"
              disabled={isSubmitting}
            />
          </div>
          <div className={styles.fieldGroup}>
            <label className={styles.label} htmlFor="repo-branch">
              Default branch
            </label>
            <input
              id="repo-branch"
              className={`${styles.input} ${styles.mono}`}
              value={branch}
              onChange={(event) => setBranch(event.target.value)}
              placeholder="Auto-detect from remote HEAD"
              disabled={isSubmitting}
            />
          </div>
          {(error || jobError) && (
            <div className={styles.error} role="alert">
              {error || jobError}
            </div>
          )}
        </form>
      )}
    </AetherModal>
  );
}
