/**
 * AddRepoModal — "Add Repo" dialog.
 *
 * Replaces the old inline sidebar form with a focused two-field modal:
 * Repository URL + Default branch. Both are wired to the real
 * addWorkspaceRepos API (which accepts an optional `branch`). A clone-style
 * URL (https:// or git@) is sent as `clone_urls`; anything else is treated as
 * a local `repos` path, preserving loom's existing behaviour.
 */
import { useEffect, useRef, useState, type FormEvent } from "react";

import { AetherModal, aetherModalStyles } from "@/components/AetherModal";
import { addWorkspaceRepos } from "@/hooks/api";

import styles from "./AddRepoModal.module.css";

/** Matches the detection used by the legacy inline add-repo form. */
const CLONE_URL_RE = /^(https:\/\/|git@)/;

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
  const [branch, setBranch] = useState("main");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const urlRef = useRef<HTMLInputElement>(null);

  // Reset + focus whenever the dialog opens.
  useEffect(() => {
    if (!isOpen) return;
    setUrl(initialUrl);
    setBranch("main");
    setError(null);
    setIsSubmitting(false);
    const id = window.setTimeout(() => urlRef.current?.focus(), 0);
    return () => window.clearTimeout(id);
  }, [isOpen, initialUrl]);

  const trimmedUrl = url.trim();
  const valid = trimmedUrl.length > 0 && workspaceId.length > 0;

  const handleSubmit = async (event: FormEvent): Promise<void> => {
    event.preventDefault();
    if (!valid || isSubmitting) return;
    setIsSubmitting(true);
    setError(null);
    const trimmedBranch = branch.trim();
    try {
      await addWorkspaceRepos(workspaceId, {
        ...(CLONE_URL_RE.test(trimmedUrl)
          ? { clone_urls: [trimmedUrl] }
          : { repos: [trimmedUrl] }),
        ...(trimmedBranch ? { branch: trimmedBranch } : {}),
      });
      onSuccess();
      onClose();
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Failed to add repository",
      );
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <AetherModal
      isOpen={isOpen}
      title="Add Repo"
      onClose={onClose}
      overlayTestId="add-repo-overlay"
      footer={
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
      }
    >
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
            placeholder="main"
            disabled={isSubmitting}
          />
        </div>
        {error && (
          <div className={styles.error} role="alert">
            {error}
          </div>
        )}
      </form>
    </AetherModal>
  );
}
